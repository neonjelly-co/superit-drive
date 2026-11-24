package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// The guests' part of the public prefix: signing in with a link mailed to them, their pages, and what they do with what
// was given to them. Its routes are in routes.go with all the others, and pass guard like them; guardGuest is guard's
// part for them.
//
// A request here is a guest's (the one whose session its cookie names) or nobody's: nothing it carries about a member
// (Drive-Secret, Remote-User-Id) is read, so a member is nobody here, and a forged identity does nothing. A guest is
// decided by the access package as a person of their own kind, with actions only guests have.
//
// Signing in. The page asks for a sign-in link for an address (askForCode): the answer is the same whatever the address,
// at once, and the work is done in the background (SendCodes), one address at a time: a link is mailed only when the
// address is a guest's with something shared with them that lets them in now, at most one every drive.CodeEvery to one
// address and drive.CodesPerHourOverall an hour to all. The link leads to the guests' page with a code after the "#",
// which the browser never sends by itself: the page sends it with the address (signIn), as a change from the Drive's
// own page, so a program that only fetches the links in a mail signs nobody in and uses nothing up. A code is too long
// to guess, so wrong ones are not counted, and nothing a stranger does here takes a guest's sign-in away: the links
// already mailed go on working when another is asked for, and each works once, for drive.CodeValidFor. A session lasts
// drive.SessionLasts on that browser, in a cookie that no script reads (HttpOnly), that goes only over HTTPS
// and is taken from the Drive's own name alone when the Drive's address is HTTPS (Secure, and the __Host- name, which
// takes the path "/"; over plain HTTP only the guests' prefix gets it), and that no
// other site's page sends along with a change (SameSite=Lax; it is sent when a guest opens a link to the Drive from
// their mail). Every change also needs the Drive's own Origin, as a member's does.
//
// A session ends when it ends, when the guest signs out, and at once when the last of what was given to them is taken
// away or ends (the index removes the guest with it), or no longer lets them in anywhere (it is in the trash, gone from
// the storage, in the My files of someone who left, or its giver may no longer share it), or when their address appears
// in the member list (they become a member, and sign in as one). Once ended it does not come back: they sign in again.

// codesWaiting is how many addresses that asked for a sign-in link wait for SendCodes; more are dropped, and answered the same.
const codesWaiting = 64

// guardGuest is guard for a route under the guests' prefix: only from the Drive's own pages for a change, then the guest
// whose session the cookie names (or nobody, which only signing in allows), the place and the item the request is
// about and the access given on the way down to it, then access.Decide for the guest.
func (s *Server) guardGuest(w http.ResponseWriter, r *http.Request, rt route) {
	// The small body of a change is read before a turn is taken (answers.go), so that a body that does not arrive
	// keeps none. An upload's bytes are not: it leaves its turn before it reads any.
	if rt.action != access.UploadAsGuest && !s.bodyFirst(w, r, guestBodyMost) {
		return
	}
	leave, ok := s.guests.enter(r.Context())
	if !ok {
		if r.Context().Err() == nil {
			w.Header().Set("Retry-After", "5")
			s.refuse(w, r, http.StatusServiceUnavailable, visitorsBusy)
		}
		return
	}
	// The answer is kept while the turn is held, and sent once it is given back: a guest (or anybody) who does not take
	// it keeps nobody else waiting.
	real := w
	held := &heldAnswer{ResponseWriter: real, most: guestAnswerMost, onSpill: func() { leave() }}
	w = held
	defer func() { leave(); s.sendHeld(r, held) }()
	if !s.fromOwnPage(r) {
		s.logRefusedChange(r)
		s.refuse(w, r, http.StatusForbidden, refusedOrigin)
		return
	}
	c := call{action: rt.action}
	// A wait for something that is not the Drive's own work (a look at the storage) is not made with the turn held:
	// the turn is given back, and another taken when the wait is over.
	c.aside = func(wait func()) (ok bool) {
		leave()
		wait()
		leave, ok = s.guests.enter(r.Context())
		return ok
	}
	guest, stale, err := s.guestSession(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if guest != nil {
		c.guest, c.person = guest, access.Person{Guest: &access.Guest{ID: guest.ID}}
	}
	if stale {
		s.clearGuestCookie(w)
	}
	if c.guest == nil && rt.action != access.SignInAsGuest {
		refuseWith(w, r, http.StatusUnauthorized, map[string]any{"signIn": true}, signInFirst)
		return
	}
	if rt.place != "" && !s.findWhere(w, r, rt, &c) {
		return
	}
	if !access.Decide(c.person, rt.action, c.access) || rt.serve == nil {
		if rt.serve != nil && access.SwitchedOff(c.person, rt.action, c.access) {
			s.switchedOff(w, r, c) // given to them, and not available only while sharing outside the project is off
			return
		}
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	switch {
	case rt.action == access.DownloadAsGuest || rt.action == access.UploadAsGuest:
		leave() // it waits for its transfer slot in the outsiders' own line, which bounds them by itself
		w = real
	case rt.action == access.SignInAsGuest && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		leave() // the guests' page and the interface's files: the Drive's own bytes, which need nothing of it to send
		w = real
	}
	rt.serve(w, r, c)
}

// guestSession is the guest whose session the request's cookie names: nil for none, and stale when a cookie was sent
// for a session that has ended (or whose guest is now a member).
func (s *Server) guestSession(r *http.Request) (guest *index.Guest, stale bool, err error) {
	// Sent twice, there is no telling which the Drive set. Over HTTPS the cookie's name lets no other name set one
	// (guestCookie); over plain HTTP a page on another name under the same domain can give a browser a second one, for
	// a narrower path, which comes first, and put a guest into a session of its own choosing. Nobody is signed in then,
	// and every session the request names is ended (the one given to the browser from outside too) and the Drive's own
	// cookie taken back, as signing out would: a request for that never gets as far as signing out, being nobody's.
	if doubled := r.CookiesNamed(s.guestCookie()); len(doubled) > 1 {
		for _, ck := range doubled {
			if err := s.drive.SignOut(r.Context(), ck.Value); err != nil {
				return nil, false, err
			}
		}
		return nil, true, nil
	}
	ck, err := r.Cookie(s.guestCookie())
	if err != nil {
		return nil, false, nil
	}
	g, _, err := s.drive.SessionOf(r.Context(), ck.Value)
	if errors.Is(err, index.ErrNoSession) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, member := s.members.Current().ByEmail(g.Address); member {
		return nil, true, nil // a member signs in as one; their guest's access is theirs as a member (drive.AdmitMembers)
	}
	// Nothing given to them lets them in any more (in the trash, gone from the storage, held with its giver's My files),
	// nor will when sharing outside the project is switched back on: their sessions end, everywhere, for good.
	open, err := s.guestMayOpenAnything(r.Context(), g)
	if err != nil {
		return nil, false, err
	}
	if !open {
		if err := s.drive.EndSessionsOf(r.Context(), g.ID); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	return &g, false, nil
}

// guestMayOpenAnything says whether something given to the guest lets them in now, or will once sharing outside the
// project is switched back on where it is (access.SwitchedOff): an item an invitation to them is on that they may open,
// decided by the access package on the whole way down to it. It stops at the first. Switching sharing outside off
// stops what a guest may do, and leaves them signed in to be told so; it works again when it is switched back on, as
// links do.
func (s *Server) guestMayOpenAnything(ctx context.Context, g index.Guest) (bool, error) {
	granted, err := s.drive.GrantedToGuest(ctx, g.ID)
	if err != nil {
		return false, err
	}
	who := access.Person{Guest: &access.Guest{ID: g.ID}}
	for _, at := range granted {
		p, err := s.drive.Place(ctx, at.Place)
		if errors.Is(err, index.ErrNoPlace) {
			continue
		}
		if err != nil {
			return false, err
		}
		it, err := s.drive.Item(ctx, p, at.Item)
		if errors.Is(err, index.ErrNoItem) {
			continue
		}
		if err != nil {
			return false, err
		}
		where, err := s.accessAt(ctx, p, idsOf(pathTo(it)))
		if err != nil {
			return false, err
		}
		if access.Decide(who, access.ListAsGuest, where) || access.SwitchedOff(who, access.ListAsGuest, where) {
			return true, nil
		}
	}
	return false, nil
}

// WorkingInvitations is every item an invitation to the guest is on that works now, each invitation judged alone, as
// the access package judges it on the whole way down to its item, with sharing outside the project switched off left
// aside (it works again when that is switched back on): what a guest who joins the project keeps as a member
// (drive.AdmitMembers). One that has ended, whose giver may no longer share its item or has left the project, or whose
// item is in the trash or gone, is not among them.
func (s *Server) WorkingInvitations(ctx context.Context, g index.Guest) ([]index.Granted, error) {
	granted, err := s.drive.GrantedToGuest(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	who := access.Person{Guest: &access.Guest{ID: g.ID}}
	var out []index.Granted
	for _, at := range granted {
		p, err := s.drive.Place(ctx, at.Place)
		if errors.Is(err, index.ErrNoPlace) {
			continue
		}
		if err != nil {
			return nil, err
		}
		it, err := s.drive.Item(ctx, p, at.Item)
		if errors.Is(err, index.ErrNoItem) {
			continue
		}
		if err != nil {
			return nil, err
		}
		path := idsOf(pathTo(it))
		grants, err := s.drive.GrantsOn(ctx, p, path)
		if err != nil {
			return nil, err
		}
		// Alone: another invitation to them, on a folder above, says nothing about this one.
		grants = slices.DeleteFunc(grants, func(o index.Grant) bool { return o.Guest == g.ID && o.Item != at.Item })
		where := s.accessPlace(p, path, grants)
		if access.Decide(who, access.ListAsGuest, where) || access.SwitchedOff(who, access.ListAsGuest, where) {
			out = append(out, at)
		}
	}
	return out, nil
}

// guestCookie is the name of the guests' session cookie: with the __Host- prefix where the Drive's address is HTTPS.
// A browser takes a cookie of that name only over HTTPS, from the Drive's own name, for that name alone: no page over
// plain HTTP can set one, and neither can a page on another name under the same domain, which could otherwise give a
// guest's browser a session of its own choosing (and have the guest work, and upload, inside it).
func (s *Server) guestCookie() string {
	if s.secure() {
		return "__Host-drive-guest"
	}
	return "drive-guest"
}

// guestCookiePath is the path the guests' cookie is for: the guests' prefix; except over HTTPS, where the __Host- prefix
// takes only "/". The browser then sends it with members' requests too, where the Drive never reads it.
func (s *Server) guestCookiePath() string {
	if s.secure() {
		return "/"
	}
	return GuestPrefix
}

func (s *Server) secure() bool {
	return s.address != nil && strings.EqualFold(s.address.Scheme, "https")
}

func (s *Server) setGuestCookie(w http.ResponseWriter, token string, ends time.Time) {
	http.SetCookie(w, &http.Cookie{Name: s.guestCookie(), Value: token, Path: s.guestCookiePath(),
		MaxAge: int(ends.Sub(s.drive.Now()).Seconds()), HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearGuestCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.guestCookie(), Value: "", Path: s.guestCookiePath(), MaxAge: -1, HttpOnly: true,
		Secure: s.secure(), SameSite: http.SameSiteLaxMode})
}

// toGuestPage sends the address without its last slash to the guests' page.
//
//	GET /s/guest
func (s *Server) toGuestPage(w http.ResponseWriter, r *http.Request, _ call) {
	to := GuestPrefix + "/"
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, to, http.StatusFound)
}

// guestPage serves the guests' interface: one page that signs them in and shows what was shared with them.
//
//	GET /s/guest/[?place=…&item=…]
func (s *Server) guestPage(w http.ResponseWriter, r *http.Request, _ call) {
	if _, err := fs.Stat(s.ui, "guest.html"); err != nil {
		s.refuse(w, r, http.StatusServiceUnavailable, noInterface)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.ui, "guest.html")
}

// guestAsset serves one of the interface's files to the guests' page, from the guests' prefix: the same files as the
// members', which the reverse proxy lets nobody else reach without signing in.
//
//	GET /s/guest/assets/{file...}
//	GET /s/guest/favicon.svg
func (s *Server) guestAsset(w http.ResponseWriter, r *http.Request, c call) {
	s.serveFile(w, r, c, strings.TrimPrefix(r.URL.Path, GuestPrefix+"/"))
}

// askForCode asks for a sign-in link to be mailed to an address. The answer is the same for every address that is one,
// at once: whether a link is sent (only to a guest's address with something shared with them, and not while one was
// sent to it within drive.CodeEvery) is worked out in the background (SendCodes). place and item, when given, are
// where the guest was going: the link leads there.
//
// An address waits once: asked for again before SendCodes has come to it, it takes no second place, so nobody fills
// the line with one address (and a line that is full of many drops the rest, answered the same).
//
//	POST /s/guest/api/code   {"address"[, "place", "item"]}
//	202 {"message"}
func (s *Server) askForCode(w http.ResponseWriter, r *http.Request, _ call) {
	var body struct {
		Address string `json:"address"`
		Place   int64  `json:"place"`
		Item    int64  `json:"item"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	address, ok := guestAddress(body.Address)
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, notAnEmail)
		return
	}
	s.askingMu.Lock()
	if !s.asking[address] {
		select {
		case s.codes <- codeAsk{address: address, lang: text.Of(r), place: body.Place, item: body.Item}:
			s.asking[address] = true
		default:
			s.codesDropped.Log(s.log, slog.LevelWarn, "asks for a sign-in link were dropped: too many addresses are waiting", "waiting", codesWaiting)
		}
	}
	s.askingMu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]string{"message": codeAsked.In(text.Of(r))})
}

// signIn signs a guest in with the code of a sign-in link mailed to their address, and keeps them signed in on this
// browser for drive.SessionLasts: the answer sets the session's cookie. The guests' page sends it when the link is
// opened.
//
//	POST /s/guest/api/sign-in   {"address", "code"}
//	200 {"address"}   400 a code that is not right, was used, or has ended
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, _ call) {
	var body struct {
		Address string `json:"address"`
		Code    string `json:"code"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	address, ok := guestAddress(body.Address)
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, notAnEmail)
		return
	}
	if len(body.Code) != drive.CodeLength {
		s.refuse(w, r, http.StatusBadRequest, wrongCode)
		return
	}
	g, token, ends, err := s.drive.SignIn(r.Context(), address, body.Code, s.codeKey)
	switch {
	case errors.Is(err, drive.ErrWrongCode):
		s.refuse(w, r, http.StatusBadRequest, wrongCode)
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.setGuestCookie(w, token, ends)
	writeJSON(w, http.StatusOK, map[string]any{"address": g.Address})
}

// signOut ends the guest's session on this browser.
//
//	POST /s/guest/api/sign-out   204
func (s *Server) signOut(w http.ResponseWriter, r *http.Request, _ call) {
	if ck, err := r.Cookie(s.guestCookie()); err == nil { // one cookie: with two, guestSession has ended both already
		if err := s.drive.SignOut(r.Context(), ck.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.clearGuestCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// guestMe says which address the guest signed in with, and which project's Drive this is.
//
//	GET /s/guest/api/me   200 {"address", "project"}
func (s *Server) guestMe(w http.ResponseWriter, _ *http.Request, c call) {
	writeJSON(w, http.StatusOK, map[string]any{"address": c.guest.Address, "project": s.project})
}

// guestItemJSON is a file or folder as a guest sees it: what they may do with it, and nothing about who else can see
// it or who added it.
type guestItemJSON struct {
	itemJSON
	Can []access.Action `json:"can"`
}

// guestCan is every action the guest may do at where, in the order access.AsAGuestAtAnItem lists them.
func guestCan(who access.Person, where *access.Place) []access.Action {
	out := []access.Action{}
	for _, a := range access.AsAGuestAtAnItem {
		if access.Decide(who, a, where) {
			out = append(out, a)
		}
	}
	return out
}

// guestSharedJSON is one item of a guest's "Shared with me": the item, its place by id, their role at it, who shared
// it with them (a member's name, or the project's), and until when, if the invitation ends.
type guestSharedJSON struct {
	guestItemJSON
	Place    int64       `json:"place"`
	Role     access.Role `json:"role"`
	SharedBy string      `json:"sharedBy"`
	Shared   time.Time   `json:"shared"`
	Until    *time.Time  `json:"until,omitempty"`
}

// guestSharedWithMe lists everything shared with the guest that lets them in now: each the highest item an invitation
// to them reaches on its way down (access.SharedWithMe), the most recently shared first. unavailable says that
// something else given to them would let them in but for sharing outside the project being switched off where it is:
// it is not listed (not even by name) until it is switched back on.
//
//	GET /s/guest/api/shared-with-me   200 {"items": [{"id", "name", "kind", "size", "changed", "opens", "can", "place",
//	                                                  "role", "sharedBy", "shared", "until"}], "unavailable"}
func (s *Server) guestSharedWithMe(w http.ResponseWriter, r *http.Request, c call) {
	shares, unavailable, err := s.sharedWithGuest(r.Context(), *c.guest)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []guestSharedJSON{}
	for _, sh := range shares {
		e := guestSharedJSON{guestItemJSON: guestItemJSON{itemJSON: itemOf(sh.item), Can: guestCan(c.person, sh.where)},
			Place: sh.place.ID, Role: access.RoleAt(c.person, sh.where), SharedBy: s.project}
		for _, g := range sh.grants {
			if g.Item == sh.item.ID && g.Guest == c.guest.ID {
				e.Shared = g.Given
				if m, ok := s.members.Current().Lookup(g.By); ok && strings.TrimSpace(m.DisplayName) != "" {
					e.SharedBy = strings.TrimSpace(m.DisplayName)
				}
				if !g.Ends.IsZero() {
					ends := g.Ends
					e.Until = &ends
				}
			}
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "unavailable": unavailable})
}

// sharedWithGuest is everything shared with the guest that lets them in now, decided on the whole way down to each
// item, the most recently shared first; unavailable says that something given to them would let them in but for
// sharing outside the project being switched off where it is.
func (s *Server) sharedWithGuest(ctx context.Context, g index.Guest) (out []aShare, unavailable bool, err error) {
	granted, err := s.drive.GrantedToGuest(ctx, g.ID)
	if err != nil {
		return nil, false, err
	}
	who := access.Person{Guest: &access.Guest{ID: g.ID}}
	for _, at := range granted {
		p, err := s.drive.Place(ctx, at.Place)
		if errors.Is(err, index.ErrNoPlace) {
			continue // a shared folder in the trash
		}
		if err != nil {
			return nil, false, err
		}
		it, err := s.drive.Item(ctx, p, at.Item)
		if errors.Is(err, index.ErrNoItem) {
			continue // in the trash, gone, or missing from the storage
		}
		if err != nil {
			return nil, false, err
		}
		path := idsOf(pathTo(it))
		grants, err := s.drive.GrantsOn(ctx, p, path)
		if err != nil {
			return nil, false, err
		}
		where := s.accessPlace(p, path, grants)
		if access.SharedWithMe(who, where) {
			out = append(out, aShare{place: p, item: it, grants: grants, where: where})
		}
		unavailable = unavailable || access.SwitchedOff(who, access.ListAsGuest, where)
	}
	return out, unavailable, nil
}

// guestItem is one file or folder a guest may see: what they may do with it, their role, and the folders above it that
// they may open (from the highest of them).
//
//	GET /s/guest/api/places/{place}/items/{item}   200 {"item": {…, "can"}, "path": [{"id", "name"}], "role"}
func (s *Server) guestItem(w http.ResponseWriter, r *http.Request, c call) {
	open, _ := openPath(c.person, c.access.Up(len(c.access.Path)-1), c.item.Above)
	s.used(r, c)
	writeJSON(w, http.StatusOK, map[string]any{
		"item": guestItemJSON{itemJSON: itemOf(*c.item), Can: guestCan(c.person, c.access)},
		"path": pathOf(open), "role": access.RoleAt(c.person, c.access),
	})
}

// guestItems lists what is in a folder a guest may see, as a member's listing does (looked at on the storage, ?wait),
// with what they may do with each, and nothing about who else can see it or who added it. The path holds only the
// folders they may open: from the one given to them down to this one.
//
//	GET /s/guest/api/places/{place}/items/{folder}/items[?wait]
//	200 {"path", "role", "can", "checking", "items": [{"id", "name", "kind", "size", "changed", "opens", "can"}]}
func (s *Server) guestItems(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, notAFolderToList)
	if !ok {
		return
	}
	checking, ok := s.looked(w, r, c, folder)
	if !ok {
		return
	}
	givenOn, ok := s.givenInside(w, r, c, folder)
	if !ok {
		return
	}
	if !r.URL.Query().Has("wait") {
		s.used(r, c)
	}
	open, _ := openPath(c.person, c.access, pathTo(*folder))
	s.writeListing(w, r, c, folder, map[string]any{
		"path": pathOf(open), "role": access.RoleAt(c.person, c.access), "can": guestCan(c.person, c.access), "checking": checking,
	}, func(it index.Item) any {
		return guestItemJSON{itemJSON: itemOf(it), Can: guestCan(c.person, c.access.Below(it.ID, givenOn[it.ID]))}
	})
}

// guestMove moves a file or folder inside what was given to the guest, into a folder there they may upload into: never
// out of it, never into a place's top folder, never into another place.
//
//	POST /s/guest/api/places/{place}/items/{item}/move   {"to": 42}
//	200 the item   404 a folder that is not there or that they may not add to   409 a name taken there
func (s *Server) guestMove(w http.ResponseWriter, r *http.Request, c call) {
	var body struct {
		To json.RawMessage `json:"to"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	folderID, ok := anId(string(body.To))
	if body.To == nil || !ok {
		s.refuse(w, r, http.StatusBadRequest, unreadable)
		return
	}
	var folder index.Item
	var err error
	if folderID != 0 {
		folder, err = s.drive.Item(r.Context(), *c.place, folderID)
	}
	if folderID == 0 || errors.Is(err, index.ErrNoItem) {
		s.refuse(w, r, http.StatusNotFound, noSuchDestination)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	where, err := s.accessAt(r.Context(), *c.place, idsOf(pathTo(folder)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !access.Decide(c.person, access.UploadAsGuest, where) {
		s.refuse(w, r, http.StatusNotFound, noSuchDestination) // the same answer as for a folder that is not there
		return
	}
	var it index.Item
	if !s.apart(w, r, c, func() { it, err = s.drive.Move(r.Context(), *c.place, *c.item, &folder) }) {
		return
	}
	var taken *drive.Taken
	switch {
	case err == nil:
		s.used(r, c)
		writeJSON(w, http.StatusOK, itemOf(it))
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, takenThere)
	default:
		s.changeFailed(w, r, err, c.item.Name)
	}
}

// guestDelete moves a file or folder inside what was given to the guest into its place's trash, where its owner or
// managers find it. A guest has no trash of their own, and does not restore.
//
//	DELETE /s/guest/api/places/{place}/items/{item}   204
func (s *Server) guestDelete(w http.ResponseWriter, r *http.Request, c call) {
	// Who deleted it is not recorded as a member: only whoever sees the place's whole trash restores it.
	var err error
	if !s.apart(w, r, c, func() { _, err = s.drive.Delete(r.Context(), *c.place, *c.item, "") }) {
		return
	}
	if err != nil {
		s.changeFailed(w, r, err, c.item.Name)
		return
	}
	s.used(r, c)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// SendCodes works through the asks for a sign-in link (askForCode), one at a time, until ctx ends: for each address that
// is a guest's with something shared with them that lets them in now, and not a member's, a code is made and its link
// mailed, unless one was sent to the address lately (drive.CodeEvery) or too many to everyone (drive.CodesPerHourOverall):
// reaching either is logged, once a minute at most. Nothing is logged about an address that is no guest's, and no
// address and no code is ever logged.
func (s *Server) SendCodes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ask := <-s.codes:
			s.askingMu.Lock()
			delete(s.asking, ask.address)
			s.askingMu.Unlock()
			if err := s.sendCode(ctx, ask); err != nil && ctx.Err() == nil {
				s.log.Error("a guest's sign-in link could not be made", "error", err)
			}
		}
	}
}

// codeAsk is an address that asked for a sign-in link, the language of the page that asked (the mail is in it), and
// where the guest was going, if the page said (place and item: the link leads there).
type codeAsk struct {
	address     string
	lang        text.Lang
	place, item int64
}

func (s *Server) sendCode(ctx context.Context, ask codeAsk) error {
	if _, member := s.members.Current().ByEmail(ask.address); member {
		return nil
	}
	g, err := s.drive.GuestByAddress(ctx, ask.address)
	if errors.Is(err, index.ErrNoGuest) {
		return nil
	}
	if err != nil {
		return err
	}
	open, err := s.guestMayOpenAnything(ctx, g)
	if err != nil || !open {
		return err
	}
	code, result, err := s.drive.NewCode(ctx, g, s.codeKey)
	if err != nil {
		return err
	}
	// Anyone who knows a guest's address causes these lines by asking again: each is written at most once in seldom.Each.
	switch result {
	case index.TooSoon:
		s.codesTooSoon.Log(s.log, slog.LevelInfo, "a sign-in link was asked for a guest who was sent one lately: none is sent, and the one sent still works",
			"guest", g.ID, "every", drive.CodeEvery.String())
		return nil
	case index.TooMany:
		s.codesTooMany.Log(s.log, slog.LevelWarn, "no sign-in link is sent now: as many were sent in the last hour as the Drive sends to all guests together",
			"most", drive.CodesPerHourOverall)
		return nil
	}
	_ = s.mail.Deliver(ctx, mail.Message{
		To:      g.Address,
		Subject: codeSubject.In(ask.lang, mail.Clean(s.project)),
		Body:    codeBody.In(ask.lang, s.guestLink(ask.place, ask.item, g.Address, code)),
		About:   []any{"mail", "sign-in link", "guest", g.ID},
	})
	return nil
}
