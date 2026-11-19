package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
)

// The handlers for shared folders as a whole: making one, deleting it into the trash and restoring it. Who has access
// to it is in sharing.go. Their routes are in routes.go with all the others.

// newSharedFolder makes a shared folder, owned by the project, with the person asking as its first manager, and
// answers 201 with it.
//
//	POST /api/places   {"name": "Clients"}
//
// A name the person can already see among the shared folders is refused (409, as for a folder). One they cannot see
// is not: refusing it would tell them that a shared folder of that name exists. Both are then called the same, and
// the new one's top folder on the storage gets a number.
func (s *Server) newSharedFolder(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Name string `json:"name"`
	}
	if !s.readBody(w, r, &body) {
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
	for _, p := range shared {
		if p.Name == body.Name && access.Decide(who, access.List, s.accessPlace(p, nil, given[p.ID])) {
			s.refuseTaken(w, r, &drive.Taken{Name: body.Name, Folder: true}, "There is already a shared folder called “"+body.Name+"”. Choose another name.")
			return
		}
	}
	p, err := s.drive.NewSharedFolder(r.Context(), body.Name, c.member.UserID)
	if err != nil {
		s.changeFailed(w, r, err, body.Name)
		return
	}
	where, err := s.accessAt(r.Context(), p, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, placeOf(who, p, where))
}

type deletedPlaceJSON struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"` // "shared-folder"
	Name string `json:"name"`
	// Size is the sum of the files in it as far as the Drive knows them, its own trash included.
	Size int64 `json:"size"`
	// Deleted is when it went to the trash; Removed when the Drive removes it for good.
	Deleted time.Time `json:"deleted"`
	Removed time.Time `json:"removed"`
}

func deletedPlaceOf(e drive.DeletedSharedFolder) deletedPlaceJSON {
	return deletedPlaceJSON{ID: e.ID, Kind: string(e.Kind), Name: e.Name, Size: e.Size, Deleted: e.Deleted, Removed: e.Removed}
}

// deleteSharedFolder moves a shared folder, with everything in it, into the trash, and answers with it as the trash
// now holds it. Nobody can open it until a manager restores it; 30 days later it is removed for good.
//
//	DELETE /api/places/{place}
func (s *Server) deleteSharedFolder(w http.ResponseWriter, r *http.Request, c call) {
	e, err := s.drive.DeleteSharedFolder(r.Context(), *c.place)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, deletedPlaceOf(e))
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "Something in “"+c.place.Name+"” is being uploaded or changed right now. Try again when that is done.")
	default:
		s.failed(w, r, err)
	}
}

// deletedSharedFolders lists the shared folders in the trash that the person may restore (their managers and the
// project admins), the most recently deleted first. Those that have been there for 30 days are removed for good first.
//
//	GET /api/places/deleted
func (s *Server) deletedSharedFolders(w http.ResponseWriter, r *http.Request, c call) {
	all, err := s.drive.DeletedSharedFolders(r.Context())
	if err != nil {
		s.failed(w, r, err)
		return
	}
	given, err := s.drive.TopGrantsFor(r.Context(), c.member.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	out := []deletedPlaceJSON{}
	for _, e := range all {
		if access.Decide(who, access.RestoreSharedFolder, s.accessPlace(e.Place, nil, given[e.ID])) {
			out = append(out, deletedPlaceOf(e))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": out})
}

// restoreSharedFolder brings a shared folder out of the trash, with everything in it and the roles given on it as
// they were, and answers with it.
//
//	POST /api/places/{place}/restore
func (s *Server) restoreSharedFolder(w http.ResponseWriter, r *http.Request, c call) {
	p, err := s.drive.RestoreSharedFolder(r.Context(), *c.place)
	var taken *drive.Taken
	switch {
	case err == nil:
		where := *c.access
		where.Deleted = false
		writeJSON(w, http.StatusOK, placeOf(personOf(c.member), p, &where))
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "Something called “"+taken.Name+"” was put where this shared folder goes back to on the Drive storage, "+
			"so it cannot go back. Move that away on the storage first.")
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "“"+c.place.Name+"” is being changed right now. Try again in a moment.")
	default:
		s.failed(w, r, err)
	}
}
