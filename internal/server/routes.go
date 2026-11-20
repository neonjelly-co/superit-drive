package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// routes is every route the Drive answers. Add new ones here, each with the action it is. A route that takes a place
// must also be tried by an outsider in test/shared_folders_test.go, and a route that takes an item (or an upload in
// progress, which is about the folder it goes into) by someone with access to part of the place in
// test/sharing_test.go; a route under the public prefix by a visitor trying to get out of their link in
// test/links_test.go (a test here fails until it is).
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
		{pattern: "GET /api/members", action: access.Enter, serve: s.memberList},
		{pattern: "GET /api/places", action: access.Enter, serve: s.places},
		{pattern: "GET /api/usage", action: access.SeeUsage, serve: s.usage},
		// What others shared with the person that they reach only through it (shared_with_me.go): each item is decided
		// there by access.SharedWithMe.
		{pattern: "GET /api/shared-with-me", action: access.Enter, serve: s.sharedWithMe},
		// Finding files and folders by name across everything the person can see (search.go): where it looks, and each
		// result, are decided there through access.
		{pattern: "GET /api/search", action: access.Enter, serve: s.search},

		// Shared folders as a whole (shared.go).
		{pattern: "POST /api/places", action: access.MakeSharedFolder, serve: s.newSharedFolder},
		{pattern: "DELETE /api/places/{place}", action: access.DeleteSharedFolder, place: "place", serve: s.deleteSharedFolder},
		{pattern: "GET /api/places/deleted", action: access.Enter, serve: s.deletedSharedFolders},
		{pattern: "POST /api/places/{place}/restore", action: access.RestoreSharedFolder, place: "place", inTrash: true, serve: s.restoreSharedFolder},

		// Who has access to a shared folder as a whole, and to an item, and giving it (sharing.go). "everyone" is
		// everyone in the project; members are named by their user id, in a part of the address of their own.
		{pattern: "GET /api/places/{place}/access", action: access.SeeAccess, place: "place", serve: s.whoHasAccess},
		{pattern: "PUT /api/places/{place}/access/members/{member}", action: access.ManageAccess, place: "place", serve: s.giveAccess(toMember)},
		{pattern: "DELETE /api/places/{place}/access/members/{member}", action: access.ManageAccess, place: "place", serve: s.takeAccess(toMember)},
		{pattern: "PUT /api/places/{place}/access/everyone", action: access.ManageAccess, place: "place", serve: s.giveAccess(toEveryone)},
		{pattern: "DELETE /api/places/{place}/access/everyone", action: access.ManageAccess, place: "place", serve: s.takeAccess(toEveryone)},
		{pattern: "GET /api/places/{place}/items/{item}/access", action: access.SeeAccess, place: "place", item: "item", serve: s.whoHasAccess},
		{pattern: "PUT /api/places/{place}/items/{item}/access/members/{member}", action: access.Share, place: "place", item: "item", serve: s.giveAccess(toMember)},
		{pattern: "DELETE /api/places/{place}/items/{item}/access/members/{member}", action: access.Share, place: "place", item: "item", serve: s.takeAccess(toMember)},
		{pattern: "PUT /api/places/{place}/items/{item}/access/everyone", action: access.Share, place: "place", item: "item", serve: s.giveAccess(toEveryone)},
		{pattern: "DELETE /api/places/{place}/items/{item}/access/everyone", action: access.Share, place: "place", item: "item", serve: s.takeAccess(toEveryone)},
		// The public link on a file or folder (links.go): whoever may share it makes one, sees it and removes it.
		{pattern: "GET /api/places/{place}/items/{item}/link", action: access.ManageLink, place: "place", item: "item", serve: s.linkOf},
		{pattern: "POST /api/places/{place}/items/{item}/link", action: access.MakeLink, place: "place", item: "item", serve: s.makeLink},
		{pattern: "DELETE /api/places/{place}/items/{item}/link", action: access.ManageLink, place: "place", item: "item", serve: s.removeLink},

		// What is in a place.
		{pattern: "GET /api/places/{place}/items", action: access.List, place: "place", serve: s.items},
		{pattern: "GET /api/places/{place}/items/{item}/items", action: access.List, place: "place", item: "item", serve: s.items},
		{pattern: "POST /api/places/{place}/folders", action: access.MakeFolder, place: "place", serve: s.newFolder},
		{pattern: "POST /api/places/{place}/items/{item}/folders", action: access.MakeFolder, place: "place", item: "item", serve: s.newFolder},
		{pattern: "POST /api/places/{place}/items/{item}/rename", action: access.Rename, place: "place", item: "item", serve: s.rename},
		// Moves (moves.go): inside the place, or out of one's own My files into a shared folder, decided again where it
		// goes; and, before one, who would see it there.
		{pattern: "POST /api/places/{place}/items/{item}/move", action: access.Move, place: "place", item: "item", serve: s.move},
		{pattern: "GET /api/places/{place}/items/{item}/move", action: access.Move, place: "place", item: "item", serve: s.movePreview},
		{pattern: "DELETE /api/places/{place}/items/{item}", action: access.Delete, place: "place", item: "item", serve: s.delete},

		{pattern: "GET /api/places/{place}/trash", action: access.SeeTrash, place: "place", serve: s.trash},
		{pattern: "POST /api/places/{place}/trash/{item}/restore", action: access.Restore, place: "place", trashed: "item", serve: s.restore},

		// Transfers: what moves a file's bytes (transfers.go). A download is of a file, or of a folder (or some items
		// in it) as one zip; an upload is in one request, or resumable: started, then a part per request, then finished.
		{pattern: "GET /api/places/{place}/items/{item}/download", action: access.Download, place: "place", item: "item", serve: s.download},
		// Opening a file in the browser is reading it, as a download is: only the kinds on a fixed list are shown there
		// (opening.go), and anything else is sent as a download.
		{pattern: "GET /api/places/{place}/items/{item}/open", action: access.Download, place: "place", item: "item", serve: s.open},
		{pattern: "GET /api/places/{place}/zip", action: access.Download, place: "place", serve: s.zip},
		{pattern: "GET /api/places/{place}/items/{item}/zip", action: access.Download, place: "place", item: "item", serve: s.zip},
		{pattern: "POST /api/places/{place}/files", action: access.Upload, place: "place", serve: s.upload},
		{pattern: "POST /api/places/{place}/items/{item}/files", action: access.Upload, place: "place", item: "item", serve: s.upload},
		{pattern: "POST /api/places/{place}/uploads", action: access.Upload, place: "place", serve: s.startUpload},
		{pattern: "POST /api/places/{place}/items/{item}/uploads", action: access.Upload, place: "place", item: "item", serve: s.startUpload},
		{pattern: "GET /api/places/{place}/uploads/{upload}", action: access.Upload, place: "place", upload: "upload", serve: s.uploadState},
		{pattern: "PUT /api/places/{place}/uploads/{upload}", action: access.Upload, place: "place", upload: "upload", serve: s.uploadPart},
		{pattern: "POST /api/places/{place}/uploads/{upload}/finish", action: access.Upload, place: "place", upload: "upload", serve: s.finishUpload},
		{pattern: "DELETE /api/places/{place}/uploads/{upload}", action: access.Upload, place: "place", upload: "upload", serve: s.cancelUpload},

		// The public prefix: what a visitor reaches through a public link, without signing in (visitors.go). Only GET
		// (and HEAD); always a visitor here, whatever the request says about who sent it; the place is the one the link's
		// item is in, and an item named here must be that item or inside it. A file's bytes go out as they do to members.
		{pattern: "GET /s/{token}", action: access.SeeThroughLink, link: "token", serve: s.visitorPage},
		{pattern: "GET /s/{token}/items/{item}", action: access.SeeThroughLink, link: "token", item: "item", serve: s.visitorPage},
		{pattern: "GET /s/{token}/items/{item}/download", action: access.DownloadThroughLink, link: "token", item: "item", serve: s.download},
		{pattern: "GET /s/{token}/items/{item}/open", action: access.DownloadThroughLink, link: "token", item: "item", serve: s.open},
		{pattern: "GET /s/{token}/items/{item}/zip", action: access.DownloadThroughLink, link: "token", item: "item", serve: s.zip},
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
	Kind string `json:"kind"` // "my-files" or "shared-folder"
	// Name is a shared folder's name; "My files" has none.
	Name string `json:"name,omitempty"`
	// Role is the person's role on a shared folder as a whole: "manager", "editor" or "viewer".
	Role string `json:"role,omitempty"`
	// Can is every action the person may do in the place's top folder (access.InAPlace), so the interface offers only
	// those. The Drive decides again on every request.
	Can []access.Action `json:"can"`
}

func placeOf(who access.Person, p index.Place, where *access.Place) placeJSON {
	j := placeJSON{ID: p.ID, Kind: string(p.Kind), Name: p.Name, Can: can(who, where)}
	if p.Kind == index.SharedFolder {
		j.Role = string(access.RoleAt(who, where))
	}
	return j
}

// can is every action the person may do at where, in the order access.InAPlace lists them.
func can(who access.Person, where *access.Place) []access.Action {
	out := []access.Action{}
	for _, a := range access.InAPlace {
		if access.Decide(who, a, where) {
			out = append(out, a)
		}
	}
	return out
}

// places lists the places the person can open: their own "My files" (made the first time they ask), then every shared
// folder they have a role on as a whole, by name. Project admins have one on every shared folder. A shared folder the
// person has no role on is not listed at all, even when something in it was shared with them.
func (s *Server) places(w http.ResponseWriter, r *http.Request, c call) {
	mine, err := s.drive.MyFiles(r.Context(), c.member)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	shared, err := s.drive.SharedFolders(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	given, err := s.drive.TopGrantsFor(r.Context(), c.member.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	out := []placeJSON{}
	for _, p := range append([]index.Place{mine}, shared...) {
		where := s.accessPlace(p, nil, given[p.ID])
		if access.Decide(who, access.List, where) {
			out = append(out, placeOf(who, p, where))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out})
}

type memberJSON struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"` // on the project: "admin" or "member"
}

// memberList lists the project's members, by name, as the member list has them: for choosing whom to give access.
// Every member may see it; it says nothing about anyone's files.
func (s *Server) memberList(w http.ResponseWriter, _ *http.Request, _ call) {
	out := []memberJSON{}
	for _, m := range s.members.Current().All() {
		out = append(out, memberJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: string(m.Role)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
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
	// Opens is true for a file that opens in the browser (…/open shows it); any other file is downloaded.
	Opens bool `json:"opens,omitempty"`
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
		j.Opens = opensInTheBrowser(it.Name)
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

// listedJSON is an item in a listing: what the person may do with it, and who can see it.
type listedJSON struct {
	itemJSON
	// Can is every action the person may do with it (access.InAPlace), so the interface offers only those.
	Can []access.Action `json:"can"`
	// SeenBy is who can see it, for the list's "who can see it".
	SeenBy seenByJSON `json:"seenBy"`
}

type seenByJSON struct {
	// Everyone is true when everyone in the project can see it.
	Everyone bool `json:"everyone"`
	// Members is, when Everyone is false, every member in the member list who can see it, by user id: its owner or its
	// shared folder's managers and the project admins, and everyone it was shared with, on it or on a folder above.
	Members []string `json:"members"`
	// Link is true when anyone with a public link can see it: one on it, or on a folder above it, that works now.
	Link bool `json:"link"`
}

// seenBy is who can see what is at where: anyone with a link (from where.Links), and everyone, or each member access
// gives a role there.
func seenBy(list *members.List, where *access.Place) seenByJSON {
	out := seenByJSON{Members: []string{}, Link: access.ByLink(where)}
	if access.ToEveryone(where) != access.NoRole {
		out.Everyone = true
		return out
	}
	for _, m := range list.All() {
		if access.RoleAt(personOf(m), where) != access.NoRole {
			out.Members = append(out.Members, m.UserID)
		}
	}
	return out
}

// recheckWait is how long a listing asked with ?wait waits for the look at its folder on the storage.
const recheckWait = 10 * time.Second

// items lists what is in a folder of a place: each item with what the person may do with it and who can see it, the
// path down to the folder, and what they may do in it.
//
// The path holds only the folders the person can open: for someone given a folder deep inside a place, it starts at
// that folder, and the names of the folders above it are never sent to them. fromTop says whether it starts at the
// place's top folder (it is empty there).
//
// The list comes from the index, at once. Opening a folder also has it looked at on the storage in the background
// (drive.Recheck), which brings the index in step with changes made there directly; checking says that look is under
// way. The interface then asks once more with ?wait, which waits (at most recheckWait) until the look is done, starts no
// other, and answers the list as the index has it then, with checking still true only when the look is not done yet.
//
//	GET /api/places/{place}/items[?wait]
//	GET /api/places/{place}/items/{folder}/items[?wait]
func (s *Server) items(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "This is a file, not a folder.")
	if !ok {
		return
	}
	var checking bool
	if r.URL.Query().Has("wait") {
		checking = s.drive.Rechecked(r.Context(), *c.place, folder, recheckWait)
		if r.Context().Err() != nil {
			return
		}
		// The look may have found the folder gone: from now on it answers as one that is not there.
		if folder != nil {
			if _, err := s.drive.Item(r.Context(), *c.place, folder.ID); errors.Is(err, index.ErrNoItem) {
				s.refuse(w, r, http.StatusNotFound, notHere)
				return
			}
		}
	} else {
		checking = s.drive.Recheck(*c.place, folder)
	}
	inside, err := s.drive.GrantsInside(r.Context(), *c.place, folder)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	givenOn := map[int64][]access.Grant{}
	for _, g := range inside {
		givenOn[g.Item] = append(givenOn[g.Item], accessGrant(g))
	}
	var folders []index.Item
	if folder != nil {
		folders = pathTo(*folder)
	}
	// The public links on the folders down to this one reach everything in it; those on its items, only them.
	here, err := s.withLinksOn(r.Context(), c.access, idsOf(folders))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	links, err := s.drive.LinksInside(r.Context(), *c.place, folder)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	linkOn := map[int64][]access.Link{}
	for _, l := range s.accessLinks(links) {
		linkOn[l.Item] = append(linkOn[l.Item], l)
	}
	who := personOf(c.member)
	list := s.members.Current()
	open, fromTop := openPath(who, c.access, folders)
	head, err := json.Marshal(map[string]any{
		"path": pathOf(open), "fromTop": fromTop, "role": access.RoleAt(who, c.access), "can": can(who, c.access),
		"checking": checking,
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}

	// A folder can hold thousands of items (10,000 put on the storage directly, more uploaded through the Drive): they
	// are read from the index a page at a time and sent as they are made, so a list costs the Drive the same little
	// memory whatever its length, however many are asked for at once.
	first, more, err := s.drive.ChildrenPage(r.Context(), *c.place, folder, nil, listPage)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	out := bufio.NewWriterSize(w, 32<<10)
	_, _ = out.Write(head[:len(head)-1])
	_, _ = out.WriteString(`,"items":[`)
	sent := 0
	for page := first; ; {
		for _, it := range page {
			where := here.Below(it.ID, givenOn[it.ID]).WithLinks(linkOn[it.ID]...)
			item, err := json.Marshal(listedJSON{itemJSON: itemOf(it), Can: can(who, where), SeenBy: seenBy(list, where)})
			if err != nil {
				s.cutShort(r, err)
			}
			if sent > 0 {
				_ = out.WriteByte(',')
			}
			if _, err := out.Write(item); err != nil {
				return // the person has gone
			}
			sent++
		}
		if !more {
			break
		}
		last := page[len(page)-1]
		if page, more, err = s.drive.ChildrenPage(r.Context(), *c.place, folder, &last, listPage); err != nil {
			s.cutShort(r, err)
		}
	}
	_, _ = out.WriteString("]}\n")
	_ = out.Flush()
}

// listPage is how many items of a folder a listing reads from the index at a time.
const listPage = 500

// cutShort ends an answer that has begun, after a failure that leaves it unfinished: the connection is ended, so the
// browser sees it failed rather than take a part for the whole.
func (s *Server) cutShort(r *http.Request, err error) {
	if r.Context().Err() == nil {
		s.log.Error("an answer was cut short", "route", r.Pattern, "error", err)
	}
	panic(http.ErrAbortHandler)
}

// openPath is the part of a path of folders (from the top of a place down) that the person can open. where must be
// about the last of them, or something inside it, and hold what was given on all of them. Access only widens down
// the tree, so it is the folders from the first one they can open down to the last; fromTop says whether they can open
// the place's top folder, and so all of them.
func openPath(who access.Person, where *access.Place, folders []index.Item) (open []index.Item, fromTop bool) {
	fromTop = access.Decide(who, access.List, where.Up(0))
	for i := range folders {
		if fromTop || access.Decide(who, access.List, where.Up(i+1)) {
			return folders[i:], fromTop
		}
	}
	return nil, fromTop
}

// openFolders is openPath for a path of folders in place p that no decision has gathered the access for yet.
func (s *Server) openFolders(ctx context.Context, who access.Person, p index.Place, folders []index.Item) ([]folderJSON, error) {
	where, err := s.accessAt(ctx, p, idsOf(folders))
	if err != nil {
		return nil, err
	}
	open, _ := openPath(who, where, folders)
	return pathOf(open), nil
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

var errCannotGoBack = errors.New("the person may not put it back where it would go")

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
	case errors.Is(err, index.ErrNoItem):
		// It went to the trash, or somewhere else, after guard found it: the same answer guard gives from now on.
		s.refuse(w, r, http.StatusNotFound, notHere)
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

// trashOf describes an item of the trash for the person: From holds only the folders they can open. whole says they
// can open the place's top folder, and so every folder in it.
func (s *Server) trashOf(ctx context.Context, who access.Person, p index.Place, e drive.TrashEntry, whole bool) (trashJSON, error) {
	from := pathOf(e.From)
	if !whole {
		var err error
		if from, err = s.openFolders(ctx, who, p, e.From); err != nil {
			return trashJSON{}, err
		}
	}
	j := trashJSON{ID: e.ID, Name: e.Name, Kind: "file", Size: e.Size, Deleted: e.Deleted, Removed: e.Removed,
		Replaced: e.Replaced, From: from}
	if e.Folder {
		j.Kind = "folder"
	}
	return j, nil
}

// delete moves a file or folder, with everything in it, into the place's trash, and answers with it as the trash
// now holds it.
//
//	DELETE /api/places/{place}/items/{item}
func (s *Server) delete(w http.ResponseWriter, r *http.Request, c call) {
	e, err := s.drive.Delete(r.Context(), *c.place, *c.item, c.member.UserID)
	if err != nil {
		s.changeFailed(w, r, err, c.item.Name)
		return
	}
	who := personOf(c.member)
	j, err := s.trashOf(r.Context(), who, *c.place, e, access.Decide(who, access.List, c.access.Up(0)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// trash lists what is in a place's trash, the most recently deleted first. What has been there for 30 days is removed
// for good before the list is made. Only those who may put things into the place's top folder may see its trash
// (access.SeeTrash); someone given part of a place restores what they deleted there by its address.
func (s *Server) trash(w http.ResponseWriter, r *http.Request, c call) {
	entries, err := s.drive.Trash(r.Context(), *c.place)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	// Only those who may put things into the top folder see the trash (access.SeeTrash), so they can open every folder.
	who := personOf(c.member)
	whole := access.Decide(who, access.List, c.access)
	out := make([]trashJSON, 0, len(entries))
	for _, e := range entries {
		j, err := s.trashOf(r.Context(), who, *c.place, e, whole)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// restore moves a file or folder out of the place's trash, back to the folder it was deleted from (or the top folder,
// when that folder is gone), and answers with it and the folders it is now in that the person can open. Whoever could
// have deleted it from where it goes back to may, if they may see the place's whole trash or put it there themselves.
//
// guard decided on the folder it goes back to as the index had it. The folder it actually goes into is decided on
// again, by the same rule, just before it moves: that folder can have gone (to the trash, or from the storage, and
// then it goes to the top folder instead) or moved since, and nobody puts anything into a folder they may not add to.
//
//	POST /api/places/{place}/trash/{item}/restore
func (s *Server) restore(w http.ResponseWriter, r *http.Request, c call) {
	who := personOf(c.member)
	it, err := s.drive.Restore(r.Context(), *c.place, *c.trashed, func(into *index.Item) error {
		var path []index.Item
		if into != nil {
			path = pathTo(*into)
		}
		where, err := s.accessAt(r.Context(), *c.place, append(idsOf(path), c.trashed.ID))
		if err != nil {
			return err
		}
		where.DeletedBy = c.trashed.DeletedBy
		if !access.Decide(who, access.Restore, where) {
			return errCannotGoBack
		}
		return nil
	})
	if errors.Is(err, errCannotGoBack) {
		s.refuse(w, r, http.StatusConflict, "This cannot go back: the folder it was deleted from is gone, or you can no longer add to it.")
		return
	}
	if errors.Is(err, drive.ErrGone) {
		s.refuse(w, r, http.StatusNotFound, "This is no longer in the trash on the Drive storage.")
		return
	}
	if err != nil {
		s.changeFailed(w, r, err, c.trashed.Name)
		return
	}
	path, err := s.openFolders(r.Context(), personOf(c.member), *c.place, it.Above)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": itemOf(it), "path": path})
}

// failed answers a request whose work went wrong: on the storage, or in the Drive itself.
func (s *Server) failed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return // the person has gone: see fail
	}
	if errors.Is(err, drive.ErrPlaceGone) {
		// The shared folder went to the trash after guard found it: the same answer guard gives from now on.
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
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
