package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Making, seeing and removing the public link on a file or folder, in the details panel. Their routes are in routes.go
// with all the others; guard has decided before any of these runs (access.MakeLink, access.ManageLink: whoever may
// share the item). What visitors reach through a link is in visitors.go.

// linkJSON is the public link on an item, as whoever may manage it sees it.
type linkJSON struct {
	// Kind is what it lets visitors do: "view" (see and download).
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
	// Address is the link's address: only in the answer that made it. The Drive keeps only a hash of its token, and can
	// never show it again.
	Address string `json:"address,omitempty"`
}

func (s *Server) linkJSONOf(l index.Link, where *access.Place, address string) linkJSON {
	list := s.members.Current()
	j := linkJSON{Kind: l.Kind, Made: l.Made, Uses: l.Uses, Address: address,
		Works: access.Decide(visitorOf(l, list), access.SeeThroughLink, where)}
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
		writeJSON(w, http.StatusOK, map[string]any{"link": nil})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": s.linkJSONOf(l, c.access, "")})
}

// makeLink makes a public link to a file or folder: anyone with its address may see it, and everything inside it, and
// download them, without signing in, until it is removed. The answer is the only one that holds the address: copy it
// then. An item has one link at most: when it has one, a new one is made only in its place ("replace": true), and the
// old address stops working for good.
//
//	POST /api/places/{place}/items/{item}/link    {"replace": true}   (the body may be left out)
//	201 {"link": {…, "address": "https://files.example.com/s/<token>"}}
//	409 {"error", "exists": true}    it has a link already, and replacing it was not asked for
func (s *Server) makeLink(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Replace bool `json:"replace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return
	}
	l, token, err := s.drive.MakeLink(r.Context(), *c.place, *c.item, string(access.ViewLink), c.member.UserID, body.Replace)
	switch {
	case errors.Is(err, index.ErrLinkExists):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "This already has a public link. Make a new one in its place to change its address.",
			"exists": true,
		})
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
