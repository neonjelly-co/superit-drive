package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// The handlers for shared folders as a whole: making one, deleting it into the trash and restoring it, and who
// manages it. Their routes are in routes.go with all the others.

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
	given, err := s.drive.GivenTo(r.Context(), c.member.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	for _, p := range shared {
		if p.Name == body.Name && access.Decide(who, access.List, accessPlace(p, given[p.ID])) {
			s.refuseTaken(w, r, &drive.Taken{Name: body.Name, Folder: true}, "There is already a shared folder called “"+body.Name+"”. Choose another name.")
			return
		}
	}
	p, err := s.drive.NewSharedFolder(r.Context(), body.Name, c.member.UserID)
	if err != nil {
		s.changeFailed(w, r, err, body.Name)
		return
	}
	given[p.ID] = string(access.Manager)
	writeJSON(w, http.StatusCreated, placeOf(who, p, accessPlace(p, given[p.ID])))
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
	given, err := s.drive.GivenTo(r.Context(), c.member.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	out := []deletedPlaceJSON{}
	for _, e := range all {
		if access.Decide(who, access.RestoreSharedFolder, accessPlace(e.Place, given[e.ID])) {
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

type personJSON struct {
	UserID      string `json:"userId"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	// Role is their role on the shared folder.
	Role access.Role `json:"role"`
	// Given is true when the role was given to them on this shared folder, and so can be taken away.
	Given bool `json:"given"`
	// Admin is true when they hold the admin role on the project, which makes them a manager of every shared folder.
	Admin bool `json:"admin"`
	// Former is true for someone given a role who is no longer in the member list.
	Former bool `json:"former,omitempty"`
}

// accessOf is everyone with a role on a shared folder: those given one there, and every project admin. Members by
// name, then anyone no longer in the member list.
func (s *Server) accessOf(r *http.Request, p index.Place) ([]personJSON, error) {
	grants, err := s.drive.Grants(r.Context(), p)
	if err != nil {
		return nil, err
	}
	given := map[string]string{}
	for _, g := range grants {
		given[g.Member] = g.Role
	}
	list := s.members.Current()
	out := []personJSON{}
	for _, m := range list.All() {
		role := access.RoleIn(personOf(m), accessPlace(p, given[m.UserID]))
		if role == access.NoRole {
			continue
		}
		_, isGiven := given[m.UserID]
		out = append(out, personJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: role,
			Given: isGiven, Admin: m.Role == members.RoleAdmin})
	}
	for _, g := range grants {
		if _, ok := list.Lookup(g.Member); !ok {
			out = append(out, personJSON{UserID: g.Member, Role: access.Role(g.Role), Given: true, Former: true})
		}
	}
	return out, nil
}

func (s *Server) writeAccess(w http.ResponseWriter, r *http.Request, p index.Place) {
	people, err := s.accessOf(r, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": people})
}

// whoHasAccess says who has access to a shared folder, and in which role.
//
//	GET /api/places/{place}/access
func (s *Server) whoHasAccess(w http.ResponseWriter, r *http.Request, c call) {
	s.writeAccess(w, r, *c.place)
}

// giveAccess gives a member a role on a shared folder, and answers with who has access now. Only "manager" can be
// given so far.
//
//	PUT /api/places/{place}/access/{member}   {"role": "manager"}
func (s *Server) giveAccess(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		Role access.Role `json:"role"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	m, ok := s.members.Current().Lookup(r.PathValue("member"))
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, "This person is not a member of the project.")
		return
	}
	if body.Role != access.Manager {
		s.refuse(w, r, http.StatusBadRequest, "Only managers can be added to a shared folder so far.")
		return
	}
	if err := s.drive.Give(r.Context(), *c.place, m.UserID, string(body.Role)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAccess(w, r, *c.place)
}

var errNoManagerLeft = errors.New("the shared folder would have no manager")

// takeAccess takes away the role given to someone on a shared folder, and answers with who has access now. A project
// admin stays a manager whatever was given. It is refused when nobody in the project would be left as a manager.
//
//	DELETE /api/places/{place}/access/{member}
func (s *Server) takeAccess(w http.ResponseWriter, r *http.Request, c call) {
	list := s.members.Current()
	err := s.drive.TakeAway(r.Context(), *c.place, r.PathValue("member"), func(left []index.Grant) error {
		given := map[string]string{}
		for _, g := range left {
			given[g.Member] = g.Role
		}
		for _, m := range list.All() {
			if access.RoleIn(personOf(m), accessPlace(*c.place, given[m.UserID])) == access.Manager {
				return nil
			}
		}
		return errNoManagerLeft
	})
	switch {
	case errors.Is(err, errNoManagerLeft):
		s.refuse(w, r, http.StatusConflict, "A shared folder needs a manager. Make someone else a manager first.")
	case err != nil:
		s.fail(w, r, err)
	default:
		s.writeAccess(w, r, *c.place)
	}
}
