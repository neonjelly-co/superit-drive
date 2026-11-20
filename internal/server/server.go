// Package server is the Drive's HTTP side: the JSON API the interface calls, the interface's own files, the pages
// visitors with a public link see under the public prefix, and the guests' pages and API under its guests' part.
//
// Every route is listed in routes.go and registered in Handler, and nowhere else. Each request passes the same steps
// in guard before its handler runs: who is asking (under the guests' prefix, the guest whose session the cookie names,
// or nobody; elsewhere under the public prefix always a visitor, through the link its address names; anywhere else a
// member, identify), whether it may change anything (only from the Drive's own pages, fromOwnPage; never through a
// link), which place it concerns (if any), which item in that place or in its trash, or which of the person's own
// uploads in progress into it (if any), the access given on the place and on the way down to the item (accessAt), and
// whether they may (access.Decide). A handler only ever sees a request that passed them all. A test fails if anything
// outside this file registers a handler.
//
// transfers.go has the routes that move a file's bytes (downloads, zips, uploads), which take turns for the transfer
// slots (package transfers); visitors.go the public prefix, links.go making and removing public links; guests.go the
// guests' sign-in and pages, invitations.go inviting them.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/text"
	"github.com/neonjelly-co/superit-drive/internal/transfers"
)

// Config is what the server needs.
type Config struct {
	ProjectName string
	// Address is the Drive's own address as people reach it (DRIVE_ADDRESS). Changes are accepted only from pages
	// there.
	Address      *url.URL
	HeaderSecret string
	Members      interface{ Current() *members.List }
	Drive        *drive.Drive
	// Capacity is the size of the Drive storage in bytes (DRIVE_CAPACITY), or 0 when it is not known.
	Capacity int64
	// Mail sends the Drive's own mail, such as "shared with you"; nil, or one without a mail server, sends none.
	Mail *mail.Mailer
	// Interface is the built interface (index.html and assets/).
	Interface fs.FS
	Log       *slog.Logger
}

// Server answers the Drive's HTTP requests.
type Server struct {
	project  string
	origin   string // the origin of Config.Address, such as https://files.example.com
	secret   string
	members  interface{ Current() *members.List }
	drive    *drive.Drive
	capacity int64
	mail     *mail.Mailer
	address  *url.URL // DRIVE_ADDRESS, for the links in mail
	ui       fs.FS
	log      *slog.Logger
	// slots lets at most transfers.Slots transfers be in flight, of which visitors and guests together hold at most
	// transfers.OutsiderSlots.
	slots *transfers.Gate
	// visitors bounds how many visitors' requests are worked on at once (visitors.go); guests how many guests' (and
	// requests to sign in as one).
	visitors, guests *admission
	// codeKey keys the HMAC of the codes mailed to guests (guests.go): derived from the header secret, which the index
	// never holds.
	codeKey []byte
	// codes are the addresses that asked for a code, waiting for SendCodes (guests.go); codesDropped is when asks were
	// last dropped for want of room (Unix seconds), so that it is logged once a minute at most.
	codes        chan codeAsk
	codesDropped atomic.Int64
	// sendPolicy is the Content-Security-Policy of an upload-only link's page (sendin.go).
	sendPolicy string
}

// New makes a server.
func New(c Config) *Server {
	key := hmac.New(sha256.New, []byte(c.HeaderSecret))
	key.Write([]byte("superit-drive: guests' codes"))
	s := &Server{project: c.ProjectName, secret: c.HeaderSecret, members: c.Members, drive: c.Drive, capacity: c.Capacity,
		mail: c.Mail, address: c.Address, ui: c.Interface, log: c.Log,
		slots:    transfers.NewGate(transfers.Slots, transfers.OutsiderSlots),
		visitors: newAdmission(visitorsAtOnce, visitorsWaiting, visitorWait),
		guests:   newAdmission(visitorsAtOnce, visitorsWaiting, visitorWait),
		codeKey:  key.Sum(nil),
		codes:    make(chan codeAsk, codesWaiting)}
	if c.Address != nil {
		s.origin = originOf(c.Address)
	}
	s.sendPolicy = sendPolicyFor(s.origin)
	return s
}

// A route is one thing the Drive answers. It names the action it is and, for an action in a place, where the place's
// id is in the address. guard runs the same steps for every route before serve.
type route struct {
	pattern string
	action  access.Action
	// place is the name of the path value holding the place's id, or "" when the action concerns no place.
	place string
	// inTrash is true when the place must be a shared folder in the trash (to restore it). For every other route a
	// place in the trash is not found, like one that does not exist.
	inTrash bool
	// item is the name of the path value holding the id of an item in that place (at any depth, not in the trash),
	// or "" when the action concerns the place as a whole. A route with an item must have a place.
	item string
	// trashed is, instead, the name of the path value holding the id of an item in that place's trash.
	trashed string
	// upload is, instead, the name of the path value holding the id of an upload in progress into a folder of that
	// place, by the person asking: the request is about the folder it goes into, as an upload into it is. Another
	// person's upload is not found.
	upload string
	// link is, for a route under the public prefix (and only there), the name of the path value holding the token of
	// the public link the visitor came with. The place is the one the link's item is in; item, when given, names an item
	// in it, and the request is about the link's item itself otherwise. Every route under the public prefix but the
	// guests' has one.
	link string
	// guest is true for a route under the guests' prefix (and only there): the person is the guest whose session the
	// cookie names, or nobody. A guest's route with a place always names an item in it, or an upload in progress.
	guest bool
	serve func(http.ResponseWriter, *http.Request, call)
}

// PublicPrefix is where everything a visitor reaches lives: the one part of the Drive's address that the reverse proxy
// opens without signing in. A request under it is a visitor's, always, whatever it says about who sent it; no other
// request ever is.
const PublicPrefix = "/s/"

// underPublicPrefix says whether a request's path is under the public prefix, as routes.go and the mux see it.
func underPublicPrefix(path string) bool {
	return strings.HasPrefix(path, PublicPrefix) || path == "/s"
}

// patternUnderPublicPrefix says whether a route's pattern ("GET /s/{token}", or a path alone) is under the public
// prefix.
func patternUnderPublicPrefix(pattern string) bool {
	return underPublicPrefix(pathOfPattern(pattern))
}

// GuestPrefix is where everything a guest reaches lives, inside the public prefix: their pages, the interface's files
// for them, and their API. A request under it is a guest's, or nobody's; never a member's, never a visitor's. No link's
// token is ever "guest".
const GuestPrefix = "/s/guest"

// underGuestPrefix says whether a request's path is under the guests' prefix.
func underGuestPrefix(path string) bool {
	return path == GuestPrefix || strings.HasPrefix(path, GuestPrefix+"/")
}

// patternUnderGuestPrefix says whether a route's pattern is under the guests' prefix.
func patternUnderGuestPrefix(pattern string) bool {
	return underGuestPrefix(pathOfPattern(pattern))
}

func pathOfPattern(pattern string) string {
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}

// call is what a handler gets: who is asking, the place the request concerns (nil for no place), the item in it or in
// its trash (nil for none), and where the request is as the access package saw it. All have already passed guard.
type call struct {
	// member is the member asking; for a visitor it is empty, and link is the public link they came with; for a guest
	// it is empty, and guest is who they are.
	member members.Member
	link   *index.Link
	guest  *index.Guest
	// person is who is asking, as the access package decided on them.
	person access.Person
	place  *index.Place
	access *access.Place
	// grants is what was given on the place's top folder and on the way down to the item, as access saw it.
	grants  []index.Grant
	item    *index.Item
	trashed *index.TrashItem
	// backTo is, for an item in the trash, the folder it goes back to when restored (nil: the place's top folder).
	backTo *index.Item
	// upload is the person's upload in progress the request is about; item is then the folder it goes into (nil: the
	// place's top folder).
	upload *index.Upload
}

// by is who is asking, as what they do is recorded: a member by user id, a guest by id and address.
func (c call) by() drive.By {
	if c.guest != nil {
		return drive.By{Guest: *c.guest}
	}
	return drive.By{Member: c.member.UserID}
}

// Handler is the Drive's whole HTTP handler. This is the only place routes are registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		if patternUnderPublicPrefix(rt.pattern) != (rt.link != "" || rt.guest) || patternUnderGuestPrefix(rt.pattern) != rt.guest ||
			(rt.link != "" && rt.guest) {
			panic("server: route " + rt.pattern + ": every route under the public prefix, and no other, is a visitor's, with a link, " +
				"or under the guests' prefix a guest's")
		}
		if rt.link != "" && (rt.place != "" || rt.trashed != "" || rt.upload != "" || rt.inTrash || !visitorsMethod(rt) ||
			!slices.Contains(access.ThroughALink, rt.action)) {
			panic("server: route " + rt.pattern + " is a visitor's: it only reads (GET), or sends a file into its link's folder " +
				"(POST, and only that), through its link, with an action through a link")
		}
		if rt.guest && (!slices.Contains(access.AsAGuest, rt.action) || rt.trashed != "" || rt.inTrash ||
			(rt.place != "" && rt.item == "" && rt.upload == "")) {
			panic("server: route " + rt.pattern + " is a guest's: it has a guest's action, and in a place always an item or an upload")
		}
		if !rt.guest && slices.Contains(access.AsAGuest, rt.action) {
			panic("server: route " + rt.pattern + " is not a guest's, with a guest's action")
		}
		if rt.link == "" && slices.Contains(access.ThroughALink, rt.action) {
			panic("server: route " + rt.pattern + " is a member's, with an action through a link")
		}
		if (rt.item != "" || rt.trashed != "" || rt.upload != "" || rt.inTrash) && rt.place == "" && rt.link == "" {
			panic("server: route " + rt.pattern + " names an item, an upload or a place in the trash, but no place")
		}
		if (rt.item != "" && rt.trashed != "") || (rt.upload != "" && (rt.item != "" || rt.trashed != "")) {
			panic("server: route " + rt.pattern + " names more than one of an item, an item in the trash and an upload")
		}
		if rt.inTrash && (rt.item != "" || rt.trashed != "" || rt.upload != "") {
			panic("server: route " + rt.pattern + " names an item or an upload in a place in the trash")
		}
		mux.Handle(rt.pattern, s.guard(rt))
	}
	// Anything else is still identified first, so an outsider learns nothing from a wrong address. Under the public
	// prefix, anything else is a visitor's too: its pattern holds no token, so it is looked up as a token that is not
	// there, the same way as any other, and answered as a link that does not exist (or 405, for a change); except under
	// the guests' prefix, where it is a guest's (or nobody's), answered as an address that is not there.
	mux.Handle("/", s.guard(route{pattern: "/", action: access.Enter, serve: s.notFound}))
	visitors := s.guard(route{pattern: PublicPrefix, link: "token", action: access.SeeThroughLink})
	guests := s.guard(route{pattern: GuestPrefix + "/", guest: true, action: access.SignInAsGuest, serve: s.notFound})
	mux.Handle(PublicPrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if underGuestPrefix(r.URL.Path) {
			guests.ServeHTTP(w, r)
			return
		}
		visitors.ServeHTTP(w, r)
	}))
	return securityHeaders(mux)
}

// visitorsMethod says whether a visitor's route has the one method it may: GET, which only reads, for all but sending a
// file in through an upload-only link (UploadThroughLink, into the link's own folder), which is POST.
func visitorsMethod(rt route) bool {
	if rt.action == access.UploadThroughLink {
		return strings.HasPrefix(rt.pattern, "POST ") && rt.item == ""
	}
	return strings.HasPrefix(rt.pattern, "GET ")
}

// guard is the gate every route passes: identify the person (under the public prefix, always a visitor through the
// link the address names), refuse a change from anywhere but the Drive's own pages (and any under the public prefix),
// find the place and the item in it, gather the access given there, ask access.Decide, then serve.
func (s *Server) guard(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rt.guest {
			s.guardGuest(w, r, rt)
			return
		}
		if rt.link != "" {
			s.guardVisitor(w, r, rt)
			return
		}
		member, refused := s.identify(r)
		if refused != nil {
			s.refuse(w, r, refused.status, refused.message)
			return
		}
		if !s.fromOwnPage(r) {
			// Most often DRIVE_ADDRESS is not the address people use; the log says both, so it can be put right.
			s.log.Warn("refused a change that did not come from the Drive's own pages",
				"method", r.Method, "path", r.URL.Path, "origin", r.Header.Get("Origin"), "address", s.origin)
			s.refuse(w, r, http.StatusForbidden, refusedOrigin)
			return
		}
		c := call{member: member, person: personOf(member)}
		if rt.place != "" && !s.findWhere(w, r, rt, &c) {
			return
		}
		if !access.Decide(c.person, rt.action, c.access) {
			if access.SwitchedOff(c.person, rt.action, c.access) {
				s.switchedOff(w, r, c)
				return
			}
			// The same answer as for a place or an item that does not exist.
			s.refuse(w, r, http.StatusNotFound, notHere)
			return
		}
		rt.serve(w, r, c)
	})
}

// findWhere finds the place a route concerns and the item in it, or in its trash, or the person's upload in progress
// into it, and gathers the access given on the place's top folder and on the way down to the item (for an upload, the
// folder it goes into). ok is false when the person was already answered.
//
// The item is looked up before access decides, because what someone may do at an item depends on where it is. It is
// looked up only inside the place, so an item of another place is never found. Every refusal, and every place or item
// that is not there, gets the one answer notHere: nobody learns from it whether a place or an item they cannot reach
// exists, including inside a place where they can reach something else.
func (s *Server) findWhere(w http.ResponseWriter, r *http.Request, rt route, c *call) (ok bool) {
	p, err := s.findPlace(r, rt.place, rt.inTrash)
	if errors.Is(err, index.ErrNoPlace) {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return false
	}
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	c.place = &p
	var path []index.Item // the item, after the folders above it; none for the place's top folder
	if rt.item != "" {
		it, err := s.findItem(r, p, rt.item)
		if errors.Is(err, index.ErrNoItem) {
			s.refuse(w, r, http.StatusNotFound, notHere)
			return false
		}
		if err != nil {
			s.fail(w, r, err)
			return false
		}
		c.item = &it
		path = pathTo(it)
	}
	if rt.trashed != "" {
		t, err := s.findTrashed(r, p, rt.trashed)
		if errors.Is(err, index.ErrNoItem) {
			s.refuse(w, r, http.StatusNotFound, notHere)
			return false
		}
		if err != nil {
			s.fail(w, r, err)
			return false
		}
		c.trashed = &t
		// Restoring is decided where the item goes back to, as if it were there: whoever could have deleted it from
		// there may restore it.
		if c.backTo, err = s.drive.RestoreTo(r.Context(), p, t); err != nil {
			s.fail(w, r, err)
			return false
		}
		if c.backTo != nil {
			path = pathTo(*c.backTo)
		}
		path = append(path, t.Item)
	}
	if rt.upload != "" {
		// Found only among the person's own uploads in this place: another person's is not there for them.
		u, err := s.drive.UploadOf(r.Context(), p, r.PathValue(rt.upload), c.by())
		if errors.Is(err, index.ErrNoUpload) {
			s.refuse(w, r, http.StatusNotFound, notHere)
			return false
		}
		if err != nil {
			s.fail(w, r, err)
			return false
		}
		c.upload = &u
		if u.FolderID != 0 {
			// Decided where it goes, as an upload into that folder is: the folder must still be in the place.
			folder, err := s.drive.Item(r.Context(), p, u.FolderID)
			if errors.Is(err, index.ErrNoItem) || (err == nil && !folder.Folder) {
				s.refuse(w, r, http.StatusNotFound, notHere)
				return false
			}
			if err != nil {
				s.fail(w, r, err)
				return false
			}
			c.item = &folder
			path = pathTo(folder)
		}
	}
	if c.grants, err = s.drive.GrantsOn(r.Context(), p, idsOf(path)); err != nil {
		s.fail(w, r, err)
		return false
	}
	c.access = s.accessPlace(p, idsOf(path), c.grants)
	if c.trashed != nil {
		c.access.DeletedBy = c.trashed.DeletedBy
	}
	return true
}

// findPlace finds the place whose id is the path value name: one that is not in the trash, or, with inTrash, a shared
// folder that is.
func (s *Server) findPlace(r *http.Request, name string, inTrash bool) (index.Place, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return index.Place{}, index.ErrNoPlace
	}
	if inTrash {
		return s.drive.DeletedPlace(r.Context(), id)
	}
	return s.drive.Place(r.Context(), id)
}

func (s *Server) findItem(r *http.Request, p index.Place, name string) (index.Item, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return index.Item{}, index.ErrNoItem
	}
	return s.drive.Item(r.Context(), p, id)
}

func (s *Server) findTrashed(r *http.Request, p index.Place, name string) (index.TrashItem, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return index.TrashItem{}, index.ErrNoItem
	}
	return s.drive.TrashItem(r.Context(), p, id)
}

// fromOwnPage says whether a request may change something: only a page served from the Drive's own address may.
//
// Members are signed in by a cookie that the reverse proxy in front of the Drive holds, so a page on any other site
// could make their browser send a change (cross-site request forgery). Browsers add an Origin header to every request
// that is not a GET or HEAD, and a page cannot change it, so every other method must carry the Drive's own origin.
// GET and HEAD never change anything.
func (s *Server) fromOwnPage(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	o, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || (o.Scheme != "http" && o.Scheme != "https") || o.Host == "" || s.origin == "" {
		return false // no Origin, "null" (a sandboxed or privacy-stripped page), or not an origin at all
	}
	return originOf(o) == s.origin
}

// originOf is the origin of an address as a browser writes it in Origin: scheme and host in lower case, and the port
// only when it is not the scheme's default.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" || strings.Contains(host, ":") {
		host = net.JoinHostPort(host, port)
		host = strings.TrimSuffix(host, ":") // an IPv6 address without a port keeps its brackets only
	}
	return scheme + "://" + host
}

// accessAt describes the place p, at the item whose path is given (the ids of the folders above it, then the item; none
// for the place's top folder), the way the access package needs it: with everything given on the place's top folder
// and on that path. Every decision about a place or an item is made on what this gathers.
func (s *Server) accessAt(ctx context.Context, p index.Place, path []int64) (*access.Place, error) {
	grants, err := s.drive.GrantsOn(ctx, p, path)
	if err != nil {
		return nil, err
	}
	return s.accessPlace(p, path, grants), nil
}

// accessPlace describes the place p, at the item whose path is given, with the grants already read for it, at the
// Drive's time now (for grants that end), and with sharing outside the project off where p says it is: for the whole
// project, or for this shared folder (read with the place).
func (s *Server) accessPlace(p index.Place, path []int64, grants []index.Grant) *access.Place {
	where := &access.Place{Deleted: !p.Deleted.IsZero(), Path: path, Grants: make([]access.Grant, 0, len(grants)), Now: s.drive.Now(),
		NoSharingOutside: p.OutsideOff.Off() || p.ProjectOutsideOff.Off()}
	list := s.members.Current()
	for _, g := range grants {
		where.Grants = append(where.Grants, accessGrant(g, list))
	}
	switch p.Kind {
	case index.MyFiles:
		where.Kind = access.MyFiles
		where.Owner = p.Owner
		// Held while its owner is out of the member list, and while it is being handed over or deleted, whatever the list
		// says.
		_, stays := s.members.Current().Lookup(p.Owner)
		where.OwnerLeft = !stays || p.Acting != ""
	case index.SharedFolder:
		where.Kind = access.SharedFolder
	} // any other kind stays without one, and access refuses it
	return where
}

// accessGrant is a grant as the access package needs it: for an invitation, with whoever gave it as the member list has
// them now.
func accessGrant(g index.Grant, list *members.List) access.Grant {
	a := access.Grant{On: g.Item, Member: g.Member, Everyone: g.Everyone, Guest: g.Guest, Role: access.Role(g.Role), Ends: g.Ends}
	if g.Guest != 0 {
		a.By = memberAsPerson(g.By, list)
	}
	return a
}

// memberAsPerson is the member with the user id given as the access package needs them, as the member list has them now:
// without a role once they have left it.
func memberAsPerson(userID string, list *members.List) access.Person {
	if m, ok := list.Lookup(userID); ok {
		return personOf(m)
	}
	return access.Person{UserID: userID}
}

// pathTo is an item after the folders above it, from the top of its place down.
func pathTo(it index.Item) []index.Item {
	return append(append(make([]index.Item, 0, len(it.Above)+1), it.Above...), it)
}

func idsOf(items []index.Item) []int64 {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// personOf describes a member the way the access package needs it.
func personOf(m members.Member) access.Person {
	return access.Person{UserID: m.UserID, Role: m.Role}
}

// refuse answers with an error the person can read, in the request's language (text.Of), with values when the message
// has verbs for them: JSON for the API (the guests' too), a page under the public prefix (where every "not here" is the
// one answer for a link that does not work), plain text otherwise.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, status int, message text.Message, values ...any) {
	if strings.HasPrefix(r.URL.Path, GuestPrefix+"/api/") || strings.HasPrefix(r.URL.Path, "/api/") {
		refuseWith(w, r, status, nil, message, values...)
		return
	}
	if underPublicPrefix(r.URL.Path) && !underGuestPrefix(r.URL.Path) {
		s.visitorRefusal(w, r, status, message, values...)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Language", text.Of(r).Code())
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message.In(text.Of(r), values...) + "\n"))
}

// refuseWith answers with JSON that says why, in the request's language, as "error", and more besides. It is the one
// place that writes "error": every text a person reads goes through text, in both languages.
func refuseWith(w http.ResponseWriter, r *http.Request, status int, more map[string]any, message text.Message, values ...any) {
	out := map[string]any{"error": message.In(text.Of(r), values...)}
	for k, v := range more {
		out[k] = v
	}
	w.Header().Set("Content-Language", text.Of(r).Code())
	writeJSON(w, status, out)
}

// fail answers a request that went wrong on the Drive's side. The details go to the log, never to the person.
// A request whose person has gone (their connection closed, and with it the request's context) is not the Drive's
// failure: nobody is answered, and nothing is logged, so that nobody can fill the log by hanging up.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	s.log.Error("a request failed", "route", r.Pattern, "error", err)
	s.refuse(w, r, http.StatusInternalServerError, wentWrong)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, _ call) {
	s.refuse(w, r, http.StatusNotFound, nothingHere)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// securityHeaders adds the headers every answer carries: no framing, no guessing of content types, no third-party
// anything, and no other site loading a file's bytes into its own page (an image or script tag sends the member's
// cookie along). Under the public prefix, also: no copy kept by the browser or anything on the way, and not listed by
// search engines; and, where the address is the secret (a link's), no Referer sent from it and a policy under which
// nothing at all runs or loads but the page's own style (visitors.go). The guests' pages run the interface's own
// script, under the members' policy, and send a Referer only to the Drive itself (so that their changes carry the
// Drive's Origin, which no-referrer would make null).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", memberPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if underPublicPrefix(r.URL.Path) {
			h.Set("X-Robots-Tag", "noindex, nofollow")
			h.Set("Cache-Control", "no-store")
			if !underGuestPrefix(r.URL.Path) {
				h.Set("Content-Security-Policy", visitorPolicy)
				h.Set("Referrer-Policy", "no-referrer")
			}
		}
		next.ServeHTTP(w, r)
	})
}

// memberPolicy is the Content-Security-Policy of the interface's pages, the members' and the guests': nothing but the
// Drive's own files loads or runs.
const memberPolicy = "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
