package server

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// Inviting guests, from a member's "Give access": typing an address that is not a member's invites a guest to a file or
// folder; an address that is a member's shares it with them as with any member. Their routes are in routes.go with all
// the others; guard has decided before any of these runs. What a guest reaches is in guests.go.

// invitationMailsPerHour is how many invitations one member has mailed in an hour; the next is refused, and says so.
// Inviting an address, taking the invitation away and inviting it again mails it each time, with names the member
// chose (their own, and the file's or folder's): without a bound, a member could send anyone any number of mails from
// the Drive.
const invitationMailsPerHour = 30

// maxEndYear is the last year an invitation may end in: far past any real one, and one every answer can still write.
const maxEndYear = 2999

// sharedMailsPerHour is how many "shared with you" mails one member's shares cause in an hour. Sharing with a member,
// taking it away and sharing again mails them each time, with a name the sharer chose: past this many, the share is
// made all the same, and no mail goes.
const sharedMailsPerHour = 60

// A mailCount counts the mails each person caused within a while, and says when one has had as many as they may. It is
// kept in memory, a few times per person at most: a restart forgets it, and the count starts again.
type mailCount struct {
	most   int
	within time.Duration

	mu   sync.Mutex
	sent map[string][]time.Time // by person, when each mail still counted was sent, the oldest first
}

func newMailCount(most int, within time.Duration) *mailCount {
	return &mailCount{most: most, within: within, sent: map[string][]time.Time{}}
}

// take counts one more mail for who at now; ok is false, and nothing is counted, when they have had as many as they may
// within the while before now. Whoever caused none within it is forgotten.
func (c *mailCount) take(who string, now time.Time) (ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for person, sent := range c.sent {
		sent = slices.DeleteFunc(sent, func(at time.Time) bool { return now.Sub(at) >= c.within })
		if len(sent) == 0 {
			delete(c.sent, person)
		} else {
			c.sent[person] = sent
		}
	}
	if len(c.sent[who]) >= c.most {
		return false
	}
	c.sent[who] = append(c.sent[who], now)
	return true
}

// giveBack takes the mail counted for who at at off the count again: it was not sent after all.
func (c *mailCount) giveBack(who string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sent := c.sent[who]
	if i := slices.IndexFunc(sent, func(t time.Time) bool { return t.Equal(at) }); i >= 0 {
		c.sent[who] = slices.Delete(sent, i, i+1)
	}
	if len(c.sent[who]) == 0 {
		delete(c.sent, who)
	}
}

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

// invite gives access to a file or folder to whoever has the address typed in "Give access": a member, by name, as
// giveAccess does; anyone else as a guest, invited by mail, as viewer or editor, until an end date if one is given. The
// answer is who has access now, and whom it went to: "member", or "guest" (a new one is mailed an invitation).
//
//	POST /api/places/{place}/items/{item}/access/guests   {"address", "role": "viewer" | "editor", "ends": <time> | null}
//	200 {"access": […], "member": {…}} | {"access": […], "guest": {"id", "address"}, "invited": true}
//	400 not an address, a role that cannot be given, an end date that has passed
//	409 sharing outside the project is off here ("outside": "project" | "folder"), or mail does not work ("mail": false)
//	429 the member had invitationMailsPerHour invitations mailed within the last hour (Retry-After)
func (s *Server) invite(w http.ResponseWriter, r *http.Request, c call) {
	var body invitationJSON
	if !s.readBody(w, r, &body) {
		return
	}
	if m, ok := s.members.Current().ByEmail(body.Address); ok {
		// The address is a member's: an ordinary share with them, and never an end date.
		if m.UserID == c.member.UserID {
			s.refuse(w, r, http.StatusBadRequest, notToYourself)
			return
		}
		if c.place.Kind == index.MyFiles && m.UserID == c.place.Owner {
			s.refuse(w, r, http.StatusBadRequest, theirOwnFiles)
			return
		}
		if !access.Counts(c.access.Kind, access.Grant{On: c.item.ID, Member: m.UserID, Role: body.Role}) {
			s.refuse(w, r, http.StatusBadRequest, roleRefused(c, index.Grantee{Member: m.UserID}))
			return
		}
		added, err := s.drive.Give(r.Context(), *c.place, c.item, index.Grantee{Member: m.UserID}, string(body.Role), c.member.UserID, nil)
		if added {
			s.tellShared(c, m, body.Role, text.Of(r))
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
		s.refuse(w, r, http.StatusBadRequest, notAnEmail)
		return
	}
	if !access.Decide(c.person, access.InviteGuest, c.access) {
		s.switchedOff(w, r, c) // they may share it (Share), so only the switch is in the way
		return
	}
	ends, ok := s.invitationRules(w, r, c, body)
	if !ok {
		return
	}
	if works, why := s.mail.Works(r.Context()); !works {
		refuseWith(w, r, http.StatusConflict, map[string]any{"mail": false}, cannotInvite, why.In(text.Of(r)))
		return
	}
	// The mail is counted before the invitation is made, so that none is made without its mail, and taken off the count
	// again when no mail goes: the invitation was not made, or the guest had one here already. A member who has had as
	// many mailed as they may still changes an invitation that is there, which mails nothing.
	//
	// Whether the guest has one here is read now, with invitations and their removals taking turns (inviting): on what
	// guard read, a removal sent together with the invitation made it new, mailed and uncounted.
	s.inviting.Lock()
	defer s.inviting.Unlock()
	given, err := s.drive.GrantsOn(r.Context(), *c.place, idsOf(pathTo(*c.item)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	c.grants = given
	now := s.drive.Now()
	counted := s.invitationMails.take(c.member.UserID, now)
	if !counted && !invitedHere(c, address) {
		w.Header().Set("Retry-After", strconv.Itoa(int(time.Hour/time.Second)))
		s.refuse(w, r, http.StatusTooManyRequests, tooManyInvitations, invitationMailsPerHour)
		return
	}
	guest, invited, err := s.drive.Invite(r.Context(), *c.place, *c.item, address, string(body.Role), ends, c.member.UserID)
	if counted && (err != nil || !invited) {
		s.invitationMails.giveBack(c.member.UserID, now)
	}
	if err != nil {
		s.changedAccess(w, r, c, err)
		return
	}
	if invited {
		s.tellInvited(c, guest, body.Role, ends, text.Of(r))
	}
	s.answerAccess(w, r, c, map[string]any{"guest": guestJSON{ID: guest.ID, Address: guest.Address}, "invited": invited})
}

// invitedHere says whether the guest with the address has an invitation on the item the request is about, as guard read
// what was given there.
func invitedHere(c call, address string) bool {
	return slices.ContainsFunc(c.grants, func(given index.Grant) bool {
		return given.Guest != 0 && given.GuestAddress == address && given.Item == c.item.ID
	})
}

// invitationRules checks the role and end date of an invitation: viewer or editor, and an end date still to come. ok is
// false when the person was already answered.
func (s *Server) invitationRules(w http.ResponseWriter, r *http.Request, c call, body invitationJSON) (ends time.Time, ok bool) {
	if !access.Counts(c.access.Kind, access.Grant{On: c.item.ID, Guest: 1, Role: body.Role}) {
		s.refuse(w, r, http.StatusBadRequest, guestRoles)
		return time.Time{}, false
	}
	if body.Ends != nil {
		ends = body.Ends.UTC().Truncate(time.Millisecond)
		if !ends.After(s.drive.Now()) {
			s.refuse(w, r, http.StatusBadRequest, endHasPassed)
			return time.Time{}, false
		}
		// A year past 9999 cannot be written in an answer (JSON's dates end there): kept, every list that holds the
		// invitation would fail, the admins' list of what is shared outside the project with them.
		if ends.Year() > maxEndYear {
			s.refuse(w, r, http.StatusBadRequest, endTooFar)
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
	s.changedAccess(w, r, c, s.drive.ChangeInvitation(r.Context(), *c.place, *c.item, guest, string(body.Role), ends, c.member.UserID))
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
	s.inviting.Lock() // in turn with invitations, which count on what is there (invite)
	err := s.drive.TakeAway(r.Context(), *c.place, c.item, index.Grantee{Guest: guest}, nil)
	s.inviting.Unlock()
	s.changedAccess(w, r, c, err)
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
	more["outside"] = outsideHere(*c.place)
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
	// A name, never a machine's number (x@[10.0.0.5], x@10.0.0.5): a mail server that takes those delivers to whichever
	// machine the member picked, inside its own network too.
	// Its last part is letters somewhere: all digits, or a number in hexadecimal, is how a number is written in other
	// ways (x@127.1, x@0x7f.0.0.1). (A name that leads to a machine inside is the mail server's to refuse.)
	last := domain[strings.LastIndexByte(domain, '.')+1:]
	if strings.ContainsAny(domain, "[]:") || net.ParseIP(domain) != nil || strings.HasPrefix(last, "0x") ||
		!strings.ContainsFunc(last, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
		return "", false
	}
	return address, true
}

// guestLink is the address that leads a guest to what was shared with them: the guests' page, at the item (the page
// itself when place or item is 0), with their address after the "#" (which a browser never sends), so the page can
// offer it to confirm; and, in a sign-in link, the code that signs them in, after it.
func (s *Server) guestLink(place, item int64, address, code string) string {
	link := url.Values{}
	if place > 0 && item > 0 {
		link = url.Values{"place": {strconv.FormatInt(place, 10)}, "item": {strconv.FormatInt(item, 10)}}
	}
	after := "as=" + url.QueryEscape(address)
	if code != "" {
		after += "&code=" + code
	}
	a := url.URL{}
	if s.address != nil {
		a = *s.address
	}
	a.Path, a.RawPath, a.RawQuery = GuestPrefix+"/", "", link.Encode()
	a.Fragment, _ = url.QueryUnescape(after)
	a.RawFragment = after
	return a.String()
}

// tellInvited mails a guest that the person asking has just invited them: who, to what, in which project, what they
// may do, until when, and the link to it. It returns at once: the mail is sent in the background. It never names a
// folder above what was shared, nor the shared folder it is in. It is in l, the language of the page the person
// invited from: the Drive knows nothing else of a guest it has not met.
func (s *Server) tellInvited(c call, guest index.Guest, role access.Role, ends time.Time, l text.Lang) {
	what := invitedToFile
	if c.item.Folder {
		what = invitedToFolder
	}
	sharer, name := mail.Clean(nameOf(c.member)), mail.Clean(c.item.Name)
	can := canOpen.In(l)
	if role == access.Editor && c.item.Folder {
		can += " " + guestCanEdit.In(l)
	}
	lines := []string{what.In(l, sharer, name, mail.Clean(s.project)), can}
	if !ends.IsZero() {
		lines = append(lines, accessUntil.In(l, text.Moment(l, ends)))
	}
	body := strings.Join(lines, "\n") + "\n\n" + openIt.In(l, s.guestLink(c.place.ID, c.item.ID, guest.Address, "")) + "\n\n" +
		confirmByCode.In(l) + "\n"
	s.mail.Send(mail.Message{
		To:      guest.Address,
		Subject: invitedSubject.In(l, sharer, name),
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
