package index

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Copies of the index: what counts as an access change, what a copy holds and what clearing it leaves, and which
// copies CheckCopy lets become an index. The rest (when copies are made, put on the storage and adopted) is tested
// through HTTP in the test/ package.

// anywhere is a topFolder that allows every place's folder the Drive makes.
func anywhere(kind PlaceKind, folder string) bool {
	return strings.HasPrefix(folder, "My files/") || strings.HasPrefix(folder, "Shared folders/")
}

func changes(t *testing.T, x *Index) int64 {
	t.Helper()
	n, err := x.AccessChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Every change to who may reach what counts, whichever table it is in, and writing how often links and invitations
// were used does not. A migration that rebuilds one of these tables without making its triggers again fails here.
func TestAccessChangesAreCounted(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	counts := func(what string, change func()) {
		t.Helper()
		before := changes(t, x)
		change()
		if after := changes(t, x); after <= before {
			t.Errorf("%s was not counted as an access change (%d, then %d)", what, before, after)
		}
	}
	doesNotCount := func(what string, change func()) {
		t.Helper()
		before := changes(t, x)
		change()
		if after := changes(t, x); after != before {
			t.Errorf("%s was counted as an access change (%d, then %d)", what, before, after)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	var p Place
	counts("a My files made", func() { p = place(t, x, "u1") })
	var folder, file Item
	doesNotCount("a folder and a file added", func() {
		folder = put(t, x, p, 0, "Clients", true, 0)
		file = put(t, x, p, folder.ID, "Offer.pdf", false, 10)
	})
	counts("a share", func() {
		_, err := x.Give(ctx, p.ID, file.ID, Grantee{Member: "u2"}, "viewer", now, "u1", nil)
		must(err)
	})
	counts("a role changed", func() {
		_, err := x.Give(ctx, p.ID, file.ID, Grantee{Member: "u2"}, "editor", now, "u1", nil)
		must(err)
	})
	counts("a share taken away", func() { must(x.TakeAway(ctx, p.ID, file.ID, Grantee{Member: "u2"}, nil)) })
	var link Link
	counts("a link made", func() {
		var err error
		link, err = x.MakeLink(ctx, p.ID, file.ID, bytes.Repeat([]byte{1}, 32), "view", "u1", now, false)
		must(err)
	})
	doesNotCount("a link's uses written", func() { must(x.AddUses(ctx, map[int64]Use{link.ID: {Count: 3, Last: now}})) })
	counts("a link replaced", func() {
		_, err := x.MakeLink(ctx, p.ID, file.ID, bytes.Repeat([]byte{2}, 32), "view", "u1", now, true)
		must(err)
	})
	counts("a link removed", func() { must(x.RemoveLink(ctx, p.ID, file.ID)) })
	var upload Link
	counts("an upload-only link made", func() {
		var err error
		upload, err = x.MakeLink(ctx, p.ID, folder.ID, bytes.Repeat([]byte{4}, 32), UploadLink, "u1", now, false)
		must(err)
	})
	doesNotCount("files taken in through it", func() { must(x.TakenIn(ctx, upload.ID, 1000)) })
	doesNotCount("what it took in reset by its maker", func() {
		_, err := x.ResetLink(ctx, p.ID, folder.ID, "u1")
		must(err)
	})
	counts("what it took in reset by someone else, who becomes its maker", func() {
		l, err := x.ResetLink(ctx, p.ID, folder.ID, "u-dora")
		must(err)
		if l.MadeBy != "u-dora" {
			t.Fatalf("the maker of a link Dóra reset: %q", l.MadeBy)
		}
	})
	counts("its kind changed to view", func() {
		_, err := x.SetLinkKind(ctx, p.ID, folder.ID, ViewLink, "u-dora", false)
		must(err)
	})
	doesNotCount("its kind set to what it is", func() {
		_, err := x.SetLinkKind(ctx, p.ID, folder.ID, ViewLink, "u-dora", false)
		must(err)
	})
	counts("its kind changed back to upload-only", func() {
		_, err := x.SetLinkKind(ctx, p.ID, folder.ID, UploadLink, "u-dora", false)
		must(err)
	})
	counts("it removed", func() { must(x.RemoveLink(ctx, p.ID, folder.ID)) })
	var guest Guest
	counts("a guest invited", func() {
		var err error
		guest, _, err = x.Invite(ctx, p.ID, folder.ID, "anna@client.example", "viewer", time.Time{}, now, "u1")
		must(err)
	})
	grants, err := x.GrantsOn(ctx, p.ID, []int64{folder.ID})
	must(err)
	doesNotCount("an invitation's uses written", func() {
		must(x.AddInvitationUses(ctx, map[int64]Use{grants[0].ID: {Count: 1, Last: now}}))
	})
	doesNotCount("a guest's session started", func() {
		must(x.StartSession(ctx, guest.ID, bytes.Repeat([]byte{3}, 32), now, now.Add(time.Hour), 10))
	})
	counts("an invitation changed", func() {
		must(x.ChangeInvitation(ctx, p.ID, folder.ID, guest.ID, "editor", now.Add(time.Hour), "u-reka"))
	})
	counts("an item renamed", func() {
		_, err := x.Relocate(ctx, p.ID, file.ID, folder.ID, "Offer 2026.pdf")
		must(err)
	})
	counts("an item moved", func() {
		_, err := x.Relocate(ctx, p.ID, file.ID, 0, "Offer 2026.pdf")
		must(err)
	})
	counts("an item deleted", func() { must(x.Trash(ctx, p.ID, file.ID, now, "2026-11-20 0a0b0c0d", "u1")) })
	counts("an item restored", func() {
		_, err := x.Restore(ctx, p.ID, file.ID, 0, "Offer 2026.pdf")
		must(err)
	})
	var shared Place
	counts("a shared folder made", func() {
		var err error
		shared, err = x.AddSharedFolder(ctx, "Payroll", "Shared folders/Payroll", "u1", now)
		must(err)
	})
	counts("a shared folder deleted", func() { must(x.DeleteSharedFolder(ctx, shared.ID, now, "2026-11-20 01020304")) })
	counts("a shared folder restored", func() { must(x.RestoreSharedFolder(ctx, shared.ID)) })
	counts("sharing outside switched off", func() {
		_, err := x.SwitchOutside(ctx, 0, true, now, "u1")
		must(err)
	})
	counts("sharing outside switched on", func() {
		_, err := x.SwitchOutside(ctx, 0, false, now, "u1")
		must(err)
	})
	other := place(t, x, "u3")
	counts("an act on a held My files", func() {
		_, err := x.StartAct(ctx, Act{Kind: DeleteHeld, Place: other.ID, By: "u1", At: now})
		must(err)
	})
	counts("a guest gone with their last invitation", func() {
		must(x.TakeAway(ctx, p.ID, folder.ID, Grantee{Guest: guest.ID}, nil))
	})
	doesNotCount("the walk's cursor kept", func() { must(x.SetState(ctx, "walk_cursor", "1:0")) })
}

// writeCopy writes a copy of the index into a new file, and returns where.
func writeCopy(t *testing.T, x *Index) (string, CopyInfo) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "copy.sqlite")
	info, err := x.WriteCopy(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	return file, info
}

// A copy is one file, whole while the index is written to, numbered after the copy before it, and says which storage it
// belongs to and how many access changes it holds.
func TestACopyIsOneConsistentFile(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u1")
	if _, err := x.AddStorageID(ctx, "0123abcd"); err != nil {
		t.Fatal(err)
	}
	put(t, x, p, 0, "a.txt", false, 1)
	_, first := writeCopy(t, x)
	put(t, x, p, 0, "b.txt", false, 1)
	file, second := writeCopy(t, x)
	if first.Seq != 1 || second.Seq != 2 || second.StorageID != "0123abcd" || second.Version != Version() ||
		second.Changes != changes(t, x) {
		t.Fatalf("the copies say %+v and %+v", first, second)
	}
	for _, extra := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(file + extra); err == nil {
			t.Errorf("the copy has a %s file beside it", extra)
		}
	}
	info, sum, err := CheckCopy(ctx, file, anywhere)
	if err != nil || info != second || sum.Places != 1 || sum.Items != 2 {
		t.Fatalf("checking the copy: %+v %+v %v", info, sum, err)
	}
	// It opens as an index, with what its index held.
	dir := t.TempDir()
	if err := os.Rename(file, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	y, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer y.Close()
	if items, _ := y.Children(ctx, p.ID, 0); len(items) != 2 {
		t.Fatalf("the copy, opened as an index, holds %d items", len(items))
	}
}

// Clearing a copy for the storage leaves no trace of guests' sessions or codes in the file, not even in its free pages.
func TestAClearedCopyHoldsNoTraceOfWhatWasCleared(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u1")
	f := put(t, x, p, 0, "Offer.pdf", false, 1)
	now := time.Now()
	guest, _, err := x.Invite(ctx, p.ID, f.ID, "anna@client.example", "viewer", time.Time{}, now, "u1")
	if err != nil {
		t.Fatal(err)
	}
	session := bytes.Repeat([]byte{0x5e}, 32)
	if err := x.StartSession(ctx, guest.ID, session, now, now.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	code := bytes.Repeat([]byte{0xc0}, 32)
	if r, err := x.IssueCode(ctx, guest.ID, code, now, CodeLimits{ValidFor: 30 * time.Minute, Every: 5 * time.Minute, PerGuest: 12, Overall: 100, Window: time.Hour}); err != nil || r != Issued {
		t.Fatalf("issuing a code: %v %v", r, err)
	}
	link := bytes.Repeat([]byte{0x11}, 32)
	if _, err := x.MakeLink(ctx, p.ID, f.ID, link, "view", "u1", now, false); err != nil {
		t.Fatal(err)
	}
	if err := x.SetState(ctx, "walk_cursor", "walk-cursor-marker"); err != nil {
		t.Fatal(err)
	}
	file, _ := writeCopy(t, x)
	data, _ := os.ReadFile(file)
	if !bytes.Contains(data, session) || !bytes.Contains(data, code) {
		t.Fatal("the copy before clearing does not hold the session and the code: the test proves nothing")
	}
	if err := ClearCopy(ctx, file, Clearing{Outsiders: true, State: []string{"walk_cursor"}}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(file)
	for what, b := range map[string][]byte{"a session's hash": session, "a code's HMAC": code, "the walk's cursor": []byte("walk-cursor-marker")} {
		if bytes.Contains(data, b) {
			t.Errorf("the cleared copy still holds %s", what)
		}
	}
	if !bytes.Contains(data, link) {
		t.Error("the cleared copy lost the link's hash: the link would not work after it is adopted")
	}
	if _, _, err := CheckCopy(ctx, file, anywhere); err != nil {
		t.Fatalf("the cleared copy does not pass the check: %v", err)
	}
}

// Clearing can raise the number of a copy's last copy, never lower it: an index made from it numbers its next copy after
// the higher of the two. A value the copy keeps in drive_state reads back as it was set.
func TestClearingRaisesTheCopysNumber(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	if _, err := x.AddStorageID(ctx, "0123abcd"); err != nil {
		t.Fatal(err)
	}
	writeCopy(t, x)
	writeCopy(t, x)
	file, info := writeCopy(t, x) // number 3
	if err := ClearCopy(ctx, file, Clearing{SeqAtLeast: 2}); err != nil {
		t.Fatal(err)
	}
	if got, _, err := CheckCopy(ctx, file, anywhere); err != nil || got.Seq != info.Seq {
		t.Fatalf("a copy numbered %d, raised to at least 2: %+v %v", info.Seq, got, err)
	}
	if err := ClearCopy(ctx, file, Clearing{SeqAtLeast: 41, Set: map[string]string{"left_alone": "copy-41.sqlite"}}); err != nil {
		t.Fatal(err)
	}
	if got, _, err := CheckCopy(ctx, file, anywhere); err != nil || got.Seq != 41 {
		t.Fatalf("a copy raised to at least 41: %+v %v", got, err)
	}
	if v, err := CopyState(ctx, file, "left_alone"); err != nil || v != "copy-41.sqlite" {
		t.Fatalf("the copy's left_alone: %q %v", v, err)
	}
	if v, err := CopyState(ctx, file, "no_such_key"); err != nil || v != "" {
		t.Fatalf("a key the copy does not keep: %q %v", v, err)
	}
	if err := ClearCopy(ctx, file, Clearing{SeqAtLeast: MaxCopySeq + 1}); err == nil {
		t.Fatal("a copy was raised past the highest number a copy may have")
	}

	// Opened as an index, its next copy is numbered after it.
	dir := t.TempDir()
	if err := os.Rename(file, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	y, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer y.Close()
	if _, next := writeCopy(t, y); next.Seq != 42 {
		t.Fatalf("the next copy of an index made from a copy raised to 41 is number %d", next.Seq)
	}
}

// alter changes a copy in place, as someone with write access to the storage could.
func alter(t *testing.T, file string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// triggersOf names the triggers of the copy whose names are like pattern.
func triggersOf(t *testing.T, file, pattern string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT name FROM sqlite_schema WHERE type = 'trigger' AND name LIKE ?", pattern)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	return names
}

// Only a whole copy, of a schema this Drive knows exactly, with places where the Drive puts them, passes.
func TestOnlyACopyTheDriveMadePasses(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u1")
	put(t, x, p, 0, "a.txt", false, 1)
	if _, err := x.AddSharedFolder(ctx, "Payroll", "Shared folders/Payroll", "u1", time.Now()); err != nil {
		t.Fatal(err)
	}
	fresh := func() string {
		file, _ := writeCopy(t, x)
		return file
	}
	cases := []struct {
		what   string
		change func(file string)
		want   error
	}{
		{"made by a newer Drive", func(f string) { alter(t, f, "PRAGMA user_version = 999") }, ErrCopyNewer},
		{"not an SQLite file", func(f string) { os.WriteFile(f, []byte("not a database"), 0o600) }, ErrCopyDamaged},
		{"cut short", func(f string) {
			data, _ := os.ReadFile(f)
			os.WriteFile(f, data[:len(data)/2], 0o600)
		}, ErrCopyDamaged},
		{"pages overwritten", func(f string) {
			data, _ := os.ReadFile(f)
			for i := 4096; i < len(data); i += 512 {
				data[i] ^= 0xff
			}
			os.WriteFile(f, data, 0o600)
		}, ErrCopyDamaged},
		{"a trigger of its own", func(f string) {
			alter(t, f, "CREATE TRIGGER keep_links AFTER DELETE ON links BEGIN INSERT INTO links SELECT OLD.*; END")
		}, ErrCopyDamaged},
		{"a check taken off", func(f string) { alter(t, f, "DROP INDEX grants_one_per_grantee") }, ErrCopyDamaged},
		{"numbered past any copy", func(f string) {
			alter(t, f, "UPDATE drive_state SET value = '9223372036854775807' WHERE key = 'copy_seq'")
		}, ErrCopyDamaged},
		{"a My files in the Drive's hidden folder", func(f string) {
			alter(t, f, "UPDATE places SET folder = '.superit-drive' WHERE kind = 'my-files'")
		}, ErrCopyDamaged},
		{"a shared folder above the Drive's folder", func(f string) {
			alter(t, f, "UPDATE places SET folder = 'Shared folders/../..' WHERE kind = 'shared-folder'")
		}, ErrCopyDamaged},
	}
	topFolder := func(kind PlaceKind, folder string) bool {
		top := map[PlaceKind]string{MyFiles: "My files/", SharedFolder: "Shared folders/"}[kind]
		name, ok := strings.CutPrefix(folder, top)
		return ok && name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
	}
	if _, _, err := CheckCopy(ctx, fresh(), topFolder); err != nil {
		t.Fatalf("a copy as the Drive made it: %v", err)
	}
	for _, c := range cases {
		file := fresh()
		c.change(file)
		if _, _, err := CheckCopy(ctx, file, topFolder); !errors.Is(err, c.want) {
			t.Errorf("a copy %s: got %v, want %v", c.what, err, c.want)
		}
	}
}

// olderCopy is an index as the migrations up to upTo left it, with what statements put into it, in a file of its own:
// a copy made by an older Drive.
func olderCopy(t *testing.T, upTo int, statements ...string) string {
	t.Helper()
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "older.sqlite")
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateConn(ctx, conn, upTo); err != nil {
		t.Fatal(err)
	}
	for _, s := range statements {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

// A copy made by an older Drive passes, and opens as an index brought up to date: as migration 0010 left it (before
// access changes were counted, and before upload-only links), with a public link made then and one removed since.
func TestAnOlderCopyIsBroughtUpToDate(t *testing.T) {
	ctx := context.Background()
	file := olderCopy(t, 10,
		"INSERT INTO places (id, kind, owner, folder, created_at) VALUES (1, 'my-files', 'u1', 'My files/u1', 0)",
		"INSERT INTO items (id, place_id, name, is_folder, size, changed_at) VALUES (1, 1, 'a.txt', 0, 1, 0)",
		"INSERT INTO items (id, place_id, name, is_folder, size, changed_at) VALUES (2, 1, 'Clients', 1, 0, 0)",
		"INSERT INTO items (id, place_id, name, is_folder, size, changed_at) VALUES (3, 1, 'Bakery', 1, 0, 0)",
		"INSERT INTO links (item_id, token_hash, kind, made_at, made_by, uses) VALUES (2, randomblob(32), 'view', 1, 'u1', 4)",
		"INSERT INTO links (item_id, token_hash, kind, made_at, made_by) VALUES (3, randomblob(32), 'view', 2, 'u1')",
		"DELETE FROM links WHERE item_id = 3")
	info, _, err := CheckCopy(ctx, file, anywhere)
	if err != nil || info.Version != 10 {
		t.Fatalf("an older copy: %+v %v", info, err)
	}
	dir := t.TempDir()
	if err := os.Rename(file, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	y, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer y.Close()
	if n, err := y.AccessChanges(ctx); err != nil || n != 0 {
		t.Fatalf("the older copy brought up to date counts %d access changes (%v)", n, err)
	}
	if items, _ := y.Children(ctx, 1, 0); len(items) != 3 {
		t.Fatalf("the older copy, brought up to date, holds %d items", len(items))
	}
	// Its link is as it was, a view link that has taken in nothing; a new one never gets the id of the one removed.
	l, err := y.LinkOn(ctx, 2)
	if err != nil || l.ID != 1 || l.Kind != ViewLink || l.Uses != 4 || l.TakenIn != 0 || !l.Folder {
		t.Fatalf("the link brought up to date: %+v %v", l, err)
	}
	fresh, err := y.MakeLink(ctx, 1, 3, bytes.Repeat([]byte{9}, 32), UploadLink, "u1", time.Now(), false)
	if err != nil || fresh.ID != 3 || fresh.Kind != UploadLink {
		t.Fatalf("a link made after the upgrade: %+v %v, want id 3", fresh, err)
	}
	if n, _ := y.AccessChanges(ctx); n != 1 {
		t.Fatalf("a link made after the upgrade counts %d access changes", n)
	}
}
