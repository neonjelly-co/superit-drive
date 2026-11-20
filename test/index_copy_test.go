package test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// then none for four seconds (longer than any test waits for quiet): the last copy holds every change made before the
// wait began.
func copiesSettled(t *testing.T, d *harness.Drive, after int) {
	t.Helper()
	last, since := -1, time.Now()
	harness.EventuallyWithin(t, 30*time.Second, "the copy of the index on the storage is up to date", func() bool {
		n := len(copiesMade(d))
		if n != last {
			last, since = n, time.Now()
		}
		return n > after && time.Since(since) > 4*time.Second
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
// lets change alter it, and writes it back in its place.
func alterStoredCopy(t *testing.T, d *harness.Drive, change func(db *sql.DB)) {
	t.Helper()
	at, _ := storedCopy(t, d)
	data, ok := d.Storage.Get(t, at)
	if !ok {
		t.Fatalf("no copy at %s", at)
	}
	d.Storage.Replace(t, at, altered(t, data, change))
}

// altered is data, a copy of the index, as change leaves it: changed as someone holding the file could change it.
func altered(t *testing.T, data []byte, change func(db *sql.DB)) []byte {
	t.Helper()
	file := filepath.Join(t.TempDir(), "copy.sqlite")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	change(db)
	db.Close()
	if data, err = os.ReadFile(file); err != nil {
		t.Fatal(err)
	}
	return data
}

// putBack replaces the stopped Drive's whole data folder with one that holds only backup, as its copy of the index: a
// backup or a snapshot of the data folder put back, which never holds the live index.
func putBack(t *testing.T, d *harness.Drive, backup []byte) {
	t.Helper()
	if err := os.RemoveAll(d.DataDir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(d.DataDir, "backup", "index.sqlite")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, backup, 0o600); err != nil {
		t.Fatal(err)
	}
}

// run runs a statement on a copy, and fails the test when it fails.
func run(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
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

// sharedBeforeTheCopy is Bence's files with Clients shared with Dóra and a public link on Notes.txt, and Réka's shared
// folder Payroll, given to Dóra, all of it in the copy of the index on the storage. It returns the link's page.
func sharedBeforeTheCopy(t *testing.T, d *harness.Drive) (x bences, payroll placeJSON, notesPage string) {
	t.Helper()
	b, r := d.As(bence), d.As(reka)
	x = bencesFiles(t, d)
	before := len(copiesMade(d))
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	_, notesPage = madeLink(t, d, b, x.place, x.notes.ID, false)
	payroll = madeSharedFolder(t, r, "Payroll")
	uploadedInto(t, r, payroll.ID, 0, "March.pdf", []byte("march"))
	ok(t, "giving Dóra Payroll", sendJSON(t, r, http.MethodPut, accessPath(payroll.ID, dora), map[string]string{"role": "viewer"}))
	copiesSettled(t, d, before)
	return x, payroll, notesPage
}

// builtFromTheFiles checks that the Drive on a new server holds Bence's files as the storage has them, and nothing
// that was given on them: no shares, no link, and no shared folder.
func builtFromTheFiles(t *testing.T, d *harness.Drive, payroll placeJSON, notesPage string, missing harness.Response) {
	t.Helper()
	place, items := myFiles(t, d, bence)
	if got := names(items); !slices.Equal(got, []string{"Clients", "Private", "Notes.txt"}) {
		t.Fatalf("Bence's My files built from the files: %q", got)
	}
	if got := names(list(t, d.As(bence), place, named(t, items, "Clients").ID).Items); !slices.Equal(got, []string{"Bakery", "Price list.pdf"}) {
		t.Fatalf("Clients built from the files: %q", got)
	}
	if got := sharedNames(t, d.As(dora)); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me, built from the files: %q", got)
	}
	sameAsNoLink(t, "the link on Notes.txt, built from the files", visitor(d).Get(t, notesPage), missing)
	for _, m := range []harness.Member{reka, dora} {
		if _, sees := sharedFolders(t, d, m)["Payroll"]; sees {
			t.Fatalf("%s sees Payroll, built from the files", m.Email)
		}
	}
}

// Without a copy on the storage, a Drive on a new server builds its index from the files there: the folder tree, each
// member's My files as they first open it, and no shares, links or shared folders. The log says so.
func TestWithoutACopyTheDriveBuildsItsIndexFromTheFiles(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	missing := visitor(d).Get(t, noSuchLink(t))
	_, payroll, notesPage := sharedBeforeTheCopy(t, d)
	d.Stop(t)
	d.Storage.Delete(t, ".superit-drive/index", true)

	d.OnANewServer(t, nil)
	harness.Eventually(t, "the log says there is no copy", func() bool {
		return strings.Contains(d.Logs(), "there is no copy of one to make it from: the Drive builds its index from the files")
	})
	builtFromTheFiles(t, d, payroll, notesPage, missing)
}

// A damaged copy is never adopted: the Drive builds its index from the files, says so loudly, and keeps the copy aside
// on the storage, where the next copy does not remove it.
func TestADamagedCopyIsNotAdopted(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	missing := visitor(d).Get(t, noSuchLink(t))
	_, payroll, notesPage := sharedBeforeTheCopy(t, d)
	d.Stop(t)
	at, _ := storedCopy(t, d)
	data, _ := d.Storage.Get(t, at)
	for i := 4096; i < len(data); i += 1024 {
		data[i] ^= 0xff
	}
	d.Storage.Replace(t, at, data)

	d.OnANewServer(t, nil)
	harness.Eventually(t, "the log says loudly that the copy is not used", func() bool {
		logs := d.Logs()
		return strings.Contains(logs, "level=ERROR") && strings.Contains(logs, "THE COPY OF THE INDEX ON THE STORAGE IS NOT USED") &&
			strings.Contains(logs, "damaged")
	})
	builtFromTheFiles(t, d, payroll, notesPage, missing)
	before := len(copiesMade(d))
	ok(t, "sharing Notes.txt with Dóra on the new server", share(t, d.As(bence), bencesPlace(t, d), named(t, list(t, d.As(bence), bencesPlace(t, d), 0).Items, "Notes.txt").ID, dora, "viewer"))
	copiesSettled(t, d, before)
	if got := storedCopies(t, d); len(got) != 2 || !strings.HasPrefix(got[0], "copy-") || !strings.HasPrefix(got[1], "refused-") {
		t.Fatalf("the copies' folder after the next copy: %q, want the new copy and the refused one", got)
	}
}

// bencesPlace is Bence's My files.
func bencesPlace(t *testing.T, d *harness.Drive) int64 {
	t.Helper()
	place, _ := myFiles(t, d, bence)
	return place
}

// A copy whose index belongs to another storage (another id than the mark's) is never adopted; nor is any copy when the
// storage holds no mark at all. The log says so loudly, and the copies stay on the storage.
func TestACopyOfAnotherStorageIsNotAdopted(t *testing.T) {
	t.Parallel()
	t.Run("another id", func(t *testing.T) {
		t.Parallel()
		d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
		missing := visitor(d).Get(t, noSuchLink(t))
		_, payroll, notesPage := sharedBeforeTheCopy(t, d)
		d.Stop(t)
		alterStoredCopy(t, d, func(db *sql.DB) {
			run(t, db, "UPDATE drive_state SET value = '0123456789abcdef0123456789abcdef' WHERE key = 'storage_id'")
		})
		d.OnANewServer(t, nil)
		harness.Eventually(t, "the log says the copy belongs to another storage", func() bool {
			return strings.Contains(d.Logs(), "THE COPY OF THE INDEX ON THE STORAGE IS NOT USED: it belongs to another storage")
		})
		builtFromTheFiles(t, d, payroll, notesPage, missing)
		if got := storedCopies(t, d); !slices.ContainsFunc(got, func(n string) bool { return strings.HasPrefix(n, "refused-") }) {
			t.Fatalf("the copies' folder: %q, want the refused copy kept", got)
		}
	})
	t.Run("no mark", func(t *testing.T) {
		t.Parallel()
		d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
		missing := visitor(d).Get(t, noSuchLink(t))
		_, payroll, notesPage := sharedBeforeTheCopy(t, d)
		id := storageID(t, d)
		d.Stop(t)
		d.Storage.Delete(t, ".superit-drive/storage-id", false)
		d.OnANewServer(t, nil)
		harness.Eventually(t, "the log says the storage holds copies but no mark", func() bool {
			return strings.Contains(d.Logs(), "THE STORAGE HOLDS COPIES OF AN INDEX BUT NOT THE DRIVE'S MARK") && strings.Contains(d.Logs(), id)
		})
		builtFromTheFiles(t, d, payroll, notesPage, missing)
		if got, _ := d.Storage.List(t, ".superit-drive/index/"+id); len(got) != 1 {
			t.Fatalf("the copies of the index the storage held: %+v, want them kept", got)
		}
	})
}

// A copy made by a newer Drive than the one started stops it from starting, and it says why: building an index without
// the copy would soon put a copy without the shares in its place. The copy stays as it is.
func TestACopyFromANewerDriveStopsTheDriveFromStarting(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	sharedBeforeTheCopy(t, d)
	d.Stop(t)
	alterStoredCopy(t, d, func(db *sql.DB) { run(t, db, "PRAGMA user_version = 999") })
	at, _ := storedCopy(t, d)
	data, _ := d.Storage.Get(t, at)

	out := d.OnANewServerFailing(t, nil)
	if !strings.Contains(out, "was made by a newer version of the Drive") || !strings.Contains(out, "does not start") {
		t.Fatalf("the Drive did not say a newer Drive made the copy:\n%s", out)
	}
	if again, ok := d.Storage.Get(t, at); !ok || !bytes.Equal(again, data) {
		t.Fatal("the newer copy was changed or moved")
	}
}

// A copy made by an older Drive is adopted and brought up to date, and the Drive goes on copying its index. (Copies of
// older versions still are: internal/index tests one made by the migrations up to 0010.)
func TestAnOlderCopyIsAdoptedAndBroughtUpToDate(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x, _, notesPage := sharedBeforeTheCopy(t, d)
	d.Stop(t)
	alterStoredCopy(t, d, func(db *sql.DB) { madeByVersion11(t, db) })

	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	if !strings.Contains(d.Logs(), "version=11") {
		t.Fatalf("the log does not say the copy's version was 11:\n%s", d.Logs())
	}
	if got := sharedNames(t, d.As(dora)); !slices.Contains(got, entry("Clients", x.place)) {
		t.Fatalf("Dóra's Shared with me from an older copy: %q", got)
	}
	if p := readPage(t, visitor(d), notesPage); p.name != "Notes.txt" {
		t.Fatalf("the link from an older copy: %+v", p)
	}
	before := len(copiesMade(d))
	ok(t, "sharing Notes.txt with Dóra", share(t, d.As(bence), x.place, x.notes.ID, dora, "viewer"))
	copiesSettled(t, d, before)
}

// madeByVersion11 makes a copy of the index into one a Drive whose index was at migration 0011 made, before upload-only
// links: its links of one kind, without what each took in, and its files without the label of those that came through
// a link. (The table is made again as 0006 made it; renaming it into place quotes its name, which 0006 did not.)
func madeByVersion11(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE links_0011 (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id    INTEGER NOT NULL UNIQUE REFERENCES items (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    kind       TEXT    NOT NULL CHECK (kind IN ('view')),
    made_at    INTEGER NOT NULL, -- unix milliseconds
    made_by    TEXT    NOT NULL,
    uses       INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
    used_at    INTEGER
)`,
		"INSERT INTO links_0011 SELECT id, item_id, token_hash, kind, made_at, made_by, uses, used_at FROM links",
		"DROP TABLE links",
		"ALTER TABLE links_0011 RENAME TO links",
		"PRAGMA writable_schema = ON",
		`UPDATE sqlite_schema SET sql = replace(sql, 'CREATE TABLE "links"', 'CREATE TABLE links') WHERE type = 'table' AND name = 'links'`,
		"PRAGMA writable_schema = OFF",
		"CREATE TRIGGER access_changes_links_insert AFTER INSERT ON links BEGIN UPDATE access_changes SET n = n + 1; END",
		"CREATE TRIGGER access_changes_links_delete AFTER DELETE ON links BEGIN UPDATE access_changes SET n = n + 1; END",
		`CREATE TRIGGER access_changes_links_update AFTER UPDATE OF item_id, token_hash, kind, made_at, made_by ON links
    BEGIN UPDATE access_changes SET n = n + 1; END`,
		"ALTER TABLE items DROP COLUMN added_through_link",
		"PRAGMA user_version = 11",
	} {
		run(t, db, statement)
	}
}

// What changed on the storage after the copy was made comes in once it is adopted: a file put there is seen, with the
// access of its folder; a file gone from there is hidden, its link with it; and a file deleted through the Drive after
// the copy (whose deletion the copy does not know) is in the trash as itself, and its link works again when it is
// restored.
func TestWhatChangedSinceTheCopyComesIn(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora},
		Env: with(copiesSoon("300ms", "2s"), fastWalk("1h"))})
	b := d.As(bence)
	missing := visitor(d).Get(t, noSuchLink(t))
	x, _, notesPage := sharedBeforeTheCopy(t, d)
	_, ledgerPage := madeLink(t, d, b, x.place, x.ledger.ID, false)
	// Started again, it copies the index at once; then not for ten minutes, so the copy does not know what follows.
	d.Restart(t, copiesSoon("10m", "10m"))
	harness.Eventually(t, "the copy at start", func() bool {
		return slices.Contains(copiesMade(d), "the Drive started")
	})
	b = d.As(bence)
	deleted(t, b, x.place, x.ledger.ID)
	d.Kill(t) // the server dies: no copy as it stops
	nextSecond(t, d)
	d.Storage.Put(t, "My files/bence@example.com/Clients/New offer.pdf", "new offer")
	d.Storage.Delete(t, "My files/bence@example.com/Notes.txt", false)

	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	b, o := d.As(bence), d.As(dora)
	harness.Eventually(t, "the walk after the index was made from its copy has ended", func() bool {
		return strings.Contains(d.Logs(), "the index made from a copy is in step with the storage")
	})
	// Put there since: seen, with the access of Clients.
	newOffer := named(t, list(t, o, x.place, x.clients.ID).Items, "New offer.pdf")
	if got := contentOf(t, o, x.place, newOffer.ID); got != "new offer" {
		t.Fatalf("Dóra downloading New offer.pdf, put in Clients after the copy: %s", got)
	}
	// Gone since: hidden, its link with it.
	if got := names(list(t, b, x.place, 0).Items); slices.Contains(got, "Notes.txt") {
		t.Fatalf("Bence's My files lists Notes.txt, gone from the storage: %q", got)
	}
	sameAsNoLink(t, "the link on Notes.txt, gone from the storage", visitor(d).Get(t, notesPage), missing)
	// Deleted since: in the trash as itself; its link opens nothing there, and works again when it is restored.
	if !slices.ContainsFunc(trashOf(t, b, x.place), func(e trashJSON) bool { return e.ID == x.ledger.ID }) {
		t.Fatalf("Ledger.xlsx, deleted after the copy, is not in the trash as itself: %+v", trashOf(t, b, x.place))
	}
	sameAsNoLink(t, "the link on Ledger.xlsx in the trash", visitor(d).Get(t, ledgerPage), missing)
	restored(t, b, x.place, x.ledger.ID)
	if p := readPage(t, visitor(d), ledgerPage); p.name != "Ledger.xlsx" {
		t.Fatalf("the link on Ledger.xlsx, restored: %+v", p)
	}
}

// After a rollback, the storage's copy of the index is the snapshot's. The Drive, which keeps its own index, never adopts
// it: access taken away since the snapshot stays away. It puts a fresh copy in its place, so a server lost afterwards
// comes back with sharing as it was, not as it was in the snapshot; and the rollback is old news to it.
func TestAfterARollbackTheDriveCopiesItsOwnIndexAgain(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora},
		Env: with(copiesSoon("300ms", "2s"), fastWalk("1h"))})
	missing := visitor(d).Get(t, noSuchLink(t))
	x, _, notesPage := sharedBeforeTheCopy(t, d)
	_, inSnapshot := storedCopy(t, d)
	nextSecond(t, d)
	snap := d.Storage.Snapshot(t)

	before := len(copiesMade(d))
	b := d.As(bence)
	if res := removeLink(t, b, x.place, x.notes.ID); res.Status != http.StatusNoContent {
		t.Fatalf("removing the link on Notes.txt: %d %s", res.Status, res.Body)
	}
	ok(t, "taking Clients away from Dóra", unshare(t, b, x.place, x.clients.ID, dora))
	copiesSettled(t, d, before)

	rolledBack(t, d, snap)
	harness.Eventually(t, "a fresh copy replaces the snapshot's on the storage", func() bool {
		got := storedCopies(t, d)
		return len(got) == 1 && got[0] > copyFileName(inSnapshot)
	})
	sameAsNoLink(t, "the link removed after the snapshot", visitor(d).Get(t, notesPage), missing)
	if got := sharedNames(t, d.As(dora)); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me after the rollback: %q", got)
	}

	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	sameAsNoLink(t, "the link removed after the snapshot, on a new server", visitor(d).Get(t, notesPage), missing)
	if got := sharedNames(t, d.As(dora)); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me on a new server after the rollback: %q", got)
	}
	harness.Eventually(t, "the log says the rollback was taken in already", func() bool {
		return strings.Contains(d.Logs(), "was taken in already")
	})
	if n := strings.Count(d.Logs(), "the storage was put back to a snapshot:"); n != 1 {
		t.Fatalf("the rollback was taken in %d times", n)
	}
	if r := meOf(t, d, dora).Rollback; r == nil {
		t.Fatal("the rollback's notice is gone on the new server")
	}
}

// copyFileName is the name of the copy numbered seq.
func copyFileName(seq int64) string { return fmt.Sprintf("copy-%012d.sqlite", seq) }

// The copy follows access changes: a burst of them makes one copy, once nothing changed for a while; changes that never
// stop still get a copy within the bound, while they go on.
func TestABurstOfAccessChangesMakesOneCopy(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("3s", "8s")})
	b := d.As(bence)
	x := bencesFiles(t, d)
	copiesSettled(t, d, 0)

	n := len(copiesMade(d))
	for i := range 6 {
		if i%2 == 0 {
			ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
		} else {
			ok(t, "taking Notes.txt away from Dóra", unshare(t, b, x.place, x.notes.ID, dora))
		}
		time.Sleep(200 * time.Millisecond)
	}
	copiesSettled(t, d, n)
	if got := copiesMade(d)[n:]; !slices.Equal(got, []string{"access changed"}) {
		t.Fatalf("a burst of six changes made the copies %q, want one", got)
	}

	// Started again, and left alone longer than a change waits at most: a burst still makes one copy, after it.
	d.Restart(t, nil)
	b = d.As(bence)
	harness.Eventually(t, "the copy as the Drive started", func() bool {
		return slices.Contains(copiesMade(d), "the Drive started")
	})
	time.Sleep(9 * time.Second)
	n = len(copiesMade(d))
	for i := range 6 { // over more than one look at the count
		ok(t, "giving Dóra Ledger.xlsx", share(t, b, x.place, x.ledger.ID, dora, []string{"viewer", "editor"}[i%2]))
		time.Sleep(400 * time.Millisecond)
	}
	copiesSettled(t, d, n)
	if got := copiesMade(d)[n:]; !slices.Equal(got, []string{"access changed"}) {
		t.Fatalf("a burst of six changes, long after the Drive started, made the copies %q, want one", got)
	}

	// A change every half second for twelve seconds: never three quiet seconds, yet a copy within eight of the first.
	n = len(copiesMade(d))
	for i, start := 0, time.Now(); time.Since(start) < 12*time.Second; i++ {
		ok(t, "giving Dóra Ledger.xlsx again", share(t, b, x.place, x.ledger.ID, dora, []string{"viewer", "editor"}[i%2]))
		time.Sleep(500 * time.Millisecond)
	}
	if got := copiesMade(d)[n:]; len(got) == 0 {
		t.Fatal("no copy was made while access kept changing for twelve seconds")
	}
}

// Every night, at three in the morning (UTC), the index is copied, whatever changed.
func TestTheIndexIsCopiedEveryNight(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: copiesSoon("300ms", "2s")})
	myFiles(t, d, reka)
	copiesSettled(t, d, 0)
	_, last := storedCopy(t, d)

	// Started again with its clock five seconds before three in the morning.
	now := time.Now().UTC()
	night := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, time.UTC)
	for night.Before(now.Add(10 * time.Second)) {
		night = night.Add(24 * time.Hour)
	}
	d.Restart(t, map[string]string{harness.TestClockShift: (night.Sub(now) - 5*time.Second).String()})
	harness.EventuallyWithin(t, 20*time.Second, "the nightly copy", func() bool {
		return slices.Contains(copiesMade(d), "nightly")
	})
	if _, seq := storedCopy(t, d); seq <= last+1 {
		t.Fatalf("the copy on the storage is number %d after the nightly copy; it was %d before the Drive started again", seq, last)
	}
}

// The data folder always holds a consistent copy of the index, replaced whole: whatever a backup reads from it while
// people share is one whole SQLite file. Restored onto an empty data folder (the live index is not archived), it is
// the index again, guests' sessions included: the server's own backup is as private as its index.
func TestTheDataFolderAlwaysHoldsACopyABackupCanArchive(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora},
		Env: with(mailSettings(smtp), copiesSoon("300ms", "1s"))})
	backup := filepath.Join(d.DataDir, "backup", "index.sqlite")
	harness.Eventually(t, "the data folder holds a copy from the start", func() bool {
		_, err := os.Stat(backup)
		return err == nil
	})
	b := d.As(bence)
	x := bencesFiles(t, d)
	invited(t, b, x.place, x.bakery.ID, anna, "viewer", nil)
	annaCookie := signedIn(t, d, smtp, anna)
	_, notesPage := madeLink(t, d, b, x.place, x.notes.ID, false)

	// A backup reads the copy over and over while Bence keeps sharing.
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			role := []string{"viewer", "editor"}[i%2]
			body := strings.NewReader(`{"role":"` + role + `"}`)
			_, _ = b.Try(t, http.MethodPut, sharePath(x.place, x.ledger.ID, &dora), body)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	versions := map[string]bool{}
	for range 40 {
		data, err := os.ReadFile(backup)
		if err != nil {
			t.Fatalf("reading the copy as a backup would: %v", err)
		}
		file := filepath.Join(t.TempDir(), "archived.sqlite")
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := integrityOf(t, file); got != "ok" {
			t.Fatalf("a copy a backup read is not whole: %s", got)
		}
		versions[fmt.Sprintf("%x", sha256.Sum256(data))] = true
		time.Sleep(100 * time.Millisecond)
	}
	close(stop)
	<-done
	if len(versions) < 2 {
		t.Fatal("the copy was never replaced while Bence kept sharing")
	}

	before := len(copiesMade(d))
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	copiesSettled(t, d, before)
	archive, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	// The server's data put back from the backup: the copy, not the live index.
	d.Stop(t)
	putBack(t, d, archive)
	d.Restart(t, nil)
	adopted(t, d, "data folder")
	if got := sharedNames(t, d.As(dora)); !slices.Contains(got, entry("Clients", x.place)) {
		t.Fatalf("Dóra's Shared with me, restored from the backup: %q", got)
	}
	if p := readPage(t, visitor(d), notesPage); p.name != "Notes.txt" {
		t.Fatalf("the link, restored from the backup: %+v", p)
	}
	if who := signedInAs(t, d, annaCookie); who != anna {
		t.Fatalf("Anna's session, restored from the server's own backup: %q", who)
	}
}

// integrityOf is what SQLite's integrity check says of a database file: "ok" when it is whole.
func integrityOf(t *testing.T, file string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&got); err != nil {
		return err.Error()
	}
	return got
}

// A copy goes on the storage only while the storage holds the Drive's mark with its index's id: never on a storage that
// is someone else's, empty or mounted wrongly. Once the mark is back, the copy follows.
func TestACopyGoesOnlyOnTheDrivesOwnStorage(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x := bencesFiles(t, d)
	copiesSettled(t, d, 0)
	id := storageID(t, d)
	mark, _ := d.Storage.Get(t, ".superit-drive/storage-id")
	stored, _ := d.Storage.List(t, ".superit-drive/index/"+id)

	d.Storage.Delete(t, ".superit-drive/storage-id", false)
	n := len(copiesMade(d))
	ok(t, "sharing Notes.txt with Dóra", share(t, d.As(bence), x.place, x.notes.ID, dora, "viewer"))
	harness.Eventually(t, "the log says the copy is not put on the storage", func() bool {
		return strings.Contains(d.Logs(), "but not onto the storage, which does not hold the Drive's mark")
	})
	if got, _ := d.Storage.List(t, ".superit-drive/index/"+id); len(got) != len(stored) || got[0].Name != stored[0].Name || len(copiesMade(d)) != n {
		t.Fatalf("a copy went onto a storage without the mark: %+v", got)
	}

	d.Storage.Put(t, ".superit-drive/storage-id", string(mark))
	harness.Eventually(t, "the copy goes onto the storage once the mark is back", func() bool {
		return slices.Contains(copiesMade(d)[n:], "the storage did not take the last copy")
	})
}

// Nothing in the Drive's hidden folder, where the copies of the index are, is served by any route, to anyone: a project
// admin, a member, a guest or a visitor, whatever path they try. A folder called .superit-drive in a place is a folder
// like any other there.
func TestNoRouteReachesTheHiddenFolder(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora},
		Env: with(mailSettings(smtp), copiesSoon("300ms", "2s"))})
	b := d.As(bence)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.clients.ID, false)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	guest := withCookie(d, signedIn(t, d, smtp, anna))
	copiesSettled(t, d, 0)
	at, _ := storedCopy(t, d)
	id := storageID(t, d)

	hidden := madeFolder(t, b, x.place, 0, ".superit-drive")
	inHidden := madeFolder(t, b, x.place, hidden.ID, "index")
	if got := names(list(t, b, x.place, inHidden.ID).Items); len(got) != 0 {
		t.Fatalf("a folder .superit-drive/index in Bence's My files lists %q", got)
	}
	shared := madeSharedFolder(t, d.As(reka), ".superit-drive")
	if got := names(list(t, d.As(reka), shared.ID, 0).Items); len(got) != 0 {
		t.Fatalf("a shared folder called .superit-drive lists %q", got)
	}
	for _, name := range []string{"..", ".", "../.superit-drive", ".superit-drive/index", "../../" + at} {
		if res := newFolder(t, b, x.place, 0, name); res.Status != http.StatusBadRequest {
			t.Errorf("a folder called %q: got %d %s", name, res.Status, res.Body)
		}
	}

	paths := []string{
		"/.superit-drive/storage-id", "/" + at, "/.superit-drive/index/" + id + "/",
		"/s/../.superit-drive/storage-id", "/s/%2e%2e/.superit-drive/storage-id", "/s/.superit-drive/storage-id",
		page + "/../../.superit-drive/storage-id", page + "/items/..%2f..%2f.superit-drive/download",
		"/s/guest/../../" + at, "/s/guest/assets/..%2f..%2f.superit-drive%2fstorage-id",
		"/s/guest/api/places/0/items/0/download", "/assets/../.superit-drive/storage-id", "/assets/%2e%2e%2f.superit-drive%2fstorage-id",
		fmt.Sprintf("/api/places/%d/items/..%%2f..%%2f.superit-drive/download", x.place),
		fmt.Sprintf("/api/places/%d/zip", x.place), fmt.Sprintf("/api/places/%d/items/%d/zip", x.place, hidden.ID),
		fmt.Sprintf("/api/search?q=%s", "copy"), fmt.Sprintf("/api/search?q=%s", id),
	}
	clients := map[string]*harness.Client{"a project admin": d.As(reka), "a member": b, "a guest": guest, "a visitor": visitor(d)}
	for who, c := range clients {
		for _, p := range paths {
			res, err := c.Try(t, http.MethodGet, p, nil)
			if err != nil {
				continue
			}
			if bytes.Contains(res.Body, []byte("SQLite format 3")) || bytes.Contains(res.Body, []byte(id)) {
				t.Errorf("%s reached the hidden folder at %s: %d", who, p, res.Status)
			}
		}
	}
}

// A Drive that stops (as before an upgrade, whose snapshot of the data folder is taken once it has stopped) copies its
// index as it stops when access changed since the last copy: the copy then holds every change, the last share made just
// before included. A Drive that dies (killed) writes nothing more.
func TestAStoppingDriveCopiesWhatItHasNotYet(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("1m", "10m")})
	b := d.As(bence)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	d.Stop(t)
	if got := copiesMade(d); !slices.Contains(got, "the Drive stops") {
		t.Fatalf("the Drive stopped without copying its index: %q", got)
	}
	// The snapshot holds the data folder without the live index; restored from it, the share is there.
	for _, name := range []string{"index.sqlite", "index.sqlite-wal", "index.sqlite-shm"} {
		os.Remove(filepath.Join(d.DataDir, name))
	}
	d.Restart(t, nil)
	adopted(t, d, "data folder")
	if got := sharedNames(t, d.As(dora)); !slices.Equal(got, []string{entry("Clients", x.place)}) {
		t.Fatalf("Dóra's Shared with me, restored from the copy written as the Drive stopped: %q", got)
	}

	// Stopped again with nothing new since the copy it made as it started: no copy.
	harness.Eventually(t, "the copy as the Drive started", func() bool {
		return strings.Count(strings.Join(copiesMade(d), "\n"), "the Drive started") > 0
	})
	n := len(copiesMade(d))
	d.Stop(t)
	if got := copiesMade(d)[n:]; slices.Contains(got, "the Drive stops") {
		t.Fatalf("the Drive copied its index as it stopped, with no access change since the last copy: %q", got)
	}
}

// A copy someone with the storage login planted, or changed, is adopted only if it is one the Drive could have made: one
// whose My files is the Drive's hidden folder (where the copies of the index are), or that carries a trigger of its own,
// is refused, kept aside, and the Drive builds its index from the files.
func TestAPlantedCopyIsNotAdopted(t *testing.T) {
	t.Parallel()
	for what, c := range map[string]struct{ change, why string }{
		"a My files in the hidden folder": {"UPDATE places SET folder = '.superit-drive' WHERE kind = 'my-files'",
			`a place's top folder is ".superit-drive", where the Drive never puts one`},
		"a trigger of its own": {"CREATE TRIGGER keep_links AFTER DELETE ON links BEGIN INSERT INTO links SELECT OLD.*; END",
			"which the Drive never makes"},
		"a shared folder outside the Drive's folder": {"UPDATE places SET folder = 'Shared folders/..' WHERE kind = 'shared-folder'",
			`a place's top folder is "Shared folders/..", where the Drive never puts one`},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()
			d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
			missing := visitor(d).Get(t, noSuchLink(t))
			_, payroll, notesPage := sharedBeforeTheCopy(t, d)
			d.Stop(t)
			alterStoredCopy(t, d, func(db *sql.DB) { run(t, db, c.change) })
			d.OnANewServer(t, nil)
			harness.Eventually(t, "the log says the copy is not used, and why", func() bool {
				logs := strings.ReplaceAll(d.Logs(), `\"`, `"`)
				return strings.Contains(logs, "THE COPY OF THE INDEX ON THE STORAGE IS NOT USED") && strings.Contains(logs, c.why)
			})
			builtFromTheFiles(t, d, payroll, notesPage, missing)
			if got := storedCopies(t, d); !slices.ContainsFunc(got, func(n string) bool { return strings.HasPrefix(n, "refused-") }) {
				t.Fatalf("the copies' folder: %q, want the refused copy kept", got)
			}
		})
	}
}
