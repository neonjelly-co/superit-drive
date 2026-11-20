package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	address, _ := url.Parse("https://files.example.com")
	return New(Config{Address: address, HeaderSecret: secret, Members: fixedList{l}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

// examplePath turns a route pattern into an address that matches it.
func examplePath(pattern string) string {
	_, p, _ := strings.Cut(pattern, " ")
	return strings.NewReplacer("{$}", "", "{place}", "1", "{item}", "1", "{member}", "u-reka", "{file...}", "x.js",
		"{upload}", "0123456789abcdef0123456789abcdef").Replace(p)
}

// Every route that takes a place is tried as an outsider in test/shared_folders_test.go
// (TestAMemberWithoutARoleCannotTellASharedFolderFromNone): a member without a role on a shared folder must get the
// same answer from it as from a place that does not exist. This list is that test's; a new route that takes a place
// fails here until it is added there, and here.
var placeRoutesTriedByAnOutsider = []string{
	"DELETE /api/places/{place}",
	"POST /api/places/{place}/restore",
	"GET /api/places/{place}/access",
	"PUT /api/places/{place}/access/members/{member}",
	"DELETE /api/places/{place}/access/members/{member}",
	"PUT /api/places/{place}/access/everyone",
	"DELETE /api/places/{place}/access/everyone",
	"GET /api/places/{place}/items/{item}/access",
	"PUT /api/places/{place}/items/{item}/access/members/{member}",
	"DELETE /api/places/{place}/items/{item}/access/members/{member}",
	"PUT /api/places/{place}/items/{item}/access/everyone",
	"DELETE /api/places/{place}/items/{item}/access/everyone",
	"GET /api/places/{place}/items",
	"GET /api/places/{place}/items/{item}/items",
	"POST /api/places/{place}/files",
	"POST /api/places/{place}/items/{item}/files",
	"POST /api/places/{place}/folders",
	"POST /api/places/{place}/items/{item}/folders",
	"GET /api/places/{place}/items/{item}/download",
	"GET /api/places/{place}/items/{item}/open",
	"POST /api/places/{place}/items/{item}/rename",
	"POST /api/places/{place}/items/{item}/move",
	"GET /api/places/{place}/items/{item}/move",
	"DELETE /api/places/{place}/items/{item}",
	"GET /api/places/{place}/trash",
	"POST /api/places/{place}/trash/{item}/restore",
	"GET /api/places/{place}/zip",
	"GET /api/places/{place}/items/{item}/zip",
	"POST /api/places/{place}/uploads",
	"POST /api/places/{place}/items/{item}/uploads",
	"GET /api/places/{place}/uploads/{upload}",
	"PUT /api/places/{place}/uploads/{upload}",
	"POST /api/places/{place}/uploads/{upload}/finish",
	"DELETE /api/places/{place}/uploads/{upload}",
}

func TestEveryRouteWithAPlaceIsTriedByAnOutsider(t *testing.T) {
	var got []string
	for _, rt := range bareServer(t).routes() {
		if rt.place != "" {
			got = append(got, rt.pattern)
		}
	}
	want := slices.Clone(placeRoutesTriedByAnOutsider)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the routes that take a place are\n%q\nbut the outsider test tries\n%q", got, want)
	}
}

// Every route that takes an item, an item in the trash, or an upload in progress (which is about the folder it goes
// into) is tried in test/sharing_test.go (TestSomeoneGivenPartOfAPlaceCannotTellWhatTheyWereNotGivenFromNothing) by a
// member who was given one folder deep inside someone's place: for every item they were not given (its parent, its
// sibling, the item of another place) and every upload that is not theirs, they must get the same answer as for one
// that does not exist, and the routes of the place as a whole must answer as a place that does not exist. This list is
// that test's; a new route that takes an item fails here until it is added there, and here.
var itemRoutesTriedWithPartOfAPlace = []string{
	"GET /api/places/{place}/items/{item}/access",
	"PUT /api/places/{place}/items/{item}/access/members/{member}",
	"DELETE /api/places/{place}/items/{item}/access/members/{member}",
	"PUT /api/places/{place}/items/{item}/access/everyone",
	"DELETE /api/places/{place}/items/{item}/access/everyone",
	"GET /api/places/{place}/items/{item}/items",
	"POST /api/places/{place}/items/{item}/files",
	"POST /api/places/{place}/items/{item}/folders",
	"GET /api/places/{place}/items/{item}/download",
	"GET /api/places/{place}/items/{item}/open",
	"POST /api/places/{place}/items/{item}/rename",
	"POST /api/places/{place}/items/{item}/move",
	"GET /api/places/{place}/items/{item}/move",
	"DELETE /api/places/{place}/items/{item}",
	"POST /api/places/{place}/trash/{item}/restore",
	"GET /api/places/{place}/items/{item}/zip",
	"POST /api/places/{place}/items/{item}/uploads",
	"GET /api/places/{place}/uploads/{upload}",
	"PUT /api/places/{place}/uploads/{upload}",
	"POST /api/places/{place}/uploads/{upload}/finish",
	"DELETE /api/places/{place}/uploads/{upload}",
}

func TestEveryRouteWithAnItemIsTriedWithPartOfAPlace(t *testing.T) {
	var got []string
	for _, rt := range bareServer(t).routes() {
		if rt.item != "" || rt.trashed != "" || rt.upload != "" {
			got = append(got, rt.pattern)
		}
	}
	want := slices.Clone(itemRoutesTriedWithPartOfAPlace)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the routes that take an item are\n%q\nbut the test with part of a place tries\n%q", got, want)
	}
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

// A signed-in member's browser can be made to send a change by a page on another site (cross-site request forgery).
// Every request that is not a GET or HEAD must carry the Drive's own origin, on every route, and is refused before
// anything else is looked up: the bare server would panic otherwise.
func TestEveryChangeMustComeFromTheDrivesOwnPages(t *testing.T) {
	s := bareServer(t)
	h := s.Handler()
	paths := []string{"/no/such/address", "/api/unknown"}
	for _, rt := range s.routes() {
		paths = append(paths, examplePath(rt.pattern))
	}
	origins := map[string][]string{
		"no Origin":                  nil,
		"an empty Origin":            {""},
		"Origin null":                {"null"},
		"another site":               {"https://evil.example"},
		"a sibling subdomain":        {"https://mail.example.com"},
		"the Drive's host over http": {"http://files.example.com"},
		"the Drive's host on a port": {"https://files.example.com:8443"},
		"a longer host":              {"https://files.example.com.evil.example"},
		"not an address":             {"files.example.com"},
	}
	for _, path := range paths {
		for name, origin := range origins {
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "PROPFIND"} {
				req := httptest.NewRequest(method, path, nil)
				req.Header = http.Header{SecretHeader: {secret}, UserIDHeader: {"u-reka"}}
				if origin != nil {
					req.Header["Origin"] = origin
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "did not come from the Drive's own page") {
					t.Errorf("%s %s with %s: got %d %s, want 403", method, path, name, rec.Code, rec.Body)
				}
			}
		}
	}
}

func TestTheDrivesOwnOriginIsRecognisedHoweverItIsWritten(t *testing.T) {
	cases := []struct {
		address, origin string
		same            bool
	}{
		{"https://files.example.com", "https://files.example.com", true},
		{"https://Files.Example.COM/", "https://files.example.com", true},
		{"https://files.example.com:443", "https://files.example.com", true},
		{"https://files.example.com", "https://files.example.com:443", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://localhost:5173", "http://localhost:5173", true},
		{"http://[::1]:8080", "http://[::1]:8080", true},
		{"http://files.example.com:80", "http://files.example.com", true},
		{"https://files.example.com", "http://files.example.com", false},
		{"https://files.example.com", "https://files.example.com:8443", false},
		{"http://127.0.0.1:8080", "http://localhost:8080", false},
		{"https://files.example.com", "https://example.com", false},
	}
	for _, c := range cases {
		address, err := url.Parse(c.address)
		if err != nil {
			t.Fatal(err)
		}
		s := New(Config{Address: address})
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/x", nil)
			req.Header.Set("Origin", c.origin)
			if got := s.fromOwnPage(req); got != c.same {
				t.Errorf("DRIVE_ADDRESS %s, %s with Origin %s: got %v, want %v", c.address, method, c.origin, got, c.same)
			}
		}
		// Reading never needs an origin.
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			if !s.fromOwnPage(httptest.NewRequest(method, "/api/x", nil)) {
				t.Errorf("%s without Origin was refused", method)
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
