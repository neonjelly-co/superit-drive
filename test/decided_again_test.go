package test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// Transfers decided again: a download or a zip when its turn for a slot comes, a zip as it goes on, an upload in one
// request once its file is whole; and what an upload holds, or may have in progress, while its bytes arrive.

// asMember and asGuest are the head lines that say who sends a request made by hand: a member, as the reverse proxy
// vouches for them, or a guest, by their session's cookie; both from the Drive's own page.
func asMember(d *harness.Drive, m harness.Member) string {
	return fmt.Sprintf("Drive-Secret: %s\r\nRemote-User-Id: %s\r\nOrigin: %s\r\n", d.Secret, m.UserID, d.Address)
}

func asGuest(d *harness.Drive, c *http.Cookie) string {
	return fmt.Sprintf("Cookie: %s=%s\r\nOrigin: %s\r\n", c.Name, c.Value, d.Address)
}

// trickling starts an upload in one request by hand, as who says (asMember, asGuest), to path: its head and all but the
// last ten bytes of content at once. Its rest is sent when the test says (finish).
func trickling(t *testing.T, d *harness.Drive, who, path string, content []byte) *heldSend {
	t.Helper()
	return startSendWithHead(t, d, path, who+fmt.Sprintf("Content-Length: %d\r\n", len(content)), content[:len(content)-10])
}

// memberUploadPath and guestUploadPath are where an upload in one request of a file called name into a folder goes.
func memberUploadPath(placeID, folderID int64, name string) string {
	return fmt.Sprintf("/api/places/%d/items/%d/files?name=%s", placeID, folderID, url.QueryEscape(name))
}

func guestUploadPath(placeID, folderID int64, name string) string {
	return guestItemPath(placeID, folderID, "/files?name="+url.QueryEscape(name))
}

// A download, a file opened or a zip that waited for its turn is decided again when the turn comes: through a link
// removed meanwhile, or by a guest whose invitation was taken away meanwhile, it is refused as what is not there, and
// sends nothing. One that may still be downloaded goes on.
func TestATransferThatWaitedIsDecidedAgainWhenItsTurnComes(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: mailSettings(smtp)})
	placeID, _ := myFiles(t, d, reka)
	r := d.As(reka)
	films := madeFolder(t, r, placeID, 0, "Films")
	big := uploadedInto(t, r, placeID, films.ID, "Big.mp4", randomBytes(t, 1<<20))
	other := madeFolder(t, r, placeID, 0, "Other")
	held := uploadedInto(t, r, placeID, other.ID, "Held.mp4", randomBytes(t, 16<<20)) // more than the network buffers on the way hold
	_, page := madeLink(t, d, r, placeID, films.ID, false)
	_, others := madeLink(t, d, r, placeID, other.ID, false)
	invitation := invited(t, r, placeID, films.ID, anna, "viewer", nil)
	invited(t, r, placeID, other.ID, anna, "viewer", nil) // her session goes on when Films is taken from her
	cookie := signedIn(t, d, smtp, anna)

	// Visitors through the other link hold the outsiders' eight slots.
	var holding []net.Conn
	for i := range 8 {
		conn, status := heldVisitorDownload(t, d, inLink(others, held.ID, "/download"), 5*time.Second)
		if status != http.StatusOK {
			t.Fatalf("outsider's transfer %d: got %d", i+1, status)
		}
		holding = append(holding, conn)
	}
	type waiter struct {
		what, who, path string
		want            int
		status          <-chan int
	}
	waiters := []*waiter{
		{what: "a visitor's download", path: inLink(page, big.ID, "/download"), want: http.StatusNotFound},
		{what: "a visitor's file opened", path: inLink(page, big.ID, "/open"), want: http.StatusNotFound},
		{what: "a visitor's zip", path: inLink(page, films.ID, "/zip"), want: http.StatusNotFound},
		{what: "a guest's download", who: asGuest(d, cookie), path: guestItemPath(placeID, big.ID, "/download"), want: http.StatusNotFound},
		{what: "a guest's file opened", who: asGuest(d, cookie), path: guestItemPath(placeID, big.ID, "/open"), want: http.StatusNotFound},
		{what: "a guest's zip", who: asGuest(d, cookie), path: guestItemPath(placeID, films.ID, "/zip"), want: http.StatusNotFound},
		{what: "a visitor's download through the link that works", path: inLink(others, held.ID, "/download"), want: http.StatusOK},
		{what: "the guest's download of what is still hers", who: asGuest(d, cookie), path: guestItemPath(placeID, held.ID, "/download"), want: http.StatusOK},
	}
	for _, w := range waiters {
		w.status, _ = waitingFor(func() (net.Conn, int) { return heldRequest(t, d, w.who, w.path, time.Minute) })
	}
	time.Sleep(time.Second)
	for _, w := range waiters {
		stillWaiting(t, w.what, w.status)
	}

	// While they wait, the link is removed and the invitation taken away. Then slots come free.
	if res := removeLink(t, r, placeID, films.ID); res.Status != http.StatusNoContent {
		t.Fatalf("removing the link: %d %s", res.Status, res.Body)
	}
	if res := uninvite(t, r, placeID, films.ID, invitation.Guest.ID); res.Status != http.StatusOK {
		t.Fatalf("taking Anna's invitation to Films away: %d %s", res.Status, res.Body)
	}
	for _, conn := range holding {
		conn.Close()
	}
	for _, w := range waiters {
		if got := <-w.status; got != w.want {
			t.Errorf("%s, once its turn came: got %d, want %d", w.what, got, w.want)
		}
	}
}

// startedZip asks for a zip by hand, as who says ("" for a visitor), and reads only its answer's head: the zip is on
// its way, and goes no further than the network's buffers until its body is read.
func startedZip(t *testing.T, d *harness.Drive, who, path string) *http.Response {
	t.Helper()
	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", path, address.Host, who)
	_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a zip asked for by hand: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the zip at %s: got %d", path, res.StatusCode)
	}
	return res
}

// A zip on its way is decided again before each thing it adds: once its link is removed, or its guest's invitation
// taken away, it is cut short there, and what was put into its folders after that is never in it.
func TestARunningZipStopsWhenItsLinkOrInvitationStops(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: mailSettings(smtp)})
	placeID, _ := myFiles(t, d, reka)
	r := d.As(reka)
	films := madeFolder(t, r, placeID, 0, "Films")
	first := madeFolder(t, r, placeID, films.ID, "A first")
	uploadedInto(t, r, placeID, first.ID, "Big.mp4", randomBytes(t, 16<<20)) // more than the network buffers on the way hold
	later := madeFolder(t, r, placeID, films.ID, "B later")
	uploadedInto(t, r, placeID, later.ID, "There before.txt", []byte("there before"))
	_, page := madeLink(t, d, r, placeID, films.ID, false)
	invitation := invited(t, r, placeID, films.ID, anna, "viewer", nil)
	cookie := signedIn(t, d, smtp, anna)

	// Whole, the zip holds both folders.
	if res := visitor(d).Get(t, inLink(page, films.ID, "/zip")); res.Status != http.StatusOK ||
		!slices.Equal(keys(zipEntries(t, res.Body)), []string{"Films/", "Films/A first/", "Films/A first/Big.mp4", "Films/B later/", "Films/B later/There before.txt"}) {
		t.Fatalf("the zip through the link: %d, %q", res.Status, keys(zipEntries(t, res.Body)))
	}

	for _, c := range []struct {
		what, who, path, added string
		stop                   func()
	}{
		{what: "a visitor's zip whose link was removed", path: inLink(page, films.ID, "/zip"), added: "Added after the link.txt", stop: func() {
			if res := removeLink(t, r, placeID, films.ID); res.Status != http.StatusNoContent {
				t.Fatalf("removing the link: %d %s", res.Status, res.Body)
			}
		}},
		{what: "a guest's zip whose invitation was taken away", who: asGuest(d, cookie), path: guestItemPath(placeID, films.ID, "/zip"),
			added: "Added after the invitation.txt", stop: func() {
				if res := uninvite(t, r, placeID, films.ID, invitation.Guest.ID); res.Status != http.StatusOK {
					t.Fatalf("taking Anna's invitation away: %d %s", res.Status, res.Body)
				}
			}},
	} {
		res := startedZip(t, d, c.who, c.path)
		time.Sleep(500 * time.Millisecond) // it is sending Big.mp4, as far as the buffers take it
		c.stop()
		uploadedInto(t, r, placeID, later.ID, c.added, []byte("added after"))
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err == nil {
			t.Errorf("%s ended as a whole zip (%d bytes)", c.what, len(body))
		}
		if !bytes.Contains(body, []byte("Big.mp4")) {
			t.Errorf("%s does not hold the file it was sending", c.what)
		}
		for _, name := range []string{c.added, "B later", "There before.txt"} {
			if bytes.Contains(body, []byte(name)) {
				t.Errorf("%s holds %q, which it reached after", c.what, name)
			}
		}
	}
	if !strings.Contains(d.Logs(), "a zip ended before its end") {
		t.Errorf("the Drive did not say that a zip was cut short:\n%s", d.Logs())
	}
}

// An upload in one request holds nothing in its place while its bytes arrive, however slowly: the folders above where
// it goes are renamed and moved meanwhile, and the file, once whole, goes into its folder where that is now. And the
// shared folder is deleted at once; the files, whole after that, are not stored. A member's, a guest's, and a file a
// visitor sends through an upload-only link.
func TestAnUploadThatTricklesHoldsNothingInItsPlace(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	clients := madeSharedFolder(t, b, "Clients")
	projects := madeFolder(t, b, clients.ID, 0, "Projects")
	bakery := madeFolder(t, b, clients.ID, projects.ID, "Bakery")
	archive := madeFolder(t, b, clients.ID, projects.ID, "Archive")
	uploadedInto(t, b, clients.ID, archive.ID, "Here.txt", []byte("here")) // the first upload makes the uploads folder
	invited(t, b, clients.ID, projects.ID, anna, "editor", nil)
	cookie := signedIn(t, d, smtp, anna)
	_, page := madeUploadOnly(t, d, b, clients.ID, bakery.ID, false)

	content := randomBytes(t, 64<<10)
	arriving := func(round string) map[string]*heldSend {
		t.Helper()
		sends := map[string]*heldSend{
			"a member's upload":               trickling(t, d, asMember(d, bence), memberUploadPath(clients.ID, bakery.ID, "Member "+round+".bin"), content),
			"a guest's upload":                trickling(t, d, asGuest(d, cookie), guestUploadPath(clients.ID, bakery.ID, "Guest "+round+".bin"), content),
			"a visitor's file through a link": trickling(t, d, "", sendPath(page, "Visitor "+round+".bin", newTurn()), content),
		}
		time.Sleep(500 * time.Millisecond) // each is on its way to the storage
		return sends
	}
	finished := func(sends map[string]*heldSend, when string, want int) {
		t.Helper()
		for what, h := range sends {
			if h.answeredWithin(100 * time.Millisecond) {
				t.Errorf("%s was answered before its last bytes arrived", what)
			}
			if res := h.finish(t, content[len(content)-10:]); res.Status != want {
				t.Errorf("%s, whole %s: got %d %s, want %d", what, when, res.Status, res.Body, want)
			}
		}
	}

	// The folder above is renamed, and the folder itself moved, while the files arrive.
	sends := arriving("one")
	if res := rename(t, b, clients.ID, projects.ID, "Work"); res.Status != http.StatusOK {
		t.Fatalf("renaming the folder above, while uploads into it arrive: got %d %s", res.Status, res.Body)
	}
	if res := move(t, b, clients.ID, bakery.ID, archive.ID); res.Status != http.StatusOK {
		t.Fatalf("moving the folder, while uploads into it arrive: got %d %s", res.Status, res.Body)
	}
	finished(sends, "after its folder was renamed and moved", http.StatusCreated)
	if got := onStorage(t, d, "Shared folders/Clients/Work/Archive/Bakery"); !slices.Equal(got, []string{"Guest one.bin", "Member one.bin", "Visitor one.bin"}) {
		t.Fatalf("the folder where it is now holds %q", got)
	}
	if got := onStorage(t, d, "Shared folders/Clients"); !slices.Equal(got, []string{"Work/"}) {
		t.Fatalf("the shared folder holds %q", got)
	}

	// The shared folder is deleted while the files arrive: at once.
	sends = arriving("two")
	start := time.Now()
	if res := deleteSharedFolder(t, b, clients.ID); res.Status != http.StatusOK {
		t.Fatalf("deleting the shared folder, while uploads into it arrive: got %d %s", res.Status, res.Body)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("deleting the shared folder waited %s for uploads that were still arriving", took)
	}
	finished(sends, "after its shared folder was deleted", http.StatusNotFound)
	if _, ok := d.Storage.List(t, "Shared folders/Clients"); ok {
		t.Fatal("an upload made the shared folder's top folder again")
	}
	entries := onStorage(t, d, ".superit-drive/trash/Shared folders/Clients")
	if len(entries) != 1 {
		t.Fatalf("the shared folder's trash holds %q", entries)
	}
	if got := onStorage(t, d, ".superit-drive/trash/Shared folders/Clients/"+entries[0]+"Clients/Work/Archive/Bakery"); !slices.Equal(got, []string{"Guest one.bin", "Member one.bin", "Visitor one.bin"}) {
		t.Fatalf("the folder in the trash holds %q", got)
	}
	uploadsEmpty(t, d)
}

// The My files of someone who left is handed over while a guest's upload into it, and a visitor's file through an
// upload-only link on it, still arrive: neither holds it. Whole after that, they are not stored.
func TestAHeldMyFilesIsHandedOverWhileAnUploadIntoItTrickles(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	invited(t, b, x.place, x.clients.ID, anna, "editor", nil)
	cookie := signedIn(t, d, smtp, anna)
	_, page := madeUploadOnly(t, d, b, x.place, x.bakery.ID, false)

	content := randomBytes(t, 64<<10)
	sends := map[string]*heldSend{
		"a guest's upload":                trickling(t, d, asGuest(d, cookie), guestUploadPath(x.place, x.clients.ID, "Guest.bin"), content),
		"a visitor's file through a link": trickling(t, d, "", sendPath(page, "Visitor.bin", newTurn()), content),
	}
	time.Sleep(500 * time.Millisecond) // each is on its way to the storage
	d.SetMembers(t, reka, dora)
	removed(t, d, bence)
	start := time.Now()
	actDone(t, "handing Bence's files to Dóra while uploads into them arrive", handOver(t, d.As(reka), x.place, dora))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("handing over waited %s for uploads that were still arriving", took)
	}
	for what, h := range sends {
		if res := h.finish(t, content[len(content)-10:]); res.Status != http.StatusNotFound {
			t.Errorf("%s, whole after the files were handed over: got %d %s, want 404", what, res.Status, res.Body)
		}
	}
	uploadsEmpty(t, d)
	if strings.Contains(d.Logs(), "Guest.bin") || strings.Contains(d.Logs(), "Visitor.bin") {
		t.Errorf("the Drive's log names a file that was not stored:\n%s", d.Logs())
	}
}

// An upload in one request is decided again once its file is whole: a guest whose invitation was taken away or made a
// viewer's meanwhile, a member whose access was taken away or made a viewer's, anyone whose folder was deleted, stores
// nothing, and is answered as what is not there; a guest where sharing outside the project was switched off, that it is
// not available now. (A file sent through an upload-only link: TestALinkChangedWhileAFileArrivesKeepsNothing.)
func TestAFileWhoseUploaderLostAccessWhileItArrivedIsNotStored(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, r := d.As(bence), d.As(reka)
	x := bencesFiles(t, d)
	ok(t, "Dóra an editor of Bakery", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	guest := invited(t, b, x.place, x.bakery.ID, anna, "editor", nil).Guest.ID
	invited(t, b, x.place, x.prices.ID, anna, "viewer", nil) // her session goes on when Bakery is taken from her
	cookie := signedIn(t, d, smtp, anna)

	content := randomBytes(t, 64<<10)
	// arriving sends a file into Bakery as who says, all but its last bytes; then, once change has run, the rest. The
	// Drive must not answer before the file has all arrived: it was let in, and is decided on again only then.
	arriving := func(what, who, path string, change, undo func()) harness.Response {
		t.Helper()
		h := trickling(t, d, who, path, content)
		time.Sleep(500 * time.Millisecond)
		change()
		if h.answeredWithin(300 * time.Millisecond) {
			t.Fatalf("%s: the Drive answered before the file had all arrived", what)
		}
		res := h.finish(t, content[len(content)-10:])
		undo()
		return res
	}
	asAnna := func(what string, change, undo func()) harness.Response {
		t.Helper()
		return arriving(what, asGuest(d, cookie), guestUploadPath(x.place, x.bakery.ID, "Annas.bin"), change, undo)
	}
	asDora := func(what string, change, undo func()) harness.Response {
		t.Helper()
		return arriving(what, asMember(d, dora), memberUploadPath(x.place, x.bakery.ID, "Doras.bin"), change, undo)
	}
	gone := func(what string, res harness.Response) {
		t.Helper()
		if res.Status != http.StatusNotFound || !strings.Contains(errorOf(t, res), "This is not in the Drive, or you have no access to it.") {
			t.Errorf("%s: got %d %s; want 404, as what is not there", what, res.Status, res.Body)
		}
	}
	deleteBakery, restoreBakery := func() { deleted(t, b, x.place, x.bakery.ID) }, func() { restored(t, b, x.place, x.bakery.ID) }

	gone("Anna's file arriving as her invitation was made a viewer's", asAnna("made a viewer", func() {
		ok(t, "making Anna a viewer", changeInvitation(t, b, x.place, x.bakery.ID, guest, "viewer", nil))
	}, func() {
		ok(t, "making Anna an editor again", changeInvitation(t, b, x.place, x.bakery.ID, guest, "editor", nil))
	}))

	gone("Anna's file arriving as Bakery was deleted", asAnna("Bakery deleted", deleteBakery, restoreBakery))

	unavailableToGuest(t, "Anna's file arriving as sharing outside the project was switched off", asAnna("sharing outside switched off",
		func() { switchedOutside(t, r, false) }, func() { switchedOutside(t, r, true) }))

	gone("Anna's file arriving as her invitation was taken away", asAnna("uninvited", func() {
		ok(t, "taking Anna's invitation away", uninvite(t, b, x.place, x.bakery.ID, guest))
	}, func() { guest = invited(t, b, x.place, x.bakery.ID, anna, "editor", nil).Guest.ID }))

	gone("Dóra's file arriving as she was made a viewer", asDora("made a viewer", func() {
		ok(t, "Dóra a viewer of Bakery", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	}, func() { ok(t, "Dóra an editor of Bakery again", share(t, b, x.place, x.bakery.ID, dora, "editor")) }))

	gone("Dóra's file arriving as Bakery was deleted", asDora("Bakery deleted", deleteBakery, restoreBakery))

	gone("Dóra's file arriving as her access was taken away", asDora("unshared", func() {
		if res := unshare(t, b, x.place, x.bakery.ID, dora); res.Status != http.StatusOK && res.Status != http.StatusNoContent {
			t.Fatalf("taking Bakery from Dóra: %d %s", res.Status, res.Body)
		}
	}, func() { ok(t, "Dóra an editor of Bakery again", share(t, b, x.place, x.bakery.ID, dora, "editor")) }))

	// Nothing was stored, and nothing left behind.
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Fatalf("Bakery on the storage: %q", got)
	}
	uploadsEmpty(t, d)
	// And with nothing changed meanwhile, a file that arrives is stored.
	nothing := func() {}
	if res := asAnna("nothing", nothing, nothing); res.Status != http.StatusCreated {
		t.Fatalf("Anna's file arriving with nothing changed: %d %s", res.Status, res.Body)
	}
	if res := asDora("nothing", nothing, nothing); res.Status != http.StatusCreated {
		t.Fatalf("Dóra's file arriving with nothing changed: %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/", "Annas.bin", "Doras.bin"}) {
		t.Fatalf("Bakery on the storage, in the end: %q", got)
	}
}

// uploadsInProgress is the most resumable uploads a person may have in progress at once.
const uploadsInProgress = 50

// A person has at most 50 resumable uploads in progress, in every place together: one more is refused, with a message
// that says so, whether they are a member or a guest, and nothing is made for it on the storage. One of theirs
// cancelled makes room for another; nobody else's count against them. A day after nothing more arrived, all are
// removed, however many there are, and uploads start again.
func TestAPersonHasAtMostFiftyUploadsInProgress(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	shared := madeSharedFolder(t, b, "Shared")
	dorasFiles, _ := myFiles(t, d, dora)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	g := withCookie(d, signedIn(t, d, smtp, anna))

	guestStart := func(name string) harness.Response {
		q := url.Values{"name": {name}, "size": {strconv.Itoa(1 << 20)}}
		return g.Do(t, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/uploads?"+q.Encode()), nil)
	}
	tooMany := func(what string, res harness.Response) {
		t.Helper()
		if res.Status != http.StatusTooManyRequests || !strings.Contains(errorOf(t, res), "You have 50 uploads in progress, and that is the most there can be at once.") {
			t.Fatalf("%s: got %d %s; want 429, saying that 50 are in progress", what, res.Status, res.Body)
		}
	}

	var bences []sessionJSON
	for i := range uploadsInProgress {
		// Bence's are in two places: they count together.
		place, folder := x.place, x.clients.ID
		if i%2 == 1 {
			place, folder = shared.ID, 0
		}
		bences = append(bences, startedUpload(t, b, place, folder, fmt.Sprintf("Bence %d.bin", i), 1<<20))
		startedUpload(t, o, dorasFiles, 0, fmt.Sprintf("Dóra %d.bin", i), 1<<20)
		if res := guestStart(fmt.Sprintf("Anna %d.bin", i)); res.Status != http.StatusCreated {
			t.Fatalf("Anna's upload %d: got %d %s", i+1, res.Status, res.Body)
		}
	}
	if got := len(inUploads(t, d.Storage)); got != 3*uploadsInProgress {
		t.Fatalf("the uploads folder holds %d files, want %d", got, 3*uploadsInProgress)
	}
	tooMany("Bence's 51st upload", startUpload(t, b, x.place, 0, "One more.bin", 1<<20, ""))
	tooMany("Bence's 51st upload, in the shared folder", startUpload(t, b, shared.ID, 0, "One more.bin", 1<<20, ""))
	tooMany("Dóra's 51st upload", startUpload(t, o, dorasFiles, 0, "One more.bin", 1<<20, ""))
	tooMany("Anna's 51st upload", guestStart("One more.bin"))
	if res := g.With("Accept-Language", "hu").Do(t, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/uploads?name=x.bin&size=10"), nil); res.Status != http.StatusTooManyRequests || !strings.Contains(errorOf(t, res), "egyszerre ennél több nem lehet") {
		t.Fatalf("Anna's 51st upload, in Hungarian: got %d %s", res.Status, res.Body)
	}
	if got := len(inUploads(t, d.Storage)); got != 3*uploadsInProgress {
		t.Fatalf("after the refused ones, the uploads folder holds %d files, want %d", got, 3*uploadsInProgress)
	}
	// An upload in one request is not one in progress: it goes on.
	uploadedInto(t, b, x.place, 0, "In one request.txt", []byte("small"))
	// One of Bence's cancelled makes room for one of his, and for nobody else's.
	if res := b.Do(t, http.MethodDelete, sessionPath(x.place, bences[0].ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("cancelling one of Bence's uploads: got %d %s", res.Status, res.Body)
	}
	tooMany("Dóra's 51st upload, after Bence cancelled one", startUpload(t, o, dorasFiles, 0, "One more.bin", 1<<20, ""))
	startedUpload(t, b, shared.ID, 0, "Room for one.bin", 1<<20)
	tooMany("Bence's 51st upload, again", startUpload(t, b, x.place, 0, "One more.bin", 1<<20, ""))

	// A day later every one of them is removed: more than the Drive reads at a time.
	d.Restart(t, map[string]string{harness.TestClockShift: "25h"})
	harness.Eventually(t, "the uploads folder is emptied", func() bool { return len(inUploads(t, d.Storage)) == 0 })
	startedUpload(t, d.As(bence), x.place, 0, "The day after.bin", 1<<20)
	startedUpload(t, d.As(dora), dorasFiles, 0, "The day after.bin", 1<<20)
}
