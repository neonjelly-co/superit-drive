package server

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Sharing outside the project switched off, and what a project admin oversees. Their routes are in routes.go with all
// the others; guard has decided before any of these runs (access.SwitchSharingOutside: a project admin for the whole
// project, a shared folder's managers for it; access.Oversee: a project admin).
//
// The switches live in the Drive's index, not in its settings: a project admin switches sharing outside the project
// (public links and guests) off for the whole project, a manager for one shared folder, and either back on. While it is
// off somewhere, the links and invitations there stay as they are: still listed to whoever may share their items, and
// removable, but no link opens and no guest gets in (access.Place.NoSharingOutside); a guest stays signed in, and is
// told. Switched back on, they work again as they were.
//
// Overseeing tells a project admin what leaves the project and how much space each person and each shared folder
// uses: names of items shared outside the project, never what is in anything, and nothing else of anyone's My files.

// outsideJSON is a switch of sharing outside the project: on or off, and, while off, who switched it off (while they
// are a member) and when.
type outsideJSON struct {
	On     bool        `json:"on"`
	By     *personJSON `json:"by,omitempty"`
	Former bool        `json:"former,omitempty"`
	At     *time.Time  `json:"at,omitempty"`
}

func (s *Server) outsideJSONOf(sw index.Switch) outsideJSON {
	if !sw.Off() {
		return outsideJSON{On: true}
	}
	at := sw.At
	j := outsideJSON{At: &at}
	if m, ok := s.members.Current().Lookup(sw.By); ok {
		j.By = personJSONOf(m)
	} else {
		j.Former = true
	}
	return j
}

// outsideHere says whether sharing outside the project is on in the place p: "on", or where it was switched off,
// "project" (for the whole project) or "folder" (for this shared folder). With both off, "project": switching the
// shared folder's own back on would not be enough.
func outsideHere(p index.Place) string {
	switch {
	case p.ProjectOutsideOff.Off():
		return "project"
	case p.OutsideOff.Off():
		return "folder"
	}
	return "on"
}

const (
	// guestUnavailable is what a guest is told about something given to them while sharing outside the project is off
	// where it is (%s: the project's name).
	guestUnavailable = "This is not available right now: %s has switched off sharing outside the project. It works again " +
		"once they switch it back on. Ask whoever invited you."
	// visitorUnavailable is a visitor's page for a link that would open, while sharing outside the project is off.
	visitorUnavailable = "Sharing outside the organisation that shared this is switched off for now, so the link opens " +
		"nothing. It works again once they switch it back on. Ask whoever sent it to you."
)

// switchedOff answers someone refused only because sharing outside the project is switched off where they asked
// (access.SwitchedOff): told so, rather than "not here", since with it on they would be let in, and so learn nothing
// they could not see then. A member who may share it (making a link, inviting a guest) gets 409 with "outside", where
// it was switched off ("project" or "folder"); a guest 403 with "unavailable": true; a visitor, whose link's token
// matched, a page that says the link is not available now.
func (s *Server) switchedOff(w http.ResponseWriter, r *http.Request, c call) {
	switch {
	case c.link != nil:
		s.renderVisitor(w, http.StatusForbidden, visitorView{Heading: "This link is not available", Message: visitorUnavailable})
	case c.guest != nil:
		writeJSON(w, http.StatusForbidden, map[string]any{"unavailable": true,
			"error": fmt.Sprintf(guestUnavailable, s.project)})
	default:
		where := "for this shared folder, so no public link can be made and no guest invited here. Its managers can switch it back on."
		if outsideHere(*c.place) == "project" {
			where = "for the whole project, so no public link can be made and no guest invited. A project admin can switch it back on."
		}
		writeJSON(w, http.StatusConflict, map[string]any{"outside": outsideHere(*c.place),
			"error": "Sharing outside the project is switched off " + where})
	}
}

// readSwitch reads {"on": true | false}. ok is false when the person was already answered.
func (s *Server) readSwitch(w http.ResponseWriter, r *http.Request) (on, ok bool) {
	var body struct {
		On *bool `json:"on"`
	}
	if !s.readBody(w, r, &body) {
		return false, false
	}
	if body.On == nil {
		s.refuse(w, r, http.StatusBadRequest, `Say whether sharing outside the project is on: {"on": true} or {"on": false}.`)
		return false, false
	}
	return *body.On, true
}

// switchProjectOutside switches sharing outside the project (public links and guests) off for the whole project, or
// back on, and answers with the switch as it is now. Every member sees it (GET /api/me).
//
//	PUT /api/sharing-outside   {"on": false | true}
//	200 {"sharingOutside": {"on", "by", "former", "at"}}
func (s *Server) switchProjectOutside(w http.ResponseWriter, r *http.Request, c call) {
	on, ok := s.readSwitch(w, r)
	if !ok {
		return
	}
	if _, err := s.drive.SwitchOutside(r.Context(), nil, !on, c.member.UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	sw, err := s.drive.ProjectOutsideOff(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sharingOutside": s.outsideJSONOf(sw)})
}

// switchFolderOutside switches sharing outside the project off for one shared folder, or back on, and answers with the
// shared folder as the places list gives it, its own switch included. Switched on for the folder, it is still off there
// while it is off for the whole project.
//
//	PUT /api/places/{place}/sharing-outside   {"on": false | true}
//	200 {"id", "kind", "name", "role", "can", "sharingOutside": {"on", "by", "former", "at"}, "outside"}
func (s *Server) switchFolderOutside(w http.ResponseWriter, r *http.Request, c call) {
	on, ok := s.readSwitch(w, r)
	if !ok {
		return
	}
	if _, err := s.drive.SwitchOutside(r.Context(), c.place, !on, c.member.UserID); err != nil {
		s.failed(w, r, err) // a shared folder that went to the trash meanwhile: not here
		return
	}
	p, err := s.drive.Place(r.Context(), c.place.ID)
	if errors.Is(err, index.ErrNoPlace) {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	where, err := s.accessAt(r.Context(), p, nil)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.placeOf(personOf(c.member), p, where))
}

// outsideShareJSON is one thing shared outside the project, as a project admin oversees it: a public link or a guest's
// invitation, the item it is on by name (never the folders above it, nor anything in it), the place it is in, who
// shared it, with whom, when it was last used and how often, and whether it lets anyone in now.
type outsideShareJSON struct {
	// Kind is "link" for a public link (anyone with its address), "guest" for a guest's invitation.
	Kind  string           `json:"kind"`
	Item  outsideItemJSON  `json:"item"`
	Place outsidePlaceJSON `json:"place"`
	// SharedBy is who made the link or invited the guest, while they are a member; Former says they have left.
	SharedBy *personJSON `json:"sharedBy,omitempty"`
	Former   bool        `json:"former,omitempty"`
	// Guest is, for an invitation, the guest: their address. Role is what it lets them do ("viewer": open and download;
	// "editor": also upload, rename, move and delete inside it); a view link only lets its visitors see and download
	// ("viewer"). LinkKind is a link's kind: "view", or "upload-only" (without a role: its visitors only send files in,
	// and see nothing).
	Guest    *guestJSON  `json:"guest,omitempty"`
	Role     access.Role `json:"role,omitempty"`
	LinkKind string      `json:"linkKind,omitempty"`
	// Since is when it was made or given; Until when an invitation ends, if it does.
	Since time.Time  `json:"since"`
	Until *time.Time `json:"until,omitempty"`
	// Uses is how often it was used, and Used when last.
	Uses int64      `json:"uses"`
	Used *time.Time `json:"used,omitempty"`
	// Works says whether it lets anyone in now: not while sharing outside the project is off there, an invitation's end
	// date has passed, or, in a My files, whoever made it can no longer share the item.
	Works bool `json:"works"`
}

type outsideItemJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "file" or "folder"
}

// outsidePlaceJSON is the place something shared outside is in: a shared folder by name, or whose My files it is.
type outsidePlaceJSON struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	// Name is a shared folder's name.
	Name string `json:"name,omitempty"`
	// Owner is, for a My files, its owner while they are a member; OwnerLeft says they have left (and it is held).
	Owner     *personJSON `json:"owner,omitempty"`
	OwnerLeft bool        `json:"ownerLeft,omitempty"`
	// Outside says whether sharing outside the project is on there: "on", "project" or "folder" (switched off there).
	Outside string `json:"outside"`
}

// oversightPage is how many links, then invitations, the list of what is shared outside reads from the index at a time.
const oversightPage = 200

// sharedOutside lists everything shared outside the project, for a project admin: every public link, then every guest's
// invitation, on an item that is in the Drive (not in the trash, not in a shared folder in the trash, not gone from the
// storage), each by id. Items in anyone's My files are among them, by name only: what was shared outside the project is
// what the admin oversees, and nothing else of a My files is named, opened or listed.
//
// A project can hold any number of them, so the list is read from the index a page at a time and sent as it is made,
// as a folder's listing is.
//
//	GET /api/oversight/shared-outside
//	200 {"sharingOutside": {"on", …}, "items": [{"kind": "link" | "guest", "item": {"id", "name", "kind"},
//	     "place": {"id", "kind", "name", "owner", "ownerLeft", "outside"}, "sharedBy", "former", "guest", "role",
//	     "since", "until", "uses", "used", "works"}]}
func (s *Server) sharedOutside(w http.ResponseWriter, r *http.Request, _ call) {
	ctx := r.Context()
	sw, err := s.drive.ProjectOutsideOff(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	head, err := json.Marshal(map[string]any{"sharingOutside": s.outsideJSONOf(sw)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The first page is read before anything is sent, so that a failure there is answered as one.
	links, moreLinks, err := s.drive.LinksAfter(ctx, 0, oversightPage)
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
	send := func(v outsideShareJSON) bool {
		b, err := json.Marshal(v)
		if err != nil {
			s.cutShort(r, err)
		}
		if sent > 0 {
			_ = out.WriteByte(',')
		}
		sent++
		_, err = out.Write(b)
		return err == nil
	}
	seen := placesSeen{s: s, m: map[int64]*index.Place{}}
	for {
		for _, l := range links {
			e, ok, err := s.outsideLink(ctx, &seen, l)
			if err != nil {
				s.cutShort(r, err)
			}
			if ok && !send(e) {
				return // the person has gone
			}
		}
		if !moreLinks {
			break
		}
		if links, moreLinks, err = s.drive.LinksAfter(ctx, links[len(links)-1].ID, oversightPage); err != nil {
			s.cutShort(r, err)
		}
	}
	var after int64
	for {
		invitations, more, err := s.drive.InvitationsAfter(ctx, after, oversightPage)
		if err != nil {
			s.cutShort(r, err)
		}
		for _, in := range invitations {
			e, ok, err := s.outsideInvitation(ctx, &seen, in)
			if err != nil {
				s.cutShort(r, err)
			}
			if ok && !send(e) {
				return
			}
		}
		if !more {
			break
		}
		after = invitations[len(invitations)-1].ID
	}
	_, _ = out.WriteString("]}\n")
	_ = out.Flush()
}

// placesSeen keeps the places one list has read, by id (nil: not there, or in the trash): a project has few.
type placesSeen struct {
	s *Server
	m map[int64]*index.Place
}

func (ps *placesSeen) get(ctx context.Context, id int64) (*index.Place, error) {
	if p, ok := ps.m[id]; ok {
		return p, nil
	}
	p, err := ps.s.drive.Place(ctx, id)
	if errors.Is(err, index.ErrNoPlace) {
		ps.m[id] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ps.m[id] = &p
	return &p, nil
}

// liveShareAt is where something shared outside is: its place and its item, live, and the access given on the way down
// to it. ok is false when it is not in the Drive now (in the trash, in a shared folder in the trash, gone from the
// storage).
func (s *Server) liveShareAt(ctx context.Context, seen *placesSeen, placeID, itemID int64) (p *index.Place, it index.Item, where *access.Place, ok bool, err error) {
	if p, err = seen.get(ctx, placeID); err != nil || p == nil {
		return nil, index.Item{}, nil, false, err
	}
	it, err = s.drive.Item(ctx, *p, itemID)
	if errors.Is(err, index.ErrNoItem) {
		return nil, index.Item{}, nil, false, nil
	}
	if err != nil {
		return nil, index.Item{}, nil, false, err
	}
	where, err = s.accessAt(ctx, *p, idsOf(pathTo(it)))
	return p, it, where, err == nil, err
}

func (s *Server) outsideShareOf(kind string, p index.Place, it index.Item, by string, list *members.List) outsideShareJSON {
	e := outsideShareJSON{Kind: kind, Item: outsideItemJSON{ID: it.ID, Name: it.Name, Kind: "file"},
		Place: outsidePlaceJSON{ID: p.ID, Kind: string(p.Kind), Outside: outsideHere(p)}}
	if it.Folder {
		e.Item.Kind = "folder"
	}
	switch p.Kind {
	case index.SharedFolder:
		e.Place.Name = p.Name
	case index.MyFiles:
		if m, ok := list.Lookup(p.Owner); ok {
			e.Place.Owner = personJSONOf(m)
		} else {
			e.Place.OwnerLeft = true
		}
	}
	if m, ok := list.Lookup(by); ok {
		e.SharedBy = personJSONOf(m)
	} else {
		e.Former = true
	}
	return e
}

func (s *Server) outsideLink(ctx context.Context, seen *placesSeen, l index.Link) (outsideShareJSON, bool, error) {
	p, it, where, ok, err := s.liveShareAt(ctx, seen, l.Place, l.Item)
	if !ok || err != nil {
		return outsideShareJSON{}, false, err
	}
	list := s.members.Current()
	e := s.outsideShareOf("link", *p, it, l.MadeBy, list)
	e.LinkKind, e.Since, e.Uses = l.Kind, l.Made, l.Uses
	if l.Kind == index.ViewLink {
		e.Role = access.Viewer
	}
	e.Works = linkWorks(l, where, list)
	if !l.Used.IsZero() {
		used := l.Used
		e.Used = &used
	}
	return e, true, nil
}

func (s *Server) outsideInvitation(ctx context.Context, seen *placesSeen, in index.Invitation) (outsideShareJSON, bool, error) {
	p, it, where, ok, err := s.liveShareAt(ctx, seen, in.Place, in.Item)
	if !ok || err != nil {
		return outsideShareJSON{}, false, err
	}
	list := s.members.Current()
	e := s.outsideShareOf("guest", *p, it, in.By, list)
	e.Guest = &guestJSON{ID: in.Guest, Address: in.GuestAddress}
	e.Role, e.Since, e.Uses = access.Role(in.Role), in.Given, in.Uses
	e.Works = invitationWorks(where, accessGrant(in.Grant, list))
	if !in.Ends.IsZero() {
		ends := in.Ends
		e.Until = &ends
	}
	if !in.Used.IsZero() {
		used := in.Used
		e.Used = &used
	}
	return e, true, nil
}

// spaceJSON is the space one person's My files, or one shared folder, uses.
type spaceJSON struct {
	// For a person: who, while they are a member; for one who has left (their My files held), Former, and Folder, the name
	// of their top folder on the storage (their email address when they first opened the Drive).
	Person *personJSON `json:"person,omitempty"`
	Former bool        `json:"former,omitempty"`
	Folder string      `json:"folder,omitempty"`
	// For a shared folder: its id and name, and whether it is in the trash.
	ID      int64  `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	// Used is the sum of the sizes of the files in it, its trash included, as the Drive knows them: about, as the
	// sidebar's meter.
	Used int64 `json:"used"`
}

// space says how much space each person's My files and each shared folder uses, for a project admin: every member (0
// for one who has never opened the Drive), everyone who has left with files held, and every shared folder (those in the
// trash too), the largest first; and the whole, as the sidebar's meter. Sums of sizes only: nothing in anything is
// named.
//
//	GET /api/oversight/space
//	200 {"used", "capacity", "people": [{"person" | "former", "folder", "used"}],
//	     "sharedFolders": [{"id", "name", "deleted", "used"}]}
func (s *Server) space(w http.ResponseWriter, r *http.Request, _ call) {
	ctx := r.Context()
	used, err := s.drive.UsedByPlace(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	all, err := s.drive.Places(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list := s.members.Current()
	people, shared := []spaceJSON{}, []spaceJSON{}
	var total int64
	mine := map[string]bool{}
	for _, p := range all {
		total += used[p.ID]
		switch p.Kind {
		case index.MyFiles:
			mine[p.Owner] = true
			e := spaceJSON{Used: used[p.ID]}
			if m, ok := list.Lookup(p.Owner); ok {
				e.Person = personJSONOf(m)
			} else {
				e.Former, e.Folder = true, path.Base(p.Folder)
			}
			people = append(people, e)
		case index.SharedFolder:
			shared = append(shared, spaceJSON{ID: p.ID, Name: p.Name, Deleted: !p.Deleted.IsZero(), Used: used[p.ID]})
		}
	}
	for _, m := range list.All() {
		if !mine[m.UserID] {
			people = append(people, spaceJSON{Person: personJSONOf(m)})
		}
	}
	nameOf := func(e spaceJSON) string {
		if e.Person != nil {
			return cmp.Or(e.Person.DisplayName, e.Person.Email)
		}
		return cmp.Or(e.Name, e.Folder)
	}
	largest := func(a, b spaceJSON) int {
		return cmp.Or(cmp.Compare(b.Used, a.Used), strings.Compare(index.SearchKey(nameOf(a)), index.SearchKey(nameOf(b))),
			cmp.Compare(a.ID, b.ID))
	}
	slices.SortStableFunc(people, largest)
	slices.SortStableFunc(shared, largest)
	out := map[string]any{"used": total, "people": people, "sharedFolders": shared}
	if s.capacity > 0 {
		out["capacity"] = s.capacity
	}
	writeJSON(w, http.StatusOK, out)
}
