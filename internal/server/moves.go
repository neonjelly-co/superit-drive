package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Moving a file or folder: inside its place, or out of the person's own My files into a shared folder, where it
// becomes the project's. Before such a move the interface asks who will see it there (movePreview), to warn the
// person. Their routes are in routes.go with all the others; guard has decided Move at the item before any of these
// runs, and they decide again where it goes.

const noSuchDestination = "The folder to move it into is not in the Drive, or you cannot add to it. Reload the page and try again."

const notBetweenPlaces = "Only what is in your own My files can be moved to another place, and only into a shared folder, " +
	"where it becomes the project's. Anything else is moved within its own place."

// destination finds where a move goes and decides there: the place (the item's own when placeID is 0) and the folder
// in it (nil, for folderID 0: its top folder). The person must be allowed to put things into that folder
// (access.Upload), and, in another place, to move the item out of its own (access.MoveOut, only from their own My
// files) and into that one (access.MoveIn, only into a shared folder). ok is false when they were already answered:
// a place or folder that is not there, or that they may not add to, gets noSuchDestination, the same answer for both;
// a move between places that is not allowed gets notBetweenPlaces (they may add to both ends, so it tells them nothing).
func (s *Server) destination(w http.ResponseWriter, r *http.Request, c call, placeID, folderID int64) (p index.Place, into *index.Item, where *access.Place, ok bool) {
	p = *c.place
	if placeID != 0 && placeID != c.place.ID {
		found, err := s.drive.Place(r.Context(), placeID)
		if errors.Is(err, index.ErrNoPlace) {
			s.refuse(w, r, http.StatusNotFound, noSuchDestination)
			return index.Place{}, nil, nil, false
		}
		if err != nil {
			s.fail(w, r, err)
			return index.Place{}, nil, nil, false
		}
		p = found
	}
	var path []index.Item
	if folderID != 0 {
		folder, err := s.drive.Item(r.Context(), p, folderID)
		if errors.Is(err, index.ErrNoItem) {
			s.refuse(w, r, http.StatusNotFound, noSuchDestination)
			return index.Place{}, nil, nil, false
		}
		if err != nil {
			s.fail(w, r, err)
			return index.Place{}, nil, nil, false
		}
		into, path = &folder, pathTo(folder)
	}
	where, err := s.accessAt(r.Context(), p, idsOf(path))
	if err != nil {
		s.fail(w, r, err)
		return index.Place{}, nil, nil, false
	}
	who := personOf(c.member)
	if !access.Decide(who, access.Upload, where) {
		// The same answer as for a folder that is not there.
		s.refuse(w, r, http.StatusNotFound, noSuchDestination)
		return index.Place{}, nil, nil, false
	}
	if p.ID != c.place.ID && (!access.Decide(who, access.MoveOut, c.access) || !access.Decide(who, access.MoveIn, where)) {
		s.refuse(w, r, http.StatusBadRequest, notBetweenPlaces)
		return index.Place{}, nil, nil, false
	}
	return p, into, where, true
}

// anId reads a place's or folder's id: a positive number, or nothing (null, absent, empty) for none. ok is false for
// anything else.
func anId(raw string) (id int64, ok bool) {
	switch raw = strings.TrimSpace(raw); raw {
	case "", "null":
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

// movedJSON is an item after a move, and the place it is in now.
type movedJSON struct {
	itemJSON
	Place int64 `json:"place"`
}

// move puts a file or folder, with everything in it, into another folder: "to" is the folder's id, or null for the
// top folder; "place" is the place it is in, when that is not the item's own. Inside a place, whoever may put things
// into the folder may (someone given a folder moves things within it, never out of it into a folder they cannot add
// to). Into another place, only out of the person's own My files into a shared folder (see destination): it becomes
// the project's, keeps what was given on it and on everything in it, and no longer has what it had from the folders
// above it. The interface warns first, with what movePreview says.
//
//	POST /api/places/{place}/items/{item}/move     {"to": 42}  or  {"to": 42 | null, "place": 7}
//	200 the item, with "place"; 404 for a place or folder that is not there or the person cannot add to; 400 for a move
//	between places that is not allowed; 409 for a name taken there
func (s *Server) move(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		To    json.RawMessage `json:"to"`
		Place json.RawMessage `json:"place"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	folderID, okTo := anId(string(body.To))
	placeID, okPlace := anId(string(body.Place))
	if body.To == nil || !okTo || !okPlace {
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return
	}
	p, into, _, ok := s.destination(w, r, c, placeID, folderID)
	if !ok {
		return
	}
	var it index.Item
	var err error
	if p.ID == c.place.ID {
		it, err = s.drive.Move(r.Context(), *c.place, *c.item, into)
	} else {
		it, err = s.drive.MoveToPlace(r.Context(), *c.place, *c.item, p, into)
	}
	var taken *drive.Taken
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, movedJSON{itemJSON: itemOf(it), Place: p.ID})
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already something called “"+taken.Name+"” in that folder. Rename one of them first.")
	default:
		s.changeFailed(w, r, err, c.item.Name)
	}
}

// movePreview says, before a move, who would see the item where it would go, the way "who can see this" would show
// it there: who has it by the place itself (the shared folder's managers among the project admins), what was given on
// the folders on the way down to it, and what was given on the item itself, which it keeps. becomesProjects says it
// would leave the person's My files for a shared folder, and so become the project's. Nothing moves.
//
// It is decided as the move itself would be (destination), and says nothing the person could not already see: they
// may add to the folder it would go into, and so see who has access there, and they may share the item, and so see
// what was given on it. The name of a folder above the destination is given only when they can open it.
//
//	GET /api/places/{place}/items/{item}/move?to=42[&place=7]   (no "to": the top folder)
//	200 {"becomesProjects", "access": [...as GET …/access answers...], "seenBy": {"everyone", "members"}}
func (s *Server) movePreview(w http.ResponseWriter, r *http.Request, c call) {
	folderID, okTo := anId(r.URL.Query().Get("to"))
	placeID, okPlace := anId(r.URL.Query().Get("place"))
	if !okTo || !okPlace {
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return
	}
	p, into, _, ok := s.destination(w, r, c, placeID, folderID)
	if !ok {
		return
	}
	if err := drive.CanGoInto(*c.item, into); err != nil {
		s.changeFailed(w, r, err, c.item.Name)
		return
	}
	var path []index.Item
	if into != nil {
		path = pathTo(*into)
	}
	there, err := s.drive.GrantsOn(r.Context(), p, idsOf(path))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// What was given on the item itself goes with it.
	given, err := s.drive.GrantsOn(r.Context(), *c.place, []int64{c.item.ID})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, g := range given {
		if g.Item == c.item.ID {
			there = append(there, g)
		}
	}
	moved := *c.item
	moved.Above = path
	path = append(path, moved)
	who := personOf(c.member)
	writeJSON(w, http.StatusOK, map[string]any{
		"becomesProjects": c.place.Kind == index.MyFiles && p.Kind == index.SharedFolder,
		"access":          s.accessLines(who, p, path, there),
		"seenBy":          seenBy(s.members.Current(), s.accessPlace(p, idsOf(path), there)),
	})
}
