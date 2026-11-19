package server

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
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
		{pattern: "POST /api/places/{place}/files", action: access.Upload, place: "place", serve: s.upload},
		{pattern: "GET /api/places/{place}/items/{item}/download", action: access.Download, place: "place", item: "item", serve: s.download},
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
		out = append(out, itemOf(it))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func itemOf(it index.Item) itemJSON {
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
	return j
}

// upload stores a new file in the top folder of a place and answers 201 with it.
//
//	POST /api/places/{place}/files?name=<the file's name>
//	Content-Length: <the file's size>
//
//	<the file's bytes>
//
// The body is the file itself, not a form, and streams straight through to the storage in one all-or-nothing PUT.
// Content-Length is required, so the storage can tell a whole file from a cut-off one. A name that is already there
// is refused with 409; nothing is ever replaced.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, c call) {
	if r.ContentLength < 0 {
		s.refuse(w, r, http.StatusLengthRequired, "The upload did not say how large the file is, so nothing was stored. Try again.")
		return
	}
	name := r.URL.Query().Get("name")
	it, err := s.drive.Upload(r.Context(), *c.place, name, r.Body, r.ContentLength)
	var bad *drive.BadName
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, itemOf(it))
	case errors.As(err, &bad):
		s.refuse(w, r, http.StatusBadRequest, bad.Error())
	case errors.Is(err, drive.ErrExists):
		s.refuse(w, r, http.StatusConflict, "There is already something called “"+name+"” here, so this file was not uploaded. Rename it and try again.")
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusConflict, "This folder is no longer on the Drive storage. Reload the page and try again.")
	case errors.Is(err, drive.ErrFull):
		s.refuse(w, r, http.StatusInsufficientStorage, "The Drive storage is full, so this file was not uploaded.")
	case errors.Is(err, drive.ErrCutOff) || r.Context().Err() != nil:
		// The person's connection ended (or they cancelled); the storage kept nothing. Not the Drive's failure.
		s.log.Info("an upload ended before the whole file arrived; nothing was stored", "error", err)
		s.refuse(w, r, http.StatusBadRequest, "The upload stopped before the whole file arrived, so nothing was stored. Try again.")
	default:
		s.failed(w, r, err)
	}
}

// download sends a file's bytes under its original name, as an attachment, so the browser saves it rather than
// showing it. (Opening safe kinds of file in the browser, and ranged reads, come later.)
func (s *Server) download(w http.ResponseWriter, r *http.Request, c call) {
	f, err := s.drive.Download(r.Context(), *c.place, *c.item)
	switch {
	case errors.Is(err, drive.ErrNotAFile):
		s.refuse(w, r, http.StatusNotFound, "This is a folder, and only files can be downloaded.")
		return
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusNotFound, "This file is no longer on the Drive storage. Reload the page to see what is there now.")
		return
	case err != nil:
		s.failed(w, r, err)
		return
	}
	defer f.Body.Close()

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", attachment(c.item.Name))
	h.Set("Cache-Control", "private, no-store")
	if f.Size >= 0 {
		// With the length announced, a download cut short shows as failed in the browser instead of as a short file.
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, f.Body); err != nil {
		s.log.Info("a download ended before its last byte", "error", err)
	}
}

// attachment is a Content-Disposition value that has the browser save the file under name. A name that is not plain
// ASCII is sent in the RFC 6266 filename* form, which every current browser reads.
func attachment(name string) string {
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": name}); v != "" {
		return v
	}
	return "attachment"
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
