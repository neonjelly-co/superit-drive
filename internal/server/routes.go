package server

import (
	"encoding/json"
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
//
// A folder of a place is addressed as an item; the place's top folder by the place alone. So most things that happen
// in a folder have two routes, one for the top folder and one for any other.
func (s *Server) routes() []route {
	return []route{
		// The interface.
		{pattern: "GET /{$}", action: access.Enter, serve: s.serveInterface},
		{pattern: "GET /assets/{file...}", action: access.Enter, serve: s.serveAsset},
		{pattern: "GET /favicon.svg", action: access.Enter, serve: s.serveAsset},

		// The API the interface calls.
		{pattern: "GET /api/me", action: access.Enter, serve: s.me},
		{pattern: "GET /api/places", action: access.Enter, serve: s.places},
		{pattern: "GET /api/usage", action: access.SeeUsage, serve: s.usage},

		{pattern: "GET /api/places/{place}/items", action: access.List, place: "place", serve: s.items},
		{pattern: "GET /api/places/{place}/items/{item}/items", action: access.List, place: "place", item: "item", serve: s.items},
		{pattern: "POST /api/places/{place}/files", action: access.Upload, place: "place", serve: s.upload},
		{pattern: "POST /api/places/{place}/items/{item}/files", action: access.Upload, place: "place", item: "item", serve: s.upload},
		{pattern: "POST /api/places/{place}/folders", action: access.MakeFolder, place: "place", serve: s.newFolder},
		{pattern: "POST /api/places/{place}/items/{item}/folders", action: access.MakeFolder, place: "place", item: "item", serve: s.newFolder},
		{pattern: "GET /api/places/{place}/items/{item}/download", action: access.Download, place: "place", item: "item", serve: s.download},
		{pattern: "POST /api/places/{place}/items/{item}/rename", action: access.Rename, place: "place", item: "item", serve: s.rename},
		{pattern: "POST /api/places/{place}/items/{item}/move", action: access.Move, place: "place", item: "item", serve: s.move},
		{pattern: "DELETE /api/places/{place}/items/{item}", action: access.Delete, place: "place", item: "item", serve: s.delete},

		{pattern: "GET /api/places/{place}/trash", action: access.SeeTrash, place: "place", serve: s.trash},
		{pattern: "POST /api/places/{place}/trash/{item}/restore", action: access.Restore, place: "place", trashed: "item", serve: s.restore},
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

// usage says about how much space the Drive's files take, the trash included, and the storage's capacity when it is
// known (DRIVE_CAPACITY). The figure is the Drive's own sum of the file sizes it knows: about, not exact.
func (s *Server) usage(w http.ResponseWriter, r *http.Request, _ call) {
	used, err := s.drive.Used(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := map[string]int64{"used": used}
	if s.capacity > 0 {
		out["capacity"] = s.capacity
	}
	writeJSON(w, http.StatusOK, out)
}

type itemJSON struct {
	ID      int64      `json:"id"`
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`              // "folder" or "file"
	Size    *int64     `json:"size,omitempty"`    // bytes, files only
	Changed *time.Time `json:"changed,omitempty"` // when the storage last saw it change
}

// folderJSON is a folder in a path, such as the breadcrumb.
type folderJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
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

// pathOf is a path of folders, from the top of the place down.
func pathOf(folders []index.Item) []folderJSON {
	out := make([]folderJSON, 0, len(folders))
	for _, f := range folders {
		out = append(out, folderJSON{ID: f.ID, Name: f.Name})
	}
	return out
}

// folder is the folder a request in a place is about: the item, which must be a folder, or the place's top folder
// (nil) when the route has no item. ok is false when the person was already answered.
func (s *Server) folder(w http.ResponseWriter, r *http.Request, c call, notAFolder string) (folder *index.Item, ok bool) {
	if c.item == nil {
		return nil, true
	}
	if !c.item.Folder {
		s.refuse(w, r, http.StatusBadRequest, notAFolder)
		return nil, false
	}
	return c.item, true
}

// items lists what is in a folder of a place, with the path down to it (empty for the place's top folder).
func (s *Server) items(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "This is a file, not a folder.")
	if !ok {
		return
	}
	items, err := s.drive.List(r.Context(), *c.place, folder)
	if errors.Is(err, drive.ErrGone) {
		s.refuse(w, r, http.StatusNotFound, "This folder is no longer on the Drive storage. Go up a level to see what is there now.")
		return
	}
	if err != nil {
		s.failed(w, r, err)
		return
	}
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, itemOf(it))
	}
	path := []folderJSON{}
	if folder != nil {
		path = pathOf(append(append([]index.Item{}, folder.Above...), *folder))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "path": path})
}

type uploadJSON struct {
	itemJSON
	// Replaced is true when the upload replaced a file of its name, which is now in the trash.
	Replaced bool `json:"replaced,omitempty"`
}

// upload stores a new file in a folder of a place and answers 201 with it.
//
//	POST /api/places/{place}/files?name=<the file's name>[&existing=replace|keep-both]
//	POST /api/places/{place}/items/{folder}/files?name=…
//	Content-Length: <the file's size>
//
//	<the file's bytes>
//
// The body is the file itself, not a form, and streams straight through to the storage in one all-or-nothing PUT.
// Content-Length is required, so the storage can tell a whole file from a cut-off one.
//
// When the name is taken, existing says what to do: replace the file there (it goes to the trash) or keep both (the
// upload gets a name with " (2)" or the like, and the answer has it). Without it, the answer is 409 with "exists" set to
// "file" or "folder", so the interface can ask; nothing is sent to the storage. A folder is never replaced.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "Files can only be uploaded into a folder.")
	if !ok {
		return
	}
	if r.ContentLength < 0 {
		s.refuse(w, r, http.StatusLengthRequired, "The upload did not say how large the file is, so nothing was stored. Try again.")
		return
	}
	name := r.URL.Query().Get("name")
	existing := drive.Existing(r.URL.Query().Get("existing"))
	if existing != drive.Ask && existing != drive.Replace && existing != drive.KeepBoth {
		s.refuse(w, r, http.StatusBadRequest, "The upload asked for something the Drive does not do with a name that is taken.")
		return
	}
	up, err := s.drive.Upload(r.Context(), *c.place, folder, name, r.Body, r.ContentLength, existing)
	var bad *drive.BadName
	var taken *drive.Taken
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, uploadJSON{itemJSON: itemOf(up.Item), Replaced: up.Replaced != nil})
	case errors.As(err, &bad):
		s.refuse(w, r, http.StatusBadRequest, bad.Error())
	case errors.As(err, &taken) && taken.Folder && existing == drive.Replace:
		s.refuseTaken(w, r, taken, "There is a folder called “"+taken.Name+"” here, and a file cannot replace a folder. Keep both instead.")
	case errors.As(err, &taken) && taken.Folder:
		s.refuseTaken(w, r, taken, "There is already a folder called “"+taken.Name+"” here.")
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already a file called “"+taken.Name+"” here.")
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "“"+name+"” is being uploaded or changed right now. Try again when that is done.")
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

// refuseTaken answers 409 for a name that is taken, saying what kind of item has it.
func (s *Server) refuseTaken(w http.ResponseWriter, r *http.Request, taken *drive.Taken, message string) {
	kind := "file"
	if taken.Folder {
		kind = "folder"
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": message, "exists": kind, "name": taken.Name})
}

// readBody reads a small JSON body into v. ok is false when the person was already answered.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return false
	}
	return true
}

// newFolder makes an empty folder in a folder of a place and answers 201 with it.
//
//	POST /api/places/{place}/folders               {"name": "Invoices"}
//	POST /api/places/{place}/items/{folder}/folders
func (s *Server) newFolder(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "A folder can only be made inside a folder.")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	it, err := s.drive.NewFolder(r.Context(), *c.place, folder, body.Name)
	if err != nil {
		s.changeFailed(w, r, err, body.Name)
		return
	}
	writeJSON(w, http.StatusCreated, itemOf(it))
}

// rename gives a file or folder a new name in its folder.
//
//	POST /api/places/{place}/items/{item}/rename   {"name": "Invoices 2026"}
func (s *Server) rename(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Name string `json:"name"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	it, err := s.drive.Rename(r.Context(), *c.place, *c.item, body.Name)
	if err != nil {
		s.changeFailed(w, r, err, body.Name)
		return
	}
	writeJSON(w, http.StatusOK, itemOf(it))
}

// move puts a file or folder, with everything in it, into another folder of the same place: "to" is the folder's id,
// or null for the place's top folder. The folder is looked up in the place the request was decided on, so the same
// decision covers both ends. (Moves between places come with shared folders, and need a decision on each.)
//
//	POST /api/places/{place}/items/{item}/move     {"to": 42}
func (s *Server) move(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		To json.RawMessage `json:"to"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	var to *index.Item
	switch raw := strings.TrimSpace(string(body.To)); raw {
	case "":
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return
	case "null":
	default:
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
			return
		}
		folder, err := s.drive.Item(r.Context(), *c.place, id)
		if errors.Is(err, index.ErrNoItem) {
			s.refuse(w, r, http.StatusNotFound, "The folder to move it into is not in this Drive any more. Reload the page and try again.")
			return
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		to = &folder
	}
	it, err := s.drive.Move(r.Context(), *c.place, *c.item, to)
	var taken *drive.Taken
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, itemOf(it))
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already something called “"+taken.Name+"” in that folder. Rename one of them first.")
	default:
		s.changeFailed(w, r, err, c.item.Name)
	}
}

// changeFailed answers a change to a file or folder that did not happen. name is the name it concerned.
func (s *Server) changeFailed(w http.ResponseWriter, r *http.Request, err error, name string) {
	var bad *drive.BadName
	var taken *drive.Taken
	switch {
	case errors.As(err, &bad):
		s.refuse(w, r, http.StatusBadRequest, bad.Error())
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already something called “"+taken.Name+"” here. Choose another name.")
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "“"+name+"”, or something in it, is being uploaded or changed right now. Try again when that is done.")
	case errors.Is(err, drive.ErrIntoItself):
		s.refuse(w, r, http.StatusConflict, "A folder cannot be moved into itself, or into a folder inside it.")
	case errors.Is(err, drive.ErrNotAFolder):
		s.refuse(w, r, http.StatusBadRequest, "Things can only be moved into a folder.")
	case errors.Is(err, drive.ErrTooDeep):
		s.refuse(w, r, http.StatusBadRequest, "Folders can go at most "+strconv.Itoa(index.MaxDepth-1)+" deep.")
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusNotFound, "This is no longer on the Drive storage. Reload the page to see what is there now.")
	case errors.Is(err, drive.ErrFull):
		s.refuse(w, r, http.StatusInsufficientStorage, "The Drive storage is full.")
	default:
		s.failed(w, r, err)
	}
}

type trashJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "folder" or "file"
	// Size is a file's size, or the sum of the files in a folder as far as the Drive knows them.
	Size int64 `json:"size"`
	// Deleted is when it went to the trash; Removed when the Drive removes it for good.
	Deleted time.Time `json:"deleted"`
	Removed time.Time `json:"removed"`
	// Replaced is true when an upload replaced it, rather than a person deleting it.
	Replaced bool `json:"replaced,omitempty"`
	// From is the folder it goes back to when restored, from the top of the place down; empty for the top folder.
	From []folderJSON `json:"from"`
}

func trashOf(e drive.TrashEntry) trashJSON {
	j := trashJSON{ID: e.ID, Name: e.Name, Kind: "file", Size: e.Size, Deleted: e.Deleted, Removed: e.Removed,
		Replaced: e.Replaced, From: pathOf(e.From)}
	if e.Folder {
		j.Kind = "folder"
	}
	return j
}

// delete moves a file or folder, with everything in it, into the place's trash, and answers with it as the trash
// now holds it.
//
//	DELETE /api/places/{place}/items/{item}
func (s *Server) delete(w http.ResponseWriter, r *http.Request, c call) {
	e, err := s.drive.Delete(r.Context(), *c.place, *c.item)
	if err != nil {
		s.changeFailed(w, r, err, c.item.Name)
		return
	}
	writeJSON(w, http.StatusOK, trashOf(e))
}

// trash lists what is in a place's trash, the most recently deleted first. What has been there for 30 days is removed
// for good before the list is made.
func (s *Server) trash(w http.ResponseWriter, r *http.Request, c call) {
	entries, err := s.drive.Trash(r.Context(), *c.place)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	out := make([]trashJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, trashOf(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// restore moves a file or folder out of the place's trash, back to the folder it was deleted from (or the top folder,
// when that folder is gone), and answers with it and the folders it is now in.
//
//	POST /api/places/{place}/trash/{item}/restore
func (s *Server) restore(w http.ResponseWriter, r *http.Request, c call) {
	it, err := s.drive.Restore(r.Context(), *c.place, *c.trashed)
	if errors.Is(err, drive.ErrGone) {
		s.refuse(w, r, http.StatusNotFound, "This is no longer in the trash on the Drive storage.")
		return
	}
	if err != nil {
		s.changeFailed(w, r, err, c.trashed.Name)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": itemOf(it), "path": pathOf(it.Above)})
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
