package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Inviting guests, from a member's "Give access": typing an address that is not a member's invites a guest to a file or
// folder; an address that is a member's shares it with them as with any member. Their routes are in routes.go with all
// the others; guard has decided before any of these runs. What a guest reaches is in guests.go.

// guestJSON is a guest as a member reads them: their address, and their id for the routes that change an invitation.
type guestJSON struct {
	ID      int64  `json:"id"`
	Address string `json:"address"`
}

// invitationJSON is the body that invites a guest, or changes an invitation.
type invitationJSON struct {
	Address string      `json:"address"`
	Role    access.Role `json:"role"`
	// Ends is when the access ends, as a time with its offset; none for no end date.
	Ends *time.Time `json:"ends"`
}

// cannotInvite says why guests cannot be invited, after why mail does not work.
func cannotInvite(why string) string {
	return "Inviting guests is unavailable: the Drive mails each guest an invitation and the codes they sign in with. " +
		why + " Make a public link instead."
}

// invite gives access to a file or folder to whoever has the address typed in "Give access": a member, by name, as
// giveAccess does; anyone else as a guest, invited by mail, as viewer or editor, until an end date if one is given. The
// answer is who has access now, and whom it went to: "member", or "guest" (a new one is mailed an invitation).
//
//	POST /api/places/{place}/items/{item}/access/guests   {"address", "role": "viewer" | "editor", "ends": <time> | null}
//	200 {"access": […], "member": {…}} | {"access": […], "guest": {"id", "address"}, "invited": true}
//	400 not an address, a role that cannot be given, an end date that has passed
//	409 sharing outside the project is off here ("outside": false), or mail does not work ("mail": false)
func (s *Server) invite(w http.ResponseWriter, r *http.Request, c call) {
	var body invitationJSON
	if !s.readBody(w, r, &body) {
		return
	}
	if m, ok := s.members.Current().ByEmail(body.Address); ok {
		// The address is a member's: an ordinary share with them, and never an end date.
		if m.UserID == c.member.UserID {
			s.refuse(w, r, http.StatusBadRequest, "You cannot give yourself access. Ask someone who may share it.")
			return
		}
		if c.place.Kind == index.MyFiles && m.UserID == c.place.Owner {
			s.refuse(w, r, http.StatusBadRequest, "This is in their own My files: they have it already.")
			return
		}
		if !access.Counts(c.access.Kind, access.Grant{On: c.item.ID, Member: m.UserID, Role: body.Role}) {
			s.refuse(w, r, http.StatusBadRequest, roleRefused(c, index.Grantee{Member: m.UserID}))
			return
		}
		added, err := s.drive.Give(r.Context(), *c.place, c.item, index.Grantee{Member: m.UserID}, string(body.Role), c.member.UserID, nil)
		if added {
			s.tellShared(c, m, body.Role)
		}
		if err != nil {
			s.changedAccess(w, r, c, err)
			return
		}
		s.answerAccess(w, r, c, map[string]any{"member": personJSONOf(m)})
		return
	}
	address, ok := guestAddress(body.Address)
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, "This is not an email address the Drive can send mail to. Check it, and try again.")
		return
	}
	if !access.Decide(c.person, access.InviteGuest, c.access) {
		writeJSON(w, http.StatusConflict, map[string]any{"outside": false,
			"error": "Sharing outside the project is switched off here, so no guest can be invited. Share it with members of the project."})
		return
	}
	ends, ok := s.invitationRules(w, r, c, body)
	if !ok {
		return
	}
	if works, why := s.mail.Works(r.Context()); !works {
		writeJSON(w, http.StatusConflict, map[string]any{"mail": false, "error": cannotInvite(why)})
		return
	}
	guest, invited, err := s.drive.Invite(r.Context(), *c.place, *c.item, address, string(body.Role), ends, c.member.UserID)
	if err != nil {
		s.changedAccess(w, r, c, err)
		return
	}
	if invited {
		s.tellInvited(c, guest, body.Role, ends)
	}
	s.answerAccess(w, r, c, map[string]any{"guest": guestJSON{ID: guest.ID, Address: guest.Address}, "invited": invited})
}

// invitationRules checks the role and end date of an invitation: viewer or editor, and an end date still to come. ok is
// false when the person was already answered.
func (s *Server) invitationRules(w http.ResponseWriter, r *http.Request, c call, body invitationJSON) (ends time.Time, ok bool) {
	if !access.Counts(c.access.Kind, access.Grant{On: c.item.ID, Guest: 1, Role: body.Role}) {
		s.refuse(w, r, http.StatusBadRequest, "A guest is given a file or folder as viewer or editor.")
		return time.Time{}, false
	}
	if body.Ends != nil {
		ends = body.Ends.UTC().Truncate(time.Millisecond)
		if !ends.After(s.drive.Now()) {
			s.refuse(w, r, http.StatusBadRequest, "The end date has passed. Choose one that is still to come, or none.")
			return time.Time{}, false
		}
	}
	return ends, true
}

// changeInvitation gives an invitation on a file or folder a new role and end date (none: no end date). Only while
// sharing outside the project is on there (access.InviteGuest).
//
//	PUT /api/places/{place}/items/{item}/access/guests/{guest}   {"role", "ends": <time> | null}
func (s *Server) changeInvitation(w http.ResponseWriter, r *http.Request, c call) {
	var body invitationJSON
	if !s.readBody(w, r, &body) {
		return
	}
	guest, ok := guestOfPath(r)
	if !ok {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	ends, ok := s.invitationRules(w, r, c, body)
	if !ok {
		return
	}
	s.changedAccess(w, r, c, s.drive.ChangeInvitation(r.Context(), *c.place, *c.item, guest, string(body.Role), ends))
}

// takeInvitation takes an invitation on a file or folder away: the guest can no longer reach it, and when it was their
// last, they are signed out at once, everywhere.
//
//	DELETE /api/places/{place}/items/{item}/access/guests/{guest}
func (s *Server) takeInvitation(w http.ResponseWriter, r *http.Request, c call) {
	guest, ok := guestOfPath(r)
	if !ok {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	s.changedAccess(w, r, c, s.drive.TakeAway(r.Context(), *c.place, c.item, index.Grantee{Guest: guest}, nil))
}

func guestOfPath(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("guest"), 10, 64)
	return id, err == nil && id > 0
}

// answerAccess answers with who has access now, and more.
func (s *Server) answerAccess(w http.ResponseWriter, r *http.Request, c call, more map[string]any) {
	list, err := s.accessList(r.Context(), personOf(c.member), *c.place, pathOfCall(c))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	more["access"] = list
	writeJSON(w, http.StatusOK, more)
}

// guestAddress is an address typed for a guest, as the Drive keeps it (members.Address): one plain email address, in
// ASCII, that mail can be sent to, at most 254 characters. ok is false for anything else.
func guestAddress(typed string) (string, bool) {
	address := members.Address(typed)
	if len(address) > 254 || strings.ContainsFunc(address, func(r rune) bool { return r > 0x7e || r < 0x21 }) {
		return "", false
	}
	if _, err := mail.Recipient(address); err != nil {
		return "", false
	}
	local, domain, _ := strings.Cut(address, "@")
	if local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", false
	}
	return address, true
}

// guestLink is the address that leads a guest to what was shared with them: the guests' page, at the item, with their
// address after the "#" (which a browser never sends), so the page can offer it to confirm.
func (s *Server) guestLink(place, item int64, address string) string {
	link := url.Values{"place": {strconv.FormatInt(place, 10)}, "item": {strconv.FormatInt(item, 10)}}
	if s.address == nil {
		return GuestPrefix + "/?" + link.Encode()
	}
	a := *s.address
	a.Path, a.RawPath, a.RawQuery, a.Fragment = GuestPrefix+"/", "", link.Encode(), "as="+url.QueryEscape(address)
	return a.String()
}

// tellInvited mails a guest that the person asking has just invited them: who, to what, in which project, what they
// may do, until when, and the link to it. It returns at once: the mail is sent in the background. It never names a
// folder above what was shared, nor the shared folder it is in.
func (s *Server) tellInvited(c call, guest index.Guest, role access.Role, ends time.Time) {
	what := "the file"
	if c.item.Folder {
		what = "the folder"
	}
	sharer, name := mail.Clean(nameOf(c.member)), mail.Clean(c.item.Name)
	can := "You can open and download it."
	if role == access.Editor && c.item.Folder {
		can += " As an editor you can also upload, make folders, rename, move and delete inside it."
	}
	lines := []string{fmt.Sprintf("%s invited you to %s “%s” in the Drive of %s.", sharer, what, name, mail.Clean(s.project)), can}
	if !ends.IsZero() {
		lines = append(lines, "You have access until "+ends.UTC().Format("2 January 2006, 15:04 MST")+".")
	}
	body := strings.Join(lines, "\n") + "\n\nOpen it: " + s.guestLink(c.place.ID, c.item.ID, guest.Address) + "\n\n" +
		"You will be asked to confirm your address with a code we mail to it. You need no account.\n"
	s.mail.Send(mail.Message{
		To:      guest.Address,
		Subject: fmt.Sprintf("%s invited you to “%s”", sharer, name),
		Body:    body,
		About:   []any{"mail", "invited", "guest", guest.ID, "place", c.place.ID, "item", c.item.ID},
	})
}

// invitationWorks says whether an invitation lets its guest in at its own item now: decided by the access package, as
// that guest, at that item, with that invitation (and nothing else given to them).
func invitationWorks(where *access.Place, g access.Grant) bool {
	if g.Guest == 0 {
		return false
	}
	at := -1
	for i, id := range where.Path {
		if id == g.On {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	q := *where.Up(at + 1)
	q.Grants = make([]access.Grant, 0, len(where.Grants))
	for _, other := range where.Grants {
		if other.Guest == 0 {
			q.Grants = append(q.Grants, other)
		}
	}
	q.Grants = append(q.Grants, g)
	return access.Decide(access.Person{Guest: &access.Guest{ID: g.Guest}}, access.ListAsGuest, &q)
}
