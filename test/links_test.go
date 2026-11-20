package test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's calls for a public link, as the interface makes them; and a visitor, who sends nothing but the
// address, as anyone's browser does.

type linkJSON struct {
	Kind    string      `json:"kind"`
	Made    time.Time   `json:"made"`
	MadeBy  *personJSON `json:"madeBy"`
	Former  bool        `json:"former"`
	Uses    int64       `json:"uses"`
	Used    *time.Time  `json:"used"`
	Works   bool        `json:"works"`
	Address string      `json:"address"`
}

func linkPath(placeID, itemID int64) string {
	return fmt.Sprintf("/api/places/%d/items/%d/link", placeID, itemID)
}

func makeLink(t *testing.T, c *harness.Client, placeID, itemID int64, replace bool) harness.Response {
	t.Helper()
	if replace {
		return sendJSON(t, c, http.MethodPost, linkPath(placeID, itemID), map[string]bool{"replace": true})
	}
	return c.Do(t, http.MethodPost, linkPath(placeID, itemID), nil)
}

// pageAddress is what a link's address looks like: the Drive's own address, the public prefix, and 32 characters of
// base64url (192 random bits), nothing else.
var pageAddress = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{32}$`)

// madeLink makes a link and returns it, with the path of its address: the page a visitor opens.
func madeLink(t *testing.T, d *harness.Drive, c *harness.Client, placeID, itemID int64, replace bool) (linkJSON, string) {
	t.Helper()
	res := makeLink(t, c, placeID, itemID, replace)
	if res.Status != http.StatusCreated {
		t.Fatalf("making a link on item %d: got %d %s", itemID, res.Status, res.Body)
	}
	var got struct {
		Link linkJSON `json:"link"`
	}
	res.JSON(t, &got)
	page, ok := strings.CutPrefix(got.Link.Address, d.Address)
	if !ok || !pageAddress.MatchString(page) {
		t.Fatalf("the link's address %q is not the Drive's address and /s/ and a token", got.Link.Address)
	}
	return got.Link, page
}

// linkOn is the link on an item, as whoever may manage it sees it; nil for none.
func linkOn(t *testing.T, c *harness.Client, placeID, itemID int64) *linkJSON {
	t.Helper()
	res := c.Get(t, linkPath(placeID, itemID))
	if res.Status != http.StatusOK {
		t.Fatalf("the link on item %d: got %d %s", itemID, res.Status, res.Body)
	}
	var got struct {
		Link *linkJSON `json:"link"`
	}
	res.JSON(t, &got)
	return got.Link
}

func removeLink(t *testing.T, c *harness.Client, placeID, itemID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, linkPath(placeID, itemID), nil)
}

// visitor is anyone's browser: it sends nothing but the address.
func visitor(d *harness.Drive) *harness.Client { return d.WithHeaders(http.Header{}) }

// inLink is the address of an item through a link whose page is page: its own page (what ""), "/download", "/open"
// or "/zip".
func inLink(page string, itemID int64, what string) string {
	return fmt.Sprintf("%s/items/%d%s", page, itemID, what)
}

// noSuchLink is the page of a link that was never made: 32 random characters, as a token is written.
func noSuchLink(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return "/s/" + base64.RawURLEncoding.EncodeToString(b)
}

// sameAsNoLink fails the test unless res answers exactly as want, a link that does not exist, does: the same status,
// body and headers (but the date).
func sameAsNoLink(t *testing.T, what string, res, want harness.Response) {
	t.Helper()
	if res.Status != http.StatusNotFound || res.Status != want.Status || !bytes.Equal(res.Body, want.Body) {
		t.Errorf("%s: got %d %.200s; a link that does not exist answers %d %.200s", what, res.Status, res.Body, want.Status, want.Body)
		return
	}
	for _, h := range []http.Header{res.Header, want.Header} {
		h.Del("Date")
	}
	if !headersEqual(res.Header, want.Header) {
		t.Errorf("%s: the headers %v differ from a link that does not exist's %v", what, res.Header, want.Header)
	}
}

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}

// visitorHeaders checks what every answer under the public prefix carries: not listed by search engines, the address
// never sent on and never kept, and no cookie.
func visitorHeaders(t *testing.T, what string, res harness.Response) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Robots-Tag":           "noindex, nofollow",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := res.Header.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %s is %q, want %q", what, name, got, want)
		}
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("%s: Cache-Control is %q", what, cc)
	}
	if res.Header.Get("Set-Cookie") != "" {
		t.Errorf("%s set a cookie: %q", what, res.Header.Values("Set-Cookie"))
	}
	if n := len(res.Header.Values("Content-Security-Policy")); n != 1 {
		t.Errorf("%s: %d Content-Security-Policy headers, want exactly one", what, n)
	}
}

// A visitor's page, as a visitor reads it.
type visitorPage struct {
	by, name string
	// crumbs are the folders above the one shown, from the link's item down, by name; hrefs their addresses.
	crumbs, crumbHrefs []string
	zip                string
	// rows are the items listed: "Name/" for a folder, "Name" for a file; hrefs a folder's page or a file's download,
	// and opens a file's open address, when it opens.
	rows                []string
	hrefs, opens        map[string]string
	download, openFile  string // a file's page
	noindex, hasScripts bool
}

var (
	pageBy     = regexp.MustCompile(`<p class="by">(.*?)</p>`)
	pageName   = regexp.MustCompile(`<h1>(.*?)</h1>`)
	pageCrumbs = regexp.MustCompile(`(?s)<nav class="crumbs"[^>]*>(.*?)</nav>`)
	pageAnchor = regexp.MustCompile(`<a href="([^"]*)">([^<]*)</a>`)
	pageZip    = regexp.MustCompile(`<a class="button" href="([^"]*)" download>Download all as zip</a>`)
	pageRow    = regexp.MustCompile(`<li>(.*?)</li>`)
	rowFolder  = regexp.MustCompile(`^<a class="name" href="([^"]*)">.*?<span>(.*?)</span></a>$`)
	rowFile    = regexp.MustCompile(`^<span class="name">.*?<span>(.*?)</span></span>`)
	rowOpen    = regexp.MustCompile(`<a href="([^"]*)" target="_blank" rel="noopener">Open</a>`)
	rowGet     = regexp.MustCompile(`<a href="([^"]*)" download>Download</a>`)
	fileOpen   = regexp.MustCompile(`<a class="button quiet" href="([^"]*)" target="_blank" rel="noopener">Open</a>`)
	fileGet    = regexp.MustCompile(`<a class="button" href="([^"]*)" download>Download</a>`)
)

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// readPage reads the visitor's page at path, which must open.
func readPage(t *testing.T, c *harness.Client, path string) visitorPage {
	t.Helper()
	res := c.Get(t, path)
	if res.Status != http.StatusOK || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("the page %s: got %d %s %.300s", path, res.Status, res.Header.Get("Content-Type"), res.Body)
	}
	visitorHeaders(t, "the page "+path, res)
	body := string(res.Body)
	p := visitorPage{by: first(pageBy, body), name: first(pageName, body), zip: first(pageZip, body),
		hrefs: map[string]string{}, opens: map[string]string{},
		download: first(fileGet, body), openFile: first(fileOpen, body),
		noindex:    strings.Contains(body, `<meta name="robots" content="noindex, nofollow">`),
		hasScripts: strings.Contains(strings.ToLower(body), "<script")}
	if m := pageCrumbs.FindStringSubmatch(body); m != nil {
		for _, a := range pageAnchor.FindAllStringSubmatch(m[1], -1) {
			p.crumbs = append(p.crumbs, html.UnescapeString(a[2]))
			p.crumbHrefs = append(p.crumbHrefs, html.UnescapeString(a[1]))
		}
	}
	for _, row := range pageRow.FindAllStringSubmatch(body, -1) {
		if m := rowFolder.FindStringSubmatch(row[1]); m != nil {
			name := html.UnescapeString(m[2]) + "/"
			p.rows = append(p.rows, name)
			p.hrefs[name] = html.UnescapeString(m[1])
			continue
		}
		name := first(rowFile, row[1])
		p.rows = append(p.rows, name)
		p.hrefs[name] = first(rowGet, row[1])
		if open := first(rowOpen, row[1]); open != "" {
			p.opens[name] = open
		}
	}
	return p
}

// Bence makes a public link to his Clients folder and sends it to someone. They open it without signing in, on a plain
// page: who shared it, its name, "Download all as zip" and what is in it; they open the folders inside it, and download
// and open its files, and nothing above it or beside it is ever named. A signed-in member opening it is a visitor like
// anyone else. A link to one file shows that file.
func TestAPublicLinkOpensForAnyoneWithoutSigningIn(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)

	link, page := madeLink(t, d, b, x.place, x.clients.ID, false)
	if link.Kind != "view" || link.MadeBy == nil || link.MadeBy.UserID != bence.UserID || link.Uses != 0 || link.Used != nil ||
		!link.Works || time.Since(link.Made) > time.Minute {
		t.Fatalf("the new link: %+v", link)
	}
	if got := linkOn(t, b, x.place, x.clients.ID); got == nil || got.Address != "" || !got.Works {
		t.Fatalf("the link as the details panel sees it later: %+v, want it without its address", got)
	}

	top := readPage(t, v, page)
	if top.by != "Shared by Bence Tóth" || top.name != "Clients" || len(top.crumbs) != 0 || top.zip == "" || !top.noindex || top.hasScripts {
		t.Fatalf("the link's page: %+v", top)
	}
	if !slices.Equal(top.rows, []string{"Bakery/", "Price list.pdf"}) || top.opens["Price list.pdf"] == "" {
		t.Fatalf("the link's page lists %q (opens %v)", top.rows, top.opens)
	}
	res := v.Get(t, page)
	for _, secret := range []string{bence.Email, bence.UserID, "Notes.txt", "Private", "Secret.txt", "My files"} {
		if strings.Contains(string(res.Body), secret) {
			t.Errorf("the link's page says %q", secret)
		}
	}

	// Into Bakery, then 2026: the folders above, from Clients down, and nothing above Clients.
	bakery := readPage(t, v, top.hrefs["Bakery/"])
	y2026 := readPage(t, v, bakery.hrefs["2026/"])
	if !slices.Equal(y2026.crumbs, []string{"Clients", "Bakery"}) || y2026.crumbHrefs[0] != page || y2026.name != "2026" ||
		!slices.Equal(y2026.rows, []string{"Ledger.xlsx", "Offer.pdf"}) || y2026.opens["Ledger.xlsx"] != "" || y2026.opens["Offer.pdf"] == "" {
		t.Fatalf("2026 through the link: %+v", y2026)
	}

	// A file downloads, and opens as members' files do, with the same safe list and headers.
	res = v.Get(t, y2026.hrefs["Offer.pdf"])
	if got := headersOf(t, "downloading Offer.pdf through the link", res, "Offer.pdf", []byte("offer"), "no-referrer"); got != downloaded {
		t.Errorf("Offer.pdf through the link: sent as %+v", got)
	}
	visitorHeaders(t, "downloading Offer.pdf", res)
	res = v.Get(t, y2026.opens["Offer.pdf"])
	if got := headersOf(t, "opening Offer.pdf through the link", res, "Offer.pdf", []byte("offer"), "no-referrer"); got != (sent{"application/pdf", "inline", sandboxedPolicy}) {
		t.Errorf("Offer.pdf opened through the link: sent as %+v", got)
	}
	visitorHeaders(t, "opening Offer.pdf", res)

	// Everything as a zip, from the top of the link or from a folder in it.
	res = v.Get(t, top.zip)
	visitorHeaders(t, "the zip", res)
	want := map[string]string{"Clients/": "", "Clients/Bakery/": "", "Clients/Bakery/2026/": "", "Clients/Bakery/2026/Offer.pdf": "offer",
		"Clients/Bakery/2026/Ledger.xlsx": "ledger", "Clients/Price list.pdf": "prices"}
	if res.Status != http.StatusOK || savedAs(res) != "Clients.zip" || !mapsEqual(zipEntries(t, res.Body), want) {
		t.Fatalf("Clients.zip through the link: %d %q, holds %q", res.Status, savedAs(res), keys(zipEntries(t, res.Body)))
	}
	if got := zipEntries(t, v.Get(t, y2026.zip).Body); len(got) != 3 || got["2026/Offer.pdf"] != "offer" {
		t.Fatalf("2026.zip through the link holds %q", keys(got))
	}
	// HEAD answers as a GET would, without the page.
	if res := v.Do(t, http.MethodHead, page, nil); res.Status != http.StatusOK || len(res.Body) != 0 {
		t.Fatalf("HEAD of the page: %d, %d bytes", res.Status, len(res.Body))
	}

	// A signed-in member, Bence himself included, is a visitor here like anyone: the same page, and nothing more.
	if got := b.Get(t, page); !bytes.Equal(got.Body, v.Get(t, page).Body) {
		t.Error("Bence, signed in, sees another page through his link than a visitor")
	}
	missing := v.Get(t, noSuchLink(t))
	sameAsNoLink(t, "Bence, signed in, opening Notes.txt through his link on Clients", b.Get(t, inLink(page, x.notes.ID, "/download")), missing)

	// A link to one file: the file, its size, Open (it is text) and Download; no zip.
	_, notes := madeLink(t, d, b, x.place, x.notes.ID, false)
	file := readPage(t, v, notes)
	if file.name != "Notes.txt" || file.zip != "" || len(file.rows) != 0 || file.download == "" || file.openFile == "" || file.by != "Shared by Bence Tóth" {
		t.Fatalf("the page of a link to Notes.txt: %+v", file)
	}
	if res := v.Get(t, file.download); res.Status != http.StatusOK || string(res.Body) != "notes" || savedAs(res) != "Notes.txt" {
		t.Fatalf("downloading Notes.txt through its link: %d %s", res.Status, res.Body)
	}
	if res := v.Get(t, file.openFile); res.Status != http.StatusOK || res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("opening Notes.txt through its link: %d %v", res.Status, res.Header)
	}
}

// A visitor with a working link to Bakery, deep in Bence's My files, tries to get out of it: every route under the
// public prefix with every item it does not cover (above it, beside it, in another place, in the trash, or no item at
// all), zips naming items outside it, path tricks in the address, and the members' routes. Every try gets the same
// answer as a link that does not exist, or the members' refusal, and nothing changes. Forged identity headers change
// nothing. The routes are those of internal/server/routes.go under the public prefix; a test there keeps the two lists
// equal.
func TestAVisitorCannotGetOutOfTheirLink(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.bakery.ID, false)
	// Something deleted out of Bakery: in the trash, it is not in the link any more.
	old := uploadedInto(t, b, x.place, x.bakery.ID, "Old.pdf", []byte("old"))
	deleted(t, b, x.place, old.ID)
	rekas, _ := myFiles(t, d, reka)
	rekasFile := uploaded(t, d.As(reka), rekas, "Réka's.txt", []byte("hers"))
	payroll := madeSharedFolder(t, b, "Payroll")
	slip := uploadedInto(t, b, payroll.ID, 0, "Slip.pdf", []byte("slip"))
	_, payrollLink := madeLink(t, d, b, payroll.ID, slip.ID, false)

	routes := func(page string, item string) map[string]string {
		return map[string]string{
			"GET /s/{token}":                       page,
			"GET /s/{token}/items/{item}":          page + "/items/" + item,
			"GET /s/{token}/items/{item}/download": page + "/items/" + item + "/download",
			"GET /s/{token}/items/{item}/open":     page + "/items/" + item + "/open",
			"GET /s/{token}/items/{item}/zip":      page + "/items/" + item + "/zip",
		}
	}
	if n := len(routes("", "")); n != 5 {
		t.Fatalf("the test tries %d routes under the public prefix; internal/server/server_test.go lists 5", n)
	}
	missing := v.Get(t, noSuchLink(t))
	if missing.Status != http.StatusNotFound || !strings.Contains(string(missing.Body), "This link does not work") {
		t.Fatalf("a link that does not exist: %d %s", missing.Status, missing.Body)
	}
	visitorHeaders(t, "a link that does not exist", missing)

	// Items it does not cover, through every route that takes one.
	nowhere := slip.ID + x.notes.ID + 1000
	for what, id := range map[string]string{
		"Clients, the folder above Bakery":   fmt.Sprint(x.clients.ID),
		"Price list.pdf, beside Bakery":      fmt.Sprint(x.prices.ID),
		"Secret.txt, in another folder":      fmt.Sprint(x.secret.ID),
		"Notes.txt, at the top":              fmt.Sprint(x.notes.ID),
		"Old.pdf, deleted out of Bakery":     fmt.Sprint(old.ID),
		"a file of Réka's My files":          fmt.Sprint(rekasFile.ID),
		"a file of a shared folder of Bence": fmt.Sprint(slip.ID),
		"an item that does not exist":        fmt.Sprint(nowhere),
		"no item (0)":                        "0",
		"a negative id":                      "-1",
		"Clients, written with a plus":       "+" + fmt.Sprint(x.clients.ID),
		"not a number":                       "x",
		"Clients, through Bakery, in one id": fmt.Sprintf("%d%%2F..%%2F%d", x.bakery.ID, x.clients.ID),
		"a very long number":                 strings.Repeat("9", 40),
	} {
		for route, path := range routes(page, id) {
			if route == "GET /s/{token}" {
				continue
			}
			sameAsNoLink(t, fmt.Sprintf("%s with %s", route, what), v.Get(t, path), missing)
		}
	}
	// A zip of what it covers, naming items outside it.
	for what, id := range map[string]int64{"Price list.pdf": x.prices.ID, "Secret.txt": x.secret.ID, "Old.pdf, in the trash": old.ID, "Slip.pdf": slip.ID} {
		sameAsNoLink(t, "a zip of Bakery naming "+what, v.Get(t, inLink(page, x.bakery.ID, "/zip")+"?item="+fmt.Sprint(id)), missing)
	}
	if got := zipEntries(t, v.Get(t, inLink(page, x.bakery.ID, "/zip")+"?item="+fmt.Sprint(x.y2026.ID)).Body); len(got) != 3 {
		t.Fatalf("a zip of Bakery naming 2026 holds %q", keys(got))
	}
	// Another link's item through this link, and this link's item through the other.
	sameAsNoLink(t, "Bakery's link with Slip.pdf, which has a link of its own", v.Get(t, inLink(page, slip.ID, "/download")), missing)
	sameAsNoLink(t, "Slip.pdf's link with Bakery", v.Get(t, inLink(payrollLink, x.bakery.ID, "")), missing)

	// Path tricks: whatever the address says, a visitor's request is decided on the clean path it comes to, and nothing
	// outside the link opens. (A client following the Drive's redirects, as a browser does.)
	for _, trick := range []string{
		page + "/items/" + fmt.Sprint(x.bakery.ID) + "/../" + fmt.Sprint(x.clients.ID),
		page + "/items/" + fmt.Sprint(x.offer.ID) + "/download/../../" + fmt.Sprint(x.secret.ID) + "/download",
		page + "/../" + strings.TrimPrefix(noSuchLink(t), "/s/"),
		page + "/items/" + fmt.Sprint(x.clients.ID) + "%2Fdownload",
		page + "/items//" + fmt.Sprint(x.clients.ID),
		"/s//" + strings.TrimPrefix(page, "/s/") + "/items/" + fmt.Sprint(x.clients.ID),
		page + "/",
		page + "/search?q=Secret",
		page + "/items/" + fmt.Sprint(x.bakery.ID) + "/items",
		page + "/trash",
		page + "/zip",
		"/s/",
		"/s",
		"/s/" + strings.Repeat("A", 31),
		"/s/" + strings.Repeat("A", 33),
		"/s/" + strings.ToUpper(strings.TrimPrefix(page, "/s/")),
		"/s/" + url.PathEscape("../api/places"),
		"/s/%00",
	} {
		sameAsNoLink(t, "the address "+trick, v.Get(t, trick), missing)
	}
	// Out of the prefix: the members' routes refuse a visitor, with the link's address or without it.
	for _, path := range []string{
		page + "/../../api/places",
		"/api/places",
		fmt.Sprintf("/api/places/%d/items/%d/items", x.place, x.bakery.ID),
		fmt.Sprintf("/api/places/%d/items/%d/download", x.place, x.secret.ID),
		fmt.Sprintf("/api/places/%d/trash", x.place),
		"/api/search?q=Secret",
		"/api/shared-with-me",
		"/S" + strings.TrimPrefix(page, "/s"),
		"/",
	} {
		for what, c := range map[string]*harness.Client{
			"a visitor":                 v,
			"a visitor naming Bence":    v.With("Remote-User-Id", bence.UserID).With("Remote-Email", bence.Email),
			"a visitor with the link":   v.With("Referer", d.Address+page).With("Cookie", "link="+page),
			"a visitor with the secret": v.With("Drive-Secret", d.Secret+"x").With("Remote-User-Id", bence.UserID),
		} {
			if res := c.Get(t, path); res.Status != http.StatusForbidden {
				t.Errorf("%s asking for %s: got %d %.200s, want 403", what, path, res.Status, res.Body)
			}
		}
	}

	// Forged identity under the prefix does nothing: the same page and the same refusals, even with the real secret.
	forged := map[string]*harness.Client{
		"Bence's headers, with the real secret": d.As(bence),
		"Réka's headers, with the real secret":  d.As(reka),
		"a stranger's id":                       v.With("Remote-User-Id", "u-stranger").With("Drive-Secret", d.Secret),
		"a forged secret":                       v.With("Drive-Secret", "forged").With("Remote-User-Id", bence.UserID),
	}
	for what, c := range forged {
		if got := c.Get(t, page); got.Status != http.StatusOK || !bytes.Equal(got.Body, v.Get(t, page).Body) {
			t.Errorf("%s: the page differs from a visitor's: %d", what, got.Status)
		}
		for _, id := range []int64{x.clients.ID, x.secret.ID, rekasFile.ID, old.ID} {
			sameAsNoLink(t, what+" through the link", c.Get(t, inLink(page, id, "/download")), missing)
		}
		sameAsNoLink(t, what+", with no link", c.Get(t, noSuchLink(t)), missing)
	}

	// Nothing changed, and the link still opens what it covers.
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Bakery on the storage: %q", got)
	}
	if res := v.Get(t, inLink(page, x.offer.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "offer" {
		t.Errorf("Offer.pdf through the link, after all that: %d %s", res.Status, res.Body)
	}
}

// Removing a link ends it for good: its address answers exactly as one that never existed, or a malformed one, and a
// new link gets a new address. An item has one link: a new one is made only in place of the one it has.
func TestRemovingALinkEndsItForGood(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, first := madeLink(t, d, b, x.place, x.clients.ID, false)
	if res := v.Get(t, first); res.Status != http.StatusOK {
		t.Fatalf("the link: %d", res.Status)
	}
	if res := removeLink(t, b, x.place, x.clients.ID); res.Status != http.StatusNoContent {
		t.Fatalf("removing the link: %d %s", res.Status, res.Body)
	}
	if got := linkOn(t, b, x.place, x.clients.ID); got != nil {
		t.Fatalf("the link after it was removed: %+v", got)
	}
	missing := v.Get(t, noSuchLink(t))
	malformed := []string{"/s/x", "/s/" + strings.Repeat("A", 32), "/s/" + strings.Repeat("-", 40), "/s/%E2%9C%93", "/s/" + strings.Repeat("z", 2000)}
	for _, path := range append([]string{first, inLink(first, x.bakery.ID, ""), inLink(first, x.offer.ID, "/download"),
		inLink(first, x.clients.ID, "/zip")}, malformed...) {
		sameAsNoLink(t, path, v.Get(t, path), missing)
	}
	if res := removeLink(t, b, x.place, x.clients.ID); res.Status != http.StatusNoContent {
		t.Fatalf("removing it again: %d", res.Status)
	}

	// A new one has a new address; the old one stays dead.
	_, second := madeLink(t, d, b, x.place, x.clients.ID, false)
	if second == first {
		t.Fatal("the new link got the old address")
	}
	sameAsNoLink(t, "the first link, after a new one", v.Get(t, first), missing)
	// One per item: another is refused, until it is asked for in place of this one.
	res := makeLink(t, b, x.place, x.clients.ID, false)
	if res.Status != http.StatusConflict || !strings.Contains(string(res.Body), `"exists":true`) {
		t.Fatalf("a second link on Clients: %d %s", res.Status, res.Body)
	}
	if res := v.Get(t, second); res.Status != http.StatusOK {
		t.Fatalf("the second link, after another was refused: %d", res.Status)
	}
	_, third := madeLink(t, d, b, x.place, x.clients.ID, true)
	if third == second || third == first {
		t.Fatal("the link made in place of the second has an old address")
	}
	sameAsNoLink(t, "the second link, replaced", v.Get(t, second), missing)
	if res := v.Get(t, third); res.Status != http.StatusOK {
		t.Fatalf("the third link: %d", res.Status)
	}
	// The index holds no token: none of the three addresses is in its file.
	index := indexFile(t, d)
	for _, page := range []string{first, second, third} {
		if bytes.Contains(index, []byte(strings.TrimPrefix(page, "/s/"))) {
			t.Errorf("the index holds the token of %s", page)
		}
	}
	// And the log never names one.
	if logs := d.Logs(); strings.Contains(logs, strings.TrimPrefix(third, "/s/")) || strings.Contains(logs, strings.TrimPrefix(first, "/s/")) {
		t.Errorf("the Drive's log names a link's token:\n%s", logs)
	}
}

// indexFile is the index's database file and its write-ahead log, as a copy or a backup of the data folder would hold
// them.
func indexFile(t *testing.T, d *harness.Drive) []byte {
	t.Helper()
	var out []byte
	for _, name := range []string{"index.sqlite", "index.sqlite-wal"} {
		b, _ := os.ReadFile(filepath.Join(d.DataDir, name))
		out = append(out, b...)
	}
	return out
}

// A link hangs on its item, not on its name or where it is: it follows a rename and a move (into a shared folder too),
// covers what is in the item where it is now, stops while the item is in the trash and works again when it is
// restored, serves a file's new content after it is replaced, and goes when the item is gone.
func TestALinkFollowsItsItem(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.bakery.ID, false)
	missing := v.Get(t, noSuchLink(t))

	ok(t, "renaming Bakery", rename(t, b, x.place, x.bakery.ID, "Bakery Ltd"))
	ok(t, "moving it into Private", move(t, b, x.place, x.bakery.ID, x.private.ID))
	if p := readPage(t, v, page); p.name != "Bakery Ltd" || !slices.Equal(p.rows, []string{"2026/"}) {
		t.Fatalf("the link after a rename and a move: %+v", p)
	}
	// What leaves it is no longer covered; what comes into it is.
	ok(t, "moving Offer.pdf out of it", move(t, b, x.place, x.offer.ID, x.clients.ID))
	sameAsNoLink(t, "Offer.pdf, moved out of Bakery", v.Get(t, inLink(page, x.offer.ID, "/download")), missing)
	ok(t, "moving Notes.txt into it", move(t, b, x.place, x.notes.ID, x.bakery.ID))
	if res := v.Get(t, inLink(page, x.notes.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "notes" {
		t.Fatalf("Notes.txt, moved into Bakery: %d %s", res.Status, res.Body)
	}

	// In the trash, it does not work; restored, it does again, at the same address.
	deleted(t, b, x.place, x.bakery.ID)
	sameAsNoLink(t, "the link while Bakery is in the trash", v.Get(t, page), missing)
	sameAsNoLink(t, "Notes.txt while Bakery is in the trash", v.Get(t, inLink(page, x.notes.ID, "/download")), missing)
	restored(t, b, x.place, x.bakery.ID)
	if res := v.Get(t, page); res.Status != http.StatusOK {
		t.Fatalf("the link after Bakery was restored: %d", res.Status)
	}

	// A link to a file serves its new content when an upload replaces it: it is the same file.
	_, prices := madeLink(t, d, b, x.place, x.prices.ID, false)
	if res := uploadInto(t, b, x.place, x.clients.ID, "Price list.pdf", []byte("new prices"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing Price list.pdf: %d %s", res.Status, res.Body)
	}
	if res := v.Get(t, inLink(prices, x.prices.ID, "/download")); string(res.Body) != "new prices" {
		t.Fatalf("Price list.pdf through its link after it was replaced: %d %s", res.Status, res.Body)
	}

	// Moved from Bence's My files into a shared folder, it becomes the project's, and its link goes with it.
	team := madeSharedFolder(t, b, "Team")
	ok(t, "moving Bakery into Team", moveInto(t, b, x.place, x.bakery.ID, team.ID, 0))
	if p := readPage(t, v, page); p.name != "Bakery Ltd" || !slices.Contains(p.rows, "Notes.txt") {
		t.Fatalf("the link after Bakery moved into Team: %+v", p)
	}
	if got := linkOn(t, b, team.ID, x.bakery.ID); got == nil || !got.Works {
		t.Fatalf("the link on Bakery in Team: %+v", got)
	}

	// Gone from the storage: once a listing sees it, the link goes with it, and stays gone.
	d.Storage.Delete(t, "Shared folders/Team/Bakery Ltd", true)
	list(t, b, team.ID, 0)
	sameAsNoLink(t, "the link once Bakery is gone", v.Get(t, page), missing)
	d.Storage.Put(t, "Shared folders/Team/Bakery Ltd/2026/Offer.pdf", "back on the storage")
	list(t, b, team.ID, 0)
	sameAsNoLink(t, "the link once a folder of that name is back", v.Get(t, page), missing)
}

// Whoever may share an item makes a link to it, sees it and removes it: an editor, the owner, a manager. A viewer does
// none of these, and is answered as for an item that does not exist; but they, and everyone who can see the item, see
// that anyone with the link can.
func TestWhoMayMakeSeeAndRemoveALink(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Bakery with Dóra as viewer", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	nowhere := x.notes.ID + 1000

	for what, try := range map[string]func(id int64) harness.Response{
		"making a link":    func(id int64) harness.Response { return makeLink(t, o, x.place, id, false) },
		"replacing a link": func(id int64) harness.Response { return makeLink(t, o, x.place, id, true) },
		"seeing the link":  func(id int64) harness.Response { return o.Get(t, linkPath(x.place, id)) },
		"removing a link":  func(id int64) harness.Response { return removeLink(t, o, x.place, id) },
	} {
		sameAs(t, "Dóra, a viewer of Bakery, "+what, try(x.bakery.ID), try(nowhere))
		sameAs(t, "Dóra, a viewer of Bakery, "+what+" on 2026 inside it", try(x.y2026.ID), try(nowhere))
	}
	if l := list(t, o, x.place, x.bakery.ID); slices.Contains(named(t, l.Items, "2026").Can, "make-link") ||
		slices.Contains(named(t, l.Items, "2026").Can, "manage-link") {
		t.Fatalf("Dóra, a viewer, is offered links: %q", named(t, l.Items, "2026").Can)
	}

	// Bence makes one on Bakery: Dóra sees that anyone with it can see what is in it, from Bakery.
	madeLink(t, d, b, x.place, x.bakery.ID, false)
	if got := accessLines(accessOf(t, o, itemAccessPath(x.place, x.y2026.ID))); !slices.Contains(got, "link viewer (from Bakery)") {
		t.Fatalf("who can see 2026, as Dóra sees it: %q", got)
	}
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID))); !slices.Contains(got, "link viewer (here)") {
		t.Fatalf("who can see Bakery, as Bence sees it: %q", got)
	}
	if l := list(t, o, x.place, x.bakery.ID); !named(t, l.Items, "2026").SeenBy.Link {
		t.Fatalf("2026 in Dóra's list: %+v", named(t, l.Items, "2026").SeenBy)
	}
	if l := list(t, b, x.place, x.clients.ID); !named(t, l.Items, "Bakery").SeenBy.Link || named(t, l.Items, "Price list.pdf").SeenBy.Link {
		t.Fatalf("Clients in Bence's list: Bakery %+v, Price list.pdf %+v", named(t, l.Items, "Bakery").SeenBy, named(t, l.Items, "Price list.pdf").SeenBy)
	}
	if l := list(t, b, x.place, 0); named(t, l.Items, "Clients").SeenBy.Link {
		t.Fatal("Clients, above the link, is said to be seen by anyone with it")
	}

	// An editor of Bakery makes a link inside it, and the owner sees it, with who made it, and removes it.
	ok(t, "making Dóra an editor of Bakery", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	_, page := madeLink(t, d, o, x.place, x.y2026.ID, false)
	got := linkOn(t, b, x.place, x.y2026.ID)
	if got == nil || got.MadeBy == nil || got.MadeBy.UserID != dora.UserID || !got.Works {
		t.Fatalf("Dóra's link on 2026, as Bence sees it: %+v", got)
	}
	if p := readPage(t, visitor(d), page); p.by != "Shared by Dóra Nagy" {
		t.Fatalf("Dóra's link's page: %+v", p)
	}
	if res := removeLink(t, b, x.place, x.y2026.ID); res.Status != http.StatusNoContent {
		t.Fatalf("Bence removing Dóra's link: %d %s", res.Status, res.Body)
	}
	sameAsNoLink(t, "Dóra's link, removed by Bence", visitor(d).Get(t, page), visitor(d).Get(t, noSuchLink(t)))

	// In a shared folder: an editor makes one, a manager (the project admin Réka) sees and removes it.
	team := madeSharedFolder(t, b, "Team")
	ok(t, "Dóra an editor of Team", share(t, b, team.ID, 0, dora, "editor"))
	plans := uploadedInto(t, o, team.ID, 0, "Plans.pdf", []byte("plans"))
	madeLink(t, d, o, team.ID, plans.ID, false)
	if got := linkOn(t, d.As(reka), team.ID, plans.ID); got == nil || got.MadeBy.UserID != dora.UserID {
		t.Fatalf("Dóra's link on Plans.pdf, as Réka sees it: %+v", got)
	}
	if res := removeLink(t, d.As(reka), team.ID, plans.ID); res.Status != http.StatusNoContent {
		t.Fatalf("Réka removing Dóra's link: %d", res.Status)
	}
	// Nowhere on a place's top folder: there is no such route.
	if res := d.As(reka).Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/link", team.ID), nil); res.Status != http.StatusNotFound {
		t.Fatalf("a link on Team as a whole: %d %s", res.Status, res.Body)
	}
}

// In a My files a link is its maker's share: it works while they may share its item, stops when that is taken away or
// they leave the project, and works again when they are given it back or return (as what is shared from a held My files
// does). A shared folder's links are the project's: they work whoever made them, also after their maker left. A shared
// folder in the trash opens nothing.
func TestALinkWorksWhileItsPlaceAndItsMakerAllow(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))
	works := func(what, page string) {
		t.Helper()
		if res := v.Get(t, page); res.Status != http.StatusOK {
			t.Fatalf("%s: got %d, want the page", what, res.Status)
		}
	}

	ok(t, "Dóra an editor of Bakery", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	_, doras := madeLink(t, d, o, x.place, x.y2026.ID, false)
	_, bences := madeLink(t, d, b, x.place, x.clients.ID, false)
	works("Dóra's link in Bence's My files", doras)

	// Bence makes her a viewer: she may no longer share 2026, and her link stops; he sees why.
	ok(t, "Dóra a viewer of Bakery", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	sameAsNoLink(t, "Dóra's link once she is a viewer", v.Get(t, doras), missing)
	if got := linkOn(t, b, x.place, x.y2026.ID); got == nil || got.Works {
		t.Fatalf("Dóra's link, as Bence sees it now: %+v", got)
	}
	works("Bence's own link, meanwhile", bences)
	ok(t, "Dóra an editor of Bakery again", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	works("Dóra's link once she is an editor again", doras)

	// Dóra leaves the project: her link from Bence's My files stops; Bence's own does not.
	d.SetMembers(t, reka, bence)
	harness.Eventually(t, "Dóra is gone", func() bool { return o.Get(t, "/api/me").Status == http.StatusForbidden })
	sameAsNoLink(t, "Dóra's link once she has left", v.Get(t, doras), missing)
	works("Bence's link, after Dóra left", bences)
	// Bence leaves: his My files is held, and nothing shared from it opens.
	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Bence is gone", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	sameAsNoLink(t, "Bence's link once he has left", v.Get(t, bences), missing)
	sameAsNoLink(t, "Dóra's link once Bence has left", v.Get(t, doras), missing)
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "both are back", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	works("Bence's link once he is back", bences)
	works("Dóra's link once both are back", doras)

	// In a shared folder, Dóra's link is the project's: it works after she left, and says the project shared it.
	team := madeSharedFolder(t, b, "Team")
	ok(t, "Dóra an editor of Team", share(t, b, team.ID, 0, dora, "editor"))
	plans := uploadedInto(t, o, team.ID, 0, "Plans.pdf", []byte("plans"))
	_, teams := madeLink(t, d, o, team.ID, plans.ID, false)
	ok(t, "taking Team from Dóra", takeAccess(t, b, team.ID, dora))
	works("Dóra's link in Team, after Team was taken from her", teams)
	d.SetMembers(t, reka, bence)
	harness.Eventually(t, "Dóra is gone", func() bool { return o.Get(t, "/api/me").Status == http.StatusForbidden })
	if p := readPage(t, v, teams); p.by != "Shared from Kovács Accounting" || p.name != "Plans.pdf" {
		t.Fatalf("Dóra's link in Team after she left: %+v", p)
	}
	if got := linkOn(t, b, team.ID, plans.ID); got == nil || !got.Former || got.MadeBy != nil || !got.Works {
		t.Fatalf("Dóra's link in Team, as Bence sees it after she left: %+v", got)
	}
	// Team in the trash: nothing in it opens; restored, it does again.
	ok(t, "deleting Team", deleteSharedFolder(t, b, team.ID))
	sameAsNoLink(t, "the link in Team while Team is in the trash", v.Get(t, teams), missing)
	ok(t, "restoring Team", restoreSharedFolder(t, b, team.ID))
	works("the link in Team once Team is back", teams)
}

// How often a link was used, and when last, is counted for whoever manages it: each page through it, and each download,
// file opened or zip from its start. Not a HEAD, a part after a file's start (a video seeking, a download resuming), or
// a refused request. The counts are written when the Drive stops, and kept.
func TestALinksUsesAreCounted(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.clients.ID, false)
	before := time.Now()

	counted := []string{page, inLink(page, x.bakery.ID, ""), inLink(page, x.offer.ID, "/download"),
		inLink(page, x.offer.ID, "/open"), inLink(page, x.clients.ID, "/zip")}
	for _, path := range counted {
		if res := v.Get(t, path); res.Status != http.StatusOK {
			t.Fatalf("%s: %d", path, res.Status)
		}
	}
	if res := v.With("Range", "bytes=0-1").Get(t, inLink(page, x.offer.ID, "/download")); res.Status != http.StatusPartialContent {
		t.Fatalf("the start of Offer.pdf: %d", res.Status)
	}
	for what, res := range map[string]harness.Response{
		"HEAD of the page":           v.Do(t, http.MethodHead, page, nil),
		"a part after the start":     v.With("Range", "bytes=2-").Get(t, inLink(page, x.offer.ID, "/download")),
		"a HEAD of a download":       v.Do(t, http.MethodHead, inLink(page, x.offer.ID, "/download"), nil),
		"something it does not hold": v.Get(t, inLink(page, x.notes.ID, "/download")),
	} {
		if res.Status >= 500 {
			t.Fatalf("%s: %d", what, res.Status)
		}
	}
	got := linkOn(t, b, x.place, x.clients.ID)
	if got == nil || got.Uses != int64(len(counted)+1) || got.Used == nil || got.Used.Before(before.Add(-time.Second)) || time.Since(*got.Used) > time.Minute {
		t.Fatalf("the link after %d uses: %+v", len(counted)+1, got)
	}
	// Written when the Drive stops, and there when it starts again.
	d.Restart(t, nil)
	if after := linkOn(t, d.As(bence), x.place, x.clients.ID); after == nil || after.Uses != got.Uses || !after.Used.Equal(*got.Used) {
		t.Fatalf("the link after a restart: %+v, want %+v", after, got)
	}
	// A new link in its place starts again.
	madeLink(t, d, d.As(bence), x.place, x.clients.ID, true)
	if again := linkOn(t, d.As(bence), x.place, x.clients.ID); again.Uses != 0 || again.Used != nil {
		t.Fatalf("a new link in place of the first: %+v", again)
	}
}

// A visitor reads; nothing else is answered under the public prefix, whatever it is, before anything is looked at.
func TestAVisitorOnlyReads(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.clients.ID, false)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions, "PROPFIND", "MKCOL", "MOVE"} {
		for _, path := range []string{page, inLink(page, x.bakery.ID, ""), inLink(page, x.bakery.ID, "/files?name=x.txt"), noSuchLink(t)} {
			for what, c := range map[string]*harness.Client{"a visitor": v, "Bence, signed in": b} {
				res := c.Do(t, method, path, strings.NewReader("planted"))
				if res.Status != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "GET, HEAD" {
					t.Errorf("%s: %s %s: got %d %s", what, method, path, res.Status, res.Body)
				}
				visitorHeaders(t, method+" "+path, res)
			}
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Bakery on the storage: %q", got)
	}
}
