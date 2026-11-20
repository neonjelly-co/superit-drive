package test

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // to alter a copy of the index as someone with the storage login could

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The copy of the index. The Drive keeps a copy of its index on the storage, in its hidden folder, and one in its data
// folder for backups. A Drive started on a new server, with an empty data folder, against a storage that holds files and
// a copy, adopts the copy: places, shares, links, guests, switches and the record of acts are back. These tests stand in
// for a lost server by starting the Drive again with an empty data folder (harness.Drive.OnANewServer), and look only at
// what the Drive answers, at what is on the storage, and at the files in the data folder a backup would archive.

// copiesSoon makes a test build copy its index soon after access changes: once nothing changed for quiet, and at the
// latest latest after a change; a copy the storage did not take is tried again a second later.
func copiesSoon(quiet, latest string) map[string]string {
	return map[string]string{
		"SUPERIT_DRIVE_TEST_COPY_QUIET":  quiet,
		"SUPERIT_DRIVE_TEST_COPY_LATEST": latest,
		"SUPERIT_DRIVE_TEST_COPY_RETRY":  "1s",
	}
}

// with is the settings a, with b's added.
func with(a map[string]string, more ...map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for _, b := range more {
		for k, v := range b {
			out[k] = v
		}
	}
	return out
}

// copyLine is the log line of a copy that went onto the storage.
var copyLine = regexp.MustCompile(`msg="the index was copied into the data folder and onto the storage" why="?([^"=]+?)"? number=(\d+)`)

// copiesMade is how many copies the Drive put on the storage so far, as its log says, and why each was made.
func copiesMade(d *harness.Drive) []string {
	var why []string
	for _, m := range copyLine.FindAllStringSubmatch(d.Logs(), -1) {
		why = append(why, m[1])
	}
	return why
}

// copiesSettled waits until the Drive has put at least one copy on the storage after the first `after` (copiesMade), and
// then none for two seconds: the last copy holds every change made before the wait began.
func copiesSettled(t *testing.T, d *harness.Drive, after int) {
	t.Helper()
	last, since := -1, time.Now()
	harness.EventuallyWithin(t, 30*time.Second, "the copy of the index on the storage is up to date", func() bool {
		n := len(copiesMade(d))
		if n != last {
			last, since = n, time.Now()
		}
		return n > after && time.Since(since) > 2*time.Second
	})
}

// storageID is the id in the Drive's mark on the storage.
func storageID(t *testing.T, d *harness.Drive) string {
	t.Helper()
	mark, ok := d.Storage.Get(t, ".superit-drive/storage-id")
	if !ok {
		t.Fatal("the storage holds no mark")
	}
	return strings.TrimSpace(string(mark))
}

// storedCopies lists the files in the folder of the copies of the index of the storage's id.
func storedCopies(t *testing.T, d *harness.Drive) []string {
	t.Helper()
	entries, _ := d.Storage.List(t, ".superit-drive/index/"+storageID(t, d))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	return names
}

// storedCopy is the path, on the storage, of the latest copy of the index, and its number.
func storedCopy(t *testing.T, d *harness.Drive) (string, int64) {
	t.Helper()
	var best string
	var seq int64 = -1
	for _, name := range storedCopies(t, d) {
		var n int64
		if _, err := fmt.Sscanf(name, "copy-%d.sqlite", &n); err == nil && strings.HasSuffix(name, ".sqlite") && n > seq {
			best, seq = name, n
		}
	}
	if seq < 0 {
		t.Fatalf("the storage holds no copy of the index: %q", storedCopies(t, d))
	}
	return ".superit-drive/index/" + storageID(t, d) + "/" + best, seq
}

// alterStoredCopy changes the latest copy on the storage, as someone with the storage login could: it downloads it,
// runs the statements on it, and writes it back in its place.
func alterStoredCopy(t *testing.T, d *harness.Drive, statements ...string) {
	t.Helper()
	at, _ := storedCopy(t, d)
	data, ok := d.Storage.Get(t, at)
	if !ok {
		t.Fatalf("no copy at %s", at)
	}
	file := filepath.Join(t.TempDir(), "copy.sqlite")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			t.Fatalf("%s: %v", s, err)
		}
	}
	db.Close()
	data, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	d.Storage.Replace(t, at, data)
}

// adopted waits until the Drive says it made its index from a copy (from says which: "storage" or "data folder").
func adopted(t *testing.T, d *harness.Drive, from string) {
	t.Helper()
	want := map[string]string{"storage": "the Drive made it from the copy on the storage",
		"data folder": "the Drive made it from the data folder's copy"}[from]
	harness.Eventually(t, "the log says the index was made from "+from, func() bool { return strings.Contains(d.Logs(), want) })
}

// A server lost: the Drive starts on a new one, with an empty data folder and a new header secret, against the same
// storage, which holds the files and the copy of the index the Drive wrote after its last access change. Everything is
// back: the places, what was shared with members and with everyone, the public link (the same address opens the same
// file), the guest's invitation (they sign in again, with a new code, and see what was shared with them), the shared
// folder with its managers and its switch of sharing outside the project, and the record of a held My files handed
// over. Guests' sessions are not: the guest's old cookie lets nobody in.
func TestALostServerComesBackFromTheCopyOnTheStorage(t *testing.T) {
	t.Parallel()
	erik := harness.Member{UserID: "e7aa-erik", Email: "erik@example.com", DisplayName: "Erik Szabó", Role: "member"}
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora, erik},
		Env: with(mailSettings(smtp), copiesSoon("300ms", "2s"), fastWalk("1h"))})
	r, b, o := d.As(reka), d.As(bence), d.As(dora)
	missing := visitor(d).Get(t, noSuchLink(t))

	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Notes.txt with everyone", shareWithEveryone(t, b, x.place, x.notes.ID, "viewer"))
	_, notesPage := madeLink(t, d, b, x.place, x.notes.ID, false)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	annaCookie := signedIn(t, d, smtp, anna)

	payroll := madeSharedFolder(t, r, "Payroll")
	march := uploadedInto(t, r, payroll.ID, 0, "March.pdf", []byte("march"))
	ok(t, "giving Dóra Payroll", sendJSON(t, r, http.MethodPut, accessPath(payroll.ID, dora), map[string]string{"role": "viewer"}))
	ok(t, "making Bence a manager of Payroll", makeManager(t, r, payroll.ID, bence))
	switchedFolderOutside(t, r, payroll.ID, false)

	erikPlace, _ := myFiles(t, d, erik)
	uploadedInto(t, d.As(erik), erikPlace, 0, "Handover.txt", []byte("erik's"))
	d.SetMembers(t, reka, bence, dora)
	removed(t, d, erik)
	act := actDone(t, "handing Erik's files over to Dóra", handOver(t, r, erikPlace, dora))

	before := len(copiesMade(d))
	ok(t, "sharing Secret.txt with Dóra, last", share(t, b, x.place, x.secret.ID, dora, "viewer"))
	copiesSettled(t, d, before)
	if got := storedCopies(t, d); len(got) != 1 || !strings.HasPrefix(got[0], "copy-") {
		t.Fatalf("the copies' folder on the storage holds %q, want one copy", got)
	}

	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	r, b, o = d.As(reka), d.As(bence), d.As(dora)

	// Places, and what was shared with members and with everyone.
	if got, _ := myFiles(t, d, bence); got != x.place {
		t.Fatalf("Bence's My files is place %d, was %d", got, x.place)
	}
	if got, want := sorted(sharedNames(t, o)), sorted([]string{entry("Clients", x.place), entry("Notes.txt", x.place), entry("Secret.txt", x.place)}); !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me on the new server: %q, want %q", got, want)
	}
	if got := contentOf(t, o, x.place, x.prices.ID); got != "prices" {
		t.Fatalf("Dóra downloading Price list.pdf in Clients: %s", got)
	}
	if got := contentOf(t, d.As(reka), x.place, x.notes.ID); got != "notes" {
		t.Fatalf("Réka downloading Notes.txt, shared with everyone: %s", got)
	}
	// The public link: the same address opens the same file.
	if p := readPage(t, visitor(d), notesPage); p.name != "Notes.txt" {
		t.Fatalf("the link on Notes.txt on the new server: %+v", p)
	}
	if res := visitor(d).Get(t, inLink(notesPage, x.notes.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "notes" {
		t.Fatalf("downloading through the link: %d %s", res.Status, res.Body)
	}
	sameAsNoLink(t, "a link that never existed", visitor(d).Get(t, noSuchLink(t)), missing)
	// The guest: their old session is gone; they sign in again with a new code and see what was shared with them.
	if who := signedInAs(t, d, annaCookie); who != "" {
		t.Fatalf("the guest's session from the lost server still works, as %s", who)
	}
	annaCookie = signedIn(t, d, smtp, anna)
	if items, _ := guestShared(t, withCookie(d, annaCookie)); len(items) != 1 || items[0].ID != x.bakery.ID || items[0].Role != "editor" {
		t.Fatalf("Anna's Shared with me on the new server: %+v", items)
	}
	// The shared folder, its managers, its switch.
	if got := managers(whoHasAccess(t, r, payroll.ID)); !slices.Contains(got, bence.UserID+" manager (given)") ||
		!slices.Contains(got, dora.UserID+" viewer (given)") {
		t.Fatalf("Payroll's managers on the new server: %q", got)
	}
	if got := contentOf(t, o, payroll.ID, march.ID); got != "march" {
		t.Fatalf("Dóra downloading March.pdf: %s", got)
	}
	if p := placeOutside(t, b, payroll.ID); p.Outside != "folder" || p.SharingOutside == nil || p.SharingOutside.On {
		t.Fatalf("sharing outside Payroll on the new server: %+v", p)
	}
	// The record of the hand-over, and what was handed over.
	if _, acts := heldList(t, r); len(acts) != 1 || acts[0].ID != act.ID || acts[0].Kind != "hand-over" || acts[0].Into != act.Into {
		t.Fatalf("the record of acts on the new server: %+v", acts)
	}
	doraPlace, _ := myFiles(t, d, dora)
	if got := contentOf(t, o, doraPlace, pathIn(t, o, doraPlace, act.Into, "Handover.txt").ID); got != "erik's" {
		t.Fatalf("Dóra downloading Erik's file, handed over to her: %s", got)
	}

	// The new server keeps copies of its own: one in its data folder, and the next one on the storage replaces the one it
	// was made from.
	if data, err := os.ReadFile(filepath.Join(d.DataDir, "backup", "index.sqlite")); err != nil || !bytes.HasPrefix(data, []byte("SQLite format 3\x00")) {
		t.Fatalf("the new server's data folder holds no copy of the index (%v)", err)
	}
	harness.Eventually(t, "the walk after the index was made from its copy has ended", func() bool {
		return strings.Contains(d.Logs(), "the index made from a copy is in step with the storage")
	})
}

var _ = strconv.Itoa
