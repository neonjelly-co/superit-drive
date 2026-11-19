package server

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// routes is every route the Drive answers. Add new ones here, each with the action it is.
func (s *Server) routes() []route {
	return []route{
		// The interface.
		{pattern: "GET /{$}", action: access.Enter, serve: s.serveInterface},
		{pattern: "GET /assets/{file...}", action: access.Enter, serve: s.serveAsset},
		{pattern: "GET /favicon.svg", action: access.Enter, serve: s.serveAsset},

		// The API the interface calls.
		{pattern: "GET /api/me", action: access.Enter, serve: s.me},
		{pattern: "GET /api/places", action: access.Enter, serve: s.places},
		{pattern: "GET /api/places/{place}/items", action: access.List, place: "place", serve: s.items},
	}
}

// me says who is signed in and which project this Drive belongs to.
func (s *Server) me(w http.ResponseWriter, _ *http.Request, c call) {
	writeJSON(w, http.StatusOK, map[string]string{
		"userId":      c.member.UserID,
		"email":       c.member.Email,
		"displayName": c.member.DisplayName,
		"role":        string(c.member.Role),
		"project":     s.project,
	})
}

type placeJSON struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
}

// places lists the places the person can see. So far that is their own "My files", made the first time they ask.
func (s *Server) places(w http.ResponseWriter, r *http.Request, c call) {
	mine, err := s.drive.MyFiles(r.Context(), c.member)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	who := access.Person{UserID: c.member.UserID, Role: c.member.Role}
	out := []placeJSON{}
	for _, p := range []index.Place{mine} {
		if access.Decide(who, access.List, accessPlace(p)) {
			out = append(out, placeJSON{ID: p.ID, Kind: string(p.Kind)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out})
}

type itemJSON struct {
	ID      int64      `json:"id"`
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`              // "folder" or "file"
	Size    *int64     `json:"size,omitempty"`    // bytes, files only
	Changed *time.Time `json:"changed,omitempty"` // when the storage last saw it change
}

// items lists what is in the top folder of a place.
func (s *Server) items(w http.ResponseWriter, r *http.Request, c call) {
	items, err := s.drive.List(r.Context(), *c.place)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		j := itemJSON{ID: it.ID, Name: it.Name, Kind: "file"}
		if it.Folder {
			j.Kind = "folder"
		} else {
			size := it.Size
			j.Size = &size
		}
		if !it.Changed.IsZero() && it.Changed.Unix() > 0 {
			changed := it.Changed
			j.Changed = &changed
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// failed answers a request whose work went wrong: on the storage, or in the Drive itself.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, drive.ErrStorage) {
		s.fail(w, r, err)
		return
	}
	s.log.Error("the storage could not answer", "route", r.Pattern, "error", err)
	message := "The Drive storage did not answer. Try again in a moment."
	if errors.Is(err, storage.ErrUnauthorized) {
		message = "The Drive storage refused the Drive's login. The storage settings need checking."
	}
	s.refuse(w, r, http.StatusBadGateway, message)
}

// serveInterface serves the interface's page.
func (s *Server) serveInterface(w http.ResponseWriter, r *http.Request, _ call) {
	if _, err := fs.Stat(s.ui, "index.html"); err != nil {
		s.refuse(w, r, http.StatusServiceUnavailable, "The interface was not built into this Drive. Build it with `make build`.")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.ui, "index.html")
}

// serveAsset serves one of the interface's files. Files under assets/ have their content's hash in their name, so a
// browser may keep them.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, c call) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if info, err := fs.Stat(s.ui, name); err != nil || info.IsDir() {
		s.notFound(w, r, c)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.ui, name)
}
