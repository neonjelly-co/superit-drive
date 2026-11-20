package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
)

// The guests' part of the public prefix: signing in with a code mailed to them, their pages, and what they do with what
// was given to them. Its routes are in routes.go with all the others, and pass guard like them; guardGuest is guard's
// part for them.
//
// A request here is a guest's (the one whose session its cookie names) or nobody's: nothing it carries about a member
// (Drive-Secret, Remote-User-Id) is read, so a member is nobody here, and a forged identity does nothing. A guest is
// decided by the access package as a person of their own kind, with actions only guests have.
//
// Signing in. The page asks for a code for an address (askForCode): the answer is the same whatever the address, at
// once, and the work is done in the background (SendCodes), one address at a time: a code is sent only when the address
// is a guest's with something shared with them that lets them in now, at most CodesPerHour an hour to one address and
// CodesPerHourOverall to all. Then the code and the address sign in (signIn): every try does the same work, so how long
// it takes tells nothing either. A session lasts drive.SessionLasts on that browser, in a cookie that only the guests'
// prefix gets (Path), that no script reads (HttpOnly), that goes only over HTTPS when the Drive's address is HTTPS
// (Secure, and the __Secure- name), and that no other site's page sends along with a change (SameSite=Lax; it is sent
// when a guest opens a link to the Drive from their mail). Every change also needs the Drive's own Origin, as a
// member's does.
//
// A session ends when it ends, when the guest signs out, and at once when the last of what was given to them is taken
// away or ends (the index removes the guest with it), or no longer lets them in anywhere (it is in the trash, gone from
// the storage, in the My files of someone who left, or its giver may no longer share it), or when their address appears
// in the member list (they become a member, and sign in as one). Once ended it does not come back: they sign in again.

// codesWaiting is how many asks for a code wait for SendCodes; more are dropped, and answered the same.
const codesWaiting = 64

const (
	codeAsked   = "If anything on this Drive is shared with this address, a code is on its way to it. It works for 10 minutes."
	wrongCode   = "This code is not right, or it no longer works. Check it, or ask for a new one."
	tooManyCode = "Too many wrong codes were tried on this Drive in the last hour. Try again later."
	signInFirst = "Sign in to see this."
	notAnEmail  = "This is not an email address the Drive can send mail to. Check it, and try again."
)

// guardGuest is guard for a route under the guests' prefix: only from the Drive's own pages for a change, then the guest
// whose session the cookie names (or nobody, which only signing in allows), the place and the item the request is
// about and the access given on the way down to it, then access.Decide for the guest.
func (s *Server) guardGuest(w http.ResponseWriter, r *http.Request, rt route) {
	leave, ok := s.guests.enter(r.Context())
	if !ok {
		if r.Context().Err() == nil {
			w.Header().Set("Retry-After", "5")
			s.refuse(w, r, http.StatusServiceUnavailable, visitorsBusy)
		}
		return
	}
	defer leave()
	if !s.fromOwnPage(r) {
		s.log.Warn("refused a change that did not come from the Drive's own pages",
			"method", r.Method, "path", r.URL.Path, "origin", r.Header.Get("Origin"), "address", s.origin)
		s.refuse(w, r, http.StatusForbidden, refusedOrigin)
		return
	}
	var c call
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
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": signInFirst, "signIn": true})
		return
	}
	if rt.place != "" && !s.findWhere(w, r, rt, &c) {
		return
	}
	if !access.Decide(c.person, rt.action, c.access) || rt.serve == nil {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	if rt.action == access.DownloadAsGuest || rt.action == access.UploadAsGuest {
		leave() // it waits for its transfer slot in the outsiders' own line, which bounds them by itself
	}
	rt.serve(w, r, c)
}

// guestSession is the guest whose session the request's cookie names: nil for none, and stale when a cookie was sent
// for a session that has ended (or whose guest is now a member).
func (s *Server) guestSession(r *http.Request) (guest *index.Guest, stale bool, err error) {
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
	// Nothing given to them lets them in any more (in the trash, gone from the storage, held with its giver's My files,
	// sharing outside the project switched off): their sessions end, everywhere, for good.
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

// guestMayOpenAnything says whether something given to the guest lets them in now: an item an invitation to them is on
// that they may open, decided by the access package on the whole way down to it. It stops at the first.
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
		if access.Decide(who, access.ListAsGuest, where) {
			return true, nil
		}
	}
	return false, nil
}

// guestCookie is the name of the guests' session cookie: with the __Secure- prefix where the Drive's address is HTTPS,
// so that a page over plain HTTP can never set one.
func (s *Server) guestCookie() string {
	if s.secure() {
		return "__Secure-drive-guest"
	}
	return "drive-guest"
}

func (s *Server) secure() bool {
	return s.address != nil && strings.EqualFold(s.address.Scheme, "https")
}

func (s *Server) setGuestCookie(w http.ResponseWriter, token string, ends time.Time) {
	http.SetCookie(w, &http.Cookie{Name: s.guestCookie(), Value: token, Path: GuestPrefix,
		MaxAge: int(ends.Sub(s.drive.Now()).Seconds()), HttpOnly: true, Secure: s.secure(), SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearGuestCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: s.guestCookie(), Value: "", Path: GuestPrefix, MaxAge: -1, HttpOnly: true,
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
		s.refuse(w, r, http.StatusServiceUnavailable, "The interface was not built into this Drive. Build it with `make build`.")
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

// askForCode asks for a code to be mailed to an address, to sign in as a guest. The answer is the same for every
// address that is one, at once: whether a code is sent (only to a guest's address with something shared with them, at
// most CodesPerHour an hour) is worked out in the background (SendCodes).
//
//	POST /s/guest/api/code   {"address"}
//	202 {"message"}
func (s *Server) askForCode(w http.ResponseWriter, r *http.Request, _ call) {
	var body struct {
		Address string `json:"address"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	address, ok := guestAddress(body.Address)
	if !ok {
		s.refuse(w, r, http.StatusBadRequest, notAnEmail)
		return
	}
	select {
	case s.codes <- address:
	default:
		if now := time.Now().Unix(); s.codesDropped.Swap(now) < now-60 {
			s.log.Warn("asks for a code were dropped: too many are waiting")
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": codeAsked})
}

// signIn signs a guest in with a code mailed to their address, and keeps them signed in on this browser for
// drive.SessionLasts: the answer sets the session's cookie.
//
//	POST /s/guest/api/sign-in   {"address", "code"}
//	200 {"address"}   400 a code that is not right, or no longer works   429 too many wrong codes lately
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
	code := strings.Join(strings.Fields(body.Code), "")
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		s.refuse(w, r, http.StatusBadRequest, "A code is six digits. Type it as the mail has it.")
		return
	}
	g, token, ends, err := s.drive.SignIn(r.Context(), address, code, s.codeKey)
	switch {
	case errors.Is(err, drive.ErrTooManyTries):
		w.Header().Set("Retry-After", "3600")
		s.refuse(w, r, http.StatusTooManyRequests, tooManyCode)
		return
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
	if ck, err := r.Cookie(s.guestCookie()); err == nil {
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
// to them reaches on its way down (access.SharedWithMe), the most recently shared first.
//
//	GET /s/guest/api/shared-with-me   200 {"items": [{"id", "name", "kind", "size", "changed", "opens", "can", "place",
//	                                                  "role", "sharedBy", "shared", "until"}]}
func (s *Server) guestSharedWithMe(w http.ResponseWriter, r *http.Request, c call) {
	shares, err := s.sharedWithGuest(r.Context(), *c.guest)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// sharedWithGuest is everything shared with the guest that lets them in now, decided on the whole way down to each
// item, the most recently shared first.
func (s *Server) sharedWithGuest(ctx context.Context, g index.Guest) ([]aShare, error) {
	granted, err := s.drive.GrantedToGuest(ctx, g.ID)
	if err != nil {
		return nil, err
	}
	who := access.Person{Guest: &access.Guest{ID: g.ID}}
	var out []aShare
	for _, at := range granted {
		p, err := s.drive.Place(ctx, at.Place)
		if errors.Is(err, index.ErrNoPlace) {
			continue // a shared folder in the trash
		}
		if err != nil {
			return nil, err
		}
		it, err := s.drive.Item(ctx, p, at.Item)
		if errors.Is(err, index.ErrNoItem) {
			continue // in the trash, gone, or missing from the storage
		}
		if err != nil {
			return nil, err
		}
		path := idsOf(pathTo(it))
		grants, err := s.drive.GrantsOn(ctx, p, path)
		if err != nil {
			return nil, err
		}
		where := s.accessPlace(p, path, grants)
		if access.SharedWithMe(who, where) {
			out = append(out, aShare{place: p, item: it, grants: grants, where: where})
		}
	}
	return out, nil
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
	folder, ok := s.folder(w, r, c, "This is a file, not a folder.")
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
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
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
	it, err := s.drive.Move(r.Context(), *c.place, *c.item, &folder)
	var taken *drive.Taken
	switch {
	case err == nil:
		s.used(r, c)
		writeJSON(w, http.StatusOK, itemOf(it))
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already something called “"+taken.Name+"” in that folder. Rename one of them first.")
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
	if _, err := s.drive.Delete(r.Context(), *c.place, *c.item, ""); err != nil {
		s.changeFailed(w, r, err, c.item.Name)
		return
	}
	s.used(r, c)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// SendCodes works through the asks for a code (askForCode), one at a time, until ctx ends: for each address that is a
// guest's with something shared with them that lets them in now, and not a member's, a code is made and mailed, unless
// too many were sent lately (drive.CodesPerHour, drive.CodesPerHourOverall). Nothing is logged about an address that is
// no guest's, and no address and no code is ever logged.
func (s *Server) SendCodes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case address := <-s.codes:
			if err := s.sendCode(ctx, address); err != nil && ctx.Err() == nil {
				s.log.Error("a guest's code could not be made", "error", err)
			}
		}
	}
}

func (s *Server) sendCode(ctx context.Context, address string) error {
	if _, member := s.members.Current().ByEmail(address); member {
		return nil
	}
	g, err := s.drive.GuestByAddress(ctx, address)
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
	code, ok, err := s.drive.NewCode(ctx, g, s.codeKey)
	if err != nil {
		return err
	}
	if !ok {
		s.log.Info("a guest's code was not sent: too many codes were sent lately", "guest", g.ID)
		return nil
	}
	project := mail.Clean(s.project)
	_ = s.mail.Deliver(ctx, mail.Message{
		To:      g.Address,
		Subject: "Your code for the Drive of " + project,
		Body: fmt.Sprintf("Your code is %s\n\nType it on the page where you asked for it. It works once, for 10 minutes.\n\n"+
			"If you did not ask for it, you can ignore this mail: nobody can sign in without the code.\n", code),
		About: []any{"mail", "code", "guest", g.ID},
	})
	return nil
}
