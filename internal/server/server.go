// Package server is the Drive's HTTP side: the JSON API the interface calls and the interface's own files.
//
// Every route is listed in routes.go and registered in Handler, and nowhere else. Each request passes the same three
// steps in guard before its handler runs: who is asking (identify), which place it concerns (if any), and whether they
// may (access.Decide). A handler only ever sees a request that passed all three. A test fails if anything outside
// this file registers a handler.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Config is what the server needs.
type Config struct {
	ProjectName  string
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
	secret  string
	members interface{ Current() *members.List }
	drive   *drive.Drive
	ui      fs.FS
	log     *slog.Logger
}

// New makes a server.
func New(c Config) *Server {
	return &Server{project: c.ProjectName, secret: c.HeaderSecret, members: c.Members, drive: c.Drive, ui: c.Interface, log: c.Log}
}

// A route is one thing the Drive answers. It names the action it is and, for an action in a place, where the place's
// id is in the address. guard runs the same steps for every route before serve.
type route struct {
	pattern string
	action  access.Action
	// place is the name of the path value holding the place's id, or "" when the action concerns no place.
	place string
	serve func(http.ResponseWriter, *http.Request, call)
}

// call is what a handler gets: who is asking, and the place the request concerns (nil for no place). Both have
// already passed identify and access.Decide.
type call struct {
	member members.Member
	place  *index.Place
}

// Handler is the Drive's whole HTTP handler. This is the only place routes are registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.pattern, s.guard(rt))
	}
	// Anything else is still identified first, so an outsider learns nothing from a wrong address.
	mux.Handle("/", s.guard(route{pattern: "/", action: access.Enter, serve: s.notFound}))
	return securityHeaders(mux)
}

// guard is the gate every route passes: identify the person, find the place, ask access.Decide, then serve.
func (s *Server) guard(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		member, refused := s.identify(r)
		if refused != nil {
			s.refuse(w, r, refused.status, refused.message)
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
		rt.serve(w, r, c)
	})
}

const noSuchPlace = "There is no such place in this Drive."

func (s *Server) findPlace(r *http.Request, name string) (index.Place, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return index.Place{}, index.ErrNoPlace
	}
	return s.drive.Place(r.Context(), id)
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
// anything.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
