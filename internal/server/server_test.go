package server

import (
	"context"
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
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
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
		"{upload}", "0123456789abcdef0123456789abcdef", "{token}", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "{guest}", "1").Replace(p)
}

// memberRoutes are the routes outside the public prefix: every one of them needs a member signed in.
func memberRoutes(s *Server) []route {
	return slices.DeleteFunc(s.routes(), func(rt route) bool { return rt.link != "" || rt.guest })
}

// guestRoutes are the routes under the guests' prefix.
func guestRoutes(s *Server) []route {
	return slices.DeleteFunc(s.routes(), func(rt route) bool { return !rt.guest })
}

// Every route that takes a place is tried as an outsider in test/shared_folders_test.go
// (TestAMemberWithoutARoleCannotTellASharedFolderFromNone): a member without a role on a shared folder must get the
// same answer from it as from a place that does not exist. This list is that test's; a new route that takes a place
// fails here until it is added there, and here.
var placeRoutesTriedByAnOutsider = []string{
	"DELETE /api/places/{place}",
	"POST /api/places/{place}/restore",
	"PUT /api/places/{place}/sharing-outside",
	"POST /api/held/{place}/hand-over",
	"DELETE /api/held/{place}",
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
	"POST /api/places/{place}/items/{item}/access/guests",
	"PUT /api/places/{place}/items/{item}/access/guests/{guest}",
	"DELETE /api/places/{place}/items/{item}/access/guests/{guest}",
	"GET /api/places/{place}/items/{item}/link",
	"POST /api/places/{place}/items/{item}/link",
	"PUT /api/places/{place}/items/{item}/link",
	"POST /api/places/{place}/items/{item}/link/reset",
	"DELETE /api/places/{place}/items/{item}/link",
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
	for _, rt := range memberRoutes(bareServer(t)) {
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
	"POST /api/places/{place}/items/{item}/access/guests",
	"PUT /api/places/{place}/items/{item}/access/guests/{guest}",
	"DELETE /api/places/{place}/items/{item}/access/guests/{guest}",
	"GET /api/places/{place}/items/{item}/link",
	"POST /api/places/{place}/items/{item}/link",
	"PUT /api/places/{place}/items/{item}/link",
	"POST /api/places/{place}/items/{item}/link/reset",
	"DELETE /api/places/{place}/items/{item}/link",
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
	for _, rt := range memberRoutes(bareServer(t)) {
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
	paths := []string{"/no/such/address", "/api/unknown", "/S/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "/s%2FAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	for _, rt := range memberRoutes(s) {
		paths = append(paths, examplePath(rt.pattern))
	}
	cases := map[string]http.Header{
		"no secret":            {UserIDHeader: {"u-reka"}},
		"wrong secret":         {SecretHeader: {strings.Repeat("x", len(secret))}, UserIDHeader: {"u-reka"}},
		"secret with a suffix": {SecretHeader: {secret + "x"}, UserIDHeader: {"u-reka"}},
		"no user id":           {SecretHeader: {secret}},
		"not a member":         {SecretHeader: {secret}, UserIDHeader: {"u-stranger"}},
		// A proxy that adds a header instead of replacing it leaves the browser's own value first.
		"two user ids": {SecretHeader: {secret}, UserIDHeader: {"u-reka", "u-reka"}},
		"two secrets":  {SecretHeader: {secret, secret}, UserIDHeader: {"u-reka"}},
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
	for _, rt := range memberRoutes(s) {
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

// Every route that takes a link is tried in test/links_test.go (TestAVisitorCannotGetOutOfTheirLink) by a visitor with a
// working link on one folder: for every item it does not cover (above it, beside it, in another place, in the trash),
// and for links that do not work, they must get the same answer as for a link that does not exist. This list is that
// test's; a new route under the public prefix fails here until it is added there, and here.
var linkRoutesTriedByAVisitor = []string{
	"GET /s/{token}",
	"GET /s/{token}/items/{item}",
	"GET /s/{token}/items/{item}/download",
	"GET /s/{token}/items/{item}/open",
	"GET /s/{token}/items/{item}/zip",
	"POST /s/{token}/files",
}

func TestEveryRouteWithALinkIsTriedByAVisitor(t *testing.T) {
	var got []string
	for _, rt := range bareServer(t).routes() {
		if rt.link != "" {
			got = append(got, rt.pattern)
		}
	}
	want := slices.Clone(linkRoutesTriedByAVisitor)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the routes that take a link are\n%q\nbut the visitor test tries\n%q", got, want)
	}
}

// The tripwire between members, visitors and guests. A route under the public prefix is a visitor's, and decided only
// as one, through its link, with an action only a visitor may do; or, under the guests' prefix, a guest's, with an action
// only a guest may do; every other route is a member's, decided only for a member, with an action no visitor or guest may
// do. So even a guard handing a visitor or a guest to a member's route, or a member to theirs, would be refused by the
// access package: this tries every route's action with every kind of visitor, guest and member, in every kind of place.
func TestNoRouteIsDecidedForAnotherKindOfPerson(t *testing.T) {
	member := access.Person{UserID: "u-reka", Role: members.RoleAdmin}
	visitors := []access.Person{
		{Link: &access.Link{Item: 10, Kind: access.ViewLink, By: member}},
		{Link: &access.Link{Item: 10, Kind: access.UploadLink, Folder: true, By: member}},
		// A signed-in member who came with a link, as a request under the prefix with forged headers would be.
		{UserID: "u-reka", Role: members.RoleAdmin, Link: &access.Link{Item: 10, Kind: access.ViewLink, By: member}},
		{UserID: "u-reka", Role: members.RoleAdmin, Link: &access.Link{Item: 10, Kind: access.UploadLink, Folder: true, By: member}},
	}
	guests := []access.Person{
		{Guest: &access.Guest{ID: 7}},
		// A guest whose request also carried a member's identity.
		{UserID: "u-reka", Role: members.RoleAdmin, Guest: &access.Guest{ID: 7}},
	}
	invited := []access.Grant{{On: 10, Guest: 7, Role: access.Editor, By: member}, {On: 0, Guest: 7, Role: access.Editor, By: member}}
	places := []*access.Place{nil,
		{Kind: access.MyFiles, Owner: "u-reka"},
		{Kind: access.MyFiles, Owner: "u-reka", Path: []int64{10}},
		{Kind: access.MyFiles, Owner: "u-reka", Path: []int64{10, 11}, Grants: invited},
		{Kind: access.SharedFolder, Grants: invited},
		{Kind: access.SharedFolder, Path: []int64{10, 11}, Grants: invited},
		{Kind: access.SharedFolder, Deleted: true},
	}
	for _, rt := range bareServer(t).routes() {
		public, forGuests := patternUnderPublicPrefix(rt.pattern), patternUnderGuestPrefix(rt.pattern)
		if forGuests != rt.guest || forGuests != slices.Contains(access.AsAGuest, rt.action) {
			t.Errorf("%s: under the guests' prefix %v, a guest's %v, a guest's action %v: want all or none",
				rt.pattern, forGuests, rt.guest, slices.Contains(access.AsAGuest, rt.action))
		}
		visitors_ := public && !forGuests
		if visitors_ != (rt.link != "") || visitors_ != slices.Contains(access.ThroughALink, rt.action) {
			t.Errorf("%s: under the public prefix %v, with a link %v, a visitor's action %v: want all or none",
				rt.pattern, visitors_, rt.link != "", slices.Contains(access.ThroughALink, rt.action))
		}
		// A visitor reads (GET), but for sending a file into their link's own folder (POST, and only that action).
		sends := rt.action == access.UploadThroughLink
		if visitors_ && ((sends && (!strings.HasPrefix(rt.pattern, "POST ") || rt.item != "")) || (!sends && !strings.HasPrefix(rt.pattern, "GET ")) ||
			rt.place != "" || rt.trashed != "" || rt.upload != "") {
			t.Errorf("%s is a visitor's: it must only read, or send a file into its link's own folder, and find its place through its link", rt.pattern)
		}
		if forGuests && (rt.trashed != "" || rt.inTrash || (rt.place != "" && rt.item == "" && rt.upload == "")) {
			t.Errorf("%s is a guest's: in a place it names an item or an upload, never a place as a whole or the trash", rt.pattern)
		}
		for _, place := range places {
			if !visitors_ {
				for _, v := range visitors {
					if access.Decide(v, rt.action, place) {
						t.Errorf("%s, not a visitor's route, was allowed for a visitor in %+v", rt.pattern, place)
					}
				}
			}
			if !forGuests {
				for _, g := range guests {
					if access.Decide(g, rt.action, place) {
						t.Errorf("%s, not a guest's route, was allowed for a guest in %+v", rt.pattern, place)
					}
				}
			}
			if public && access.Decide(member, rt.action, place) {
				t.Errorf("%s, a route under the public prefix, was allowed for a member in %+v", rt.pattern, place)
			}
		}
	}
}

// Under the guests' prefix nobody is a member, whatever headers they send: without a guest's session, every route but
// signing in answers 401, before anything else is looked at (the bare server would panic otherwise), and with a
// member's headers and the secret too.
func TestAMemberIsNobodyUnderTheGuestsPrefix(t *testing.T) {
	s := bareServer(t)
	h := s.Handler()
	for _, rt := range guestRoutes(s) {
		if rt.action == access.SignInAsGuest {
			continue
		}
		method, _, _ := strings.Cut(rt.pattern, " ")
		for name, headers := range map[string]http.Header{
			"nothing":           {"Origin": {"https://files.example.com"}},
			"a member's":        {SecretHeader: {secret}, UserIDHeader: {"u-reka"}, "Origin": {"https://files.example.com"}},
			"a wrong cookie":    {"Cookie": {"drive-guest-x=1; __Secure-drive-guest=1"}, "Origin": {"https://files.example.com"}},
			"a member's cookie": {SecretHeader: {secret}, UserIDHeader: {"u-reka"}, "Cookie": {"authelia_session=abc"}, "Origin": {"https://files.example.com"}},
		} {
			req := httptest.NewRequest(method, examplePath(rt.pattern), strings.NewReader("{}"))
			req.Header = headers.Clone()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"signIn":true`) {
				t.Errorf("%s with %s headers: got %d %s, want 401", rt.pattern, name, rec.Code, rec.Body)
			}
			checkGuestHeaders(t, rt.pattern, rec.Header())
		}
	}
}

// A guest's change, like a member's, comes only from the Drive's own pages: refused with 403 before anything else, on
// every route under the guests' prefix that changes something, signing in included.
func TestAGuestsChangesMustComeFromTheDrivesOwnPages(t *testing.T) {
	s := bareServer(t)
	h := s.Handler()
	paths := []string{GuestPrefix + "/api/nothing", GuestPrefix + "/x"}
	for _, rt := range guestRoutes(s) {
		paths = append(paths, examplePath(rt.pattern))
	}
	for _, path := range paths {
		for name, origin := range map[string][]string{"no Origin": nil, "Origin null": {"null"}, "another site": {"https://evil.example"},
			"the Drive over http": {"http://files.example.com"}} {
			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
				req := httptest.NewRequest(method, path, strings.NewReader(`{"address":"anna@client.example"}`))
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

// checkGuestHeaders checks the headers every answer under the guests' prefix carries: not listed by search engines,
// never kept, and the members' policy for the interface's own script.
func checkGuestHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Robots-Tag":                 "noindex, nofollow",
		"Referrer-Policy":              "same-origin",
		"Cache-Control":                "no-store",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Security-Policy":      memberPolicy,
	} {
		if got := h.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %s is %q, want %q", what, name, got, want)
		}
	}
}

// Under the public prefix, a visitor only reads: any other method is refused with 405 before anything else is looked
// at (the bare server would panic otherwise), whatever headers come with it, and the answer is the same for every
// address there.
//
// The one exception is sending a file in through an upload-only link: POST, to that one route only. Every other method
// there is refused the same way (the route for it is POST, and nothing else is).
func TestVisitorsOnlyRead(t *testing.T) {
	s := bareServer(t)
	h := s.Handler()
	paths := []string{"/s/", "/s/x", "/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/nothing/here", "/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/items/1/files",
		"/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/files/x", "/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/files/", "/s/guestx/files"}
	sendsIn := ""
	for _, rt := range s.routes() {
		if rt.link != "" {
			paths = append(paths, examplePath(rt.pattern))
		}
		if rt.action == access.UploadThroughLink {
			if sendsIn != "" || rt.pattern != "POST /s/{token}/files" {
				t.Fatalf("visitors send files in through %q and %q: only one route, POST /s/{token}/files, may", sendsIn, rt.pattern)
			}
			sendsIn = examplePath(rt.pattern)
		}
	}
	sendIn := regexp.MustCompile(`^/s/[^/]+/files$`)
	if !sendIn.MatchString(sendsIn) {
		t.Fatalf("the route visitors send files in through is at %q", sendsIn)
	}
	var first string
	for _, path := range paths {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, "PROPFIND", "MKCOL"} {
			if sendIn.MatchString(path) && method == http.MethodPost {
				continue // the one change a visitor makes: tried through HTTP, test/upload_only_links_test.go
			}
			for _, headers := range []http.Header{
				{},
				{SecretHeader: {secret}, UserIDHeader: {"u-reka"}, "Origin": {"https://files.example.com"}},
			} {
				req := httptest.NewRequest(method, path, strings.NewReader("x"))
				req.Header = headers.Clone()
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
					t.Errorf("%s %s: got %d, Allow %q, want 405 with GET, HEAD", method, path, rec.Code, rec.Header().Get("Allow"))
				}
				if first == "" {
					first = rec.Body.String()
				} else if rec.Body.String() != first {
					t.Errorf("%s %s: the answer differs from another address's", method, path)
				}
				checkVisitorHeaders(t, method+" "+path, rec.Header())
				if rec.Header().Get("Set-Cookie") != "" {
					t.Errorf("%s %s set a cookie", method, path)
				}
			}
		}
	}
}

// checkVisitorHeaders checks the headers every answer under the public prefix carries.
func checkVisitorHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Robots-Tag":                 "noindex, nofollow",
		"Referrer-Policy":              "no-referrer",
		"Cache-Control":                "no-store",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Security-Policy":      visitorPolicy,
	} {
		if got := h.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %s is %q, want %q", what, name, got, want)
		}
	}
}

// The visitors' pages' policy lets nothing load or run but their own style: it is at least as strict as the members'.
func TestTheVisitorsPolicyIsStrict(t *testing.T) {
	for _, part := range []string{"default-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(visitorPolicy, part) {
			t.Errorf("the visitors' policy %q lacks %q", visitorPolicy, part)
		}
	}
	for _, loose := range []string{"'unsafe-inline'", "'unsafe-eval'", "script-src", "'self'", "*", "data:", "http"} {
		if strings.Contains(visitorPolicy, loose) {
			t.Errorf("the visitors' policy %q allows %q", visitorPolicy, loose)
		}
	}
}

// An upload-only link's page runs one script, its own, by its hash, which may talk only to the public prefix of the
// Drive's address; everything else is as strict as the visitors' policy. The page holds exactly that script and style.
func TestTheSendPagesPolicyAllowsOnlyItsOwnScript(t *testing.T) {
	s := bareServer(t)
	want := "default-src 'none'; style-src '" + styleHash(visitorStyle) + "'; script-src '" + styleHash(sendScript) +
		"'; connect-src https://files.example.com/s/; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	if s.sendPolicy != want {
		t.Fatalf("the send page's policy is\n%s\nwant\n%s", s.sendPolicy, want)
	}
	for _, loose := range []string{"'unsafe-inline'", "'unsafe-eval'", "'self'", "*", "data:", "blob:", "'strict-dynamic'"} {
		if strings.Contains(s.sendPolicy, loose) {
			t.Errorf("the send page's policy allows %q", loose)
		}
	}
	// One script, and one hash, in every language: the words are in the page (data-words), not in the script.
	for _, lang := range []string{"en", "hu"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/s/token", nil)
		req.Header.Set("Accept-Language", lang)
		s.renderVisitor(rec, req, http.StatusOK, visitorView{Name: "Invoices", By: "Shared by Bence", Folder: true, Send: true})
		if got := rec.Header().Get("Content-Security-Policy"); got != s.sendPolicy {
			t.Fatalf("the send page in %s went out with the policy %q", lang, got)
		}
		page := rec.Body.String()
		scripts := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindAllStringSubmatch(page, -1)
		styles := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindAllStringSubmatch(page, -1)
		if len(scripts) != 1 || styleHash(scripts[0][1]) != styleHash(sendScript) || len(styles) != 1 || styleHash(styles[0][1]) != styleHash(visitorStyle) {
			t.Fatalf("the send page's script and style in %s do not match their hashes:\n%s", lang, page)
		}
		if strings.Count(page, "<script") != 1 || strings.Contains(page, "<form") || strings.Contains(page, " on") && regexp.MustCompile(` on[a-z]+=`).MatchString(page) {
			t.Fatalf("the send page in %s has more than its one script, or a form, or an inline handler:\n%s", lang, page)
		}
		if !strings.Contains(page, `<html lang="`+lang+`">`) || !strings.Contains(page, `data-words="`) {
			t.Fatalf("the send page in %s does not say its language, or holds no words for its script:\n%s", lang, page)
		}
	}
	// Without the Drive's address, the page's own origin.
	if p := sendPolicyFor(""); !strings.Contains(p, "connect-src 'self';") {
		t.Fatalf("the send page's policy without an address: %s", p)
	}
	// A view link's page has no script, and nor has the page of an upload-only link that has taken in all it may.
	for what, v := range map[string]visitorView{
		"a view link's page":                  {Name: "Invoices", By: "Shared by Bence", Folder: true},
		"the page of a full upload-only link": {Name: "Invoices", By: "Shared by Bence", Folder: true, Send: true, Full: true},
	} {
		rec := httptest.NewRecorder()
		s.renderVisitor(rec, httptest.NewRequest(http.MethodGet, "/s/token", nil), http.StatusOK, v)
		if strings.Contains(rec.Body.String(), "<script") || rec.Header().Get("Content-Security-Policy") != "" {
			t.Fatalf("%s has a script, or a policy of its own: %q", what, rec.Header().Get("Content-Security-Policy"))
		}
	}
}

// Visitors' requests are worked on a few at a time; a few more wait, each a while; any more are refused at once.
func TestAdmission(t *testing.T) {
	a := newAdmission(2, 1, 200*time.Millisecond)
	l1, ok1 := a.enter(context.Background())
	l2, ok2 := a.enter(context.Background())
	if !ok1 || !ok2 {
		t.Fatal("the first two were not let in")
	}
	waited := make(chan bool, 1)
	go func() {
		leave, ok := a.enter(context.Background())
		if ok {
			defer leave()
		}
		waited <- ok
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if _, ok := a.enter(context.Background()); ok || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("one more than may wait: let in %v after %v, want refused at once", ok, time.Since(start))
	}
	l1()
	l1() // leaving twice leaves once
	if !<-waited {
		t.Fatal("the one waiting was not let in when a turn came free")
	}
	// Waiting longer than the while: refused.
	l3, _ := a.enter(context.Background())
	if _, ok := a.enter(context.Background()); ok {
		t.Fatal("let in with every turn taken")
	}
	l2()
	l3()
	if _, ok := a.enter(context.Background()); !ok {
		t.Fatal("not let in with turns free")
	}
}

// Every route under the guests' prefix that takes an item or an upload in progress is tried in test/guests_test.go
// (TestAGuestCannotGetOutOfWhatWasGivenToThem) by a guest given one folder deep inside someone's place: for every item
// they were not given and every upload that is not theirs, they must get the same answer as for one that does not
// exist. This list is that test's; a new guest's route fails here until it is added there, and here.
var guestRoutesTriedByAGuest = []string{
	"GET /s/guest/api/places/{place}/items/{item}",
	"GET /s/guest/api/places/{place}/items/{item}/items",
	"GET /s/guest/api/places/{place}/items/{item}/download",
	"GET /s/guest/api/places/{place}/items/{item}/open",
	"GET /s/guest/api/places/{place}/items/{item}/zip",
	"POST /s/guest/api/places/{place}/items/{item}/files",
	"POST /s/guest/api/places/{place}/items/{item}/uploads",
	"GET /s/guest/api/places/{place}/uploads/{upload}",
	"PUT /s/guest/api/places/{place}/uploads/{upload}",
	"POST /s/guest/api/places/{place}/uploads/{upload}/finish",
	"DELETE /s/guest/api/places/{place}/uploads/{upload}",
	"POST /s/guest/api/places/{place}/items/{item}/folders",
	"POST /s/guest/api/places/{place}/items/{item}/rename",
	"POST /s/guest/api/places/{place}/items/{item}/move",
	"DELETE /s/guest/api/places/{place}/items/{item}",
}

func TestEveryGuestsRouteIsTriedByAGuest(t *testing.T) {
	var got []string
	for _, rt := range guestRoutes(bareServer(t)) {
		if rt.place != "" {
			got = append(got, rt.pattern)
		}
	}
	want := slices.Clone(guestRoutesTriedByAGuest)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the guests' routes that take an item or an upload are\n%q\nbut the guest test tries\n%q", got, want)
	}
}

// What a guest types as their address is kept as members.Address writes it, and only when mail can be sent to it: one
// plain address in ASCII, nothing that could add a header or a recipient.
func TestAGuestsAddress(t *testing.T) {
	for typed, want := range map[string]string{
		"anna@client.example":                             "anna@client.example",
		" Anna.Kiss+drive@Client.EXAMPLE ":                "anna.kiss+drive@client.example",
		"a.n.n.a@client.example":                          "a.n.n.a@client.example",
		"anna":                                            "",
		"anna@client":                                     "",
		"anna@.example":                                   "",
		"Anna <anna@client.example>":                      "",
		"anna@client.example, b@c.de":                     "",
		"anna@client.example\r\nBcc: evil@example.com":    "",
		"anna@client.example\nRCPT TO:<evil@example.com>": "",
		"anna @client.example":                            "",
		"ánna@client.example":                             "",
		"anna@client.example.":                            "",
		"\"anna\"@client.example":                         "",
		strings.Repeat("a", 250) + "@b.cd":                "",
	} {
		got, ok := guestAddress(typed)
		if ok != (want != "") || got != want {
			t.Errorf("%q: got %q %v, want %q", typed, got, ok, want)
		}
	}
}
