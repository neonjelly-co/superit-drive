package server

import (
	"errors"
	"net/http"
	"path"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// When a member is removed (drive/held.go). Their routes are in routes.go with all the others; guard has decided before
// any of these runs: seeing the held My files and the record is overseeing (access.Oversee, a project admin);
// handing one over and deleting one are access.HandOverHeld and access.DeleteHeld, a project admin's at the top folder of
// a My files whose owner has left the member list. Nothing in a held My files is listed, opened or named by any of
// them: only whose it was, how many files and how many bytes.

// actWait is how long a hand-over or a deletion is waited for before it is answered as under way.
const actWait = 10 * time.Second

// heldJSON is a held My files, as a project admin sees it.
type heldJSON struct {
	// Place is its id: what the acts on it name.
	Place int64 `json:"place"`
	// UserID is the removed member's stable user id. Folder is the name of their My files' top folder on the storage:
	// their email address when they first opened the Drive (the Drive no longer knows their name).
	UserID string `json:"userId"`
	Folder string `json:"folder"`
	// Files and Size are how many files, and how many bytes, are in it outside its trash; Used is all it takes, its trash
	// included, as the space used counts it.
	Files int64 `json:"files"`
	Size  int64 `json:"size"`
	Used  int64 `json:"used"`
	// Act is the hand-over or deletion under way on it, if any; Back says its owner is in the member list again meanwhile
	// (the act goes on all the same).
	Act  *actJSON `json:"act,omitempty"`
	Back bool     `json:"back,omitempty"`
}

// actJSON is a hand-over or a deletion of a held My files: the record of who did it, when, and what.
type actJSON struct {
	ID int64 `json:"id"`
	// Kind is "hand-over" or "delete".
	Kind string `json:"kind"`
	// UserID and Folder say whose files they were, as heldJSON does; Files and Size what was in them, outside their trash.
	UserID string `json:"userId"`
	Folder string `json:"folder"`
	Files  int64  `json:"files"`
	Size   int64  `json:"size"`
	// By is the project admin who did it, while they are a member (ByFormer once they have left); At when.
	By       *personJSON `json:"by,omitempty"`
	ByFormer bool        `json:"byFormer,omitempty"`
	At       time.Time   `json:"at"`
	// For a hand-over: To is who got them, while they are a member (ToFormer once they have left), and Into the folder
	// they became in To's My files.
	To       *personJSON `json:"to,omitempty"`
	ToFormer bool        `json:"toFormer,omitempty"`
	Into     string      `json:"into,omitempty"`
	// Done is when the Drive finished it; none while it is under way.
	Done *time.Time `json:"done,omitempty"`
}

func actJSONOf(a index.Act, list *members.List) *actJSON {
	j := &actJSON{ID: a.ID, Kind: a.Kind, UserID: a.Owner, Folder: path.Base(a.Folder), Files: a.Files, Size: a.Size, At: a.At}
	if m, ok := list.Lookup(a.By); ok {
		j.By = personJSONOf(m)
	} else {
		j.ByFormer = true
	}
	if a.Kind == index.HandOver {
		j.Into = a.ToName
		if m, ok := list.Lookup(a.ToMember); ok {
			j.To = personJSONOf(m)
		} else {
			j.ToFormer = true
		}
	}
	if !a.Done.IsZero() {
		done := a.Done
		j.Done = &done
	}
	return j
}

// held lists, for a project admin, every held My files (its owner is not in the member list, or a hand-over or deletion
// of it is under way) and the record of every hand-over and deletion, the most recent first.
//
//	GET /api/held
//	200 {"held": [{"place", "userId", "folder", "files", "size", "used", "act", "back"}],
//	     "acts": [{"id", "kind", "userId", "folder", "files", "size", "by", "byFormer", "at", "to", "toFormer", "into", "done"}]}
func (s *Server) held(w http.ResponseWriter, r *http.Request, _ call) {
	list := s.members.Current()
	held, err := s.drive.Held(r.Context(), list)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	acts, err := s.drive.Acts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		Held []heldJSON `json:"held"`
		Acts []*actJSON `json:"acts"`
	}{Held: []heldJSON{}, Acts: []*actJSON{}}
	for _, h := range held {
		j := heldJSON{Place: h.ID, UserID: h.Owner, Folder: path.Base(h.Folder), Files: h.Files, Size: h.Size, Used: h.Used}
		if h.Act != nil {
			j.Act = actJSONOf(*h.Act, list)
			_, j.Back = list.Lookup(h.Owner)
		}
		out.Held = append(out.Held, j)
	}
	for _, a := range acts {
		out.Acts = append(out.Acts, actJSONOf(a, list))
	}
	writeJSON(w, http.StatusOK, out)
}

// stillHeld says, once the place is locked, whether the owner of the held My files is still out of the member list.
func (s *Server) stillHeld(p index.Place) func() bool {
	return func() bool {
		_, back := s.members.Current().Lookup(p.Owner)
		return !back
	}
}

// handOver hands a held My files, as a whole, to another member, for a project admin: it becomes a folder in their My
// files, named after the removed member's folder (with " (2)" and so on when that is taken), without anything given on it
// or in it; what was in its trash is removed. 200 with the act once it is done; 202 with it while it is under way (the
// Drive goes on with it by itself).
//
//	POST /api/held/{place}/hand-over   {"to": "<user id of a member>"}
//	200 | 202 {"act": {…}}   400 not a member   409 another act under way, its owner back, or changes in it under way
func (s *Server) handOver(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		To string `json:"to"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	list := s.members.Current()
	to, ok := list.Lookup(body.To)
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, "Choose a member of the project to hand the files to.")
		return
	}
	a, err := s.drive.HandOver(r.Context(), *c.place, to, c.member.UserID, s.stillHeld(*c.place), actWait)
	s.acted(w, r, a, err)
}

// deleteHeld deletes a held My files for good, as a whole, its trash included, for a project admin. 200 with the act
// once it is done; 202 with it while it is under way.
//
//	DELETE /api/held/{place}
//	200 | 202 {"act": {…}}   409 another act under way, its owner back, or changes in it under way
func (s *Server) deleteHeld(w http.ResponseWriter, r *http.Request, c call) {
	a, err := s.drive.DeleteHeld(r.Context(), *c.place, c.member.UserID, s.stillHeld(*c.place), actWait)
	s.acted(w, r, a, err)
}

// acted answers a hand-over or a deletion.
func (s *Server) acted(w http.ResponseWriter, r *http.Request, a index.Act, err error) {
	list := s.members.Current()
	switch {
	case errors.Is(err, drive.ErrNotHeld):
		s.refuse(w, r, http.StatusConflict, "The person these files belong to is a member of the project again: the files are theirs, as they were.")
	case errors.Is(err, drive.ErrActing):
		out := map[string]any{"error": "These files, or the My files they were to go to, are being handed over or deleted already. Try again once that is done."}
		if a.ID != 0 {
			out["act"] = actJSONOf(a, list)
		}
		writeJSON(w, http.StatusConflict, out)
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "Something in these files is still being changed. Try again in a moment.")
	case errors.Is(err, drive.ErrPlaceGone), errors.Is(err, index.ErrNoPlace):
		s.refuse(w, r, http.StatusNotFound, notHere)
	case err != nil && a.ID != 0:
		// Recorded, and under way (its storage steps failed, and were logged): the Drive goes on with it.
		writeJSON(w, http.StatusAccepted, map[string]any{"act": actJSONOf(a, list)})
	case err != nil:
		s.failed(w, r, err)
	case a.Done.IsZero():
		writeJSON(w, http.StatusAccepted, map[string]any{"act": actJSONOf(a, list)})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"act": actJSONOf(a, list)})
	}
}
