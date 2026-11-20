package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// The handlers for who has access, and for giving it: on a shared folder as a whole (its managers decide) and on any
// file or folder (whoever may share it: its owner, an editor, a manager). Their routes are in routes.go with all the
// others; guard has decided before any of these runs.

// accessJSON is one line of "who can see this": someone with access, the role they have by it, and where it comes
// from.
type accessJSON struct {
	// Everyone is true for everyone in the project; Link for anyone with the public link on it, or on a folder above it,
	// while it works: through a view link (LinkKind "view") as viewer, who sees and downloads; through an upload-only
	// link on the item itself (LinkKind "upload-only", and no role), who sends files in and sees nothing. Guest is for a
	// guest invited by address. Otherwise the line is about one member, by user id, with their email address and name
	// from the member list.
	Everyone    bool        `json:"everyone,omitempty"`
	Link        bool        `json:"link,omitempty"`
	LinkKind    string      `json:"linkKind,omitempty"`
	Guest       *guestJSON  `json:"guest,omitempty"`
	UserID      string      `json:"userId,omitempty"`
	Email       string      `json:"email,omitempty"`
	DisplayName string      `json:"displayName,omitempty"`
	Role        access.Role `json:"role,omitempty"`
	// Until is when it ends, for access given with an end date (an invitation, or a guest's that is now a member's).
	Until *time.Time `json:"until,omitempty"`
	// For an invitation, to whoever may share the item: whether it lets its guest in now (not after its end date, not
	// once whoever gave it in a My files can no longer share it, not where sharing outside the project is off), and how
	// often its guest used it, and when last.
	Works *bool      `json:"works,omitempty"`
	Uses  *int64     `json:"uses,omitempty"`
	Used  *time.Time `json:"used,omitempty"`
	// Owner is true for the member whose "My files" it is; Admin for a project admin, a manager of every shared folder.
	// Neither was given, and neither can be taken away.
	Owner bool `json:"owner,omitempty"`
	Admin bool `json:"admin,omitempty"`
	// Given is true for access given right here: on this item, or, in a shared folder's own list, on it as a whole.
	// Whoever may share it can change it or take it away here.
	Given bool `json:"given"`
	// Inherited is true for access given on a folder above, or on the shared folder as a whole. From is that folder
	// (id 0 for the shared folder itself), when the person asking can open it; nil when they cannot, so they never
	// learn its name.
	Inherited bool        `json:"inherited,omitempty"`
	From      *folderJSON `json:"from,omitempty"`
	// Former is true for access given to someone no longer in the member list.
	Former bool `json:"former,omitempty"`
}

// accessList is who has access to the item at the end of path (the folders above it, then the item) in place p, or to
// the place as a whole when path is empty, as the person who asks may see it: first who has it by the place itself
// (the owner of a My files; on a shared folder, every project admin), then what was given, from the top down, each
// in the order it was given, and last anyone with a public link on the way down that works now. Grants that count for
// nothing (access.Counts) are left out.
func (s *Server) accessList(ctx context.Context, who access.Person, p index.Place, path []index.Item) ([]accessJSON, error) {
	grants, err := s.drive.GrantsOn(ctx, p, idsOf(path))
	if err != nil {
		return nil, err
	}
	links, err := s.drive.LinksOn(ctx, idsOf(path))
	if err != nil {
		return nil, err
	}
	return s.accessLines(who, p, path, grants, links), nil
}

// accessLines is accessList with the grants on the place's top folder and on path, and the links on path, already
// gathered: also for where an item would be, before a move.
func (s *Server) accessLines(who access.Person, p index.Place, path []index.Item, grants []index.Grant, links []index.Link) []accessJSON {
	ids := idsOf(path)
	grants = slices.Clone(grants)
	where := s.accessPlace(p, ids, grants)
	list := s.members.Current()
	out := []accessJSON{}
	switch p.Kind {
	case index.MyFiles:
		if m, ok := list.Lookup(p.Owner); ok {
			out = append(out, accessJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: access.Owner, Owner: true})
		}
	case index.SharedFolder:
		for _, m := range list.All() {
			if m.Role == members.RoleAdmin {
				out = append(out, accessJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: access.Manager, Admin: true})
			}
		}
	}
	depth := map[int64]int{0: 0}
	for i, id := range ids {
		depth[id] = i + 1
	}
	slices.SortStableFunc(grants, func(a, b index.Grant) int { return depth[a.Item] - depth[b.Item] })
	grants = s.drive.WithUses(grants)
	mayShare := len(ids) > 0 && access.Decide(who, access.Share, where)
	for _, g := range grants {
		at, onPath := depth[g.Item]
		if !onPath || !access.Counts(where.Kind, accessGrant(g, list)) {
			continue
		}
		e := accessJSON{Everyone: g.Everyone, Role: access.Role(g.Role), Given: at == len(ids)}
		if !g.Ends.IsZero() {
			ends := g.Ends
			e.Until = &ends
		}
		switch {
		case g.Guest != 0:
			e.Guest = &guestJSON{ID: g.Guest, Address: g.GuestAddress}
			if mayShare {
				works, uses := invitationWorks(where, accessGrant(g, list)), g.Uses
				e.Works, e.Uses = &works, &uses
				if !g.Used.IsZero() {
					used := g.Used
					e.Used = &used
				}
			}
		case !g.Everyone:
			e.UserID = g.Member
			if m, ok := list.Lookup(g.Member); ok {
				e.Email, e.DisplayName = m.Email, m.DisplayName
			} else {
				e.Former = true
			}
		}
		if at < len(ids) {
			e.Inherited, e.From = true, inheritedFrom(who, where, p, path, at)
		}
		out = append(out, e)
	}
	// Anyone with a link on the item, or on a folder above it, that lets them in there now: as viewers. And anyone with
	// an upload-only link on the item itself, who sends files in and sees nothing (so not on anything inside it).
	for _, l := range links {
		at, onPath := depth[l.Item]
		if !onPath || at == 0 {
			continue
		}
		visitor := visitorOf(l, list)
		var e accessJSON
		switch {
		case access.Decide(visitor, access.SeeThroughLink, where):
			e = accessJSON{Link: true, LinkKind: index.ViewLink, Role: access.Viewer, Given: at == len(ids)}
		case access.Decide(visitor, access.UploadThroughLink, where):
			e = accessJSON{Link: true, LinkKind: index.UploadLink, Given: true}
		default:
			continue
		}
		if at < len(ids) {
			e.Inherited, e.From = true, inheritedFrom(who, where, p, path, at)
		}
		out = append(out, e)
	}
	return out
}

// inheritedFrom is the folder access given at depth at on the way down to where (0: the shared folder as a whole)
// comes from, when the person can open it; nil when they cannot, so they never learn its name.
func inheritedFrom(who access.Person, where *access.Place, p index.Place, path []index.Item, at int) *folderJSON {
	if !access.Decide(who, access.List, where.Up(at)) {
		return nil
	}
	if at == 0 {
		return &folderJSON{ID: 0, Name: p.Name}
	}
	return &folderJSON{ID: path[at-1].ID, Name: path[at-1].Name}
}

// pathOfCall is the item a request is about, after the folders above it; none for a place as a whole.
func pathOfCall(c call) []index.Item {
	if c.item == nil {
		return nil
	}
	return pathTo(*c.item)
}

func (s *Server) writeAccess(w http.ResponseWriter, r *http.Request, c call) {
	list, err := s.accessList(r.Context(), personOf(c.member), *c.place, pathOfCall(c))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access": list, "outside": outsideHere(*c.place)})
}

// whoHasAccess says who has access to an item, or to a shared folder as a whole, and in which role.
//
//	GET /api/places/{place}/access
//	GET /api/places/{place}/items/{item}/access
func (s *Server) whoHasAccess(w http.ResponseWriter, r *http.Request, c call) {
	s.writeAccess(w, r, c)
}

// grantee says who a route that gives or takes access is about.
type grantee bool

const (
	toMember   grantee = false // the member named by the path value "member"
	toEveryone grantee = true  // everyone in the project
)

func (g grantee) of(r *http.Request) index.Grantee {
	if g == toEveryone {
		return index.Grantee{Everyone: true}
	}
	return index.Grantee{Member: r.PathValue("member")}
}

var errNoManagerLeft = errors.New("the shared folder would have no manager")

// keepsAManager refuses a change to what was given on shared folder p as a whole that would leave nobody in the member
// list managing it. There is always someone while the project has an admin.
func (s *Server) keepsAManager(p index.Place) func(after []index.Grant) error {
	list := s.members.Current()
	return func(after []index.Grant) error {
		where := s.accessPlace(p, nil, after)
		for _, m := range list.All() {
			if access.RoleAt(personOf(m), where) == access.Manager {
				return nil
			}
		}
		return errNoManagerLeft
	}
}

// giveAccess gives a member, or everyone in the project, a role on an item or on a shared folder as a whole, in place
// of what they had there, and answers with who has access now.
//
//	PUT /api/places/{place}/access/members/{member}              {"role": "viewer" | "editor" | "manager"}
//	PUT /api/places/{place}/access/everyone                      {"role": "viewer" | "editor"}
//	PUT /api/places/{place}/items/{item}/access/members/{member} {"role": "viewer" | "editor"}
//	PUT /api/places/{place}/items/{item}/access/everyone         {"role": "viewer" | "editor"}
//
// Only members can be given access (guests come later), including those who have never opened the Drive, and nobody
// gives access on an item to themselves. Changing a manager of a shared folder into something less is refused when
// nobody would be left managing it.
func (s *Server) giveAccess(who grantee) func(http.ResponseWriter, *http.Request, call) {
	return func(w http.ResponseWriter, r *http.Request, c call) {
		var body struct {
			Role access.Role `json:"role"`
		}
		if !s.readBody(w, r, &body) {
			return
		}
		to := who.of(r)
		if !to.Everyone {
			m, ok := s.members.Current().Lookup(to.Member)
			if !ok {
				s.refuse(w, r, http.StatusBadRequest, notInTheProject)
				return
			}
			to.Member = m.UserID
			if c.place.Kind == index.MyFiles && m.UserID == c.place.Owner {
				s.refuse(w, r, http.StatusBadRequest, theirOwnFiles)
				return
			}
			// Access on an item is given to others. Given to oneself it would only outlast what one was given above,
			// after the person who gave that took it away. (A manager stepping down on a shared folder as a whole changes
			// their own role there, under the rule that it keeps a manager.)
			if c.item != nil && m.UserID == c.member.UserID {
				s.refuse(w, r, http.StatusBadRequest, notToYourself)
				return
			}
		}
		var on int64
		if c.item != nil {
			on = c.item.ID
		}
		if !access.Counts(c.access.Kind, access.Grant{On: on, Member: to.Member, Everyone: to.Everyone, Role: body.Role}) {
			s.refuse(w, r, http.StatusBadRequest, roleRefused(c, to))
			return
		}
		var check func([]index.Grant) error
		if c.item == nil {
			check = s.keepsAManager(*c.place)
		}
		added, err := s.drive.Give(r.Context(), *c.place, c.item, to, string(body.Role), c.member.UserID, check)
		if added && !to.Everyone {
			if m, ok := s.members.Current().Lookup(to.Member); ok {
				s.tellShared(c, m, body.Role, text.Of(r))
			}
		}
		s.changedAccess(w, r, c, err)
	}
}

// tellShared mails a member that the person asking has just shared something with them by name: a new share, not a
// role changed or given again, and never to the person sharing. Sharing with everyone in the project sends nothing. It
// returns at once: the mail is sent in the background, and whatever happens to it, the share stands.
//
// The mail says only what the member may now see: the sharer's name, the item's name (or the shared folder's, when it
// was given as a whole), the project's, and a link to it at DRIVE_ADDRESS. It never names a folder above what was
// shared, nor the shared folder an item is in. It is in l, the language of the page the person shared from: the Drive
// knows no other language of the member it goes to.
func (s *Server) tellShared(c call, to members.Member, role access.Role, l text.Lang) {
	if !s.mail.On() || to.UserID == c.member.UserID || s.address == nil {
		return
	}
	link := url.Values{"place": {strconv.FormatInt(c.place.ID, 10)}}
	what, name := sharedSharedFolderWithYou, c.place.Name
	switch {
	case c.item == nil:
	case c.item.Folder:
		what, name = sharedFolderWithYou, c.item.Name
		link.Set("folder", strconv.FormatInt(c.item.ID, 10))
	default:
		// A file has no page of its own: the folder it is in, when they may open it, and otherwise "Shared with me",
		// which lists it.
		what, name = sharedFileWithYou, c.item.Name
		if !access.Decide(personOf(to), access.List, c.access.Up(len(c.access.Path)-1)) {
			link = url.Values{"shared": {""}}
		} else if c.item.ParentID != 0 {
			link.Set("folder", strconv.FormatInt(c.item.ParentID, 10))
		}
	}
	address := *s.address
	address.Path, address.RawQuery = "/", strings.TrimSuffix(link.Encode(), "=")
	sharer, name := mail.Clean(nameOf(c.member)), mail.Clean(name)
	var can string
	switch role {
	case access.Viewer:
		can = canOpen.In(l)
	case access.Editor:
		can = canEdit.In(l)
	case access.Manager:
		can = canManage.In(l)
	}
	body := what.In(l, sharer, name, mail.Clean(s.project)) + "\n" + can + "\n\n" + openIt.In(l, address.String()) + "\n"
	s.mail.Send(mail.Message{
		To:      to.Email,
		Subject: sharedSubject.In(l, sharer, name),
		Body:    body,
		About:   []any{"mail", "shared with you", "recipient", to.UserID, "place", c.place.ID, "item", itemID(c.item)},
	})
}

// nameOf is a member's name as people read it: their display name, or their email address.
func nameOf(m members.Member) string {
	if strings.TrimSpace(m.DisplayName) != "" {
		return m.DisplayName
	}
	return m.Email
}

func itemID(it *index.Item) int64 {
	if it == nil {
		return 0
	}
	return it.ID
}

// roleRefused says which roles can be given where the request tried to give one that cannot.
func roleRefused(c call, to index.Grantee) text.Message {
	switch {
	case c.item != nil:
		return rolesOnAnItem
	case to.Everyone:
		return rolesForEveryone
	}
	return rolesOnASharedFolder
}

// takeAccess takes away what was given to a member, or to everyone in the project, on an item or on a shared folder as
// a whole, and answers with who has access now. Access they have from a folder above stays; it is changed there.
// Taking away a manager of a shared folder is refused when nobody would be left managing it.
//
//	DELETE /api/places/{place}/access/members/{member}
//	DELETE /api/places/{place}/access/everyone
//	DELETE /api/places/{place}/items/{item}/access/members/{member}
//	DELETE /api/places/{place}/items/{item}/access/everyone
func (s *Server) takeAccess(who grantee) func(http.ResponseWriter, *http.Request, call) {
	return func(w http.ResponseWriter, r *http.Request, c call) {
		var check func([]index.Grant) error
		if c.item == nil {
			check = s.keepsAManager(*c.place)
		}
		err := s.drive.TakeAway(r.Context(), *c.place, c.item, who.of(r), check)
		s.changedAccess(w, r, c, err)
	}
}

// changedAccess answers a change to who has access: with who has it now, or with why it did not happen.
func (s *Server) changedAccess(w http.ResponseWriter, r *http.Request, c call, err error) {
	switch {
	case err == nil:
		s.writeAccess(w, r, c)
	case errors.Is(err, errNoManagerLeft):
		s.refuse(w, r, http.StatusConflict, needsAManager)
	case errors.Is(err, index.ErrNoItem), errors.Is(err, drive.ErrPlaceGone):
		// It went to the trash after guard found it: the same answer guard gives from now on.
		s.refuse(w, r, http.StatusNotFound, notHere)
	default:
		s.fail(w, r, err)
	}
}
