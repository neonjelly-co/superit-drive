package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

const secret = "0123456789abcdef0123456789abcdef"

type fixedList struct{ l *members.List }

func (f fixedList) Current() *members.List { return f.l }

// A server with a member list and nothing else: no drive, no storage, no interface. Any handler that runs would
// panic on the missing parts, so a passing test proves the request was stopped before its handler.
func bareServer(t *testing.T) *Server {
	l, err := members.Parse([]byte(`{"members":[{"userId":"u-reka","email":"reka@example.com","role":"member"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{HeaderSecret: secret, Members: fixedList{l}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

// examplePath turns a route pattern into an address that matches it.
func examplePath(pattern string) string {
	_, p, _ := strings.Cut(pattern, " ")
	return strings.NewReplacer("{$}", "", "{place}", "1", "{file...}", "x.js").Replace(p)
}

func TestEveryRouteIsGuarded(t *testing.T) {
	s := bareServer(t)
	h := s.Handler()
	paths := []string{"/no/such/address", "/api/unknown"}
	for _, rt := range s.routes() {
		paths = append(paths, examplePath(rt.pattern))
	}
	cases := map[string]http.Header{
		"no secret":            {UserIDHeader: {"u-reka"}},
		"wrong secret":         {SecretHeader: {strings.Repeat("x", len(secret))}, UserIDHeader: {"u-reka"}},
		"secret with a suffix": {SecretHeader: {secret + "x"}, UserIDHeader: {"u-reka"}},
		"no user id":           {SecretHeader: {secret}},
		"not a member":         {SecretHeader: {secret}, UserIDHeader: {"u-stranger"}},
	}
	for _, path := range paths {
		for name, headers := range cases {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				req := httptest.NewRequest(method, path, nil)
				req.Header = headers.Clone()
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s %s with %s: got %d, want 403", method, path, name, rec.Code)
				}
			}
		}
	}
}

// Handlers are registered in server.go and nowhere else, so no route can skip guard. This reads the Drive's Go
// sources and fails on any other place that registers an HTTP handler.
func TestOnlyServerGoRegistersHandlers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(root, "internal", "server", "server.go")
	registers := regexp.MustCompile(`\.(Handle|HandleFunc)\(|http\.NewServeMux\(|http\.(ListenAndServe|Serve)\(`)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") && path != root) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == allowed {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if loc := registers.FindIndex(src); loc != nil {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s registers an HTTP handler (%q). Add routes in internal/server/routes.go, so they pass guard.", rel, src[loc[0]:loc[1]])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
