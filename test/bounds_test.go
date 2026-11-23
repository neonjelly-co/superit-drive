package test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// What can be asked of the Drive without limit, and is bounded: lines in its log that anyone can cause, the headers of a
// request, the mails a member has it send, and the searches under way.

// logged is the lines of the Drive's log that hold what.
func logged(d *harness.Drive, what string) []string {
	var lines []string
	for line := range strings.SplitSeq(d.Logs(), "\n") {
		if strings.Contains(line, what) {
			lines = append(lines, line)
		}
	}
	return lines
}

// A change that did not come from the Drive's own pages is refused before anyone is asked who they are, under the
// guests' prefix: anyone can send one, as often as they like, with as long an address and Origin as a request takes.
// The log says so once in a while, with both cut short, however many there were; a member's browser that another
// site's page makes send changes fills the log no faster.
func TestRefusedChangesAreLoggedSeldomAndCutShort(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	const line = "refused a change that did not come from the Drive's own pages"
	long := strings.Repeat("a", 8<<10)
	stranger := d.WithHeaders(http.Header{}).From("https://" + long + ".example")
	for range 40 {
		if res := stranger.Do(t, http.MethodPost, guestAPI+"/code/"+long, strings.NewReader(`{}`)); res.Status != http.StatusForbidden {
			t.Fatalf("a change from another site's page: got %d %s", res.Status, res.Body)
		}
	}
	member := d.As(reka).From("https://" + long + ".example")
	for range 40 {
		if res := member.Do(t, http.MethodPost, "/api/shared-folders/"+long, strings.NewReader(`{}`)); res.Status != http.StatusForbidden {
			t.Fatalf("a member's change from another site's page: got %d %s", res.Status, res.Body)
		}
	}
	lines := logged(d, line)
	if len(lines) != 1 {
		t.Fatalf("80 refused changes within a moment wrote %d lines, want 1", len(lines))
	}
	if len(lines[0]) > 1024 || !strings.Contains(lines[0], "origin=") || !strings.Contains(lines[0], "address=http://drive.test") {
		t.Fatalf("the line is %d bytes, want the Origin and the Drive's address in under 1024:\n%.2000s", len(lines[0]), lines[0])
	}
}

// A guest's address is all it takes to ask for sign-in links for them. While the one they were sent is not used, no
// other is sent so soon, and the log says so once in a while, not for every ask.
func TestLinksNotSentAreLoggedSeldom(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: mailSettings(smtp)})
	x := bencesFiles(t, d)
	invited(t, d.As(bence), x.place, x.bakery.ID, anna, "viewer", nil)
	smtp.WaitFor(t, 1)
	const asked = 12
	for range asked {
		if res := askForCode(t, browser(d), anna); res.Status != http.StatusAccepted {
			t.Fatalf("asking for a link: %d %s", res.Status, res.Body)
		}
		nthCode(t, smtp, anna, 1) // one ask at a time: asks for an address still waiting are one ask
	}
	const line = "a sign-in link was asked for a guest who was sent one lately"
	harness.Eventually(t, "the log says a link was not sent", func() bool { return len(logged(d, line)) > 0 })
	quietly(t, smtp, 2) // the invitation and one link: the asks still waiting have been worked through
	if lines := logged(d, line); len(lines) != 1 {
		t.Fatalf("%d asks after the one that was answered wrote %d lines, want 1:\n%s", asked-1, len(lines), strings.Join(lines, "\n"))
	}
}

// A request's headers are read before anything is known about who sent it. The Drive reads a few tens of kilobytes of
// them and no more.
func TestARequestWithTooManyHeadersIsRefused(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	if res := c.With("X-Padding", strings.Repeat("a", 16<<10)).Get(t, "/api/me"); res.Status != http.StatusOK {
		t.Fatalf("a request with 16 KiB of headers: got %d %s, want 200", res.Status, res.Body)
	}
	for _, size := range []int{64 << 10, 512 << 10} {
		res, err := c.With("X-Padding", strings.Repeat("a", size)).Try(t, http.MethodGet, "/api/me", nil)
		if err == nil && res.Status != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("a request with %d KiB of headers: got %d, want 431", size>>10, res.Status)
		}
		// With an error, the Drive closed the connection before the whole request was sent, which is as good.
	}
	for _, v := range []*harness.Client{visitor(d), browser(d)} {
		res, err := v.With("X-Padding", strings.Repeat("a", 64<<10)).Try(t, http.MethodGet, guestAPI+"/me", nil)
		if err == nil && res.Status != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("a stranger's request with 64 KiB of headers: got %d, want 431", res.Status)
		}
	}
}

// The storage has names of its own that it refuses: Apache will not look at, write or make anything whose name starts
// with ".ht". Such a name is a name the Drive cannot store, and the person who chose it is told to choose another,
// as for any other name that cannot be used: it is not the storage failing, and not an error in the log each time.
func TestANameTheStorageRefusesIsAnUnusableName(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	unusable := func(what string, res harness.Response) {
		t.Helper()
		if message := errorOf(t, res); res.Status != http.StatusBadRequest || !strings.Contains(message, "storage does not take this name") {
			t.Errorf("%s: got %d %s, want 400 and that the storage does not take this name", what, res.Status, res.Body)
		}
	}
	for _, name := range []string{".htaccess", ".htpasswd"} {
		// A member uploads, in one request or in parts, makes a folder and renames.
		unusable("uploading "+name, uploadInto(t, b, x.place, x.bakery.ID, name, []byte("Require all granted"), ""))
		unusable("uploading "+name+" to keep both", uploadInto(t, b, x.place, x.bakery.ID, name, []byte("Require all granted"), "keep-both"))
		unusable("uploading "+name+" to replace", uploadInto(t, b, x.place, x.bakery.ID, name, []byte("Require all granted"), "replace"))
		unusable("starting an upload of "+name, startUpload(t, b, x.place, x.bakery.ID, name, 19, ""))
		unusable("making a folder called "+name, newFolder(t, b, x.place, x.bakery.ID, name))
		unusable("renaming a file "+name, rename(t, b, x.place, x.notes.ID, name))
		unusable("renaming a folder "+name, rename(t, b, x.place, x.private.ID, name))
	}
	unusable("making a shared folder called .htaccess", newSharedFolder(t, d.As(reka), ".htaccess"))

	// A visitor sends one through an upload-only link.
	_, page := madeUploadOnly(t, d, b, x.place, x.bakery.ID, false)
	v := visitor(d)
	for range 5 {
		unusable("sending .htaccess through a link", sendThrough(t, v, page, ".htaccess", []byte("Require all granted")))
	}
	if n := takenIn(t, b, x.place, x.bakery.ID); n != 0 {
		t.Errorf("the link took in %d bytes of files it refused", n)
	}
	sentThrough(t, v, page, "Receipt.pdf", []byte("receipt"))

	// A guest who may add to the folder uploads, makes a folder and renames.
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	g := withCookie(d, signedIn(t, d, smtp, anna)).From(d.Address)
	unusable("a guest uploading .htaccess", guestUpload(t, g, x.place, x.bakery.ID, ".htaccess", []byte("Require all granted"), ""))
	unusable("a guest making a folder called .htaccess",
		sendJSON(t, g, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/folders"), map[string]string{"name": ".htaccess"}))
	unusable("a guest renaming a file .htaccess",
		sendJSON(t, g, http.MethodPost, guestItemPath(x.place, x.offer.ID, "/rename"), map[string]string{"name": ".htaccess"}))

	// Nothing came of any of it, nothing is left of the files, and everything is where it was.
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); fmt.Sprint(got) != "[2026/ Receipt.pdf]" {
		t.Errorf("Bakery on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com"); fmt.Sprint(got) != "[Clients/ Notes.txt Private/]" {
		t.Errorf("Bence's My files on the storage: %q", got)
	}
	harness.Eventually(t, "nothing is left in the uploads folder", func() bool { return len(inUploads(t, d.Storage)) == 0 })
	// The log: no error, and one line, cut short, that says what the storage answered.
	if lines := logged(d, "level=ERROR"); len(lines) != 0 {
		t.Errorf("names the storage refuses were logged as errors:\n%s", strings.Join(lines, "\n"))
	}
	lines := logged(d, "the storage refused a name")
	if len(lines) != 1 || !strings.Contains(lines[0], "403 Forbidden") || !strings.Contains(lines[0], "level=WARN") {
		t.Errorf("the log's lines about the names the storage refused, want one warning with its answer:\n%s", strings.Join(lines, "\n"))
	}
}

// Inviting an address, taking the invitation away and inviting it again mails it each time, with names the member
// chose. A member has the Drive send a fixed number of invitations in an hour; the next is refused, says so, and makes
// no invitation. Another member's count is their own.
func TestAMemberIsMailedAFixedNumberOfInvitationsInAnHour(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	const most = 30
	for i := range most {
		got := invited(t, b, x.place, x.bakery.ID, anna, "viewer", nil)
		if !got.Invited || got.Guest == nil {
			t.Fatalf("invitation %d: %+v", i+1, got)
		}
		// Inviting her again where she is invited already mails nothing, and counts for nothing.
		if again := invited(t, b, x.place, x.bakery.ID, anna, "viewer", nil); again.Invited {
			t.Fatalf("invited twice to the same folder: %+v", again)
		}
		if res := uninvite(t, b, x.place, x.bakery.ID, got.Guest.ID); res.Status != http.StatusOK {
			t.Fatalf("taking invitation %d away: %d %s", i+1, res.Status, res.Body)
		}
	}
	for _, address := range []string{anna, bob} {
		res := invite(t, b, x.place, x.bakery.ID, address, "viewer", nil)
		if message := errorOf(t, res); res.Status != http.StatusTooManyRequests || !strings.Contains(message, "30 times within the last hour") ||
			res.Header.Get("Retry-After") == "" {
			t.Fatalf("invitation %d in an hour, to %s: got %d %s, want 429 that says so", most+1, address, res.Status, res.Body)
		}
	}
	if line := guestLine(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID)), bob); line != nil {
		t.Fatalf("a refused invitation was made all the same: %+v", line)
	}
	quietly(t, smtp, most)
	// It is said in the person's language.
	hu := b.With("Accept-Language", "hu")
	if res := invite(t, hu, x.place, x.bakery.ID, bob, "viewer", nil); res.Status != http.StatusTooManyRequests || !strings.Contains(errorOf(t, res), "30 alkalommal") {
		t.Fatalf("refused in Hungarian: %d %s", res.Status, res.Body)
	}
	// Sharing with a member is not an invitation, and Réka's invitations are counted for her alone.
	if got := invited(t, b, x.place, x.bakery.ID, reka.Email, "viewer", nil); got.Member == nil {
		t.Fatalf("sharing with a member after too many invitations: %+v", got)
	}
	rekasPlace, _ := myFiles(t, d, reka)
	report := uploaded(t, d.As(reka), rekasPlace, "Report.pdf", []byte("report"))
	if got := invited(t, d.As(reka), rekasPlace, report.ID, cleo, "viewer", nil); !got.Invited {
		t.Fatalf("Réka inviting a guest after Bence's too many: %+v", got)
	}
}

// A search that takes longer than a search may is ended, and the person is told so: here every search does, since the
// test gives them no time at all.
func TestASearchThatTakesTooLongIsEndedAndSaysSo(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: map[string]string{"SUPERIT_DRIVE_TEST_SEARCH_MOST": "1ns"}})
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	uploaded(t, c, placeID, "Report.pdf", []byte("x"))
	start := time.Now()
	res := c.Get(t, searchPath("report"))
	if message := errorOf(t, res); res.Status != http.StatusServiceUnavailable || !strings.Contains(message, "took too long") {
		t.Fatalf("a search with no time to run in: got %d %s, want 503 that says it took too long", res.Status, res.Body)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("it was ended after %v", took)
	}
	if lines := logged(d, "level=ERROR"); len(lines) != 0 {
		t.Errorf("a search that was ended is logged as an error:\n%s", strings.Join(lines, "\n"))
	}
	// Everything else is answered as ever.
	if got := list(t, c, placeID, 0); len(got.Items) != 1 {
		t.Errorf("listing My files: %+v", got)
	}
}
