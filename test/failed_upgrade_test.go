package test

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// An upgrade that failed and was undone. The server agent upgrades the Drive by stopping it, taking a snapshot of its
// data folder (the copy of the index, never the live index) and starting the newer version with the data folder as it
// is: the newer version brings the index up to its schema and copies it onto the storage as it starts. When a step of
// the upgrade fails, the agent stops the newer version, puts the snapshot back as the data folder and starts this
// version again: with no index, its own copy in the data folder, and on the storage a copy numbered after it, made by
// the newer version. These tests stand in for the newer version by starting the same Drive again, and for its newer
// schema by changing the schema version of its copy on the storage, as someone with the storage login could. They look
// only at what the Drive answers and logs, at what is on the storage, and at the data folder's copy, as a backup would.

// A failedUpgrade is what an upgrade that failed left: the snapshot of the data folder's copy of the index, taken before
// it, and the copy the newer version left on the storage (where it is, its number and its bytes).
type failedUpgrade struct {
	snapshot []byte
	newer    string
	seq      int64
	data     []byte
}

// upgradeFailed does what the server agent does in an upgrade that fails, up to putting the snapshot back: it stops the
// Drive and takes the snapshot; starts the newer version, through which Bence shares Secret.txt with Dóra and puts
// "Under the newer version.txt" into Clients, which Dóra was given before; and stops it once its copy holds that. With
// newerSchema, the newer version's copy on the storage is of a schema this Drive does not know, as a version with a
// migration this one lacks makes it; without, it is of this Drive's schema. The Drive is left stopped.
func upgradeFailed(t *testing.T, d *harness.Drive, x bences, newerSchema bool) failedUpgrade {
	t.Helper()
	d.Stop(t)
	snapshot, err := os.ReadFile(filepath.Join(d.DataDir, "backup", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, before := storedCopy(t, d)

	n := len(copiesMade(d))
	d.Restart(t, nil)
	b := d.As(bence)
	ok(t, "sharing Secret.txt with Dóra under the newer version", share(t, b, x.place, x.secret.ID, dora, "viewer"))
	uploadedInto(t, b, x.place, x.clients.ID, "Under the newer version.txt", []byte("newer"))
	copiesSettled(t, d, n)
	d.Stop(t)
	if newerSchema {
		alterStoredCopy(t, d, func(db *sql.DB) { run(t, db, "PRAGMA user_version = 999") })
	}
	at, seq := storedCopy(t, d)
	if seq <= before {
		t.Fatalf("the newer version's copy on the storage is number %d; the one before the upgrade was %d", seq, before)
	}
	data, _ := d.Storage.Get(t, at)
	return failedUpgrade{snapshot: snapshot, newer: at, seq: seq, data: data}
}

// newerAside is the name of a copy made by a newer Drive, moved aside in the copies' folder: group 1 is its name before.
var newerAside = regexp.MustCompile(`^newer-version-[0-9]{8}T[0-9]{6}Z-(copy-[0-9]+\.sqlite)$`)

// isNewerAside says whether a name in the copies' folder is the copy numbered seq, moved aside.
func isNewerAside(seq int64) func(string) bool {
	return func(name string) bool {
		m := newerAside.FindStringSubmatch(name)
		return m != nil && m[1] == copyFileName(seq)
	}
}

// movedAside waits until the copy numbered seq is no longer in the copies' folder under its name but moved aside there,
// and returns what the file moved aside holds.
func movedAside(t *testing.T, d *harness.Drive, seq int64) []byte {
	t.Helper()
	var aside string
	harness.Eventually(t, "the newer copy is moved aside on the storage", func() bool {
		got := storedCopies(t, d)
		if i := slices.IndexFunc(got, isNewerAside(seq)); i >= 0 && !slices.Contains(got, copyFileName(seq)) {
			aside = got[i]
		}
		return aside != ""
	})
	data, _ := d.Storage.Get(t, ".superit-drive/index/"+storageID(t, d)+"/"+aside)
	return data
}

// copyNumbers are the numbers of the copies the Drive put on the storage so far, as its log says.
func copyNumbers(d *harness.Drive) []int64 {
	var numbers []int64
	for _, m := range copyLine.FindAllStringSubmatch(d.Logs(), -1) {
		n, _ := strconv.ParseInt(m[2], 10, 64)
		numbers = append(numbers, n)
	}
	return numbers
}

// logLines are the lines of the Drive's log that hold every one of parts.
func logLines(d *harness.Drive, parts ...string) []string {
	var lines []string
	for _, line := range strings.Split(d.Logs(), "\n") {
		if !slices.ContainsFunc(parts, func(p string) bool { return !strings.Contains(line, p) }) {
			lines = append(lines, line)
		}
	}
	return lines
}

// leftAlone is how the log begins to say that a newer copy was left alone.
const leftAlone = "THE COPY OF THE INDEX ON THE STORAGE IS LEFT ALONE: the copy of the index was made by a newer version of the Drive"

// After an upgrade that failed, whose newer version left a copy of a newer schema on the storage, the Drive starts from
// the data folder's copy, and says loudly that it left the newer copy alone. What was given under the newer version is
// not in its index; the file put there under it is on the storage, and comes in with the access of its folder. The newer
// copy stays as it is until the Drive has put a copy of its own on the storage, numbered after it; then it is moved
// aside there, and kept. From then on the Drive's own copies are the ones adopted; the upgrade tried again (the newer
// version starts with the index, and copies it) leaves the newer copy aside.
func TestADriveRolledBackAfterAFailedUpgradeStarts(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x, _, notesPage := sharedBeforeTheCopy(t, d)
	u := upgradeFailed(t, d, x, true)

	putBack(t, d, u.snapshot)
	d.Restart(t, nil)
	adopted(t, d, "data folder")
	if got := logLines(d, "level=ERROR", leftAlone, "copy="+u.newer+" number="+strconv.FormatInt(u.seq, 10)); len(got) != 1 {
		t.Fatalf("the log does not say loudly, once, that the newer copy was left alone:\n%s", d.Logs())
	}
	o := d.As(dora)
	if got := sharedNames(t, o); !slices.Equal(got, []string{entry("Clients", x.place)}) {
		t.Fatalf("Dóra's Shared with me after the upgrade was undone: %q, want Clients alone (Secret.txt was given under the newer version)", got)
	}
	if p := readPage(t, visitor(d), notesPage); p.name != "Notes.txt" {
		t.Fatalf("the link on Notes.txt, made before the upgrade: %+v", p)
	}
	harness.Eventually(t, "the file put there under the newer version comes in", func() bool {
		return slices.Contains(names(list(t, o, x.place, x.clients.ID).Items), "Under the newer version.txt")
	})
	if got := contentOf(t, o, x.place, named(t, list(t, o, x.place, x.clients.ID).Items, "Under the newer version.txt").ID); got != "newer" {
		t.Fatalf("Dóra downloading the file put in Clients under the newer version: %s", got)
	}

	// The newer copy, moved aside once the Drive's first copy is there, numbered right after it.
	if aside := movedAside(t, d, u.seq); !bytes.Equal(aside, u.data) {
		t.Fatal("the newer copy moved aside is not the copy the newer version left")
	}
	if got := copyNumbers(d); !slices.Contains(got, u.seq+1) {
		t.Fatalf("the copies the Drive put on the storage are numbered %v; the first after the upgrade was undone should be %d", got, u.seq+1)
	}

	// Shared after the upgrade was undone: in the Drive's own copies.
	before := len(copiesMade(d))
	ok(t, "sharing Notes.txt with Dóra after the upgrade was undone", share(t, d.As(bence), x.place, x.notes.ID, dora, "viewer"))
	copiesSettled(t, d, before)

	// The upgrade tried again: the newer version starts with the index, and copies it as it starts.
	before = len(copiesMade(d))
	d.Restart(t, nil)
	harness.Eventually(t, "the copy as the Drive starts with its index", func() bool { return len(copiesMade(d)) > before })
	if got := storedCopies(t, d); !slices.ContainsFunc(got, isNewerAside(u.seq)) || slices.Contains(got, copyFileName(u.seq)) {
		t.Fatalf("the copies' folder after a later start: %q, want the newer copy still aside", got)
	}

	// A server lost now comes back from the Drive's own copy on the storage.
	d.OnANewServer(t, nil)
	adopted(t, d, "storage")
	if got, want := sorted(sharedNames(t, d.As(dora))), sorted([]string{entry("Clients", x.place), entry("Notes.txt", x.place)}); !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me on a new server: %q, want %q", got, want)
	}
	if got := logLines(d, leftAlone); len(got) != 1 {
		t.Fatalf("the newer copy was left alone %d times", len(got))
	}
	if got := storedCopies(t, d); !slices.ContainsFunc(got, isNewerAside(u.seq)) {
		t.Fatalf("the copies' folder on a new server: %q, want the newer copy still aside", got)
	}
}

// The Drive dies just after its first copy went onto the storage, before it moved the newer copy aside: the newer copy
// is still there as it was, and the Drive's own copy is numbered after it. A server lost then comes back from the
// Drive's own copy, not the newer one; knowing from it that the newer copy is still to be moved aside, it moves it
// aside and keeps it.
func TestANewerCopyLeftAloneIsKeptWhenTheDriveDiesBeforeMovingItAside(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x, _, _ := sharedBeforeTheCopy(t, d)
	u := upgradeFailed(t, d, x, true)

	putBack(t, d, u.snapshot)
	out := d.RestartFailing(t, map[string]string{harness.TestStopAt: "own copy stored"})
	if !strings.Contains(out, "stopped at own copy stored") || !strings.Contains(out, leftAlone) {
		t.Fatalf("the Drive did not leave the newer copy alone and die after its first copy:\n%s", out)
	}
	if got := storedCopies(t, d); !slices.Contains(got, copyFileName(u.seq)) || !slices.Contains(got, copyFileName(u.seq+1)) {
		t.Fatalf("the copies' folder when the Drive died: %q, want the newer copy and the Drive's own, numbered after it", got)
	}
	if data, ok := d.Storage.Get(t, u.newer); !ok || !bytes.Equal(data, u.data) {
		t.Fatal("the newer copy was changed before the Drive moved it aside")
	}

	d.OnANewServer(t, map[string]string{harness.TestStopAt: ""})
	adopted(t, d, "storage")
	if got := logLines(d, "the Drive made it from the copy on the storage", "number="+strconv.FormatInt(u.seq+1, 10)); len(got) != 1 {
		t.Fatalf("the new server did not make its index from the Drive's own copy, number %d:\n%s", u.seq+1, d.Logs())
	}
	if aside := movedAside(t, d, u.seq); !bytes.Equal(aside, u.data) {
		t.Fatal("the newer copy moved aside is not the copy the newer version left")
	}
	if got := sharedNames(t, d.As(dora)); !slices.Equal(got, []string{entry("Clients", x.place)}) {
		t.Fatalf("Dóra's Shared with me on the new server: %q, want Clients alone", got)
	}
	if got := logLines(d, leftAlone); len(got) != 1 {
		t.Fatalf("the newer copy was left alone %d times", len(got))
	}
}

// When the newer copy cannot be moved aside (someone with the storage login put files where it would go), it stays where
// it is, as it was, beside the Drive's own copy: it is not removed with the copies before that one. The next copy tries
// again, and moves it aside once nothing is in the way.
func TestANewerCopyThatCannotBeMovedAsideStaysWhereItIs(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x, _, _ := sharedBeforeTheCopy(t, d)
	u := upgradeFailed(t, d, x, true)

	folder := ".superit-drive/index/" + storageID(t, d)
	until := time.Now().Add(20 * time.Second)
	inTheWay := map[string]bool{}
	for at := time.Now().UTC().Truncate(time.Second); at.Before(until); at = at.Add(time.Second) {
		name := "newer-version-" + at.UTC().Format("20060102T150405Z") + "-" + copyFileName(u.seq)
		d.Storage.Put(t, folder+"/"+name, "in the way")
		inTheWay[name] = true
	}
	putBack(t, d, u.snapshot)
	d.Restart(t, nil)
	adopted(t, d, "data folder")
	harness.Eventually(t, "the log says the newer copy could not be moved aside", func() bool {
		return len(logLines(d, "level=WARN", "the copy of the index made by a newer version of the Drive could not be moved aside")) > 0
	})
	if time.Now().After(until) {
		t.Fatal("the Drive's first copy came after the files in the way: the test proves nothing")
	}
	if got := storedCopies(t, d); !slices.Contains(got, copyFileName(u.seq)) || !slices.Contains(got, copyFileName(u.seq+1)) {
		t.Fatalf("the copies' folder after the Drive's first copy: %q, want the newer copy beside the Drive's own", got)
	}
	if data, ok := d.Storage.Get(t, u.newer); !ok || !bytes.Equal(data, u.data) {
		t.Fatal("the newer copy that could not be moved aside was changed or removed")
	}

	// Nothing is in the way any more: the copy after the next access change moves it aside.
	time.Sleep(time.Until(until))
	before := len(copiesMade(d))
	ok(t, "sharing Notes.txt with Dóra", share(t, d.As(bence), x.place, x.notes.ID, dora, "viewer"))
	copiesSettled(t, d, before)
	got := storedCopies(t, d)
	if slices.Contains(got, copyFileName(u.seq)) {
		t.Fatalf("the copies' folder after the next copy: %q, want the newer copy moved aside", got)
	}
	var aside []string
	for _, name := range got {
		if isNewerAside(u.seq)(name) && !inTheWay[name] {
			aside = append(aside, name)
		}
	}
	if len(aside) != 1 {
		t.Fatalf("the copies' folder after the next copy: %q, want the newer copy moved aside once", got)
	}
	if data, _ := d.Storage.Get(t, folder+"/"+aside[0]); !bytes.Equal(data, u.data) {
		t.Fatal("the newer copy moved aside is not the copy the newer version left")
	}
}

// Without a copy of its own in the data folder to start from, a copy on the storage made by a newer Drive still stops the
// Drive from starting, as it does on a new server: a damaged copy there, one of another storage, or one of an older
// schema is not the Drive's own. A copy in the data folder made by a newer Drive stops it whatever the storage holds.
// The Drive says why, and the newer copy on the storage stays as it is.
func TestANewerCopyStillStopsTheDriveWithoutItsOwnCopyInTheDataFolder(t *testing.T) {
	t.Parallel()
	newerOnStorage := "was made by a newer version of the Drive (its schema is version 999"
	for what, c := range map[string]struct {
		newerSchema bool
		change      func(t *testing.T, data []byte) []byte
		says        []string
	}{
		"a damaged copy in the data folder": {true, func(t *testing.T, data []byte) []byte {
			data = bytes.Clone(data)
			for i := 4096; i < len(data); i += 1024 {
				data[i] ^= 0xff
			}
			return data
		}, []string{"THE DATA FOLDER'S COPY OF THE INDEX IS NOT USED", newerOnStorage}},
		"a copy of another storage in the data folder": {true, func(t *testing.T, data []byte) []byte {
			return altered(t, data, func(db *sql.DB) {
				run(t, db, "UPDATE drive_state SET value = '0123456789abcdef0123456789abcdef' WHERE key = 'storage_id'")
			})
		}, []string{newerOnStorage}},
		"a copy of an older schema in the data folder": {true, func(t *testing.T, data []byte) []byte {
			return altered(t, data, func(db *sql.DB) { madeByVersion11(t, db) })
		}, []string{newerOnStorage}},
		"a copy made by a newer Drive in the data folder": {false, func(t *testing.T, data []byte) []byte {
			return altered(t, data, func(db *sql.DB) { run(t, db, "PRAGMA user_version = 999") })
		}, []string{"the data folder holds no index, and its copy", "was made by a newer version of the Drive"}},
	} {
		t.Run(what, func(t *testing.T) {
			t.Parallel()
			d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
			x, _, _ := sharedBeforeTheCopy(t, d)
			u := upgradeFailed(t, d, x, c.newerSchema)

			putBack(t, d, c.change(t, u.snapshot))
			out := d.RestartFailing(t, nil)
			for _, says := range append(c.says, "does not start") {
				if !strings.Contains(out, says) {
					t.Fatalf("the Drive did not say %q:\n%s", says, out)
				}
			}
			if strings.Contains(out, leftAlone) {
				t.Fatalf("the Drive left the newer copy alone:\n%s", out)
			}
			if data, ok := d.Storage.Get(t, u.newer); !ok || !bytes.Equal(data, u.data) {
				t.Fatal("the newer version's copy on the storage was changed or moved")
			}
		})
	}
}

// After an upgrade to a version of the same schema that failed, the newer version's copy on the storage, numbered after
// the data folder's, is the one the Drive starts from, as a later copy on the storage always is: what was given under
// the newer version stays, as the files put there under it do. Neither the storage nor its copy was undone.
func TestAfterAFailedUpgradeOfTheSameSchemaTheLaterCopyOnTheStorageWins(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: copiesSoon("300ms", "2s")})
	x, _, _ := sharedBeforeTheCopy(t, d)
	u := upgradeFailed(t, d, x, false)

	putBack(t, d, u.snapshot)
	d.Restart(t, nil)
	adopted(t, d, "storage")
	if got := logLines(d, "the Drive made it from the copy on the storage", "number="+strconv.FormatInt(u.seq, 10)); len(got) != 1 {
		t.Fatalf("the Drive did not make its index from the newer version's copy, number %d:\n%s", u.seq, d.Logs())
	}
	if got, want := sorted(sharedNames(t, d.As(dora))), sorted([]string{entry("Clients", x.place), entry("Secret.txt", x.place)}); !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me after an upgrade of the same schema was undone: %q, want %q", got, want)
	}
	if got := logLines(d, leftAlone); len(got) != 0 {
		t.Fatalf("a copy of the same schema was left alone: %q", got)
	}
}
