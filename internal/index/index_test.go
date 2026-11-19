package index

import (
	"context"
	"database/sql"
	"errors"
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
	it, err := x.PutItem(context.Background(), p.ID, folderID, Seen{Name: name, Folder: folder, Size: size, Changed: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return it
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
	offer, err := x.Item(ctx, 1, 9)
	if err != nil || offer.Name != "Offer.docx" || offer.Size != 84 || len(offer.Above) != 1 || offer.Above[0].ID != 7 {
		t.Fatalf("got %+v, %v", offer, err)
	}
	p, _ := x.PlaceByID(ctx, 1)
	if n := put(t, x, p, 0, "New.txt", false, 1); n.ID <= 9 {
		t.Fatalf("a new item got id %d, which is not after the old ones", n.ID)
	}
}

// Access will hang on item ids, and a page can still hold the id of an item that is gone: an id is never given to a
// second item, even when the first is removed for good and it was the newest.
func TestAnIdIsNeverGivenToASecondItem(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	a := put(t, x, p, 0, "a.txt", false, 1)
	if err := x.Trash(ctx, p.ID, a.ID, time.Now(), "entry"); err != nil {
		t.Fatal(err)
	}
	if err := x.RemoveFromTrash(ctx, p.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if b := put(t, x, p, 0, "a.txt", false, 1); b.ID == a.ID {
		t.Fatalf("the id %d was given to a second item", a.ID)
	}
	// Nor when a listing drops an item the storage no longer has.
	c := put(t, x, p, 0, "c.txt", false, 1)
	if err := x.SetChildren(ctx, p.ID, 0, nil, func(string) bool { return false }, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
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
	if _, err := x.Item(ctx, bence.ID, offer.ID); !errors.Is(err, ErrNoItem) {
		t.Errorf("found through another place: %v", err)
	}

	// Deleting Drafts takes everything in it along, out of reach until it comes back, with the same ids.
	if err := x.Trash(ctx, reka.ID, drafts.ID, time.Now(), "entry"); err != nil {
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
	if err := x.Trash(ctx, p.ID, early.ID, time.Now().Add(-time.Hour), "e1"); err != nil {
		t.Fatal(err)
	}
	if err := x.Trash(ctx, p.ID, drafts.ID, time.Now(), "e2"); err != nil {
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

func TestAListingLeavesTheIndexAloneWhenTheFolderChangedMeanwhile(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	p := place(t, x, "u-reka")
	a := put(t, x, p, 0, "a.txt", false, 1)
	seen := []Seen{{Name: "b.txt", Size: 2}}
	if err := x.SetChildren(ctx, p.ID, 0, seen, func(string) bool { return false }, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	items, _ := x.Children(ctx, p.ID, 0)
	if len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("a stale listing changed the folder: %+v", items)
	}
	// Names being written are left alone; the rest follows the storage.
	keep := func(name string) bool { return name == "a.txt" }
	if err := x.SetChildren(ctx, p.ID, 0, seen, keep, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	items, _ = x.Children(ctx, p.ID, 0)
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
	}
	if !slices.Equal(names, []string{"a.txt", "b.txt"}) || items[0].ID != a.ID {
		t.Fatalf("got %+v", items)
	}
	// A folder where a file was is a new item, with a new id.
	if err := x.SetChildren(ctx, p.ID, 0, []Seen{{Name: "a.txt", Folder: true}}, func(string) bool { return false }, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	items, _ = x.Children(ctx, p.ID, 0)
	if len(items) != 1 || !items[0].Folder || items[0].ID == a.ID {
		t.Fatalf("got %+v", items)
	}
}
