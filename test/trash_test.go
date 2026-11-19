package test

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The trash of Réka's "My files" on the storage: a hidden folder beside the places.
const rekasTrash = ".superit-drive/trash/My files/reka@example.com"

// An entry in a trash on the storage: the day it was deleted and a random part.
var trashEntry = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} [0-9a-f]{8}/$`)

func deleteItem(t *testing.T, c *harness.Client, placeID, itemID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, fmt.Sprintf("/api/places/%d/items/%d", placeID, itemID), nil)
}

// deleted deletes an item and fails the test unless the Drive moved it to the trash.
func deleted(t *testing.T, c *harness.Client, placeID, itemID int64) trashJSON {
	t.Helper()
	res := deleteItem(t, c, placeID, itemID)
	if res.Status != http.StatusOK {
		t.Fatalf("deleting item %d: got %d %s", itemID, res.Status, res.Body)
	}
	var e trashJSON
	res.JSON(t, &e)
	return e
}

func trashOf(t *testing.T, c *harness.Client, placeID int64) []trashJSON {
	t.Helper()
	res := c.Get(t, fmt.Sprintf("/api/places/%d/trash", placeID))
	if res.Status != http.StatusOK {
		t.Fatalf("the trash: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Items []trashJSON `json:"items"`
	}
	res.JSON(t, &l)
	return l.Items
}

func restore(t *testing.T, c *harness.Client, placeID, itemID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/trash/%d/restore", placeID, itemID), nil)
}

func restored(t *testing.T, c *harness.Client, placeID, itemID int64) restoredJSON {
	t.Helper()
	res := restore(t, c, placeID, itemID)
	if res.Status != http.StatusOK {
		t.Fatalf("restoring item %d: got %d %s", itemID, res.Status, res.Body)
	}
	var r restoredJSON
	res.JSON(t, &r)
	return r
}

// entryOf finds the one entry in Réka's trash on the storage that holds name, and fails the test when there is not
// exactly one.
func entryOf(t *testing.T, d *harness.Drive, name string) string {
	t.Helper()
	var found []string
	for _, e := range onStorage(t, d, rekasTrash) {
		if !trashEntry.MatchString(e) {
			t.Fatalf("%q in the trash on the storage is not named as an entry", e)
		}
		if slices.Contains(onStorage(t, d, rekasTrash+"/"+e), name) || slices.Contains(onStorage(t, d, rekasTrash+"/"+e), name+"/") {
			found = append(found, rekasTrash+"/"+strings.TrimSuffix(e, "/"))
		}
	}
	if len(found) != 1 {
		t.Fatalf("%q is in %d entries of the trash on the storage, want 1", name, len(found))
	}
	return found[0]
}

func TestDeleteMovesIntoAHiddenTrashFolderAndRestoreMovesBack(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	drafts := madeFolder(t, c, placeID, 0, "Drafts")
	content := randomBytes(t, 30_000)
	offer := uploadedInto(t, c, placeID, drafts.ID, "Offer for Bakery.docx", content)

	before := time.Now().Add(-time.Minute)
	e := deleted(t, c, placeID, offer.ID)
	if e.ID != offer.ID || e.Name != "Offer for Bakery.docx" || e.Kind != "file" || e.Size != int64(len(content)) || e.Replaced {
		t.Fatalf("the Drive answered %+v", e)
	}
	if e.Deleted.Before(before) || !e.Removed.Equal(e.Deleted.Add(30*24*time.Hour)) {
		t.Fatalf("deleted %s, removed for good %s: want 30 days apart", e.Deleted, e.Removed)
	}
	if len(e.From) != 1 || e.From[0].ID != drafts.ID {
		t.Fatalf("it goes back to %+v, want Drafts", e.From)
	}

	// Gone from its folder, on the storage and in the list; in a hidden folder of the trash instead, whole.
	if l := list(t, c, placeID, drafts.ID); len(l.Items) != 0 {
		t.Fatalf("Drafts still lists %q", names(l.Items))
	}
	if got := onStorage(t, d, rekasTop+"/Drafts"); len(got) != 0 {
		t.Fatalf("Drafts on the storage still holds %q", got)
	}
	entry := entryOf(t, d, "Offer for Bakery.docx")
	if got, _ := d.Storage.Get(t, entry+"/Offer for Bakery.docx"); !bytes.Equal(got, content) {
		t.Fatal("the file in the trash on the storage is not the one deleted")
	}
	if got := onStorage(t, d, ""); !slices.Equal(got, []string{".superit-drive/", "My files/"}) {
		t.Fatalf("the Drive's folder on the storage holds %q", got)
	}
	// The Trash view lists it; the place's own list never shows the trash.
	if trash := trashOf(t, c, placeID); len(trash) != 1 || trash[0].ID != offer.ID {
		t.Fatalf("the trash lists %+v", trash)
	}
	if top := list(t, c, placeID, 0); !slices.Equal(names(top.Items), []string{"Drafts"}) {
		t.Fatalf("the top folder lists %q", names(top.Items))
	}

	// Restore moves it back where it was, with its id and its bytes, and empties its entry.
	r := restored(t, c, placeID, offer.ID)
	if r.Item.ID != offer.ID || r.Item.Name != "Offer for Bakery.docx" || len(r.Path) != 1 || r.Path[0].ID != drafts.ID {
		t.Fatalf("restored %+v into %+v", r.Item, r.Path)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Drafts/Offer for Bakery.docx"); !bytes.Equal(got, content) {
		t.Fatal("the restored file on the storage is not the one deleted")
	}
	if got := onStorage(t, d, rekasTrash); len(got) != 0 {
		t.Fatalf("the trash on the storage still holds %q", got)
	}
	if trash := trashOf(t, c, placeID); len(trash) != 0 {
		t.Fatalf("the trash still lists %+v", trash)
	}
	if res := c.Get(t, downloadPath(placeID, offer.ID)); res.Status != http.StatusOK || !bytes.Equal(res.Body, content) {
		t.Fatalf("downloading the restored file: %d", res.Status)
	}
	// Restoring twice finds nothing in the trash.
	if res := restore(t, c, placeID, offer.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring again: got %d %s, want 404", res.Status, res.Body)
	}
}

// A folder goes to the trash with everything in it and comes back the same way, every id kept. Something deleted out
// of it earlier is a trash item of its own.
func TestAFolderGoesToTheTrashWithEverythingInIt(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	clients := madeFolder(t, c, placeID, 0, "Clients")
	bakery := madeFolder(t, c, placeID, clients.ID, "Bakery Ltd")
	ledger := uploadedInto(t, c, placeID, bakery.ID, "Ledger.xlsx", randomBytes(t, 1000))
	contract := uploadedInto(t, c, placeID, bakery.ID, "Contract.pdf", randomBytes(t, 500))
	early := deleted(t, c, placeID, contract.ID)

	e := deleted(t, c, placeID, clients.ID)
	if e.Kind != "folder" || e.Size != 1000 || len(e.From) != 0 {
		t.Fatalf("the Drive answered %+v: want a folder holding 1000 bytes, from the top folder", e)
	}
	trash := trashOf(t, c, placeID)
	if len(trash) != 2 || trash[0].ID != clients.ID || trash[1].ID != early.ID {
		t.Fatalf("the trash lists %+v: want Clients, then Contract.pdf", trash)
	}
	// Contract.pdf's folder is in the trash itself, so it would go back to the top folder.
	if len(trash[1].From) != 0 {
		t.Fatalf("Contract.pdf goes back to %+v, want the top folder", trash[1].From)
	}
	entry := entryOf(t, d, "Clients")
	if got := onStorage(t, d, entry+"/Clients/Bakery Ltd"); !slices.Equal(got, []string{"Ledger.xlsx"}) {
		t.Fatalf("the folder in the trash on the storage holds %q", got)
	}
	// Nothing inside a folder in the trash can be reached.
	for _, path := range []string{listPath(placeID, bakery.ID), downloadPath(placeID, ledger.ID)} {
		if res := c.Get(t, path); res.Status != http.StatusNotFound {
			t.Errorf("%s in the trash: got %d, want 404", path, res.Status)
		}
	}
	if res := restore(t, c, placeID, ledger.ID); res.Status != http.StatusNotFound {
		t.Errorf("restoring something inside a folder in the trash: got %d %s, want 404", res.Status, res.Body)
	}

	restored(t, c, placeID, clients.ID)
	l := list(t, c, placeID, bakery.ID)
	if len(l.Items) != 1 || l.Items[0].ID != ledger.ID {
		t.Fatalf("Bakery Ltd after the restore lists %+v", l.Items)
	}
	// Now Contract.pdf goes back into Bakery Ltd, where it was.
	trash = trashOf(t, c, placeID)
	if len(trash) != 1 || len(trash[0].From) != 2 || trash[0].From[1].ID != bakery.ID {
		t.Fatalf("the trash lists %+v: want Contract.pdf, going back to Clients/Bakery Ltd", trash)
	}
	r := restored(t, c, placeID, contract.ID)
	if r.Item.ID != contract.ID || len(r.Path) != 2 || r.Path[1].ID != bakery.ID {
		t.Fatalf("restored %+v into %+v", r.Item, r.Path)
	}
	if got := onStorage(t, d, rekasTop+"/Clients/Bakery Ltd"); !slices.Equal(got, []string{"Contract.pdf", "Ledger.xlsx"}) {
		t.Fatalf("Bakery Ltd on the storage: %q", got)
	}
}

// Restoring never replaces anything: a name taken where it goes back to gets " (2)" added, and a folder that has gone
// sends it to the top folder.
func TestARestoreNeverReplacesAndFindsAPlace(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	first := uploadedInto(t, c, placeID, 0, "Budget.xlsx", []byte("first"))
	deleted(t, c, placeID, first.ID)
	uploadedInto(t, c, placeID, 0, "Budget.xlsx", []byte("second"))
	r := restored(t, c, placeID, first.ID)
	if r.Item.ID != first.ID || r.Item.Name != "Budget (2).xlsx" || len(r.Path) != 0 {
		t.Fatalf("restored %+v into %+v, want Budget (2).xlsx at the top", r.Item, r.Path)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Budget.xlsx"); string(got) != "second" {
		t.Fatalf("Budget.xlsx holds %q", got)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Budget (2).xlsx"); string(got) != "first" {
		t.Fatalf("Budget (2).xlsx holds %q", got)
	}

	// Its folder was removed from the storage directly while it was in the trash.
	old := madeFolder(t, c, placeID, 0, "Old")
	memo := uploadedInto(t, c, placeID, old.ID, "Memo.txt", []byte("memo"))
	deleted(t, c, placeID, memo.ID)
	d.Storage.Delete(t, rekasTop+"/Old", true)
	r = restored(t, c, placeID, memo.ID)
	if r.Item.ID != memo.ID || len(r.Path) != 0 {
		t.Fatalf("restored %+v into %+v, want the top folder", r.Item, r.Path)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Memo.txt"); string(got) != "memo" {
		t.Fatalf("Memo.txt holds %q", got)
	}
}

// The storage wins: something removed from the trash on the storage directly is no longer in the Trash view.
func TestTheTrashFollowsTheStorage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	a := uploadedInto(t, c, placeID, 0, "a.txt", []byte("a"))
	b := uploadedInto(t, c, placeID, 0, "b.txt", []byte("b"))
	deleted(t, c, placeID, a.ID)
	deleted(t, c, placeID, b.ID)

	d.Storage.Delete(t, entryOf(t, d, "a.txt"), true)
	if trash := trashOf(t, c, placeID); len(trash) != 1 || trash[0].ID != b.ID {
		t.Fatalf("the trash lists %+v, want only b.txt", trash)
	}
	if res := restore(t, c, placeID, a.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring what is gone: got %d %s", res.Status, res.Body)
	}
	// And something that went from inside its entry is gone when it is restored.
	d.Storage.Delete(t, entryOf(t, d, "b.txt")+"/b.txt", false)
	if res := restore(t, c, placeID, b.ID); res.Status != http.StatusNotFound || !strings.Contains(errorOf(t, res), "trash") {
		t.Fatalf("restoring what is gone from its entry: got %d %s", res.Status, res.Body)
	}
	if trash := trashOf(t, c, placeID); len(trash) != 0 {
		t.Fatalf("the trash lists %+v", trash)
	}
}

// Items are removed for good 30 days after they were deleted, by the Drive on its own: when it starts, every hour, and
// before the Trash view is shown. The test moves the Drive's clock forward (harness.TestClockShift) and restarts it.
func TestTheTrashKeepsItemsFor30Days(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	old := uploadedInto(t, d.As(reka), placeID, 0, "Old.bin", randomBytes(t, 4000))
	recent := uploadedInto(t, d.As(reka), placeID, 0, "Recent.bin", randomBytes(t, 300))
	kept := uploadedInto(t, d.As(reka), placeID, 0, "Kept.bin", randomBytes(t, 20))
	deleted(t, d.As(reka), placeID, old.ID)
	oldEntry := entryOf(t, d, "Old.bin")

	// 29 days later: Old.bin is still there; Recent.bin is deleted.
	d.Restart(t, map[string]string{harness.TestClockShift: (29 * 24 * time.Hour).String()})
	if trash := trashOf(t, d.As(reka), placeID); len(trash) != 1 || trash[0].ID != old.ID {
		t.Fatalf("after 29 days the trash lists %+v, want Old.bin", trash)
	}
	deleted(t, d.As(reka), placeID, recent.ID)
	recentEntry := entryOf(t, d, "Recent.bin")
	if used := usage(t, d).Used; *used != 4320 {
		t.Fatalf("used %d, want 4320: everything, the trash included", *used)
	}
	// Leftovers on the storage that the index does not know: one from long ago, one from today, and something that is
	// not an entry at all.
	d.Storage.Put(t, rekasTrash+"/2026-01-02 0badc0de/Leftover.txt", "left over")
	today := time.Now().Add(29 * 24 * time.Hour).UTC().Format("2006-01-02") // as the Drive's clock has it
	d.Storage.Put(t, rekasTrash+"/"+today+" 0badc0de/Leftover.txt", "left over today")
	d.Storage.Put(t, rekasTrash+"/Not an entry/Note.txt", "not the Drive's")

	// 31 days after Old.bin was deleted, 2 after Recent.bin: the Drive removes Old.bin as it starts, before anyone
	// looks at the trash.
	d.Restart(t, map[string]string{harness.TestClockShift: (31 * 24 * time.Hour).String()})
	harness.Eventually(t, "Old.bin is removed from the trash for good", func() bool {
		_, ok := d.Storage.List(t, oldEntry)
		return !ok
	})
	if used := usage(t, d).Used; *used != 320 {
		t.Fatalf("used %d, want 320 once Old.bin is gone for good", *used)
	}
	trash := trashOf(t, d.As(reka), placeID)
	if len(trash) != 1 || trash[0].ID != recent.ID {
		t.Fatalf("after 31 days the trash lists %+v, want only Recent.bin", trash)
	}
	if got := onStorage(t, d, recentEntry); !slices.Equal(got, []string{"Recent.bin"}) {
		t.Fatalf("Recent.bin's entry holds %q", got)
	}
	if res := restore(t, d.As(reka), placeID, old.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring Old.bin after 31 days: got %d %s, want 404", res.Status, res.Body)
	}
	got := onStorage(t, d, rekasTrash)
	if slices.Contains(got, "2026-01-02 0badc0de/") || !slices.Contains(got, today+" 0badc0de/") || !slices.Contains(got, "Not an entry/") {
		t.Fatalf("the trash on the storage holds %q: want the old leftover gone, the rest kept", got)
	}
	if _, items := myFiles(t, d, reka); !slices.Equal(names(items), []string{"Kept.bin"}) || items[0].ID != kept.ID {
		t.Fatalf("My files lists %q", names(items))
	}
}
