package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Making, seeing and removing the public link on a file or folder, in the details panel. Their routes are in routes.go
// with all the others; guard has decided before any of these runs (access.MakeLink, access.ManageLink: whoever may
// share the item). What visitors reach through a link is in visitors.go.

// linkJSON is the public link on an item, as whoever may manage it sees it.
type linkJSON struct {
	// Kind is what it lets visitors do: "view" (see and download), or, on a folder, "upload-only" (send files in, and
	// see nothing).
	Kind string `json:"kind"`
	// Made is when it was made; MadeBy who made it, while they are in the project (Former once they have left it).
	Made   time.Time   `json:"made"`
	MadeBy *personJSON `json:"madeBy,omitempty"`
	Former bool        `json:"former,omitempty"`
	// Uses is how often it was used (a page through it, or a download, a file opened or a zip from its start), and Used
	// when last; nothing else about who used it is kept.
	Uses int64      `json:"uses"`
	Used *time.Time `json:"used,omitempty"`
	// Works says whether it lets visitors in now. It does not while its maker can no longer share the item in a My
	// files (it works again if they can again), nor where sharing outside the project is off.
	Works bool `json:"works"`
	// For an upload-only link: TakenIn is how many bytes of files it has taken in since it was made or last reset, and
	// Files how many files; Limit and FilesLimit the most it takes in before it stops, and Full whether it has reached
	// either: then it takes no file until it is reset.
	TakenIn    *int64 `json:"takenIn,omitempty"`
	Limit      *int64 `json:"limit,omitempty"`
	Files      *int64 `json:"files,omitempty"`
	FilesLimit *int64 `json:"filesLimit,omitempty"`
	Full       bool   `json:"full,omitempty"`
	// Address is the link's address: only in the answer that made it. The Drive keeps only a hash of its token, and can
	// never show it again.
	Address string `json:"address,omitempty"`
}

func (s *Server) linkJSONOf(l index.Link, where *access.Place, address string) linkJSON {
	list := s.members.Current()
	j := linkJSON{Kind: l.Kind, Made: l.Made, Uses: l.Uses, Address: address, Works: linkWorks(l, where, list)}
	if l.Kind == index.UploadLink {
		taken, limit, files, filesLimit := l.TakenIn, s.drive.LinkLimit(), l.TakenFiles, s.drive.LinkFiles()
		j.TakenIn, j.Limit, j.Files, j.FilesLimit, j.Full = &taken, &limit, &files, &filesLimit, s.drive.LinkFull(l)
	}
	if m, ok := list.Lookup(l.MadeBy); ok {
		j.MadeBy = personJSONOf(m)
	} else {
		j.Former = true
	}
	if !l.Used.IsZero() {
		used := l.Used
		j.Used = &used
	}
	return j
}

// linkOf answers with the public link on an item, or with none.
//
//	GET /api/places/{place}/items/{item}/link
//	200 {"link": {"kind", "made", "madeBy", "former", "uses", "used", "works"} | null}
func (s *Server) linkOf(w http.ResponseWriter, r *http.Request, c call) {
	l, err := s.drive.LinkOn(r.Context(), *c.item)
	if errors.Is(err, index.ErrNoLink) {
		writeJSON(w, http.StatusOK, map[string]any{"link": nil, "outside": outsideHere(*c.place)})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": s.linkJSONOf(l, c.access, ""), "outside": outsideHere(*c.place)})
}

// linkWorks says whether a link lets visitors in now: whether its page opens, through a link of either kind.
func linkWorks(l index.Link, where *access.Place, list *members.List) bool {
	return access.Decide(visitorOf(l, list), access.EnterThroughLink, where)
}

// linkKindOf reads the kind of link a request asks for: "view" (the default) or "upload-only". ok is false when the
// person was already answered.
func (s *Server) linkKindOf(w http.ResponseWriter, r *http.Request, kind string) (string, bool) {
	switch kind {
	case "", index.ViewLink:
		return index.ViewLink, true
	case index.UploadLink:
		return kind, true
	}
	s.refuse(w, r, http.StatusBadRequest, linkKinds)
	return "", false
}

// makeLink makes a public link to a file or folder, until it is removed: a view link (anyone with its address may see
// it, and everything inside it, and download them, without signing in), or, on a folder, an upload-only link (anyone
// with its address may send files into it, and sees nothing of what is there). The answer is the only one that holds
// the address: copy it then. An item has one link at most: when it has one, a new one is made only in its place
// ("replace": true), and the old address stops working for good.
//
//	POST /api/places/{place}/items/{item}/link    {"replace": true, "kind": "view" | "upload-only"}   (all may be left out)
//	201 {"link": {…, "address": "https://files.example.com/s/<token>"}}
//	400 an upload-only link on a file, or a kind there is not
//	409 {"error", "exists": true}    it has a link already, and replacing it was not asked for
func (s *Server) makeLink(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Replace bool   `json:"replace"`
		Kind    string `json:"kind"`
	}
	if err := readSmall(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		s.refuse(w, r, http.StatusBadRequest, unreadable)
		return
	}
	kind, ok := s.linkKindOf(w, r, body.Kind)
	if !ok {
		return
	}
	l, token, err := s.drive.MakeLink(r.Context(), *c.place, *c.item, kind, c.member.UserID, body.Replace)
	switch {
	case errors.Is(err, index.ErrLinkKind):
		s.refuse(w, r, http.StatusBadRequest, fileLinkKind)
		return
	case errors.Is(err, index.ErrLinkExists):
		refuseWith(w, r, http.StatusConflict, map[string]any{"exists": true}, linkExists)
		return
	case errors.Is(err, index.ErrNoItem), errors.Is(err, drive.ErrPlaceGone):
		// It went to the trash after guard found it: the same answer guard gives from now on.
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"link": s.linkJSONOf(l, c.access, s.linkAddress(token))})
}

// setLinkKind changes what the public link on a folder lets visitors do, keeping its address: "view" (see and download
// everything in it) or "upload-only" (send files in, and see nothing). Whoever has the address can then do what the new
// kind lets them: making an upload-only link a view link shows the folder to everyone it was sent to. A file's link is
// a view link only. What an upload-only link has taken in is kept through a change of kind.
//
//	PUT /api/places/{place}/items/{item}/link    {"kind": "view" | "upload-only"}
//	200 {"link": {…}}
//	400 an upload-only link on a file, or a kind there is not
//	409 {"error"}    it has no link
func (s *Server) setLinkKind(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Kind string `json:"kind"`
		// TakeOver makes the person asking the link's maker also when its kind stays as it is.
		TakeOver bool `json:"takeOver"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	if body.Kind == "" {
		s.refuse(w, r, http.StatusBadRequest, linkKinds)
		return
	}
	kind, ok := s.linkKindOf(w, r, body.Kind)
	if !ok {
		return
	}
	if !s.mayTakeLink(w, r, c) {
		return
	}
	l, err := s.drive.SetLinkKind(r.Context(), *c.place, *c.item, kind, c.member.UserID, body.TakeOver)
	s.changedLink(w, r, c, l, err)
}

// mayTakeLink says whether the person asking may become the maker of the link on c.item, as changing its kind,
// resetting it or taking it over makes them: its maker always; anyone else only while it works. A link that has stopped
// (in a My files, its maker may no longer share the item) stays stopped, whoever asks: made another's, it would work
// again at the address its maker knows, who was to be kept out. A new link in its place has a new address. ok is false
// when the person was already answered; a link that is not there is for the change itself to answer.
func (s *Server) mayTakeLink(w http.ResponseWriter, r *http.Request, c call) (ok bool) {
	l, err := s.drive.LinkOn(r.Context(), *c.item)
	if errors.Is(err, index.ErrNoLink) {
		return true
	}
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if l.MadeBy != c.member.UserID && !linkWorks(l, c.access, s.members.Current()) {
		s.refuse(w, r, http.StatusConflict, linkStopped)
		return false
	}
	return true
}

// resetLink lets an upload-only link that has taken in all it may (drive.LinkLimit) take files again: what it has taken
// in counts from 0 again. Its address, and the files it took in, stay as they are.
//
//	POST /api/places/{place}/items/{item}/link/reset
//	200 {"link": {…}}
//	409 {"error"}    it has no link
func (s *Server) resetLink(w http.ResponseWriter, r *http.Request, c call) {
	if !s.mayTakeLink(w, r, c) {
		return
	}
	l, err := s.drive.ResetLink(r.Context(), *c.place, *c.item, c.member.UserID)
	s.changedLink(w, r, c, l, err)
}

// changedLink answers a change to the link on c.item.
func (s *Server) changedLink(w http.ResponseWriter, r *http.Request, c call, l index.Link, err error) {
	switch {
	case errors.Is(err, index.ErrLinkKind):
		s.refuse(w, r, http.StatusBadRequest, fileLinkKind)
	case errors.Is(err, index.ErrNoLink):
		s.refuse(w, r, http.StatusConflict, noLinkYet)
	case errors.Is(err, index.ErrNoItem), errors.Is(err, drive.ErrPlaceGone):
		s.refuse(w, r, http.StatusNotFound, notHere)
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"link": s.linkJSONOf(l, c.access, "")})
	}
}

// removeLink removes the public link on a file or folder, if it has one: its address is dead for good, and answers as
// one that never existed. A new link gets a new address.
//
//	DELETE /api/places/{place}/items/{item}/link
//	204
func (s *Server) removeLink(w http.ResponseWriter, r *http.Request, c call) {
	err := s.drive.RemoveLink(r.Context(), *c.place, *c.item)
	switch {
	case errors.Is(err, index.ErrNoItem), errors.Is(err, drive.ErrPlaceGone):
		s.refuse(w, r, http.StatusNotFound, notHere)
	case err != nil:
		s.fail(w, r, err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}

// linkAddress is the address of the link whose token is given, at DRIVE_ADDRESS.
func (s *Server) linkAddress(token string) string {
	if s.address == nil {
		return PublicPrefix + token
	}
	address := *s.address
	address.Path, address.RawPath, address.RawQuery, address.Fragment = PublicPrefix+token, "", "", ""
	return address.String()
}

// accessLinks are links as the access package needs them, with their makers as the member list has them now.
func (s *Server) accessLinks(links []index.Link) []access.Link {
	list := s.members.Current()
	out := make([]access.Link, 0, len(links))
	for _, l := range links {
		out = append(out, accessLink(l, list))
	}
	return out
}

// withLinksOn is where, with the public links on the items of path added: for whether anyone with a link can see it
// (access.ByLink).
func (s *Server) withLinksOn(ctx context.Context, where *access.Place, path []int64) (*access.Place, error) {
	links, err := s.drive.LinksOn(ctx, path)
	if err != nil {
		return nil, err
	}
	return where.WithLinks(s.accessLinks(links)...), nil
}
