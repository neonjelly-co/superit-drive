package test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// Upload-only links: a folder's public link through which anyone with its address sends files into the folder, and
// sees nothing of what is there. The members' calls, as the interface makes them; the visitor's, as the page's script
// makes them (one POST per file, its length in Content-Length, its name and a turn of its own), or by hand, as anyone
// could.

// linkLimit makes a test build stop an upload-only link at bytes rather than 10 GB (SUPERIT_DRIVE_TEST_LINK_LIMIT).
func linkLimit(bytes int) map[string]string {
	return map[string]string{"SUPERIT_DRIVE_TEST_LINK_LIMIT": strconv.Itoa(bytes)}
}

// tenGB is what an upload-only link takes in until it is reset, outside tests: 10 GB, as storage is sold.
const tenGB = 10_000_000_000

func makeLinkOfKind(t *testing.T, c *harness.Client, placeID, itemID int64, kind string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, linkPath(placeID, itemID), map[string]string{"kind": kind})
}

// madeUploadOnly makes an upload-only link on a folder and returns it, with the path of its page.
func madeUploadOnly(t *testing.T, d *harness.Drive, c *harness.Client, placeID, folderID int64, replace bool) (linkJSON, string) {
	t.Helper()
	res := sendJSON(t, c, http.MethodPost, linkPath(placeID, folderID), map[string]any{"kind": "upload-only", "replace": replace})
	if res.Status != http.StatusCreated {
		t.Fatalf("making an upload-only link on item %d: got %d %s", folderID, res.Status, res.Body)
	}
	l := linkIn(t, res)
	page, ok := strings.CutPrefix(l.Address, d.Address)
	if !ok || !pageAddress.MatchString(page) {
		t.Fatalf("the link's address %q is not the Drive's address and /s/ and a token", l.Address)
	}
	return l, page
}

func setLinkKind(t *testing.T, c *harness.Client, placeID, itemID int64, kind string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, linkPath(placeID, itemID), map[string]string{"kind": kind})
}

func resetLink(t *testing.T, c *harness.Client, placeID, itemID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, linkPath(placeID, itemID)+"/reset", nil)
}

// linkIn is the link an answer holds.
func linkIn(t *testing.T, res harness.Response) linkJSON {
	t.Helper()
	var got struct {
		Link *linkJSON `json:"link"`
	}
	res.JSON(t, &got)
	if got.Link == nil {
		t.Fatalf("the answer holds no link: %s", res.Body)
	}
	return *got.Link
}

// changedLink fails the test unless res is 200 with the link, and returns it.
func changedLink(t *testing.T, what string, res harness.Response) linkJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s: got %d %s", what, res.Status, res.Body)
	}
	return linkIn(t, res)
}

// takenIn is what an upload-only link has taken in, as whoever may manage it sees it.
func takenIn(t *testing.T, c *harness.Client, placeID, folderID int64) int64 {
	t.Helper()
	l := linkOn(t, c, placeID, folderID)
	if l == nil || l.TakenIn == nil {
		t.Fatalf("the link on item %d says nothing of what it took in: %+v", folderID, l)
	}
	return *l.TakenIn
}

// newTurn is a turn of its own for one file, as the page's script makes one.
func newTurn() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sendPath is where an upload-only link's page sends a file called name, as its script does.
func sendPath(page, name, turn string) string {
	q := url.Values{"name": {name}}
	if turn != "" {
		q.Set("turn", turn)
	}
	return page + "/files?" + q.Encode()
}

// sendThrough sends a file through an upload-only link's page as the page's script does: one POST, with its length,
// and again with the same turn while the Drive says it is not its turn.
func sendThrough(t *testing.T, c *harness.Client, page, name string, content []byte) harness.Response {
	t.Helper()
	return sendWithTurn(t, c, page, name, content, newTurn())
}

func sendWithTurn(t *testing.T, c *harness.Client, page, name string, content []byte, turn string) harness.Response {
	t.Helper()
	for range 30 {
		res := c.Do(t, http.MethodPost, sendPath(page, name, turn), bytes.NewReader(content))
		if res.Status != http.StatusServiceUnavailable {
			return res
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("sending %q: the Drive stayed busy", name)
	return harness.Response{}
}

// sentThrough sends a file through an upload-only link's page, and fails the test unless it was taken: 201, saying its
// size and nothing else.
func sentThrough(t *testing.T, c *harness.Client, page, name string, content []byte) {
	t.Helper()
	res := sendThrough(t, c, page, name, content)
	if res.Status != http.StatusCreated || strings.TrimSpace(string(res.Body)) != fmt.Sprintf(`{"size":%d}`, len(content)) {
		t.Fatalf("sending %q through the link: got %d %s", name, res.Status, res.Body)
	}
	visitorHeaders(t, "sending "+name, res)
}

// refusedAsTooLarge fails the test unless res says the file would take the link past its limit (413), and whether the
// link is full.
func refusedAsTooLarge(t *testing.T, what string, res harness.Response, full bool) {
	t.Helper()
	var got struct {
		Error string `json:"error"`
		Full  *bool  `json:"full"`
	}
	if res.Status == http.StatusRequestEntityTooLarge {
		res.JSON(t, &got)
	}
	if res.Status != http.StatusRequestEntityTooLarge || got.Full == nil || *got.Full != full || got.Error == "" {
		t.Errorf("%s: got %d %s; want 413 with full %v", what, res.Status, res.Body, full)
	}
}

// sendPage is an upload-only link's page, as a visitor's browser reads it.
type sendPage struct {
	by, name, body string
	// drop: a place to choose or drop files, with a button to send them; hidden: it says that what is in the folder
	// cannot be seen; full: it says the link takes no more files; rows: anything listed.
	drop, hidden, full bool
	rows               int
	// script is the page's one script; policy its Content-Security-Policy.
	script, policy string
	scripts        int
}

var pageScripts = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// readSendPage reads an upload-only link's page, which must open.
func readSendPage(t *testing.T, c *harness.Client, page string) sendPage {
	t.Helper()
	res := c.Get(t, page)
	if res.Status != http.StatusOK || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("the page %s: got %d %s %.300s", page, res.Status, res.Header.Get("Content-Type"), res.Body)
	}
	visitorHeaders(t, "the page "+page, res)
	body := string(res.Body)
	p := sendPage{by: first(pageBy, body), name: first(pageName, body), body: body,
		drop: strings.Contains(body, `<input type="file" id="files" multiple>`) &&
			strings.Contains(body, "Drop files here, or choose them") && strings.Contains(body, `<button class="button" id="send"`),
		hidden:  strings.Contains(body, "What is in it stays hidden: you cannot see its files, their names, or what anyone else sent."),
		full:    strings.Contains(body, "This link has taken in all it can for now."),
		rows:    len(pageRow.FindAllString(body, -1)),
		policy:  res.Header.Get("Content-Security-Policy"),
		scripts: strings.Count(strings.ToLower(body), "<script")}
	if m := pageScripts.FindStringSubmatch(body); m != nil {
		p.script = m[1]
	}
	return p
}

// hashOf is how a Content-Security-Policy names a script by its content.
func hashOf(script string) string {
	sum := sha256.Sum256([]byte(script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// heldSend is a file on its way through a link's page, sent by hand on a connection of its own: its head and its first
// bytes at once, the rest when the test says (or never: cut off).
type heldSend struct {
	conn net.Conn
	r    *bufio.Reader
}

func startSend(t *testing.T, d *harness.Drive, page, name string, size int, first []byte) *heldSend {
	t.Helper()
	return startSendWithHead(t, d, sendPath(page, name, newTurn()), fmt.Sprintf("Content-Length: %d\r\n", size), first)
}

func startSendWithHead(t *testing.T, d *harness.Drive, path, head string, first []byte) *heldSend {
	t.Helper()
	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/octet-stream\r\n%s\r\n", path, address.Host, head)
	if _, err := conn.Write(first); err != nil {
		t.Fatal(err)
	}
	return &heldSend{conn: conn, r: bufio.NewReader(conn)}
}

// answeredWithin says whether the Drive has answered within wait, without reading the answer.
func (h *heldSend) answeredWithin(wait time.Duration) bool {
	_ = h.conn.SetReadDeadline(time.Now().Add(wait))
	_, err := h.r.Peek(1)
	_ = h.conn.SetReadDeadline(time.Time{})
	return err == nil
}

// finish sends the rest of the file, and returns the Drive's answer.
func (h *heldSend) finish(t *testing.T, rest []byte) harness.Response {
	t.Helper()
	_ = h.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, _ = h.conn.Write(rest) // the Drive may have answered already, and read no more
	return h.answer(t)
}

// answer is the Drive's answer.
func (h *heldSend) answer(t *testing.T) harness.Response {
	t.Helper()
	_ = h.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	res, err := http.ReadResponse(h.r, nil)
	if err != nil {
		t.Fatalf("no answer to a file sent by hand: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return harness.Response{Status: res.StatusCode, Header: res.Header, Body: body}
}

// clientsOnStorage is what Bence's Clients holds on the storage.
func clientsOnStorage(t *testing.T, d *harness.Drive) []string {
	t.Helper()
	return onStorage(t, d, "My files/bence@example.com/Clients")
}

// uploadsEmpty waits until nothing is left in the hidden uploads folder: a file that was not kept is removed.
func uploadsEmpty(t *testing.T, d *harness.Drive) {
	t.Helper()
	harness.Eventually(t, "the uploads folder is empty", func() bool { return len(inUploads(t, d.Storage)) == 0 })
}

// A folder's public link can be a view link or an upload-only one, and changed from one to the other at the same
// address; a file's is a view link only. Whoever may share the folder makes, changes and sees it; "who can see this"
// and what a project admin oversees say which it is, and an upload-only link lets nobody see anything.
func TestAFoldersLinkIsViewOrUploadOnlyAndAFilesOnlyView(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))

	// A file's link can only be a view link: asked for an upload-only one, nothing is made. Nor is a kind there is not.
	if res := makeLinkOfKind(t, b, x.place, x.notes.ID, "upload-only"); res.Status != http.StatusBadRequest ||
		!strings.Contains(errorOf(t, res), "Only a folder's link can be upload-only") {
		t.Fatalf("an upload-only link on Notes.txt: %d %s", res.Status, res.Body)
	}
	for _, kind := range []string{"edit", "upload", "VIEW", " view", "upload-only "} {
		if res := makeLinkOfKind(t, b, x.place, x.clients.ID, kind); res.Status != http.StatusBadRequest {
			t.Fatalf("a link of the kind %q on Clients: %d %s", kind, res.Status, res.Body)
		}
	}
	if linkOn(t, b, x.place, x.notes.ID) != nil || linkOn(t, b, x.place, x.clients.ID) != nil {
		t.Fatal("a refused link was made")
	}

	// A folder's: upload-only. It says what it has taken in, and the most it takes.
	link, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	if link.Kind != "upload-only" || !link.Works || link.TakenIn == nil || *link.TakenIn != 0 || link.Limit == nil ||
		*link.Limit != tenGB || link.Full || link.MadeBy == nil || link.MadeBy.UserID != bence.UserID {
		t.Fatalf("the new upload-only link: %+v", link)
	}
	if got := linkOn(t, b, x.place, x.clients.ID); got.Kind != "upload-only" || got.Address != "" || *got.TakenIn != 0 || *got.Limit != tenGB {
		t.Fatalf("the upload-only link, as the details panel sees it later: %+v", got)
	}
	if p := readSendPage(t, v, page); p.name != "Clients" || !p.drop || p.rows != 0 {
		t.Fatalf("the upload-only link's page: %+v", p)
	}

	// Its kind changes, at the same address: made a view link, it shows the folder to whoever has the address; made
	// upload-only again, it shows nothing again. The same kind again changes nothing.
	if got := changedLink(t, "making it a view link", setLinkKind(t, b, x.place, x.clients.ID, "view")); got.Kind != "view" ||
		got.TakenIn != nil || got.Limit != nil || got.Address != "" || !got.Works {
		t.Fatalf("the link made a view link: %+v", got)
	}
	if p := readPage(t, v, page); p.name != "Clients" || !slices.Equal(p.rows, []string{"Bakery/", "Price list.pdf"}) {
		t.Fatalf("the link's page as a view link: %+v", p)
	}
	for range 2 {
		if got := changedLink(t, "making it upload-only", setLinkKind(t, b, x.place, x.clients.ID, "upload-only")); got.Kind != "upload-only" || *got.TakenIn != 0 {
			t.Fatalf("the link made upload-only again: %+v", got)
		}
	}
	if p := readSendPage(t, v, page); !p.drop || p.rows != 0 {
		t.Fatalf("the link's page, upload-only again: %+v", p)
	}
	sameAsNoLink(t, "Bakery through the link, upload-only again", v.Get(t, inLink(page, x.bakery.ID, "")), missing)
	for _, body := range []any{map[string]string{"kind": "edit"}, map[string]string{"kind": ""}, map[string]string{}, "upload-only"} {
		if res := sendJSON(t, b, http.MethodPut, linkPath(x.place, x.clients.ID), body); res.Status != http.StatusBadRequest {
			t.Fatalf("changing the link's kind with %v: %d %s", body, res.Status, res.Body)
		}
	}

	// A file's view link cannot be made upload-only, and takes no file.
	_, notes := madeLink(t, d, b, x.place, x.notes.ID, false)
	if res := setLinkKind(t, b, x.place, x.notes.ID, "upload-only"); res.Status != http.StatusBadRequest ||
		!strings.Contains(errorOf(t, res), "Only a folder's link can be upload-only") {
		t.Fatalf("Notes.txt's link made upload-only: %d %s", res.Status, res.Body)
	}
	if got := linkOn(t, b, x.place, x.notes.ID); got.Kind != "view" || got.TakenIn != nil {
		t.Fatalf("Notes.txt's link after that: %+v", got)
	}
	if p := readPage(t, v, notes); p.name != "Notes.txt" || p.download == "" {
		t.Fatalf("Notes.txt's page after that: %+v", p)
	}
	sameAsNoLink(t, "a file sent through Notes.txt's link", sendThrough(t, v, notes, "Planted.txt", []byte("planted")), missing)
	// Bakery has no link: there is none to change or reset.
	for what, res := range map[string]harness.Response{
		"changing the kind of Bakery's link": setLinkKind(t, b, x.place, x.bakery.ID, "view"),
		"resetting Bakery's link":            resetLink(t, b, x.place, x.bakery.ID),
	} {
		if res.Status != http.StatusConflict {
			t.Fatalf("%s, which it does not have: %d %s", what, res.Status, res.Body)
		}
	}

	// "Who can see this": anyone with the link sends files into Clients, and sees nothing, in it or anywhere inside it.
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.clients.ID))); !slices.Contains(got, "upload-only link (here)") ||
		slices.ContainsFunc(got, func(l string) bool { return strings.HasPrefix(l, "link ") }) {
		t.Fatalf("who can see Clients: %q", got)
	}
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID))); slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, "link") }) {
		t.Fatalf("who can see Bakery, inside Clients: %q", got)
	}
	for _, it := range list(t, b, x.place, x.clients.ID).Items {
		if it.SeenBy.Link {
			t.Errorf("%s, in Clients, is said to be seen by anyone with the link", it.Name)
		}
	}
	if named(t, list(t, b, x.place, 0).Items, "Clients").SeenBy.Link {
		t.Error("Clients is said to be seen by anyone with its upload-only link")
	}

	// Dóra, a viewer of Clients, sees the line, and can change nothing: as for something that does not exist.
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	if got := accessLines(accessOf(t, o, itemAccessPath(x.place, x.clients.ID))); !slices.Contains(got, "upload-only link (here)") {
		t.Fatalf("who can see Clients, as Dóra sees it: %q", got)
	}
	nowhere := x.notes.ID + 1000
	sameAs(t, "Dóra, a viewer, making Clients' link a view link", setLinkKind(t, o, x.place, x.clients.ID, "view"), setLinkKind(t, o, x.place, nowhere, "view"))
	sameAs(t, "Dóra, a viewer, resetting Clients' link", resetLink(t, o, x.place, x.clients.ID), resetLink(t, o, x.place, nowhere))
	if got := linkOn(t, b, x.place, x.clients.ID); got.Kind != "upload-only" {
		t.Fatalf("Clients' link after Dóra tried: %+v", got)
	}

	// A project admin oversees it as an upload-only link: without a role, as its visitors see nothing.
	shares, _ := sharedOutside(t, d.As(reka))
	i := slices.IndexFunc(shares, func(e outsideShareJSON) bool { return e.Kind == "link" && e.Item.ID == x.clients.ID })
	if i < 0 || shares[i].LinkKind != "upload-only" || shares[i].Role != "" || !shares[i].Works {
		t.Fatalf("what Réka oversees: %+v", shares)
	}
	if j := slices.IndexFunc(shares, func(e outsideShareJSON) bool { return e.Item.ID == x.notes.ID }); j < 0 || shares[j].LinkKind != "view" || shares[j].Role != "viewer" {
		t.Fatalf("Notes.txt's link, as Réka oversees it: %+v", shares)
	}
}

// An upload-only link's page offers a place to choose or drop files and says that what is in the folder cannot be seen,
// and shows none of it. Files sent from it land in the folder, whole; nothing in the folder opens or downloads through
// it, the folder itself included, and nothing is sent into a folder inside it.
func TestAnUploadOnlyLinksPageTakesFilesAndShowsNothing(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	missing := v.Get(t, noSuchLink(t))

	p := readSendPage(t, v, page)
	if p.by != "Shared by Bence Tóth" || p.name != "Clients" || !p.drop || !p.hidden || p.full || p.rows != 0 || p.scripts != 1 {
		t.Fatalf("the upload-only link's page: %+v", p)
	}
	for _, secret := range []string{"Bakery", "Price list", "2026", "Offer", "Notes", "Private", "Secret", bence.Email, bence.UserID,
		"My files", "Download"} {
		if strings.Contains(p.body, secret) {
			t.Errorf("the upload-only link's page says %q", secret)
		}
	}
	// Its one script runs by its hash, and may talk only to the Drive's public prefix; no form goes anywhere, and
	// nothing else loads or runs.
	for _, want := range []string{"default-src 'none'", "script-src " + hashOf(p.script), "connect-src " + d.Address + "/s/;",
		"form-action 'none'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(p.policy, want) {
			t.Errorf("the page's policy %q does not say %q", p.policy, want)
		}
	}
	for _, loose := range []string{"unsafe", "'self'", "*", "data:", "blob:"} {
		if strings.Contains(p.policy, loose) {
			t.Errorf("the page's policy %q allows %q", p.policy, loose)
		}
	}
	if strings.Contains(p.body, "<form") || regexp.MustCompile(` on[a-z]+=`).MatchString(p.body) {
		t.Error("the page has a form, or a script in an attribute")
	}
	if res := v.Do(t, http.MethodHead, page, nil); res.Status != http.StatusOK || len(res.Body) != 0 {
		t.Fatalf("HEAD of the page: %d, %d bytes", res.Status, len(res.Body))
	}
	// A signed-in member is a visitor here like anyone: the same page.
	if got := b.Get(t, page); !bytes.Equal(got.Body, v.Get(t, page).Body) {
		t.Error("Bence, signed in, sees another page through his link than a visitor")
	}

	// Two files, as the page sends them: each lands in Clients whole, and the uploads folder is left empty.
	photo := randomBytes(t, 300<<10)
	sentThrough(t, v, page, "Brief.pdf", []byte("brief"))
	sentThrough(t, v, page, "Photo 1.jpg", photo)
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/Brief.pdf"); string(got) != "brief" {
		t.Fatalf("Brief.pdf on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/Photo 1.jpg"); !bytes.Equal(got, photo) {
		t.Fatal("Photo 1.jpg on the storage is not what was sent")
	}
	uploadsEmpty(t, d)
	l := list(t, b, x.place, x.clients.ID)
	if !slices.Equal(names(l.Items), []string{"Bakery", "Brief.pdf", "Photo 1.jpg", "Price list.pdf"}) {
		t.Fatalf("Clients after two files were sent: %q", names(l.Items))
	}
	brief := named(t, l.Items, "Brief.pdf")
	// What it took in, and its uses: the page opened three times (HEAD aside), and two files.
	if got := linkOn(t, b, x.place, x.clients.ID); *got.TakenIn != int64(5+len(photo)) || got.Uses != 5 || got.Used == nil || got.Full {
		t.Fatalf("the link after two files: %+v", got)
	}

	// Nothing opens or downloads through it: not Clients itself, not what is in it, not what was sent through it.
	for what, id := range map[string]int64{"Clients": x.clients.ID, "Bakery": x.bakery.ID, "2026": x.y2026.ID,
		"Price list.pdf": x.prices.ID, "Offer.pdf": x.offer.ID, "Brief.pdf, sent through it": brief.ID, "Notes.txt": x.notes.ID} {
		for _, path := range []string{inLink(page, id, ""), inLink(page, id, "/download"), inLink(page, id, "/open"), inLink(page, id, "/zip"),
			inLink(page, x.clients.ID, "/zip") + "?item=" + fmt.Sprint(id)} {
			sameAsNoLink(t, what+" at "+strings.TrimPrefix(path, page), v.Get(t, path), missing)
		}
	}
	for _, trick := range []string{page + "/", page + "/zip", page + "/items", page + "/files", page + "/files/", page + "/search?q=Offer"} {
		sameAsNoLink(t, "the address "+trick, v.Get(t, trick), missing)
	}
	// Nothing goes into a folder inside it, nor anywhere but its own route: anything but reading is refused there. An
	// address is decided on as the Drive's router cleans it: one that comes to the link's own route sends into Clients
	// itself, and nowhere else.
	for _, path := range []string{inLink(page, x.bakery.ID, "/files?name=Planted.txt"), page + "?name=Planted.txt", page + "/files/Planted.txt",
		page + "/files/../items/" + fmt.Sprint(x.bakery.ID) + "/files?name=Planted.txt", page + "/items/" + fmt.Sprint(x.bakery.ID)} {
		if res := v.Do(t, http.MethodPost, path, strings.NewReader("planted")); res.Status != http.StatusMethodNotAllowed &&
			res.Status != http.StatusNotFound {
			t.Errorf("POST %s: got %d %s", path, res.Status, res.Body)
		}
	}
	if res := v.Do(t, http.MethodPost, page+"/items/"+fmt.Sprint(x.bakery.ID)+"/../../files?name=Dots.txt", strings.NewReader("dots")); res.Status != http.StatusCreated &&
		res.Status != http.StatusMethodNotAllowed && res.Status != http.StatusNotFound {
		t.Errorf("POST through Bakery and back: got %d %s", res.Status, res.Body)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch, "MKCOL", "MOVE"} {
		if res := v.Do(t, method, sendPath(page, "Planted.txt", ""), strings.NewReader("planted")); res.Status != http.StatusMethodNotAllowed ||
			res.Header.Get("Allow") != "GET, HEAD" {
			t.Errorf("%s of the address files are sent to: got %d %v", method, res.Status, res.Header)
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Bakery on the storage: %q", got)
	}
	if got := slices.DeleteFunc(clientsOnStorage(t, d), func(n string) bool { return n == "Dots.txt" }); !slices.Equal(got,
		[]string{"Bakery/", "Brief.pdf", "Photo 1.jpg", "Price list.pdf"}) {
		t.Errorf("Clients on the storage: %q", got)
	}
}

// A file sent through an upload-only link whose name is taken in the folder, by a file, a folder, a file put on the
// storage directly or one sent before, replaces nothing: it is kept beside it under the next free name. The visitor's
// answer is the same either way, to the byte, so nothing tells them what is in the folder.
func TestATakenNameIsNeitherRevealedNorReplaced(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	// Put there directly, by someone with the storage login: the Drive has not seen it yet.
	d.Storage.Put(t, "My files/bence@example.com/Clients/Direct.txt", "direct")

	content := []byte("from outside")
	free := sendThrough(t, v, page, "New.txt", content)
	if free.Status != http.StatusCreated {
		t.Fatalf("New.txt through the link: %d %s", free.Status, free.Body)
	}
	for what, name := range map[string]string{
		"a file there":                      "Price list.pdf",
		"a folder there":                    "Bakery",
		"a file put on the storage there":   "Direct.txt",
		"a file sent through the link":      "New.txt",
		"a file inside a folder there":      "Offer.pdf",
		"a file of another folder":          "Notes.txt",
		"a name with a number, now taken":   "Price list (2).pdf",
		"a free name, after all the others": "Free.txt",
	} {
		res := sendThrough(t, v, page, name, content)
		res.Header.Del("Date")
		free.Header.Del("Date")
		if res.Status != free.Status || !bytes.Equal(res.Body, free.Body) || !headersEqual(res.Header, free.Header) {
			t.Errorf("sending %q, %s: got %d %s %v; a free name got %d %s %v", name, what, res.Status, res.Body, res.Header,
				free.Status, free.Body, free.Header)
		}
	}

	// Nothing was replaced: each is beside what was there.
	for path, want := range map[string]string{
		"Price list.pdf": "prices", "Price list (2).pdf": "from outside", "Price list (3).pdf": "from outside",
		"Bakery (2)": "from outside", "Direct.txt": "direct", "Direct (2).txt": "from outside",
		"New.txt": "from outside", "New (2).txt": "from outside", "Offer.pdf": "from outside", "Notes.txt": "from outside",
		"Free.txt": "from outside", "Bakery/2026/Offer.pdf": "offer",
	} {
		if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/"+path); string(got) != want {
			t.Errorf("Clients/%s on the storage holds %q, want %q", path, got, want)
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Bakery on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Notes.txt"); string(got) != "notes" {
		t.Errorf("Notes.txt, in another folder: %q", got)
	}
	// Bence sees each, labelled as come through a link; what was there before is not.
	l := list(t, b, x.place, x.clients.ID)
	for _, it := range l.Items {
		outside := !slices.Contains([]string{"Bakery", "Price list.pdf", "Direct.txt"}, it.Name)
		if (it.AddedBy != nil && it.AddedBy.Link) != outside {
			t.Errorf("%s in Clients: added by %+v", it.Name, it.AddedBy)
		}
	}
	if got := len(l.Items); got != 12 {
		t.Errorf("Clients holds %d items: %q", got, names(l.Items))
	}
}

// A name sent through a link stays one name in the link's own folder: anything that is not a plain name (a path, a
// line break, a control character, not text, too long, or turning the direction of text) is refused before anything
// is read, and nothing is stored, anywhere; names that only look like paths land in the folder as they are.
func TestNamesSentThroughALinkStayInItsFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence)
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.bakery.ID, false)

	for _, name := range []string{"", ".", "..", "../Notes.txt", "../../Notes.txt", "2026/Planted.txt", "/Planted.txt", "Planted/",
		"Planted\x00.txt", "Planted\n.txt", "Planted\r\nX-Planted: yes", "Planted\t.txt", "Planted\x7f.txt", "\xff\xfe.txt",
		strings.Repeat("a", 252) + ".txt", "Invoice\u202efdp.exe", "Report\u2066.pdf\u2069", "Report\u202a.pdf"} {
		res := sendThrough(t, v, page, name, []byte("planted"))
		if res.Status != http.StatusBadRequest || errorOf(t, res) == "" {
			t.Errorf("sending a file called %q: got %d %s", name, res.Status, res.Body)
		}
	}
	// No name at all, or two.
	if res := v.Do(t, http.MethodPost, page+"/files", strings.NewReader("planted")); res.Status != http.StatusBadRequest {
		t.Errorf("sending a file without a name: %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Fatalf("Bakery on the storage after names it refused: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com"); !slices.Equal(got, []string{"Clients/", "Notes.txt", "Private/"}) {
		t.Fatalf("Bence's My files on the storage after names it refused: %q", got)
	}

	// Names that only look like something else are names like any other, in Bakery.
	odd := []string{"..Notes.txt", ". hidden", ".superit-drive", `..\..\Notes.txt`, "%2e%2e%2fNotes.txt", "?name=x&y", "#frag",
		"Név ő ű.txt", "日本語.txt", strings.Repeat("a", 251) + ".txt", " spaced .txt"}
	for _, name := range odd {
		sentThrough(t, v, page, name, []byte(name))
	}
	for _, name := range odd {
		if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/Bakery/"+name); string(got) != name {
			t.Errorf("%q in Bakery on the storage: %q", name, got)
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com"); !slices.Equal(got, []string{"Clients/", "Notes.txt", "Private/"}) {
		t.Fatalf("Bence's My files on the storage after odd names: %q", got)
	}
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Notes.txt"); string(got) != "notes" {
		t.Fatalf("Notes.txt: %q", got)
	}
}

// An upload-only link stops taking files once it has taken in its limit (10 GB; here a few bytes): a file that would
// take it past the limit is refused before any of it is read, and nothing of it is kept. Its page then says so, and
// offers nothing to send. It takes files again, from 0, once whoever may share the folder resets it; changing its kind
// keeps what it took in.
func TestAnUploadOnlyLinkStopsAtItsLimitUntilItIsReset(t *testing.T) {
	t.Parallel()
	const limit = 1000
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: linkLimit(limit)})
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	link, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	if *link.Limit != limit {
		t.Fatalf("the link's limit: %+v", link)
	}

	sentThrough(t, v, page, "a.bin", randomBytes(t, 600))
	if got := linkOn(t, b, x.place, x.clients.ID); *got.TakenIn != 600 || got.Full {
		t.Fatalf("the link after 600 bytes: %+v", got)
	}
	refusedAsTooLarge(t, "401 bytes more", sendThrough(t, v, page, "b.bin", randomBytes(t, 401)), false)
	sentThrough(t, v, page, "c.bin", randomBytes(t, 400))
	if got := linkOn(t, b, x.place, x.clients.ID); *got.TakenIn != limit || !got.Full {
		t.Fatalf("the link after 1000 bytes: %+v", got)
	}
	// Full: not one byte more, not even an empty file. Its page says so, with nothing to send and no script.
	refusedAsTooLarge(t, "one byte more", sendThrough(t, v, page, "d.bin", []byte("d")), true)
	refusedAsTooLarge(t, "an empty file", sendThrough(t, v, page, "e.bin", nil), true)
	if p := readSendPage(t, v, page); !p.full || p.drop || p.scripts != 0 || !p.hidden || p.name != "Clients" ||
		strings.Contains(p.policy, "script-src") {
		t.Fatalf("the page of the full link: %+v", p)
	}
	if got := clientsOnStorage(t, d); !slices.Equal(got, []string{"Bakery/", "Price list.pdf", "a.bin", "c.bin"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	uploadsEmpty(t, d)

	// Its kind changes, and back: what it took in stays.
	changedLink(t, "making it a view link", setLinkKind(t, b, x.place, x.clients.ID, "view"))
	if got := changedLink(t, "making it upload-only again", setLinkKind(t, b, x.place, x.clients.ID, "upload-only")); *got.TakenIn != limit || !got.Full {
		t.Fatalf("the link after its kind changed and back: %+v", got)
	}
	// Only whoever may share Clients resets it: Dóra, a viewer, is answered as for nothing there.
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	sameAs(t, "Dóra resetting the link", resetLink(t, o, x.place, x.clients.ID), resetLink(t, o, x.place, x.notes.ID+1000))
	refusedAsTooLarge(t, "a file after Dóra tried", sendThrough(t, v, page, "f.bin", []byte("f")), true)

	// Bence resets it: it takes files again, from 0, up to its limit; what it took stays, and its address and uses too.
	before := linkOn(t, b, x.place, x.clients.ID)
	reset := changedLink(t, "resetting the link", resetLink(t, b, x.place, x.clients.ID))
	if *reset.TakenIn != 0 || reset.Full || reset.Uses != before.Uses || !reset.Works {
		t.Fatalf("the link after a reset: %+v", reset)
	}
	if p := readSendPage(t, v, page); p.full || !p.drop {
		t.Fatalf("the page after a reset: %+v", p)
	}
	sentThrough(t, v, page, "g.bin", randomBytes(t, limit))
	refusedAsTooLarge(t, "one byte past the limit again", sendThrough(t, v, page, "h.bin", []byte("h")), true)
	if got := clientsOnStorage(t, d); !slices.Equal(got, []string{"Bakery/", "Price list.pdf", "a.bin", "c.bin", "g.bin"}) {
		t.Fatalf("Clients on the storage after the reset: %q", got)
	}
}

// Files racing through an upload-only link at once never take it past its limit, whatever they say about themselves:
// each file's length counts from before its first byte is read; one cut off keeps nothing and gives its room back; one
// that sends more than it said is kept as long as it said; one that does not say is refused; one that says more than
// the link can take is refused at once.
func TestFilesRacingThroughALinkNeverPassItsLimit(t *testing.T) {
	t.Parallel()
	const limit = 1000
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: linkLimit(limit)})
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)

	// Six files of 300 bytes at once: three fit, whichever they are; the others are refused.
	var held []*heldSend
	for i := range 6 {
		held = append(held, startSend(t, d, page, fmt.Sprintf("race %d.bin", i), 300, bytes.Repeat([]byte{'a' + byte(i)}, 100)))
	}
	time.Sleep(500 * time.Millisecond)
	kept, refused := 0, 0
	for i, h := range held {
		switch res := h.finish(t, bytes.Repeat([]byte{'a' + byte(i)}, 200)); res.Status {
		case http.StatusCreated:
			kept++
		case http.StatusRequestEntityTooLarge:
			refused++
		default:
			t.Fatalf("file %d of six at once: %d %s", i, res.Status, res.Body)
		}
	}
	if kept != 3 || refused != 3 || takenIn(t, b, x.place, x.clients.ID) != 900 {
		t.Fatalf("six files of 300 bytes at once, with room for 1000: %d kept, %d refused, %d taken in", kept, refused,
			takenIn(t, b, x.place, x.clients.ID))
	}
	if got := clientsOnStorage(t, d); len(got) != 2+3 {
		t.Fatalf("Clients after six at once: %q", got)
	}

	// One cut off midway keeps nothing, and its room comes back.
	cut := startSend(t, d, page, "cut.bin", 100, []byte("half"))
	time.Sleep(300 * time.Millisecond)
	refusedAsTooLarge(t, "a file while the cut one is on its way", sendThrough(t, v, page, "late.bin", randomBytes(t, 100)), false)
	cut.conn.Close()
	harness.Eventually(t, "the room the cut-off file kept comes back", func() bool {
		return sendThrough(t, v, page, "fits.bin", randomBytes(t, 100)).Status == http.StatusCreated
	})
	if got := takenIn(t, b, x.place, x.clients.ID); got != limit {
		t.Fatalf("taken in after the cut-off file and one that fits: %d", got)
	}
	if got := clientsOnStorage(t, d); slices.Contains(got, "cut.bin") || slices.Contains(got, "late.bin") || !slices.Contains(got, "fits.bin") {
		t.Fatalf("Clients after a file was cut off: %q", got)
	}
	uploadsEmpty(t, d)
	changedLink(t, "resetting the link", resetLink(t, b, x.place, x.clients.ID))

	// More bytes than it said: as many as it said are kept, and counted; the rest is not a file.
	long := startSendWithHead(t, d, sendPath(page, "long.bin", ""), "Content-Length: 10\r\n", []byte("0123456789EXTRABYTES"))
	if res := long.answer(t); res.Status != http.StatusCreated || strings.TrimSpace(string(res.Body)) != `{"size":10}` {
		t.Fatalf("a file of 20 bytes that says 10: %d %s", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/long.bin"); string(got) != "0123456789" {
		t.Fatalf("long.bin on the storage: %q", got)
	}
	if got := takenIn(t, b, x.place, x.clients.ID); got != 10 {
		t.Fatalf("taken in after a file that said 10 bytes: %d", got)
	}
	// Without its length (sent in chunks): refused, nothing kept.
	chunked := startSendWithHead(t, d, sendPath(page, "chunked.bin", ""), "Transfer-Encoding: chunked\r\n", []byte("5\r\nhello\r\n0\r\n\r\n"))
	if res := chunked.answer(t); res.Status != http.StatusLengthRequired {
		t.Fatalf("a file without its length: %d %s", res.Status, res.Body)
	}
	// A length past what the link can take: refused at once, before anything is sent.
	huge := startSendWithHead(t, d, sendPath(page, "huge.bin", ""), "Content-Length: 999999999999999\r\n", nil)
	if !huge.answeredWithin(5 * time.Second) {
		t.Fatal("a file far larger than the link can take was not refused at once")
	}
	refusedAsTooLarge(t, "a file far larger than the link can take", huge.answer(t), false)
	for _, length := range []string{"-1", "ten", "18446744073709551616"} {
		h := startSendWithHead(t, d, sendPath(page, "odd.bin", ""), "Content-Length: "+length+"\r\n", nil)
		if res := h.answer(t); res.Status != http.StatusBadRequest {
			t.Errorf("a file whose length is %q: %d", length, res.Status)
		}
	}
	if got := clientsOnStorage(t, d); slices.Contains(got, "chunked.bin") || slices.Contains(got, "huge.bin") || slices.Contains(got, "odd.bin") {
		t.Fatalf("Clients after files without a length it takes: %q", got)
	}
	if got := takenIn(t, b, x.place, x.clients.ID); got != 10 {
		t.Fatalf("taken in after files it refused: %d", got)
	}
	uploadsEmpty(t, d)
}

// A file on its way through an upload-only link is decided on again once it has all arrived: if the link was made a
// view link meanwhile, removed, replaced, its folder deleted, its maker can no longer share the folder or has left, or
// sharing outside the project was switched off, nothing is kept, nothing is counted, and the visitor is answered as the
// link now answers.
func TestALinkChangedWhileAFileArrivesKeepsNothing(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, r, o, v := d.As(bence), d.As(reka), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	ok(t, "Dóra an editor of Bakery", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	_, doras := madeUploadOnly(t, d, o, x.place, x.bakery.ID, false)
	missing := v.Get(t, noSuchLink(t))
	clients := clientsOnStorage(t, d)

	// arriving sends a file through page, all but its last bytes; then, once change has run, the rest. The Drive must not
	// answer before the file has all arrived: it was let in, and is decided on again only then.
	arriving := func(what, page string, change, undo func()) harness.Response {
		t.Helper()
		content := randomBytes(t, 64<<10)
		h := startSend(t, d, page, "Arriving.bin", len(content), content[:len(content)-10])
		time.Sleep(500 * time.Millisecond)
		change()
		if h.answeredWithin(300 * time.Millisecond) {
			t.Fatalf("%s: the Drive answered before the file had all arrived", what)
		}
		res := h.finish(t, content[len(content)-10:])
		undo()
		return res
	}
	gone := func(what string, res harness.Response) {
		t.Helper()
		if res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missing.Body) {
			t.Errorf("%s: got %d %.200s; want the answer of a link that does not work", what, res.Status, res.Body)
		}
		visitorHeaders(t, what, res)
	}

	gone("a file arriving as the link was made a view link", arriving("made a view link", page, func() {
		changedLink(t, "making it a view link", setLinkKind(t, b, x.place, x.clients.ID, "view"))
	}, func() {
		changedLink(t, "making it upload-only", setLinkKind(t, b, x.place, x.clients.ID, "upload-only"))
	}))

	gone("a file arriving as Clients was deleted", arriving("Clients deleted", page, func() { deleted(t, b, x.place, x.clients.ID) },
		func() { restored(t, b, x.place, x.clients.ID) }))

	notAvailable(t, "a file arriving as sharing outside the project was switched off", arriving("sharing outside switched off", page,
		func() { switchedOutside(t, r, false) }, func() { switchedOutside(t, r, true) }))

	gone("a file arriving through Dóra's link as she was made a viewer", arriving("Dóra made a viewer", doras, func() {
		ok(t, "Dóra a viewer of Bakery", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	}, func() { ok(t, "Dóra an editor of Bakery again", share(t, b, x.place, x.bakery.ID, dora, "editor")) }))

	gone("a file arriving as Bence left the project", arriving("Bence left", page, func() {
		d.SetMembers(t, reka, dora)
		harness.Eventually(t, "Bence is gone", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	}, func() {
		d.SetMembers(t, reka, bence, dora)
		harness.Eventually(t, "Bence is back", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	}))

	gone("a file arriving through the link as it was replaced", arriving("replaced", page,
		func() { _, page = madeUploadOnly(t, d, b, x.place, x.clients.ID, true) }, func() {}))

	gone("a file arriving through the link as it was removed", arriving("removed", page, func() {
		if res := removeLink(t, b, x.place, x.clients.ID); res.Status != http.StatusNoContent {
			t.Fatalf("removing the link: %d", res.Status)
		}
	}, func() {}))

	// Nothing was kept, nothing counted, nothing left behind; Dóra's link, which works, took in nothing.
	if got := clientsOnStorage(t, d); !slices.Equal(got, clients) {
		t.Fatalf("Clients on the storage: %q, want %q", got, clients)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Fatalf("Bakery on the storage: %q", got)
	}
	if got := linkOn(t, b, x.place, x.bakery.ID); got == nil || *got.TakenIn != 0 || !got.Works {
		t.Fatalf("Dóra's link on Bakery: %+v", got)
	}
	uploadsEmpty(t, d)
	// And with everything back as it was, a file arriving through Dóra's link is kept.
	if res := arriving("nothing", doras, func() {}, func() {}); res.Status != http.StatusCreated {
		t.Fatalf("a file arriving with nothing changed: %d %s", res.Status, res.Body)
	}
}

// Sharing outside the project switched off (for a shared folder, or for the whole project) stops an upload-only link:
// its page and sending say it is not available, nothing is kept, and it cannot be made, changed or reset there; it
// works again once switched back on. A held My files opens nothing; handed over, its links are gone for good.
func TestAnUploadOnlyLinkStopsWithSharingOutsideAndAHeldMyFiles(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, r, v := d.As(bence), d.As(reka), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))
	inbox := madeSharedFolder(t, b, "Inbox")
	fromClients := madeFolder(t, b, inbox.ID, 0, "From clients")
	other := madeFolder(t, b, inbox.ID, 0, "Other")
	_, page := madeUploadOnly(t, d, b, inbox.ID, fromClients.ID, false)
	sentThrough(t, v, page, "Before.pdf", []byte("before"))

	for _, off := range []struct {
		where              string
		switchOff, onAgain func()
	}{
		{"folder", func() { switchedFolderOutside(t, b, inbox.ID, false) }, func() { switchedFolderOutside(t, b, inbox.ID, true) }},
		{"project", func() { switchedOutside(t, r, false) }, func() { switchedOutside(t, r, true) }},
	} {
		off.switchOff()
		notAvailable(t, "the page, switched off for the "+off.where, v.Get(t, page))
		notAvailable(t, "a file sent, switched off for the "+off.where, sendThrough(t, v, page, "During.pdf", []byte("during")))
		sameAsNoLink(t, "a folder inside it, switched off for the "+off.where, v.Get(t, inLink(page, fromClients.ID, "")), missing)
		if got := linkOn(t, b, inbox.ID, fromClients.ID); got == nil || got.Works {
			t.Fatalf("the link, switched off for the %s: %+v", off.where, got)
		}
		refusedOutside(t, "making an upload-only link, switched off for the "+off.where, makeLinkOfKind(t, b, inbox.ID, other.ID, "upload-only"), off.where)
		refusedOutside(t, "changing its kind, switched off for the "+off.where, setLinkKind(t, b, inbox.ID, fromClients.ID, "view"), off.where)
		refusedOutside(t, "resetting it, switched off for the "+off.where, resetLink(t, b, inbox.ID, fromClients.ID), off.where)
		off.onAgain()
		if p := readSendPage(t, v, page); !p.drop {
			t.Fatalf("the page, switched on again for the %s: %+v", off.where, p)
		}
	}
	if got := onStorage(t, d, "Shared folders/Inbox/From clients"); !slices.Equal(got, []string{"Before.pdf"}) {
		t.Fatalf("From clients on the storage: %q", got)
	}
	if got := linkOn(t, b, inbox.ID, fromClients.ID); *got.TakenIn != 6 || got.Kind != "upload-only" {
		t.Fatalf("the link after sharing outside was switched off and on: %+v", got)
	}
	sentThrough(t, v, page, "After.pdf", []byte("after"))

	// Bence's own upload-only link on Clients, in his My files: held while he is gone, back with him.
	_, clients := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Bence is gone", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	sameAsNoLink(t, "the page of Bence's link, his My files held", v.Get(t, clients), missing)
	sameAsNoLink(t, "a file sent through Bence's link, his My files held", sendThrough(t, v, clients, "Held.pdf", []byte("held")), missing)
	// The shared folder's link is the project's: it works on without him.
	sentThrough(t, v, page, "Without Bence.pdf", []byte("without"))
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Bence is back", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	sentThrough(t, v, clients, "Back.pdf", []byte("back"))

	// Handed over to Dóra: the link is gone for good, and the folder arrives without one.
	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Bence is gone again", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	actDone(t, "handing Bence's files to Dóra", handOver(t, r, x.place, dora))
	sameAsNoLink(t, "the page of Bence's link, handed over", v.Get(t, clients), missing)
	sameAsNoLink(t, "a file sent through Bence's link, handed over", sendThrough(t, v, clients, "Handed.pdf", []byte("handed")), missing)
	doras, _ := myFiles(t, d, dora)
	if got := linkOn(t, d.As(dora), doras, x.clients.ID); got != nil {
		t.Fatalf("Clients, handed to Dóra, has a link: %+v", got)
	}
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Bence is back again", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	sameAsNoLink(t, "the page of Bence's link once he is back", v.Get(t, clients), missing)
}

// Files that came through a link are labelled so in the list, for every member who can see them, as a guest's are
// labelled with their address; a member's upload over one takes the label off. Nothing else is.
func TestFilesThatCameThroughALinkAreLabelled(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, bence, dora)
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	sentThrough(t, v, page, "From outside.pdf", []byte("outside"))
	mine := uploadedInto(t, b, x.place, x.clients.ID, "Mine.pdf", []byte("mine"))

	for who, c := range map[string]*harness.Client{"Bence": b, "Dóra": o} {
		l := list(t, c, x.place, x.clients.ID)
		if got := named(t, l.Items, "From outside.pdf").AddedBy; got == nil || !got.Link || got.Guest != "" {
			t.Errorf("From outside.pdf in %s's list: added by %+v", who, got)
		}
		for _, name := range []string{"Mine.pdf", "Price list.pdf", "Bakery"} {
			if got := named(t, l.Items, name).AddedBy; got != nil {
				t.Errorf("%s in %s's list: added by %+v", name, who, got)
			}
		}
	}
	if found, _, _ := search(t, o, "From outside"); len(found) != 1 || found[0].AddedBy == nil || !found[0].AddedBy.Link {
		t.Errorf("Dóra's search for it: %+v", found)
	}
	// Bence replaces it with his own: the same file, no longer from outside.
	outside := named(t, list(t, b, x.place, x.clients.ID).Items, "From outside.pdf")
	if res := uploadInto(t, b, x.place, x.clients.ID, "From outside.pdf", []byte("checked"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing it: %d %s", res.Status, res.Body)
	}
	if got := named(t, list(t, b, x.place, x.clients.ID).Items, "From outside.pdf"); got.ID != outside.ID || got.AddedBy != nil {
		t.Fatalf("From outside.pdf after Bence replaced it: %+v", got)
	}
	if got := named(t, list(t, b, x.place, x.clients.ID).Items, "Mine.pdf"); got.ID != mine.ID || got.AddedBy != nil {
		t.Fatalf("Mine.pdf: %+v", got)
	}
}

// Strangers sending files take only the few transfer slots outsiders may hold: with all of them taken by files on their
// way, another is told to wait its turn, and keeps its place in line, while members upload and download as before and
// the link's page still opens.
func TestStrangersSendingFilesTakeOnlyTheOutsidersSlots(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c, v := d.As(reka), visitor(d)
	placeID, _ := myFiles(t, d, reka)
	inbox := madeFolder(t, c, placeID, 0, "Inbox")
	notes := uploadedInto(t, c, placeID, 0, "Notes.txt", []byte("notes"))
	_, page := madeUploadOnly(t, d, c, placeID, inbox.ID, false)

	const size = 1 << 20
	var held []*heldSend
	for i := range 8 {
		held = append(held, startSend(t, d, page, fmt.Sprintf("held %d.bin", i), size, make([]byte, 64<<10)))
	}
	time.Sleep(time.Second)
	for i, h := range held {
		if h.answeredWithin(50 * time.Millisecond) {
			res := h.answer(t)
			t.Fatalf("held file %d was answered: %d %s", i, res.Status, res.Body)
		}
	}
	// A ninth: not its turn. It keeps its place.
	turn := newTurn()
	res := v.Do(t, http.MethodPost, sendPath(page, "Ninth.bin", turn), bytes.NewReader([]byte("ninth")))
	if res.Status != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" || !strings.Contains(string(res.Body), `"busy":true`) {
		t.Fatalf("a ninth file while eight are on their way: %d %v %s", res.Status, res.Header, res.Body)
	}
	// Members are not kept waiting, and the page still opens.
	start := time.Now()
	uploadedInto(t, c, placeID, 0, "Member.pdf", []byte("member"))
	if res := c.Get(t, downloadPath(placeID, notes.ID)); res.Status != http.StatusOK || string(res.Body) != "notes" {
		t.Fatalf("a member's download while strangers send: %d", res.Status)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("a member's upload and download took %s while strangers sent files", time.Since(start))
	}
	if p := readSendPage(t, v, page); !p.drop {
		t.Fatalf("the page while eight files arrive: %+v", p)
	}

	for i, h := range held {
		if res := h.finish(t, make([]byte, size-64<<10)); res.Status != http.StatusCreated {
			t.Fatalf("held file %d: %d %s", i, res.Status, res.Body)
		}
	}
	if res := sendWithTurn(t, v, page, "Ninth.bin", []byte("ninth"), turn); res.Status != http.StatusCreated {
		t.Fatalf("the ninth file once its turn came: %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "My files/reka@example.com/Inbox"); len(got) != 9 {
		t.Fatalf("Inbox on the storage: %q", got)
	}
}

// Changing a link's kind is an access change: the copy of the index follows it, and a Drive started from that copy on a
// new server has the link at the same address, of the kind it was changed to.
func TestALinksKindGoesIntoTheCopyOfTheIndex(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: copiesSoon("300ms", "2s")})
	b, v := d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)
	copiesSettled(t, d, 0)
	n := len(copiesMade(d))
	changedLink(t, "making it a view link", setLinkKind(t, b, x.place, x.clients.ID, "view"))
	copiesSettled(t, d, n)

	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	b, v = d.As(bence), visitor(d)
	if p := readPage(t, v, page); p.name != "Clients" || !slices.Equal(p.rows, []string{"Bakery/", "Price list.pdf"}) {
		t.Fatalf("the link's page on the new server: %+v", p)
	}
	changedLink(t, "making it upload-only on the new server", setLinkKind(t, b, x.place, x.clients.ID, "upload-only"))
	sentThrough(t, v, page, "New server.pdf", []byte("new"))
}
