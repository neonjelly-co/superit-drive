package index

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// These cover the rules the index itself keeps: ids that last and are never reused, lookups that never leave a place
// or reach into the trash. Everything else is tested through HTTP in the test/ package.

func open(t *testing.T) *Index {
	t.Helper()
	x, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x
}

func place(t *testing.T, x *Index, owner string) Place {
	t.Helper()
	p, err := x.AddMyFiles(context.Background(), owner, "My files/"+owner)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func put(t *testing.T, x *Index, p Place, folderID int64, name string, folder bool, size int64) Item {
	t.Helper()
	it, err := x.PutItem(context.Background(), p.ID, folderID, Seen{Name: name, Folder: folder, Size: size, Changed: time.Now()}, time.Now(), AddedBy{})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// look is one look at a folder of the place on the Drive's own storage (folderID 0: its top folder, path the names down
// to it), made at the time given, that found seen: what has been missing for ten minutes and is not seen is taken as
// found gone at a second look too, as the Drive's Stat would.
func look(t *testing.T, x *Index, p Place, folderID int64, path []string, seen []Seen, at time.Time) Outcome {
	t.Helper()
	ctx := context.Background()
	old := at.Add(-10 * time.Minute)
	gone, err := x.MissingIn(ctx, p.ID, folderID, old, 100)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := map[int64]time.Time{}
	for _, m := range gone {
		if !slices.ContainsFunc(seen, func(s Seen) bool { return s.Name == m.Name }) {
			confirmed[m.ID] = m.Since
		}
	}
	never := func() bool { return false }
	o, err := x.Reconcile(ctx, p.ID, folderID, Reread{Path: path, Seen: seen, Leave: func(string) bool { return false },
		Busy: never, Stale: never, At: at, Hide: true, Old: old, Confirmed: confirmed})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// missingNamed finds the missing item called name in the place's top folder.
func missingNamed(t *testing.T, x *Index, p Place, name string) Missing {
	t.Helper()
	all, err := x.MissingIn(context.Background(), p.ID, 0, time.Now().Add(24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no missing item called %q", name)
	return Missing{}
}

// gone is what the Drive does with what the storage no longer shows in a folder: one look marks it missing, and a
// second look, more than ten minutes later, drops it.
func gone(t *testing.T, x *Index, p Place, folderID int64, path []string, seen []Seen) {
	t.Helper()
	look(t, x, p, folderID, path, seen, time.Now())
	look(t, x, p, folderID, path, seen, time.Now().Add(11*time.Minute))
}

// An index made before folders and the trash keeps its items and their ids, and new ids follow the old ones.
func TestTheMigrationKeepsItemsAndTheirIds(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := migrations.ReadFile("migrations/0001_places_and_items.sql")
	for _, q := range []string{
		string(first),
		"PRAGMA user_version = 1",
		"INSERT INTO places (id, kind, owner, folder, created_at) VALUES (1, 'my-files', 'u-reka', 'My files/reka@example.com', 0)",
		"INSERT INTO items (id, place_id, parent_id, name, is_folder, size, changed_at) VALUES (7, 1, NULL, 'Drafts', 1, 0, 0)",
		"INSERT INTO items (id, place_id, parent_id, name, is_folder, size, changed_at) VALUES (9, 1, 7, 'Offer.docx', 0, 84, 0)",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	x, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	// The items outlive the rebuild of places (0003), which drops the old table: with foreign keys on, that would have
	// deleted every item with it.
	offer, err := x.Item(ctx, 1, 9)
	if err != nil || offer.Name != "Offer.docx" || offer.Size != 84 || len(offer.Above) != 1 || offer.Above[0].ID != 7 {
		t.Fatalf("got %+v, %v", offer, err)
	}
	p, err := x.PlaceByID(ctx, 1)
	if err != nil || p.Kind != MyFiles || p.Owner != "u-reka" || p.Folder != "My files/reka@example.com" || p.Name != "" || !p.Deleted.IsZero() {
		t.Fatalf("Réka's My files after the migrations: %+v, %v", p, err)
	}
	if n := put(t, x, p, 0, "New.txt", false, 1); n.ID <= 9 {
		t.Fatalf("a new item got id %d, which is not after the old ones", n.ID)
	}
	// And foreign keys are enforced again once the migrations are done.
	if _, err := x.db.ExecContext(ctx, "INSERT INTO items (place_id, name, is_folder, changed_at) VALUES (999, 'x', 0, 0)"); err == nil {
		t.Fatal("an item of a place that does not exist was accepted: foreign keys are off")
	}
	if _, err := x.db.ExecContext(ctx, "INSERT INTO grants (place_id, grantee, member, role, given_at) VALUES (999, 'member', 'u', 'manager', 0)"); err == nil {
		t.Fatal("a role on a place that does not exist was accepted: foreign keys are off")
	}
}

// The managers given before sharing (0003) are still managers of the same shared folders after it (0004).
func TestTheMigrationToSharingKeepsTheManagers(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_places_and_items.sql", "0002_folders_and_trash.sql", "0003_shared_folders.sql"} {
		script, _ := migrations.ReadFile("migrations/" + name)
		if _, err := db.ExecContext(ctx, string(script)); err != nil {
			t.Fatal(name, err)
		}
	}
	for _, q := range []string{
		"PRAGMA user_version = 3",
		"INSERT INTO places (id, kind, name, folder, created_at) VALUES (4, 'shared-folder', 'Clients', 'Shared folders/Clients', 0)",
		"INSERT INTO grants (place_id, member, role, given_at) VALUES (4, 'u-bence', 'manager', 1000), (4, 'u-dora', 'manager', 2000)",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	x, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	got, err := x.GrantsOn(ctx, 4, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("the grants after the migration: %+v, %v", got, err)
	}
	for i, member := range []string{"u-bence", "u-dora"} {
		if g := got[i]; g.Item != 0 || g.Everyone || g.Member != member || g.Role != "manager" || g.Given.UnixMilli() != int64(1000*(i+1)) || g.By != "" {
			t.Errorf("grant %d: %+v", i, g)
		}
	}
}

// A shared folder is a place owned by the project: a name and a folder, no owner, and its maker as its first manager.
// In the trash it is found only as a deleted place, keeps its items and roles, and its folder stays held.
func TestASharedFolderAndItsManagers(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	clients, err := x.AddSharedFolder(ctx, "Clients", "Shared folders/Clients", "u-bence", now)
	if err != nil {
		t.Fatal(err)
	}
	if clients.Kind != SharedFolder || clients.Name != "Clients" || clients.Owner != "" || clients.Folder != "Shared folders/Clients" {
		t.Fatalf("got %+v", clients)
	}
	if got, _ := x.GrantsOn(ctx, clients.ID, nil); len(got) != 1 || got[0].Member != "u-bence" || got[0].Role != "manager" || got[0].By != "u-bence" {
		t.Fatalf("the maker's role: %+v, want manager", got)
	}
	// Two places never hold the same folder.
	if _, err := x.AddSharedFolder(ctx, "Clients", "Shared folders/Clients", "u-dora", now); err == nil {
		t.Fatal("a second place with the same folder was recorded")
	}
	if taken, _ := x.FolderTaken(ctx, "Shared folders/Clients"); !taken {
		t.Fatal("the folder is not taken")
	}
	ledger := put(t, x, clients, 0, "Ledger.xlsx", false, 40)

	dora := Grantee{Member: "u-dora"}
	if _, err := x.Give(ctx, clients.ID, 0, dora, "manager", now, "u-bence", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Give(ctx, clients.ID, 0, dora, "owner", now, "u-bence", nil); err == nil {
		t.Fatal("a role the index does not hold was given")
	}
	if given, _ := x.TopGrantsFor(ctx, "u-dora"); len(given[clients.ID]) != 1 || given[clients.ID][0].Role != "manager" || len(given) != 1 {
		t.Fatalf("Dóra's roles: %v", given)
	}
	// Taking a role away shows what would be left, and the check can refuse.
	refused := errors.New("refused")
	err = x.TakeAway(ctx, clients.ID, 0, Grantee{Member: "u-bence"}, func(left []Grant) error {
		if len(left) != 1 || left[0].Member != "u-dora" {
			t.Errorf("left: %+v", left)
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("got %v, want the check's refusal", err)
	}
	if got, _ := x.GrantsOn(ctx, clients.ID, nil); len(got) != 2 {
		t.Fatal("a refused take-away took the role away")
	}
	if err := x.TakeAway(ctx, clients.ID, 0, Grantee{Member: "u-bence"}, nil); err != nil {
		t.Fatal(err)
	}
	if grants, _ := x.GrantsOn(ctx, clients.ID, nil); len(grants) != 1 || grants[0].Member != "u-dora" {
		t.Fatalf("roles left: %+v", grants)
	}

	// In the trash.
	if err := x.DeleteSharedFolder(ctx, clients.ID, now, "2026-11-19 0badc0de"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.PlaceByID(ctx, clients.ID); !errors.Is(err, ErrNoPlace) {
		t.Fatalf("a shared folder in the trash was found as a place: %v", err)
	}
	if shared, _ := x.SharedFolders(ctx); len(shared) != 0 {
		t.Fatalf("shared folders: %+v", shared)
	}
	d, err := x.DeletedPlace(ctx, clients.ID)
	if err != nil || d.Entry != "2026-11-19 0badc0de" || d.Deleted.IsZero() {
		t.Fatalf("the deleted shared folder: %+v, %v", d, err)
	}
	if entries, _ := x.TrashEntries(ctx, clients.ID); !entries["2026-11-19 0badc0de"] {
		t.Fatalf("its entry is not known to the trash: %v", entries)
	}
	if taken, _ := x.FolderTaken(ctx, "Shared folders/Clients"); !taken {
		t.Fatal("a shared folder in the trash no longer holds its folder")
	}
	if _, err := x.DeletedPlace(ctx, ledger.PlaceID); err != nil {
		t.Fatal(err)
	}
	if err := x.RestoreSharedFolder(ctx, clients.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := x.Item(ctx, clients.ID, ledger.ID); err != nil || got.ID != ledger.ID {
		t.Fatalf("Ledger.xlsx after the restore: %+v, %v", got, err)
	}

	// Removed for good: its items and roles go with it, and its id is never given to another place.
	if err := x.RemoveSharedFolder(ctx, clients.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.PlaceByID(ctx, clients.ID); err != nil {
		t.Fatal("a shared folder that is not in the trash was removed for good")
	}
	if err := x.DeleteSharedFolder(ctx, clients.ID, now.Add(-time.Hour), "e"); err != nil {
		t.Fatal(err)
	}
	if due, _ := x.SharedFoldersDeletedBefore(ctx, now); len(due) != 1 || due[0].ID != clients.ID {
		t.Fatalf("deleted before now: %+v", due)
	}
	if err := x.RemoveSharedFolder(ctx, clients.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.DeletedPlace(ctx, clients.ID); !errors.Is(err, ErrNoPlace) {
		t.Fatalf("still there: %v", err)
	}
	if grants, _ := x.GrantsOn(ctx, clients.ID, nil); len(grants) != 0 {
		t.Fatalf("its roles outlived it: %+v", grants)
	}
	if used, _ := x.Used(ctx); used != 0 {
		t.Fatalf("its files outlived it: %d bytes", used)
	}
	again, err := x.AddSharedFolder(ctx, "Clients", "Shared folders/Clients", "u-dora", now)
	if err != nil || again.ID <= clients.ID {
		t.Fatalf("a new shared folder got id %d after %d (%v)", again.ID, clients.ID, err)
	}
}

// Access will hang on item ids, and a page can still hold the id of an item that is gone: an id is never given to a
// second item, even when the first is removed for good and it was the newest.
func TestAnIdIsNeverGivenToASecondItem(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	a := put(t, x, p, 0, "a.txt", false, 1)
	if err := x.Trash(ctx, p.ID, a.ID, time.Now(), "entry", "u-reka"); err != nil {
		t.Fatal(err)
	}
	if err := x.RemoveFromTrash(ctx, p.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if b := put(t, x, p, 0, "a.txt", false, 1); b.ID == a.ID {
		t.Fatalf("the id %d was given to a second item", a.ID)
	}
	// Nor when an item the storage no longer has is dropped.
	c := put(t, x, p, 0, "c.txt", false, 1)
	gone(t, x, p, 0, nil, nil)
	if d := put(t, x, p, 0, "d.txt", false, 1); d.ID == c.ID {
		t.Fatalf("the id %d was given to a second item", c.ID)
	}
}

func TestAnItemIsFoundOnlyInItsPlaceAndNotInTheTrash(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	reka, bence := place(t, x, "u-reka"), place(t, x, "u-bence")
	drafts := put(t, x, reka, 0, "Drafts", true, 0)
	y2026 := put(t, x, reka, drafts.ID, "2026", true, 0)
	offer := put(t, x, reka, y2026.ID, "Offer.docx", false, 84)

	got, err := x.Item(ctx, reka.ID, offer.ID)
	if err != nil || got.ParentID != y2026.ID || len(got.Above) != 2 || got.Above[0].ID != drafts.ID || got.Above[1].ID != y2026.ID {
		t.Fatalf("got %+v, %v; want Offer.docx under Drafts/2026", got, err)
	}
	// The folder it is in can be used on its own: it knows the folders above it too.
	if in := got.Above[1]; len(in.Above) != 1 || in.Above[0].ID != drafts.ID || len(got.Above[0].Above) != 0 {
		t.Fatalf("the folders above Offer.docx: %+v", got.Above)
	}
	if _, err := x.Item(ctx, bence.ID, offer.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("found through another place: %v", err)
	}

	// Deleting Drafts takes everything in it along, out of reach until it comes back, with the same ids.
	if err := x.Trash(ctx, reka.ID, drafts.ID, time.Now(), "entry", "u-reka"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{drafts.ID, y2026.ID, offer.ID} {
		if _, err := x.Item(ctx, reka.ID, id); !errors.Is(err, ErrNoItem) {
			t.Errorf("item %d in the trash was found: %v", id, err)
		}
	}
	if _, err := x.TrashItem(ctx, reka.ID, offer.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("something inside a folder in the trash was found as a trash item: %v", err)
	}
	if top, _ := x.Children(ctx, reka.ID, 0); len(top) != 0 {
		t.Errorf("the top folder still lists %+v", top)
	}
	back, err := x.Restore(ctx, reka.ID, drafts.ID, 0, "Drafts")
	if err != nil || back.ID != drafts.ID {
		t.Fatalf("restored %+v, %v", back, err)
	}
	if got, err := x.Item(ctx, reka.ID, offer.ID); err != nil || got.ID != offer.ID {
		t.Fatalf("Offer.docx after the restore: %+v, %v", got, err)
	}
}

// Removing a folder from the trash for good removes what is in it, but not what was deleted out of it earlier: that
// has its own place in the trash and its own 30 days.
func TestRemovingAFolderForGoodLeavesWhatWasDeletedOutOfItEarlier(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	drafts := put(t, x, p, 0, "Drafts", true, 0)
	early := put(t, x, p, drafts.ID, "Early.txt", false, 5)
	inside := put(t, x, p, drafts.ID, "Inside.txt", false, 7)
	if err := x.Trash(ctx, p.ID, early.ID, time.Now().Add(-time.Hour), "e1", "u-reka"); err != nil {
		t.Fatal(err)
	}
	if err := x.Trash(ctx, p.ID, drafts.ID, time.Now(), "e2", "u-reka"); err != nil {
		t.Fatal(err)
	}
	trash, _ := x.TrashItems(ctx, p.ID)
	if len(trash) != 2 || trash[0].ID != drafts.ID || trash[0].Size != 7 || trash[1].ID != early.ID || trash[1].RestoreTo != drafts.ID {
		t.Fatalf("the trash holds %+v", trash)
	}
	if used, _ := x.Used(ctx); used != 12 {
		t.Errorf("used %d, want 12: the trash counts", used)
	}
	if err := x.RemoveFromTrash(ctx, p.ID, drafts.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.TrashItem(ctx, p.ID, inside.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("Inside.txt outlived its folder: %v", err)
	}
	left, err := x.TrashItem(ctx, p.ID, early.ID)
	if err != nil || left.RestoreTo != 0 {
		t.Fatalf("Early.txt: %+v, %v; want it still in the trash, to go back to the top", left, err)
	}
	if used, _ := x.Used(ctx); used != 5 {
		t.Errorf("used %d, want 5", used)
	}
}

func TestALookLeavesTheIndexAloneWhenTheFolderChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	a := put(t, x, p, 0, "a.txt", false, 1)
	drafts := put(t, x, p, 0, "Drafts", true, 0)
	seen := []Seen{{Name: "b.txt", Size: 2}}
	never := func() bool { return false }
	always := func() bool { return true }
	none := func(string) bool { return false }
	for what, r := range map[string]Reread{
		"stale": {Seen: seen, Leave: none, Busy: never, Stale: always, At: time.Now(), Hide: true},
		"busy":  {Seen: seen, Leave: none, Busy: always, Stale: never, At: time.Now(), Hide: true},
	} {
		if _, err := x.Reconcile(ctx, p.ID, 0, r); !errors.Is(err, ErrChanged) {
			t.Fatalf("%s: %v, want ErrChanged", what, err)
		}
	}
	// A folder no longer at the path the look read is left alone.
	if _, err := x.Reconcile(ctx, p.ID, drafts.ID, Reread{Path: []string{"Old name"}, Seen: seen, Leave: none, Busy: never,
		Stale: never, At: time.Now(), Hide: true}); !errors.Is(err, ErrChanged) {
		t.Fatalf("a folder renamed meanwhile: %v, want ErrChanged", err)
	}
	if items, _ := x.Children(ctx, p.ID, drafts.ID); len(items) != 0 {
		t.Fatalf("a stale look changed Drafts: %+v", items)
	}
	items, _ := x.Children(ctx, p.ID, 0)
	if len(items) != 2 || items[1].ID != a.ID {
		t.Fatalf("a stale look changed the folder: %+v", items)
	}
	// Names being written are left alone; the rest follows the storage.
	keep := func(name string) bool { return name == "a.txt" || name == "Drafts" }
	if _, err := x.Reconcile(ctx, p.ID, 0, Reread{Seen: seen, Leave: keep, Busy: never, Stale: never, At: time.Now(), Hide: true}); err != nil {
		t.Fatal(err)
	}
	items, _ = x.Children(ctx, p.ID, 0)
	if got := namesOf(items); !slices.Equal(got, []string{"Drafts", "a.txt", "b.txt"}) || items[1].ID != a.ID {
		t.Fatalf("got %+v", items)
	}
	// A folder where a file was is a new item, with a new id.
	look(t, x, p, 0, nil, []Seen{{Name: "a.txt", Folder: true}, {Name: "Drafts", Folder: true}}, time.Now())
	items, _ = x.Children(ctx, p.ID, 0)
	if len(items) != 2 || items[1].Name != "a.txt" || !items[1].Folder || items[1].ID == a.ID {
		t.Fatalf("got %+v", items)
	}
}

// What a look does not find is hidden at once, but dropped only by a look at least ten minutes later that confirms it;
// found again before, exactly as it was, it is back with its id. Anything else under its name, and what the Drive
// itself puts there, is a new item.
func TestAnItemGoneFromTheStorageIsHiddenThenDropped(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	t0 := time.Now()
	changed := t0.Add(-time.Hour).Truncate(time.Second)
	both := []Seen{{Name: "Drafts", Folder: true, Changed: changed}, {Name: "Notes.txt", Size: 5, Changed: changed}}
	look(t, x, p, 0, nil, both, t0)
	top, _ := x.Children(ctx, p.ID, 0)
	drafts, notes := top[0], top[1]
	offer := put(t, x, p, drafts.ID, "Offer.pdf", false, 84)
	if _, err := x.Give(ctx, p.ID, notes.ID, Grantee{Everyone: true}, "viewer", time.Now(), "u-reka", nil); err != nil {
		t.Fatal(err)
	}

	// Drafts is gone: hidden, and so is everything in it, but kept.
	if o := look(t, x, p, 0, nil, both[1:], t0); o.Hidden != 1 || o.Dropped != 0 {
		t.Fatalf("first look: %+v", o)
	}
	for _, id := range []int64{drafts.ID, offer.ID} {
		if _, err := x.Item(ctx, p.ID, id); !errors.Is(err, ErrNoItem) {
			t.Errorf("item %d of a missing folder is found: %v", id, err)
		}
	}
	if got, _ := x.Children(ctx, p.ID, 0); len(got) != 1 || got[0].ID != notes.ID {
		t.Fatalf("the top folder lists %+v", got)
	}
	// Seen again five minutes later, as it was: back, the same items.
	if o := look(t, x, p, 0, nil, both, t0.Add(5*time.Minute)); o.Back != 1 {
		t.Fatalf("Drafts back: %+v", o)
	}
	if got, err := x.Item(ctx, p.ID, offer.ID); err != nil || got.Above[0].ID != drafts.ID {
		t.Fatalf("Offer.pdf after Drafts came back: %+v, %v", got, err)
	}
	// A folder of that name made anew (it changed since) is another folder; the missing one is dropped in its time.
	look(t, x, p, 0, nil, both[1:], t0.Add(6*time.Minute))
	remade := []Seen{{Name: "Drafts", Folder: true, Changed: changed.Add(time.Hour)}, both[1]}
	if o := look(t, x, p, 0, nil, remade, t0.Add(7*time.Minute)); o.Back != 0 || o.Added != 1 {
		t.Fatalf("Drafts made anew: %+v", o)
	}
	if _, err := x.Item(ctx, p.ID, offer.ID); !errors.Is(err, ErrNoItem) {
		t.Fatalf("Offer.pdf is found in the new Drafts: %v", err)
	}
	if o := look(t, x, p, 0, nil, remade, t0.Add(17*time.Minute)); o.Dropped != 1 {
		t.Fatalf("the old Drafts, ten minutes on: %+v", o)
	}
	both[0] = remade[0]

	// Notes.txt gone, then a look nine minutes later: still kept, with its grant.
	look(t, x, p, 0, nil, both[:1], t0.Add(time.Minute))
	if o := look(t, x, p, 0, nil, both[:1], t0.Add(10*time.Minute)); o.Dropped != 0 {
		t.Fatalf("dropped after nine minutes: %+v", o)
	}
	if got, _ := x.GrantsOn(ctx, p.ID, []int64{notes.ID}); len(got) != 1 {
		t.Fatalf("the grant on Notes.txt while it is missing: %+v", got)
	}
	// The Drive uploads a new Notes.txt there meanwhile: a new item, with nothing given on it.
	fresh := put(t, x, p, 0, "Notes.txt", false, 9)
	if fresh.ID == notes.ID {
		t.Fatal("an upload took over a missing item's id")
	}
	if got, _ := x.GrantsOn(ctx, p.ID, []int64{fresh.ID}); len(got) != 0 {
		t.Fatalf("the new Notes.txt has %+v", got)
	}
	// Eleven minutes on: the missing one is dropped, with its grant; the new one stays.
	if o := look(t, x, p, 0, nil, []Seen{both[0], {Name: "Notes.txt", Size: 9, Changed: fresh.Changed}}, t0.Add(12*time.Minute)); o.Dropped != 1 {
		t.Fatalf("a look eleven minutes on: %+v", o)
	}
	var grants int
	if err := x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM grants WHERE item_id = ?", notes.ID).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("%d grants outlived the dropped item (%v)", grants, err)
	}
	if got, _ := x.Children(ctx, p.ID, 0); len(got) != 2 || got[1].ID != fresh.ID {
		t.Fatalf("the top folder lists %+v", got)
	}

	// Without a second look that confirms it, a missing item is never dropped, however long it has been missing.
	notesNow := []Seen{{Name: "Notes.txt", Size: 9, Changed: fresh.Changed}}
	look(t, x, p, 0, nil, notesNow, t0.Add(18*time.Minute)) // the new Drafts missing
	newDrafts := missingNamed(t, x, p, "Drafts")
	never := func() bool { return false }
	if o, err := x.Reconcile(ctx, p.ID, 0, Reread{Seen: notesNow, Leave: func(string) bool { return false },
		Busy: never, Stale: never, At: t0.Add(time.Hour), Hide: true, Old: t0.Add(50 * time.Minute)}); err != nil || o.Dropped != 0 {
		t.Fatalf("unconfirmed: %+v, %v", o, err)
	}
	// Nor when the storage may not be the Drive's own (Hide false): nothing is hidden or dropped then.
	if o, err := x.Reconcile(ctx, p.ID, 0, Reread{Leave: func(string) bool { return false }, Busy: never, Stale: never,
		At: t0.Add(2 * time.Hour), Old: t0.Add(time.Hour), Confirmed: map[int64]time.Time{newDrafts.ID: t0.Add(18 * time.Minute)}}); err != nil || o != (Outcome{}) {
		t.Fatalf("an untrusted look: %+v, %v", o, err)
	}
	if got, _ := x.Children(ctx, p.ID, 0); len(got) != 1 || got[0].ID != fresh.ID {
		t.Fatalf("the top folder lists %+v", got)
	}
}

// A grant on an item hangs on its id: it lasts through rename, move and the trash, goes when the item is removed for
// good or the storage no longer has it, and is never given on an item of another place or one in the trash.
func TestAGrantOnAnItemLivesAndGoesWithIt(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence, reka := place(t, x, "u-bence"), place(t, x, "u-reka")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	offer := put(t, x, bence, clients.ID, "Offer.pdf", false, 84)
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)
	toReka := Grantee{Member: "u-reka"}
	everyone := Grantee{Everyone: true}

	if added, err := x.Give(ctx, bence.ID, clients.ID, toReka, "viewer", now, "u-bence", nil); err != nil || !added {
		t.Fatalf("a first grant to Réka: added %v, %v", added, err)
	}
	if _, err := x.Give(ctx, bence.ID, offer.ID, everyone, "editor", now, "u-bence", nil); err != nil {
		t.Fatal(err)
	}
	// "everyone" is not a member, even one whose user id is "everyone".
	if _, err := x.Give(ctx, bence.ID, offer.ID, Grantee{Member: "everyone"}, "viewer", now, "u-bence", nil); err != nil {
		t.Fatal(err)
	}
	// Giving again replaces the role, and who gave it.
	if added, err := x.Give(ctx, bence.ID, clients.ID, toReka, "editor", now.Add(time.Second), "u-dora", nil); err != nil || added {
		t.Fatalf("Réka's role changed: added %v, %v", added, err)
	}
	got, err := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, offer.ID})
	if err != nil || len(got) != 3 {
		t.Fatalf("grants on Clients and Offer.pdf: %+v, %v", got, err)
	}
	if g := got[2]; g.Item != clients.ID || g.Member != "u-reka" || g.Role != "editor" || g.By != "u-dora" {
		t.Fatalf("Réka's grant after giving again: %+v", g)
	}
	if g := got[0]; g.Item != offer.ID || !g.Everyone || g.Member != "" || g.Role != "editor" {
		t.Fatalf("everyone's grant: %+v", g)
	}
	if g := got[1]; g.Everyone || g.Member != "everyone" || g.Role != "viewer" {
		t.Fatalf("the member called everyone: %+v", g)
	}
	if inside, _ := x.GrantsInside(ctx, bence.ID, 0); len(inside) != 1 || inside[0].Item != clients.ID {
		t.Fatalf("grants on what is in the top folder: %+v", inside)
	}
	if err := x.TakeAway(ctx, bence.ID, offer.ID, Grantee{Member: "everyone"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{offer.ID}); len(got) != 1 || !got[0].Everyone {
		t.Fatalf("taking the member called everyone's grant took: %+v", got)
	}

	// Not on an item of another place, through Give or behind its back; not on one in the trash.
	if _, err := x.Give(ctx, reka.ID, offer.ID, toReka, "editor", now, "u-reka", nil); !errors.Is(err, ErrNoItem) {
		t.Fatalf("a grant on Bence's item through Réka's place: %v", err)
	}
	if _, err := x.db.ExecContext(ctx, "INSERT INTO grants (place_id, item_id, grantee, member, role, given_at) VALUES (?, ?, 'member', 'u-reka', 'editor', 0)", reka.ID, offer.ID); err == nil {
		t.Fatal("the index holds a grant in one place on an item of another")
	}
	if _, err := x.db.ExecContext(ctx, "INSERT INTO grants (place_id, item_id, grantee, member, role, given_at) VALUES (?, ?, 'member', 'u-reka', 'manager', 0)", bence.ID, notes.ID); err == nil {
		t.Fatal("the index holds a manager on an item")
	}
	if _, err := x.db.ExecContext(ctx, "INSERT INTO grants (place_id, item_id, grantee, member, role, given_at) VALUES (?, ?, 'everyone', 'u-reka', 'viewer', 0)", bence.ID, notes.ID); err == nil {
		t.Fatal("the index holds a grant to everyone and a member at once")
	}
	if err := x.Trash(ctx, bence.ID, clients.ID, now, "e1", "u-dora"); err != nil {
		t.Fatal(err)
	}
	// The trash knows who put an item there, until it comes back.
	if tr, err := x.TrashItem(ctx, bence.ID, clients.ID); err != nil || tr.DeletedBy != "u-dora" {
		t.Fatalf("Clients in the trash: %+v, %v", tr, err)
	}
	for _, it := range []Item{clients, offer} {
		if _, err := x.Give(ctx, bence.ID, it.ID, Grantee{Member: "u-dora"}, "viewer", now, "u-bence", nil); !errors.Is(err, ErrNoItem) {
			t.Fatalf("a grant on %s in the trash: %v", it.Name, err)
		}
	}

	// Through rename, move, the trash and restore, the grants stay with the items.
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, offer.ID}); len(got) != 2 {
		t.Fatalf("grants while in the trash: %+v", got)
	}
	if _, err := x.Restore(ctx, bence.ID, clients.ID, 0, "Clients (2)"); err != nil {
		t.Fatal(err)
	}
	if err := x.Trash(ctx, bence.ID, clients.ID, now, "e3", ""); err != nil {
		t.Fatal(err)
	}
	if tr, err := x.TrashItem(ctx, bence.ID, clients.ID); err != nil || tr.DeletedBy != "" {
		t.Fatalf("Clients in the trash again, by someone not known: %+v, %v", tr, err)
	}
	if _, err := x.Restore(ctx, bence.ID, clients.ID, 0, "Clients (2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Relocate(ctx, bence.ID, offer.ID, 0, "Offer 2026.pdf"); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, offer.ID}); len(got) != 2 {
		t.Fatalf("grants after the restore and the move: %+v", got)
	}

	// Removed for good from the trash, or dropped because the storage no longer has it: its grants go with it.
	if err := x.Trash(ctx, bence.ID, offer.ID, now, "e2", "u-reka"); err != nil {
		t.Fatal(err)
	}
	if err := x.RemoveFromTrash(ctx, bence.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	gone(t, x, bence, 0, nil, []Seen{{Name: "Notes.txt", Size: 5}})
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, offer.ID}); len(got) != 0 {
		t.Fatalf("grants outlived their items: %+v", got)
	}
	var left int
	if err := x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM grants").Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d grants left in the index (%v)", left, err)
	}
}

// An upload in progress is found only in its place and only for the member who started it, and it goes when the
// folder it goes into is removed for good.
func TestAnUploadIsOnlyItsMembersAndGoesWithItsFolder(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence, reka := place(t, x, "u-bence"), place(t, x, "u-reka")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	into := Upload{ID: "0123456789abcdef0123456789abcdef", PlaceID: bence.ID, FolderID: clients.ID, Member: "u-bence",
		Name: "Big.bin", Size: 1 << 30, Started: now, Touched: now}
	top := Upload{ID: "fedcba9876543210fedcba9876543210", PlaceID: bence.ID, Member: "u-bence", Name: "Top.bin", Size: 5,
		Started: now, Touched: now.Add(-2 * time.Hour)}
	for _, u := range []Upload{into, top} {
		if err := x.AddUpload(ctx, u, 10); err != nil {
			t.Fatal(err)
		}
	}
	got, err := x.UploadOf(ctx, bence.ID, into.ID, "u-bence", 0)
	if err != nil || got.FolderID != clients.ID || got.Name != "Big.bin" || got.Size != 1<<30 || got.Touched.UnixMilli() != now.UnixMilli() {
		t.Fatalf("Bence's upload: %+v, %v", got, err)
	}
	if got, err := x.UploadOf(ctx, bence.ID, top.ID, "u-bence", 0); err != nil || got.FolderID != 0 {
		t.Fatalf("the upload into the top folder: %+v, %v", got, err)
	}
	for what, find := range map[string]func() error{
		"by another member":     func() error { _, err := x.UploadOf(ctx, bence.ID, into.ID, "u-reka", 0); return err },
		"through another place": func() error { _, err := x.UploadOf(ctx, reka.ID, into.ID, "u-bence", 0); return err },
		"an id that is not there": func() error {
			_, err := x.UploadOf(ctx, bence.ID, "00000000000000000000000000000000", "u-bence", 0)
			return err
		},
	} {
		if err := find(); !errors.Is(err, ErrNoUpload) {
			t.Errorf("finding it %s: %v, want ErrNoUpload", what, err)
		}
	}

	if old, err := x.UploadsTouchedBefore(ctx, now.Add(-time.Hour), nil, 10); err != nil || len(old) != 1 || old[0].ID != top.ID {
		t.Fatalf("uploads untouched for an hour: %+v, %v", old, err)
	}
	if err := x.TouchUpload(ctx, top.ID, now); err != nil {
		t.Fatal(err)
	}
	if old, _ := x.UploadsTouchedBefore(ctx, now.Add(-time.Hour), nil, 10); len(old) != 0 {
		t.Fatalf("uploads untouched for an hour, after a part arrived: %+v", old)
	}

	// Clients removed for good: the upload into it goes too.
	if _, err := x.db.ExecContext(ctx, "DELETE FROM items WHERE id = ?", clients.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.UploadOf(ctx, bence.ID, into.ID, "u-bence", 0); !errors.Is(err, ErrNoUpload) {
		t.Fatalf("the upload into a folder removed for good: %v", err)
	}
	if err := x.RemoveUpload(ctx, top.ID); err != nil {
		t.Fatal(err)
	}
	for _, u := range []Upload{into, top} {
		if known, err := x.UploadKnown(ctx, u.ID); err != nil || known {
			t.Fatalf("the upload %s is still known: %v, %v", u.Name, known, err)
		}
	}
	// Not into a folder of another place.
	if err := x.AddUpload(ctx, Upload{ID: into.ID, PlaceID: reka.ID, FolderID: put(t, x, bence, 0, "Other", true, 0).ID,
		Member: "u-reka", Name: "x", Size: 1, Started: now, Touched: now}, 10); err == nil {
		t.Fatal("the index holds an upload in one place into a folder of another")
	}
}

// A person has at most so many uploads in progress, in every place together: one more is not recorded. Another
// person's do not count against them, and one of theirs gone makes room for another.
func TestAPersonHasAtMostSoManyUploadsInProgress(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence, reka := place(t, x, "u-bence"), place(t, x, "u-reka")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	anna, _, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "editor", time.Time{}, now, "u-bence")
	if err != nil {
		t.Fatal(err)
	}
	id := func(who string, n int) string { return fmt.Sprintf("%s%031d", who, n) }
	const most = 3
	for who, u := range map[string]Upload{
		"a": {PlaceID: bence.ID, Member: "u-bence"},
		"b": {PlaceID: reka.ID, Member: "u-reka"},
		"c": {PlaceID: bence.ID, FolderID: clients.ID, Guest: anna.ID},
	} {
		u.Name, u.Size, u.Started, u.Touched = "Big.bin", 1, now, now
		for n := range most {
			u.ID = id(who, n)
			if err := x.AddUpload(ctx, u, most); err != nil {
				t.Fatalf("upload %d of %q: %v", n+1, who, err)
			}
		}
		u.ID = id(who, most)
		if err := x.AddUpload(ctx, u, most); !errors.Is(err, ErrTooManyUploads) {
			t.Fatalf("one upload more of %q: %v, want ErrTooManyUploads", who, err)
		}
		if known, err := x.UploadKnown(ctx, u.ID); err != nil || known {
			t.Fatalf("the upload of %q that was one too many is known: %v, %v", who, known, err)
		}
		if n, err := x.UploadsBy(ctx, u.Member, u.Guest); err != nil || n != most {
			t.Fatalf("uploads in progress of %q: %d, %v", who, n, err)
		}
	}
	// Bence's in Réka's place count as his: every place together.
	late := Upload{ID: id("d", 0), PlaceID: reka.ID, Member: "u-bence", Name: "x", Size: 1, Started: now, Touched: now}
	if err := x.AddUpload(ctx, late, most); !errors.Is(err, ErrTooManyUploads) {
		t.Fatalf("Bence's upload in another place: %v, want ErrTooManyUploads", err)
	}
	if err := x.RemoveUpload(ctx, id("a", 0)); err != nil {
		t.Fatal(err)
	}
	if err := x.AddUpload(ctx, late, most); err != nil {
		t.Fatalf("Bence's upload once one of his was gone: %v", err)
	}
}

// The uploads nothing arrived for since a time are read a page at a time, the oldest first, each page after the last
// upload of the one before: every one of them once, those touched at the same moment too, and none touched since.
func TestAbandonedUploadsAreReadInPages(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	var want []string
	for n := range 25 {
		u := Upload{ID: fmt.Sprintf("%032d", n), PlaceID: bence.ID, Member: "u-bence", Name: "Big.bin", Size: 1, Started: now,
			Touched: now.Add(-48*time.Hour + time.Duration(n/4)*time.Minute)} // four at each moment
		if n >= 20 {
			u.Touched = now // still arriving
		} else {
			want = append(want, u.ID)
		}
		if err := x.AddUpload(ctx, u, 100); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	var last *Upload
	for pages := 0; ; pages++ {
		page, err := x.UploadsTouchedBefore(ctx, now.Add(-24*time.Hour), last, 3)
		if err != nil || len(page) > 3 {
			t.Fatalf("page %d: %d uploads, %v", pages+1, len(page), err)
		}
		for _, u := range page {
			got = append(got, u.ID)
		}
		if len(page) < 3 {
			break
		}
		last = &page[len(page)-1]
		if pages > 20 {
			t.Fatal("the pages do not end")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the abandoned uploads, a page at a time: %q, want %q", got, want)
	}
}

// What a zip of some items would hold, as the index knows it: the items and everything inside them, and nothing in
// the trash or in another place.
func TestCountInside(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	bence, reka := place(t, x, "u-bence"), place(t, x, "u-reka")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	bakery := put(t, x, bence, clients.ID, "Bakery", true, 0)
	put(t, x, bence, bakery.ID, "Offer.pdf", false, 1)
	put(t, x, bence, bakery.ID, "Ledger.xlsx", false, 1)
	old := put(t, x, bence, clients.ID, "Old.pdf", false, 1)
	notes := put(t, x, bence, 0, "Notes.txt", false, 1)
	hers := put(t, x, reka, 0, "Hers.txt", false, 1)
	if err := x.Trash(ctx, bence.ID, old.ID, time.Now(), "entry", "u-bence"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		ids   []int64
		limit int
		want  int
	}{
		{[]int64{clients.ID}, 100, 4},
		{[]int64{clients.ID, notes.ID}, 100, 5},
		{[]int64{clients.ID, notes.ID}, 3, 3},
		{[]int64{bakery.ID}, 100, 3},
		{[]int64{old.ID, hers.ID}, 100, 0},
		{nil, 100, 0},
	} {
		if n, err := x.CountInside(ctx, bence.ID, c.ids, c.limit); err != nil || n != c.want {
			t.Errorf("%v up to %d: got %d, %v; want %d", c.ids, c.limit, n, err, c.want)
		}
	}
}

// Moved into another place, an item takes everything in it along, with what was given on each of them; what was given
// on the folders above it in its old place stays there and no longer reaches it. Not while an upload goes into it.
func TestAnItemMovedToAnotherPlaceKeepsOnlyItsOwnGrants(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	team, err := x.AddSharedFolder(ctx, "Team", "Shared folders/Team", "u-bence", now)
	if err != nil {
		t.Fatal(err)
	}
	clients := put(t, x, bence, 0, "Clients", true, 0)
	bakery := put(t, x, bence, clients.ID, "Bakery", true, 0)
	y2026 := put(t, x, bence, bakery.ID, "2026", true, 0)
	offer := put(t, x, bence, y2026.ID, "Offer.pdf", false, 84)
	old := put(t, x, bence, bakery.ID, "Old.pdf", false, 3)
	if err := x.Trash(ctx, bence.ID, old.ID, now, "e1", "u-bence"); err != nil {
		t.Fatal(err)
	}
	for _, g := range []struct {
		on   int64
		to   Grantee
		role string
	}{{clients.ID, Grantee{Member: "u-reka"}, "viewer"}, {bakery.ID, Grantee{Member: "u-dora"}, "editor"}, {offer.ID, Grantee{Everyone: true}, "viewer"}} {
		if _, err := x.Give(ctx, bence.ID, g.on, g.to, g.role, now, "u-bence", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := x.GrantedTo(ctx, "u-dora"); err != nil || !slices.Equal(got, []Granted{{bence.ID, offer.ID}, {bence.ID, bakery.ID}}) {
		t.Fatalf("granted to Dóra before the move: %+v, %v", got, err)
	}

	// An upload in progress into a folder inside it belongs to the old place: the move waits for it.
	up := Upload{ID: "0123456789abcdef0123456789abcdef", PlaceID: bence.ID, FolderID: y2026.ID, Member: "u-dora", Name: "Big.bin",
		Size: 10, Started: now, Touched: now}
	if err := x.AddUpload(ctx, up, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := x.MoveToPlace(ctx, bence.ID, bakery.ID, team.ID, 0, "Bakery", Left{}); !errors.Is(err, ErrUploading) {
		t.Fatalf("moving Bakery with an upload into 2026: %v", err)
	}
	if err := x.RemoveUpload(ctx, up.ID); err != nil {
		t.Fatal(err)
	}

	// A row the index still holds under the name there is out of date: the storage has already moved Bakery there.
	put(t, x, team, 0, "Bakery", false, 1)
	moved, err := x.MoveToPlace(ctx, bence.ID, bakery.ID, team.ID, 0, "Bakery", Left{})
	if err != nil || moved.ID != bakery.ID || moved.PlaceID != team.ID || moved.ParentID != 0 || len(moved.Above) != 0 {
		t.Fatalf("Bakery moved into Team: %+v, %v", moved, err)
	}
	if it, err := x.Item(ctx, team.ID, offer.ID); err != nil || len(it.Above) != 2 || it.Above[0].ID != bakery.ID {
		t.Fatalf("Offer.pdf in Team: %+v, %v", it, err)
	}
	for _, id := range []int64{bakery.ID, y2026.ID, offer.ID} {
		if _, err := x.Item(ctx, bence.ID, id); !errors.Is(err, ErrNoItem) {
			t.Fatalf("item %d is still found in Bence's My files: %v", id, err)
		}
	}
	if children, _ := x.Children(ctx, team.ID, 0); len(children) != 1 || children[0].ID != bakery.ID || !children[0].Folder {
		t.Fatalf("Team's top folder: %+v", children)
	}
	var lines []string
	got, _ := x.GrantsOn(ctx, team.ID, []int64{bakery.ID, y2026.ID, offer.ID})
	for _, g := range got {
		lines = append(lines, fmt.Sprintf("%d %v %s %s", g.Item, g.Everyone, g.Member, g.Role))
	}
	want := []string{"0 false u-bence manager", fmt.Sprintf("%d false u-dora editor", bakery.ID), fmt.Sprintf("%d true  viewer", offer.ID)}
	if !slices.Equal(lines, want) {
		t.Fatalf("grants in Team on the way to Offer.pdf: %q, want %q", lines, want)
	}
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID}); len(got) != 1 || got[0].Member != "u-reka" {
		t.Fatalf("grants left on Clients: %+v", got)
	}
	if got, _ := x.GrantedTo(ctx, "u-dora"); !slices.Equal(got, []Granted{{team.ID, offer.ID}, {team.ID, bakery.ID}}) {
		t.Fatalf("granted to Dóra after the move: %+v", got)
	}
	// What was deleted out of Bakery earlier stays in Bence's trash, and goes back to his top folder.
	if tr, err := x.TrashItem(ctx, bence.ID, old.ID); err != nil || tr.PlaceID != bence.ID {
		t.Fatalf("Old.pdf in the trash: %+v, %v", tr, err)
	}
	if _, err := x.Item(ctx, bence.ID, bakery.ID); !errors.Is(err, ErrNoItem) {
		t.Fatal("Old.pdf's folder is still found in Bence's My files")
	}
	if _, err := x.MoveToPlace(ctx, bence.ID, bakery.ID, team.ID, 0, "Bakery", Left{}); !errors.Is(err, ErrNoItem) {
		t.Fatalf("moving Bakery out of Bence's My files again: %v", err)
	}
	// A change decided on Bakery while it was in Bence's My files never records anything in his place under it: nothing
	// in one place is ever under a folder of another, nor under a file, nor under a folder in the trash.
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)
	never := func(string) bool { return false }
	for what, err := range map[string]error{
		"an upload into Bakery, through Bence's My files": func() error {
			_, err := x.PutItem(ctx, bence.ID, bakery.ID, Seen{Name: "Late.txt"}, now, AddedBy{})
			return err
		}(),
		"a listing of Bakery, through Bence's My files": func() error {
			_, err := x.Reconcile(ctx, bence.ID, bakery.ID, Reread{Path: []string{"Clients", "Bakery"}, Seen: []Seen{{Name: "Late.txt"}},
				Leave: never, Busy: func() bool { return false }, Stale: func() bool { return false }, At: now, Hide: true})
			return err
		}(),
		"a move into Bakery, inside Bence's My files": func() error {
			_, err := x.Relocate(ctx, bence.ID, notes.ID, bakery.ID, "Notes.txt")
			return err
		}(),
		"a move into a file": func() error {
			_, err := x.Relocate(ctx, bence.ID, clients.ID, notes.ID, "Clients")
			return err
		}(),
		"a move into Team, under a folder of Bence's": func() error {
			_, err := x.MoveToPlace(ctx, bence.ID, notes.ID, team.ID, clients.ID, "Notes.txt", Left{})
			return err
		}(),
		"a restore into Bakery, through Bence's My files": func() error {
			_, err := x.Restore(ctx, bence.ID, old.ID, bakery.ID, "Old.pdf")
			return err
		}(),
	} {
		if !errors.Is(err, ErrNoFolder) || !errors.Is(err, ErrNoItem) {
			t.Errorf("%s: %v, want ErrNoFolder", what, err)
		}
	}
	if children, _ := x.Children(ctx, bence.ID, bakery.ID); len(children) != 0 {
		t.Fatalf("Bence's My files holds items under Bakery: %+v", children)
	}
	rows, err := x.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	broken := rows.Next()
	rows.Close()
	if broken {
		t.Fatal("a row refers to one that is not there after the move")
	}
}

// A public link hangs on its item: one per item, found by its token's hash wherever the item is now (in another place,
// in the trash), and gone with the item when it is removed for good or the storage no longer has it. Removing it, or
// replacing it, ends its token for good. The index holds only the token's hash.
func TestALinkLivesAndGoesWithItsItem(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	shared, err := x.AddSharedFolder(ctx, "Clients", "Shared folders/Clients", "u-bence", now)
	if err != nil {
		t.Fatal(err)
	}
	clients := put(t, x, bence, 0, "Clients", true, 0)
	offer := put(t, x, bence, clients.ID, "Offer.pdf", false, 84)
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

	l, err := x.MakeLink(ctx, bence.ID, clients.ID, hash(1), "view", "u-bence", now, false)
	if err != nil || l.Item != clients.ID || l.Place != bence.ID || l.Kind != "view" || l.MadeBy != "u-bence" || l.Uses != 0 || !l.Used.IsZero() {
		t.Fatalf("a new link on Clients: %+v, %v", l, err)
	}
	// One per item: another is refused unless it replaces the first, whose token then finds nothing.
	if _, err := x.MakeLink(ctx, bence.ID, clients.ID, hash(2), "view", "u-bence", now, false); !errors.Is(err, ErrLinkExists) {
		t.Fatalf("a second link on Clients: %v", err)
	}
	again, err := x.MakeLink(ctx, bence.ID, clients.ID, hash(2), "view", "u-dora", now, true)
	if err != nil || again.ID == l.ID || again.MadeBy != "u-dora" {
		t.Fatalf("a link in place of the first: %+v, %v", again, err)
	}
	if _, _, err := x.LinkByToken(ctx, hash(1)); !errors.Is(err, ErrNoLink) {
		t.Fatalf("the replaced link's token: %v", err)
	}
	found, held, err := x.LinkByToken(ctx, hash(2))
	if err != nil || found.ID != again.ID || !bytes.Equal(held, hash(2)) {
		t.Fatalf("the link by its token: %+v, %x, %v", found, held, err)
	}
	// Only on a live item of the place given; never a hash that is not one.
	if _, err := x.MakeLink(ctx, shared.ID, offer.ID, hash(3), "view", "u-bence", now, false); !errors.Is(err, ErrNoItem) {
		t.Fatalf("a link on Bence's item through another place: %v", err)
	}
	if _, err := x.MakeLink(ctx, bence.ID, offer.ID, []byte("short"), "view", "u-bence", now, false); err == nil {
		t.Fatal("the index holds a token hash of the wrong length")
	}
	if _, err := x.MakeLink(ctx, bence.ID, offer.ID, hash(4), "edit", "u-bence", now, false); err == nil {
		t.Fatal("the index holds a link of a kind nobody knows")
	}
	if _, err := x.MakeLink(ctx, bence.ID, notes.ID, hash(2), "view", "u-bence", now, false); err == nil {
		t.Fatal("two links share a token")
	}
	if _, err := x.MakeLink(ctx, bence.ID, offer.ID, hash(5), "view", "u-bence", now, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.LinksOn(ctx, []int64{clients.ID, offer.ID, notes.ID}); len(got) != 2 {
		t.Fatalf("links on Clients, Offer.pdf and Notes.txt: %+v", got)
	}
	if got, _ := x.LinksInside(ctx, bence.ID, 0); len(got) != 1 || got[0].Item != clients.ID {
		t.Fatalf("links on what is in the top folder: %+v", got)
	}

	// Uses add up, and the last one counts.
	if err := x.AddUses(ctx, map[int64]Use{again.ID: {Count: 3, Last: now.Add(time.Minute)}, 999: {Count: 1, Last: now}}); err != nil {
		t.Fatal(err)
	}
	if err := x.AddUses(ctx, map[int64]Use{again.ID: {Count: 2, Last: now}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.LinkOn(ctx, clients.ID); got.Uses != 5 || got.Used.UnixMilli() != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("uses of the link: %+v", got)
	}

	// In the trash it is still there, and found by its token; moved into a shared folder it follows its item.
	if err := x.Trash(ctx, bence.ID, clients.ID, now, "e1", "u-bence"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.LinkByToken(ctx, hash(2)); err != nil {
		t.Fatalf("the link of an item in the trash: %v", err)
	}
	if _, err := x.Restore(ctx, bence.ID, clients.ID, 0, "Clients"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.MoveToPlace(ctx, bence.ID, clients.ID, shared.ID, 0, "Clients", Left{}); err != nil {
		t.Fatal(err)
	}
	if got, _, err := x.LinkByToken(ctx, hash(2)); err != nil || got.Place != shared.ID || got.Item != clients.ID {
		t.Fatalf("the link after its item moved into the shared folder: %+v, %v", got, err)
	}
	if got, _ := x.LinkOn(ctx, offer.ID); got.Place != shared.ID {
		t.Fatalf("the link on Offer.pdf, inside what moved: %+v", got)
	}

	// Removed, it is gone; removed for good from the trash, or dropped because the storage no longer has it, it goes
	// with its item.
	if err := x.RemoveLink(ctx, shared.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.LinkOn(ctx, offer.ID); !errors.Is(err, ErrNoLink) {
		t.Fatalf("the removed link: %v", err)
	}
	if _, err := x.MakeLink(ctx, bence.ID, notes.ID, hash(6), "view", "u-bence", now, false); err != nil {
		t.Fatal(err)
	}
	if err := x.Trash(ctx, shared.ID, clients.ID, now, "e2", "u-bence"); err != nil {
		t.Fatal(err)
	}
	if err := x.RemoveFromTrash(ctx, shared.ID, clients.ID); err != nil {
		t.Fatal(err)
	}
	gone(t, x, bence, 0, nil, nil)
	var left int
	if err := x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM links").Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d links outlived their items (%v)", left, err)
	}
}

// A visitor's list of a folder holds at most so many items.
func TestFirstChildren(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-bence")
	for _, name := range []string{"c.txt", "a.txt", "B", "b.txt"} {
		put(t, x, p, 0, name, name == "B", 1)
	}
	got, more, err := x.FirstChildren(ctx, p.ID, 0, 3)
	if err != nil || !more || len(got) != 3 || got[0].Name != "B" || got[1].Name != "a.txt" || got[2].Name != "b.txt" {
		t.Fatalf("the first 3: %+v, more %v, %v", got, more, err)
	}
	if got, more, _ := x.FirstChildren(ctx, p.ID, 0, 4); more || len(got) != 4 {
		t.Fatalf("all 4: %d, more %v", len(got), more)
	}
	// Page after page, the whole list in the same order, each once: folders first, then files.
	put(t, x, p, 0, "A", true, 0)
	all, _ := x.Children(ctx, p.ID, 0)
	var paged []string
	var after *Item
	for {
		page, more, err := x.ChildrenAfter(ctx, p.ID, 0, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, namesOf(page)...)
		if !more {
			break
		}
		after = &page[len(page)-1]
	}
	if want := namesOf(all); !slices.Equal(paged, want) || !slices.Equal(want, []string{"A", "B", "a.txt", "b.txt", "c.txt"}) {
		t.Fatalf("page by page: %q, all at once: %q", paged, want)
	}
}

// The trash follows the storage by entry: an item whose entry is gone is hidden, and dropped only when a later look
// confirms it; an entry the index does not know, holding one item, is adopted as an item deleted that day.
func TestTheTrashFollowsTheStorageByEntry(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	a := put(t, x, p, 0, "a.txt", false, 1)
	b := put(t, x, p, 0, "b.txt", false, 2)
	t0 := time.Now()
	for _, it := range []struct {
		Item
		entry string
	}{{a, "2026-11-01 aaaaaaaa"}, {b, "2026-11-02 bbbbbbbb"}} {
		if err := x.Trash(ctx, p.ID, it.ID, t0, it.entry, "u-reka"); err != nil {
			t.Fatal(err)
		}
	}
	never := func() bool { return false }
	none := func(string) bool { return false }
	present := func(entries ...string) func(string) bool {
		return func(e string) bool { return slices.Contains(entries, e) }
	}
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	adopt := []Adopted{{Entry: "2026-11-03 cccccccc", Item: Seen{Name: "c.txt", Size: 3}, Deleted: day}}

	o, err := x.ReconcileTrash(ctx, p.ID, TrashReread{Present: present("2026-11-02 bbbbbbbb", "2026-11-03 cccccccc"), Leave: none,
		Stale: never, At: t0, Hide: true, Old: t0.Add(-10 * time.Minute), Adopt: adopt})
	if err != nil || o.Hidden != 1 || o.Added != 1 {
		t.Fatalf("first look: %+v, %v", o, err)
	}
	trash, _ := x.TrashItems(ctx, p.ID)
	if len(trash) != 2 || trash[0].ID != b.ID || trash[1].Name != "c.txt" || !trash[1].Deleted.Equal(day) || trash[1].DeletedBy != "" || trash[1].RestoreTo != 0 {
		t.Fatalf("the trash: %+v", trash)
	}
	if _, err := x.TrashItem(ctx, p.ID, a.ID); !errors.Is(err, ErrNoItem) {
		t.Fatalf("a.txt, whose entry is gone, is found: %v", err)
	}
	// Adopting again changes nothing; a later look without confirmation drops nothing; one with it drops a.txt.
	o, _ = x.ReconcileTrash(ctx, p.ID, TrashReread{Present: present("2026-11-02 bbbbbbbb", "2026-11-03 cccccccc"), Leave: none,
		Stale: never, At: t0.Add(time.Hour), Hide: true, Old: t0.Add(50 * time.Minute), Adopt: adopt})
	if o != (Outcome{}) {
		t.Fatalf("a look without confirmation: %+v", o)
	}
	missing, _ := x.TrashMissing(ctx, p.ID, t0.Add(50*time.Minute), 10)
	if len(missing) != 1 || missing[0].ID != a.ID || missing[0].Entry != "2026-11-01 aaaaaaaa" || missing[0].Name != "a.txt" {
		t.Fatalf("missing from the trash: %+v", missing)
	}
	o, _ = x.ReconcileTrash(ctx, p.ID, TrashReread{Present: present("2026-11-02 bbbbbbbb", "2026-11-03 cccccccc"), Leave: none,
		Stale: never, At: t0.Add(time.Hour), Hide: true, Old: t0.Add(50 * time.Minute),
		Confirmed: map[int64]time.Time{a.ID: missing[0].Since}})
	if o.Dropped != 1 {
		t.Fatalf("a confirmed look: %+v", o)
	}
	// An entry found again before that is back.
	x.ReconcileTrash(ctx, p.ID, TrashReread{Present: present("2026-11-03 cccccccc"), Leave: none, Stale: never, At: t0, Hide: true})
	if _, err := x.TrashItem(ctx, p.ID, b.ID); !errors.Is(err, ErrNoItem) {
		t.Fatal("b.txt's entry went, but it is still in the trash")
	}
	if o, _ := x.ReconcileTrash(ctx, p.ID, TrashReread{Present: present("2026-11-02 bbbbbbbb", "2026-11-03 cccccccc"), Leave: none,
		Stale: never, At: t0, Hide: true}); o.Back != 1 {
		t.Fatalf("b.txt's entry back: %+v", o)
	}
}

// The walk visits each place's trash, then its top folder, then its folders by id, skipping what is in the trash or
// missing; a folder made meanwhile is reached.
func TestTheWalkOrder(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	reka, bence := place(t, x, "u-reka"), place(t, x, "u-bence")
	a := put(t, x, reka, 0, "A", true, 0)
	b := put(t, x, reka, a.ID, "B", true, 0)
	put(t, x, reka, 0, "file.txt", false, 1)
	c := put(t, x, bence, 0, "C", true, 0)
	trashed := put(t, x, bence, 0, "Trashed", true, 0)
	if err := x.Trash(ctx, bence.ID, trashed.ID, time.Now(), "e", "u-bence"); err != nil {
		t.Fatal(err)
	}
	var steps []WalkStep
	for s, ok := (WalkStep{}), true; ; {
		var err error
		s, ok, err = x.NextWalkStep(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		steps = append(steps, s)
		if s == (WalkStep{Place: reka.ID, Folder: a.ID}) {
			put(t, x, reka, b.ID, "Made meanwhile", true, 0)
		}
	}
	got := make([]string, 0, len(steps))
	for _, s := range steps {
		got = append(got, s.String())
	}
	want := []string{
		fmt.Sprintf("%d:-1", reka.ID), fmt.Sprintf("%d:0", reka.ID), fmt.Sprintf("%d:%d", reka.ID, a.ID), fmt.Sprintf("%d:%d", reka.ID, b.ID),
		fmt.Sprintf("%d:%d", reka.ID, trashed.ID+1),
		fmt.Sprintf("%d:-1", bence.ID), fmt.Sprintf("%d:0", bence.ID), fmt.Sprintf("%d:%d", bence.ID, c.ID),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if s, ok := ParseWalkStep(steps[2].String()); !ok || s != steps[2] {
		t.Fatalf("a step read back: %+v, %v", s, ok)
	}
	for _, bad := range []string{"", "1", "a:b", "1:-2", "-1:0"} {
		if _, ok := ParseWalkStep(bad); ok {
			t.Errorf("%q read as a step", bad)
		}
	}
}

// A folder's link is a view link or an upload-only one; a file's only a view link. Changing the kind keeps the link's
// address, its uses and what it has taken in; a reset starts that count again.
func TestALinksKind(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u1")
	folder := put(t, x, p, 0, "Clients", true, 0)
	file := put(t, x, p, 0, "Offer.pdf", false, 5)
	now := time.Now()
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

	for what, kind := range map[string]string{"an upload-only link": UploadLink, "a kind there is not": "edit", "no kind": ""} {
		if _, err := x.MakeLink(ctx, p.ID, file.ID, hash(1), kind, "u1", now, false); !errors.Is(err, ErrLinkKind) {
			t.Errorf("%s on a file: %v", what, err)
		}
	}
	if _, err := x.MakeLink(ctx, p.ID, folder.ID, hash(1), "edit", "u1", now, false); !errors.Is(err, ErrLinkKind) {
		t.Errorf("a link of a kind there is not on a folder: %v", err)
	}
	if _, err := x.MakeLink(ctx, p.ID, file.ID, hash(2), ViewLink, "u1", now, false); err != nil {
		t.Fatal(err)
	}
	if _, err := x.SetLinkKind(ctx, p.ID, file.ID, UploadLink); !errors.Is(err, ErrLinkKind) {
		t.Errorf("a file's link made upload-only: %v", err)
	}
	if l, _ := x.LinkOn(ctx, file.ID); l.Kind != ViewLink {
		t.Errorf("the file's link after that: %+v", l)
	}

	up, err := x.MakeLink(ctx, p.ID, folder.ID, hash(3), UploadLink, "u1", now, false)
	if err != nil || up.Kind != UploadLink || !up.Folder || up.TakenIn != 0 {
		t.Fatalf("an upload-only link on a folder: %+v %v", up, err)
	}
	if err := x.TakenIn(ctx, up.ID, 700); err != nil {
		t.Fatal(err)
	}
	if err := x.AddUses(ctx, map[int64]Use{up.ID: {Count: 2, Last: now}}); err != nil {
		t.Fatal(err)
	}
	view, err := x.SetLinkKind(ctx, p.ID, folder.ID, ViewLink)
	if err != nil || view.ID != up.ID || view.Kind != ViewLink || view.TakenIn != 700 || view.Uses != 2 {
		t.Fatalf("the link made a view link: %+v %v", view, err)
	}
	if l, _, err := x.LinkByToken(ctx, hash(3)); err != nil || l.ID != up.ID {
		t.Fatalf("the link's address after its kind changed: %+v %v", l, err)
	}
	back, err := x.SetLinkKind(ctx, p.ID, folder.ID, UploadLink)
	if err != nil || back.Kind != UploadLink {
		t.Fatalf("the link made upload-only again: %+v %v", back, err)
	}
	reset, err := x.ResetLink(ctx, p.ID, folder.ID)
	if err != nil || reset.TakenIn != 0 || reset.Uses != 2 {
		t.Fatalf("the link reset: %+v %v", reset, err)
	}
	other := put(t, x, p, 0, "Bakery", true, 0)
	if _, err := x.SetLinkKind(ctx, p.ID, other.ID, ViewLink); !errors.Is(err, ErrNoLink) {
		t.Errorf("the kind of a link a folder does not have: %v", err)
	}
	if _, err := x.ResetLink(ctx, p.ID, other.ID); !errors.Is(err, ErrNoLink) {
		t.Errorf("resetting a link a folder does not have: %v", err)
	}
	// Taken in through a link removed meanwhile: nothing, and no error.
	if err := x.RemoveLink(ctx, p.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	if err := x.TakenIn(ctx, up.ID, 5); err != nil {
		t.Fatalf("taken in through a link removed meanwhile: %v", err)
	}
}

// What a link took in is counted in bytes and in files, an empty file too, and both start again at a reset; a folder's
// entries are counted up to a limit, without what is in the trash.
func TestALinksFilesAndAFoldersEntriesAreCounted(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	l, err := x.MakeLink(ctx, bence.ID, clients.ID, make([]byte, 32), UploadLink, "u-bence", now, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{0, 5, 0} {
		if err := x.TakenIn(ctx, l.ID, n); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := x.LinkByID(ctx, l.ID); got.TakenFiles != 3 || got.TakenIn != 5 {
		t.Fatalf("after three files, two of them empty: %+v", got)
	}
	if got, err := x.ResetLink(ctx, bence.ID, clients.ID); err != nil || got.TakenFiles != 0 || got.TakenIn != 0 {
		t.Fatalf("after a reset: %+v %v", got, err)
	}
	for _, name := range []string{"a", "b", "c"} {
		put(t, x, bence, clients.ID, name, false, 1)
	}
	for limit, want := range map[int]int{10: 3, 2: 2} {
		if n, err := x.EntriesIn(ctx, bence.ID, clients.ID, limit); err != nil || n != want {
			t.Fatalf("entries in Clients, counted up to %d: %d %v, want %d", limit, n, err, want)
		}
	}
}

// An index a newer Drive made (the image put back to an older version, the data folder not) is not opened: this Drive
// knows nothing of what the newer one added to it.
func TestAnIndexMadeByANewerDriveIsNotOpened(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	x, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", Version()+1)); err != nil {
		t.Fatal(err)
	}
	x.Close()
	if _, err := Open(ctx, dir); !errors.Is(err, ErrNewer) {
		t.Fatalf("opening an index of a newer schema: %v", err)
	}
}
