// Package server is the Drive's HTTP side: the JSON API the interface calls and the interface's own files.
//
// Every route is listed in routes.go and registered in Handler, and nowhere else. Each request passes the same steps
// in guard before its handler runs: who is asking (identify), whether a change came from the Drive's own pages
// (fromOwnPage), which place it concerns (if any), whether they may (access.Decide), and which item in that place (if
// any). A handler only ever sees a request that passed them all. A test fails if anything outside this file registers
// a handler.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
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
	// Interface is the built interface (index.html and assets/).
	Interface fs.FS
	Log       *slog.Logger
}

// Server answers the Drive's HTTP requests.
type Server struct {
	project string
	origin  string // the origin of Config.Address, such as https://files.example.com
	secret  string
	members interface{ Current() *members.List }
	drive   *drive.Drive
	ui      fs.FS
	log     *slog.Logger
}

// New makes a server.
func New(c Config) *Server {
	s := &Server{project: c.ProjectName, secret: c.HeaderSecret, members: c.Members, drive: c.Drive, ui: c.Interface, log: c.Log}
	if c.Address != nil {
		s.origin = originOf(c.Address)
	}
	return s
}

// A route is one thing the Drive answers. It names the action it is and, for an action in a place, where the place's
// id is in the address. guard runs the same steps for every route before serve.
type route struct {
	pattern string
	action  access.Action
	// place is the name of the path value holding the place's id, or "" when the action concerns no place.
	place string
	// item is the name of the path value holding the id of an item in that place, or "" when the action concerns
	// the place as a whole. A route with an item must have a place.
	item  string
	serve func(http.ResponseWriter, *http.Request, call)
}

// call is what a handler gets: who is asking, the place the request concerns (nil for no place) and the item in it
// (nil for none). All have already passed guard.
type call struct {
	member members.Member
	place  *index.Place
	item   *index.Item
}

// Handler is the Drive's whole HTTP handler. This is the only place routes are registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		if rt.item != "" && rt.place == "" {
			panic("server: route " + rt.pattern + " names an item but no place")
		}
		mux.Handle(rt.pattern, s.guard(rt))
	}
	// Anything else is still identified first, so an outsider learns nothing from a wrong address.
	mux.Handle("/", s.guard(route{pattern: "/", action: access.Enter, serve: s.notFound}))
	return securityHeaders(mux)
}

// guard is the gate every route passes: identify the person, refuse a change from anywhere but the Drive's own pages,
// find the place, ask access.Decide, find the item in the place, then serve.
func (s *Server) guard(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		c := call{member: member}
		var where *access.Place
		if rt.place != "" {
			p, err := s.findPlace(r, rt.place)
			if errors.Is(err, index.ErrNoPlace) {
				s.refuse(w, r, http.StatusNotFound, noSuchPlace)
				return
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			c.place = &p
			where = accessPlace(p)
		}
		who := access.Person{UserID: member.UserID, Role: member.Role}
		if !access.Decide(who, rt.action, where) {
			// The same answer as a place that does not exist: nobody learns that someone else's place is there.
			s.refuse(w, r, http.StatusNotFound, noSuchPlace)
			return
		}
		// The item is looked up only inside the place just decided on, and only after the decision, so an item of
		// another place is never found and a refused person learns nothing about which items exist.
		if rt.item != "" {
			it, err := s.findItem(r, *c.place, rt.item)
			if errors.Is(err, index.ErrNoItem) {
				s.refuse(w, r, http.StatusNotFound, noSuchItem)
				return
			}
			if err != nil {
				s.fail(w, r, err)
				return
			}
			c.item = &it
		}
		rt.serve(w, r, c)
	})
}

const (
	noSuchPlace   = "There is no such place in this Drive."
	noSuchItem    = "There is no such file or folder in this Drive."
	refusedOrigin = "This change did not come from the Drive's own page, so it was refused."
)

func (s *Server) findPlace(r *http.Request, name string) (index.Place, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return index.Place{}, index.ErrNoPlace
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

// accessPlace describes a place the way the access package needs it.
func accessPlace(p index.Place) *access.Place {
	switch p.Kind {
	case index.MyFiles:
		return &access.Place{Kind: access.MyFiles, Owner: p.Owner}
	}
	return &access.Place{} // a kind access has no rule for: refused
}

// refuse answers with an error the person can read: JSON for the API, plain text otherwise.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, status, map[string]string{"error": message})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}

// fail answers a request that went wrong on the Drive's side. The details go to the log, never to the person.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("a request failed", "route", r.Pattern, "error", err)
	s.refuse(w, r, http.StatusInternalServerError, "Something went wrong in the Drive. Try again in a moment.")
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, _ call) {
	s.refuse(w, r, http.StatusNotFound, "There is nothing at this address.")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// securityHeaders adds the headers every answer carries: no framing, no guessing of content types, no third-party
// anything, and no other site loading a file's bytes into its own page (an image or script tag sends the member's
// cookie along).
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
