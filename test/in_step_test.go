package test

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The Drive in step with its storage: a folder is listed from the index at once and looked at on the storage in the
// background as it is opened; a slow walk looks at every folder now and then. What appears on the storage directly
// takes the access of the folder it is in; what goes is hidden at once and dropped, with its sharing, only when a later
// look still misses it. Access is never read from the storage. These tests change the storage directly, as someone
// with the storage login would, and look only at what the Drive answers.

// fastWalk makes the Drive walk the storage as soon as it starts and every interval after (a test build only).
func fastWalk(every string) map[string]string {
	return map[string]string{"SUPERIT_DRIVE_TEST_WALK_EVERY": every}
}

// walked waits until the Drive has finished n walks through the storage since it started, as its log says.
func walked(t *testing.T, d *harness.Drive, n int) {
	t.Helper()
	harness.Eventually(t, fmt.Sprintf("the Drive has walked the storage %d times", n), func() bool {
		return strings.Count(d.Logs(), "the walk through the storage ended") >= n
	})
}

// itemAccess is who can see an item, one line each (accessLines).
func itemAccess(t *testing.T, c *harness.Client, placeID, itemID int64) []string {
	t.Helper()
	return accessLines(accessOf(t, c, itemAccessPath(placeID, itemID)))
}

// A folder is listed from the index at once, and says it is being looked at on the storage; asked again with ?wait,
// it answers once the look is done, with what the storage holds.
func TestAListComesFromTheIndexFirstThenTheStorage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	uploaded(t, c, placeID, "a.txt", []byte("a"))
	d.Storage.Put(t, rekasTop+"/From outside.txt", "put there directly")

	var first, second itemsJSON
	c.Get(t, listPath(placeID, 0)).JSON(t, &first)
	if !first.Checking || !slices.Equal(names(first.Items), []string{"a.txt"}) {
		t.Fatalf("the first answer: checking %v, %q; want the index's list while the storage is looked at", first.Checking, names(first.Items))
	}
	c.Get(t, listPath(placeID, 0)+"?wait").JSON(t, &second)
	if second.Checking || !slices.Equal(names(second.Items), []string{"From outside.txt", "a.txt"}) {
		t.Fatalf("after the look: checking %v, %q", second.Checking, names(second.Items))
	}
	// Waiting when no look is under way answers at once, and starts none.
	began := time.Now()
	var again itemsJSON
	c.Get(t, listPath(placeID, 0)+"?wait").JSON(t, &again)
	if again.Checking || time.Since(began) > time.Second || len(again.Items) != 2 {
		t.Fatalf("waiting with no look under way: %v, checking %v, %q", time.Since(began), again.Checking, names(again.Items))
	}
}

// Something put on the storage directly has the access of the folder it is in, and nothing of its own: whoever the
// folder was shared with sees it, a public link on the folder covers it, and nobody else sees it.
func TestWhatAppearsOnTheStorageTakesTheAccessOfItsFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	_, page := madeLink(t, d, b, x.place, x.clients.ID, false)

	d.Storage.Put(t, "My files/bence@example.com/Clients/New offer.pdf", "new")
	fresh := named(t, list(t, b, x.place, x.clients.ID).Items, "New offer.pdf")
	if got, want := sorted(itemAccess(t, b, x.place, fresh.ID)), sorted([]string{bence.UserID + " owner (owner)", dora.UserID + " viewer (from Clients)", "link viewer (from Clients)"}); !slices.Equal(got, want) {
		t.Fatalf("who can see New offer.pdf: %q, want %q", got, want)
	}
	if fresh.SeenBy == nil || !fresh.SeenBy.Link || !slices.Equal(sorted(fresh.SeenBy.Members), sorted([]string{bence.UserID, dora.UserID})) {
		t.Fatalf("the list says New offer.pdf is seen by %+v", fresh.SeenBy)
	}
	if !slices.Contains(names(list(t, o, x.place, x.clients.ID).Items), "New offer.pdf") {
		t.Fatal("Dóra, a viewer of Clients, does not see New offer.pdf in it")
	}
	if p := readPage(t, v, page); !slices.Contains(p.rows, "New offer.pdf") {
		t.Fatalf("the public link on Clients lists %q", p.rows)
	}
	if got := sharedNames(t, o); !slices.Equal(got, []string{entry("Clients", x.place)}) {
		t.Fatalf("Dóra's Shared with me: %q", got)
	}
	// Nobody else: not Réka, a project admin (My files is closed to admins), and nothing beside Clients for Dóra.
	if res := d.As(reka).Get(t, downloadPath(x.place, fresh.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("Réka downloading New offer.pdf: %d", res.Status)
	}
	d.Storage.Put(t, "My files/bence@example.com/Beside Clients.txt", "beside")
	beside := named(t, list(t, b, x.place, 0).Items, "Beside Clients.txt")
	if res := o.Get(t, downloadPath(x.place, beside.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("Dóra downloading a file beside Clients: %d", res.Status)
	}

	// In a shared folder everyone may see, everyone sees what appears in it.
	team := madeSharedFolder(t, d.As(reka), "Team")
	ok(t, "giving everyone Team", shareWithEveryone(t, d.As(reka), team.ID, 0, "viewer"))
	d.Storage.Put(t, "Shared folders/Team/Plan.txt", "plan")
	plan := named(t, list(t, o, team.ID, 0).Items, "Plan.txt")
	if plan.SeenBy == nil || !plan.SeenBy.Everyone {
		t.Fatalf("Plan.txt in Team is seen by %+v, want everyone", plan.SeenBy)
	}
}

// What goes from the storage is hidden at once, with its sharing kept; back exactly as it was before the Drive confirms
// it gone, it is the same item, its sharing and its public link with it. Confirmed gone (a look at least ten minutes
// later that still misses it), it is dropped with its sharing and its link, for good: a file put there again later is a
// new item, seen only by whoever the folder gives access to.
func TestAnItemGoneFromTheStorageIsHiddenThenDroppedWithItsSharing(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o, v := d.As(bence), d.As(dora), visitor(d)
	missing := v.Get(t, noSuchLink(t))
	x := bencesFiles(t, d)
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	_, page := madeLink(t, d, b, x.place, x.notes.ID, false)
	top := "My files/bence@example.com"

	// Moved away on the storage: Notes.txt is gone, and what took its place is a new item, shared with nobody.
	d.Storage.Move(t, top+"/Notes.txt", top+"/Notes moved away.txt", false)
	l := list(t, b, x.place, 0)
	if slices.Contains(names(l.Items), "Notes.txt") {
		t.Fatalf("Bence's My files still lists Notes.txt: %q", names(l.Items))
	}
	away := named(t, l.Items, "Notes moved away.txt")
	if away.ID == x.notes.ID || !slices.Equal(itemAccess(t, b, x.place, away.ID), []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("the moved file: %+v, seen by %q", away, itemAccess(t, b, x.place, away.ID))
	}
	if got := sharedNames(t, o); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me while Notes.txt is gone: %q", got)
	}
	sameAs(t, "Dóra downloading Notes.txt while it is gone", o.Get(t, downloadPath(x.place, x.notes.ID)), o.Get(t, downloadPath(x.place, x.notes.ID+1000)))
	sameAsNoLink(t, "the link on Notes.txt while it is gone", v.Get(t, page), missing)
	if got := foundNames(t, b, "Notes"); !slices.Equal(got, []string{"Notes moved away.txt"}) {
		t.Fatalf("searching for Notes: %q", got)
	}

	// Moved back, as it was: the same item, shared as before, its link working again.
	d.Storage.Move(t, top+"/Notes moved away.txt", top+"/Notes.txt", false)
	if back := named(t, list(t, b, x.place, 0).Items, "Notes.txt"); back.ID != x.notes.ID {
		t.Fatalf("Notes.txt came back as item %d, want %d", back.ID, x.notes.ID)
	}
	if got := sharedNames(t, o); !slices.Equal(got, []string{entry("Notes.txt", x.place)}) {
		t.Fatalf("Dóra's Shared with me once Notes.txt is back: %q", got)
	}
	if p := readPage(t, v, page); p.name != "Notes.txt" {
		t.Fatalf("the link once Notes.txt is back: %+v", p)
	}

	// Gone for real. Eleven minutes later (the Drive's clock moved on), a look confirms it, and it is dropped.
	d.Storage.Delete(t, top+"/Notes.txt", false)
	list(t, b, x.place, 0)
	d.Restart(t, map[string]string{harness.TestClockShift: "11m"})
	b, o, v = d.As(bence), d.As(dora), visitor(d)
	list(t, b, x.place, 0)
	nextSecond(t, d)
	d.Storage.Put(t, top+"/Notes.txt", "a new file of that name")
	again := named(t, list(t, b, x.place, 0).Items, "Notes.txt")
	if again.ID == x.notes.ID {
		t.Fatal("a file put there after Notes.txt was dropped took over its id")
	}
	if got := itemAccess(t, b, x.place, again.ID); !slices.Equal(got, []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("who can see the new Notes.txt: %q", got)
	}
	if got := sharedNames(t, o); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me after Notes.txt was dropped: %q", got)
	}
	sameAsNoLink(t, "the link on the dropped Notes.txt", v.Get(t, page), missing)
	sameAs(t, "the dropped Notes.txt by its id", b.Get(t, downloadPath(x.place, x.notes.ID)), b.Get(t, downloadPath(x.place, x.notes.ID+1000)))
}

// A file replaced on the storage directly (written over in place) is the same file with new content, as when an upload
// through the Drive replaces it: its link serves the new content. But a file that the Drive saw gone, and that another
// file then took the name of, is not: its link never serves that other file.
func TestAFileReplacedOnTheStorage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c, v := d.As(reka), visitor(d)
	missing := v.Get(t, noSuchLink(t))
	placeID, _ := myFiles(t, d, reka)
	a := uploaded(t, c, placeID, "a.txt", []byte("first"))
	b := uploaded(t, c, placeID, "b.txt", []byte("first"))
	_, pageA := madeLink(t, d, c, placeID, a.ID, false)
	_, pageB := madeLink(t, d, c, placeID, b.ID, false)

	d.Storage.Put(t, rekasTop+"/a.txt", "written over in place")
	if got := named(t, list(t, c, placeID, 0).Items, "a.txt"); got.ID != a.ID || *got.Size != int64(len("written over in place")) {
		t.Fatalf("a.txt written over: %+v", got)
	}
	if res := v.Get(t, inLink(pageA, a.ID, "/download")); string(res.Body) != "written over in place" {
		t.Fatalf("the link on a.txt: %d %s", res.Status, res.Body)
	}

	d.Storage.Delete(t, rekasTop+"/b.txt", false)
	list(t, c, placeID, 0)
	nextSecond(t, d)
	d.Storage.Put(t, rekasTop+"/b.txt", "first") // even with the same content: it was made anew
	if got := named(t, list(t, c, placeID, 0).Items, "b.txt"); got.ID == b.ID {
		t.Fatal("a file that took the name of one the Drive saw gone took over its id")
	}
	sameAsNoLink(t, "b.txt's link once another file took its name", v.Get(t, pageB), missing)
	sameAsNoLink(t, "b.txt's link, its file", v.Get(t, inLink(pageB, b.ID, "/download")), missing)

	// A member's own upload where a hidden item was is a new item too: it never takes over the hidden one's link.
	e := uploaded(t, c, placeID, "e.txt", []byte("first"))
	_, pageE := madeLink(t, d, c, placeID, e.ID, false)
	d.Storage.Delete(t, rekasTop+"/e.txt", false)
	list(t, c, placeID, 0)
	if again := uploaded(t, c, placeID, "e.txt", []byte("uploaded anew")); again.ID == e.ID {
		t.Fatal("an upload where a hidden item was took over its id")
	}
	sameAsNoLink(t, "e.txt's link once an upload took its name", v.Get(t, pageE), missing)

	// A change asked for on a file gone from the storage answers that it is gone, and hides it.
	f := uploaded(t, c, placeID, "f.txt", []byte("f"))
	d.Storage.Delete(t, rekasTop+"/f.txt", false)
	if res := rename(t, c, placeID, f.ID, "g.txt"); res.Status != http.StatusNotFound || !strings.Contains(errorOf(t, res), "no longer") {
		t.Fatalf("renaming a file gone from the storage: %d %s", res.Status, res.Body)
	}
	if l := c.Get(t, listPath(placeID, 0)); strings.Contains(string(l.Body), `"f.txt"`) {
		t.Fatalf("the list from the index still holds f.txt: %s", l.Body)
	}
}

// The storage holds the Drive's mark, .superit-drive/storage-id, with the id its index keeps. Without it (a storage
// empty, mounted wrongly, someone else's), a look changes nothing at all: nothing is hidden, dropped or added, and the
// log says why and what the file should hold. With it back, looks go on.
func TestNothingChangesWithoutTheDrivesMark(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	uploaded(t, c, placeID, "a.txt", []byte("a"))
	uploaded(t, c, placeID, "b.txt", []byte("b"))
	list(t, c, placeID, 0)
	mark, ok := d.Storage.Get(t, ".superit-drive/storage-id")
	if !ok || len(strings.TrimSpace(string(mark))) != 32 {
		t.Fatalf("the Drive's mark: %q", mark)
	}

	d.Storage.Delete(t, ".superit-drive/storage-id", false)
	d.Storage.Delete(t, rekasTop+"/b.txt", false)
	d.Storage.Put(t, rekasTop+"/c.txt", "c")
	if got := names(list(t, c, placeID, 0).Items); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("without the mark the list changed: %q", got)
	}
	if logs := d.Logs(); !strings.Contains(logs, "does not hold the Drive's mark") || !strings.Contains(logs, strings.TrimSpace(string(mark))) {
		t.Fatalf("the log does not say the mark is missing, and what it held:\n%s", logs)
	}
	// The whole storage emptied, as one mounted wrongly would look, and an hour on: still nothing changes.
	d.Storage.Delete(t, "My files", true)
	d.Storage.Delete(t, ".superit-drive", true)
	d.Restart(t, map[string]string{harness.TestClockShift: "1h"})
	c = d.As(reka)
	if got := names(list(t, c, placeID, 0).Items); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("an empty storage emptied the list: %q", got)
	}
	// Another Drive's mark changes nothing either.
	d.Storage.Put(t, ".superit-drive/storage-id", "0123456789abcdef0123456789abcdef\n")
	if got := names(list(t, c, placeID, 0).Items); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
		t.Fatalf("with another Drive's mark the list changed: %q", got)
	}

	// The files and the mark back: looks go on, and see what is there now.
	d.Storage.Delete(t, ".superit-drive/storage-id", false)
	d.Storage.Put(t, ".superit-drive/storage-id", string(mark))
	d.Storage.Put(t, rekasTop+"/c.txt", "c")
	if got := names(list(t, c, placeID, 0).Items); !slices.Equal(got, []string{"c.txt"}) {
		t.Fatalf("with the mark back: %q", got)
	}
}

// Looks at folders run while members change them, and never undo what the Drive itself just did: a rename, a move, an
// upload, a delete and a restore keep their ids and their sharing, and nothing the Drive changed is hidden. An upload
// under way is never seen in its folder before it is whole, and the Drive's hidden folders are never listed.
func TestLooksNeverUndoTheDrivesOwnChanges(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	project := madeFolder(t, c, placeID, 0, "Project")
	other := madeFolder(t, c, placeID, 0, "Other")
	plan := uploadedInto(t, c, placeID, project.ID, "Plan.txt", []byte("plan"))
	ok(t, "sharing Plan.txt with Bence", share(t, c, placeID, plan.ID, bence, "viewer"))

	// Members open the folders all the while: each opening is a look at the storage.
	stop := make(chan struct{})
	var looking sync.WaitGroup
	for i := range 4 {
		looking.Add(1)
		go func() {
			defer looking.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, f := range []int64{0, project.ID, other.ID} {
					res := c.Get(t, listPath(placeID, f))
					if i%2 == 0 && res.Status == http.StatusOK {
						c.Get(t, listPath(placeID, f)+"?wait")
					}
				}
			}
		}()
	}

	var kept []itemJSON
	for i := range 12 {
		name := fmt.Sprintf("Project %d", i)
		if res := rename(t, c, placeID, project.ID, name); res.Status != http.StatusOK {
			t.Fatalf("renaming Project: %d %s", res.Status, res.Body)
		}
		kept = append(kept, uploadedInto(t, c, placeID, 0, fmt.Sprintf("File %d.txt", i), []byte("file")))
		ok(t, "moving Plan.txt into Other", move(t, c, placeID, plan.ID, other.ID))
		ok(t, "moving Plan.txt back", move(t, c, placeID, plan.ID, project.ID))
		gone := uploadedInto(t, c, placeID, other.ID, fmt.Sprintf("Deleted %d.txt", i), []byte("deleted"))
		deleted(t, c, placeID, gone.ID)
		if i%3 == 0 {
			kept = append(kept, restored(t, c, placeID, gone.ID).Item)
		}
		if l := list(t, c, placeID, 0); named(t, l.Items, name).ID != project.ID {
			t.Fatalf("after renaming Project to %s the list holds %q", name, names(l.Items))
		}
	}

	// A slow upload, while folders are looked at: its file never shows before it is whole.
	r, w := io.Pipe()
	done := slowUpload(d, reka, placeID, "Slow.bin", r, 3<<20)
	for i := range 6 {
		_, _ = w.Write(make([]byte, 512<<10))
		if i == 5 {
			break // the whole file has arrived: from now on it may be in its folder
		}
		for _, it := range list(t, c, placeID, 0).Items {
			if it.Name == "Slow.bin" || strings.HasPrefix(it.Name, ".") {
				t.Errorf("listed while an upload is under way: %q", it.Name)
			}
		}
	}
	w.Close()
	if status := <-done; status != http.StatusCreated {
		t.Fatalf("the slow upload: %d", status)
	}
	close(stop)
	looking.Wait()

	all := map[string]int64{}
	for _, f := range []int64{0, project.ID, other.ID} {
		for _, it := range list(t, c, placeID, f).Items {
			all[it.Name] = it.ID
		}
	}
	for _, it := range append(kept, project, plan) {
		if id, ok := all[it.Name]; it.Name != "Project" && (!ok || id != it.ID) {
			t.Errorf("%q: listed as item %d (%v), want %d", it.Name, id, ok, it.ID)
		}
	}
	if all["Project 11"] != project.ID || all["Plan.txt"] != plan.ID || all["Slow.bin"] == 0 {
		t.Fatalf("after it all: %v", all)
	}
	if got := sharedNames(t, d.As(bence)); !slices.Equal(got, []string{entry("Plan.txt", placeID)}) {
		t.Fatalf("Bence's Shared with me: %q", got)
	}
}

// The trash is on the storage too. An entry removed there directly is hidden from the trash, and dropped when a later
// look confirms it; one put back as it was is back. An entry put there directly, holding one file or folder, shows in
// the trash, and is restored like anything else, with only the access of where it goes; anything else there is left as
// it is. What is put into the uploads folder directly is never shown anywhere.
func TestTheTrashAndTheHiddenFoldersOnTheStorage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	a := uploaded(t, c, placeID, "a.txt", []byte("a"))
	b := uploaded(t, c, placeID, "b.txt", []byte("b"))
	deleted(t, c, placeID, a.ID)
	deleted(t, c, placeID, b.ID)
	entryA, entryB := entryOf(t, d, "a.txt"), entryOf(t, d, "b.txt")

	// b's entry moved away and back, as a storage that answered wrong for a while: hidden, then back.
	d.Storage.Move(t, entryB, rekasTrash+"/elsewhere", true)
	d.Storage.Delete(t, entryA, true)
	if trash := trashOf(t, c, placeID); len(trash) != 0 {
		t.Fatalf("the trash lists %+v while both entries are gone", trash)
	}
	if res := restore(t, c, placeID, a.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring a.txt whose entry is gone: %d %s", res.Status, res.Body)
	}
	d.Storage.Move(t, rekasTrash+"/elsewhere", entryB, true)
	if trash := trashOf(t, c, placeID); len(trash) != 1 || trash[0].ID != b.ID {
		t.Fatalf("the trash lists %+v once b.txt's entry is back", trash)
	}

	// Entries put there directly: one holding one file shows; one holding two things, and a folder not named as an
	// entry, do not.
	d.Storage.Put(t, rekasTrash+"/2026-11-01 0badf00d/Gift.txt", "from outside")
	d.Storage.Put(t, rekasTrash+"/2026-11-02 0badf00d/One.txt", "one")
	d.Storage.Put(t, rekasTrash+"/2026-11-02 0badf00d/Two.txt", "two")
	d.Storage.Put(t, rekasTrash+"/Not an entry/Note.txt", "note")
	trash := trashOf(t, c, placeID)
	if len(trash) != 2 || trash[0].ID != b.ID || trash[1].Name != "Gift.txt" || trash[1].Deleted.Format("2006-01-02") != "2026-11-01" || len(trash[1].From) != 0 {
		t.Fatalf("the trash lists %+v", trash)
	}
	gift := restored(t, c, placeID, trash[1].ID)
	if gift.Item.Name != "Gift.txt" || len(gift.Path) != 0 || !slices.Equal(itemAccess(t, c, placeID, gift.Item.ID), []string{reka.UserID + " owner (owner)"}) {
		t.Fatalf("Gift.txt restored: %+v", gift)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Gift.txt"); string(got) != "from outside" {
		t.Fatalf("Gift.txt holds %q", got)
	}

	// Eleven minutes on, a.txt is dropped for good: an entry holding a.txt put back now is another item.
	d.Restart(t, map[string]string{harness.TestClockShift: "11m"})
	c = d.As(reka)
	trashOf(t, c, placeID)
	d.Storage.Put(t, entryA+"/a.txt", "a")
	trash = trashOf(t, c, placeID)
	if i := slices.IndexFunc(trash, func(e trashJSON) bool { return e.Name == "a.txt" }); i < 0 || trash[i].ID == a.ID {
		t.Fatalf("the trash lists %+v: want a.txt back as another item", trash)
	}
	if res := restore(t, c, placeID, a.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring the dropped a.txt by its id: %d %s", res.Status, res.Body)
	}

	// The uploads folder: what is put there directly is never listed or found.
	d.Storage.Put(t, uploadsFolder+"/0123456789abcdef0123456789abcdef", "not an upload of the Drive's")
	d.Storage.Put(t, uploadsFolder+"/Secret.txt", "not an upload of the Drive's")
	for _, it := range list(t, c, placeID, 0).Items {
		if strings.HasPrefix(it.Name, ".") || it.Name == "Secret.txt" {
			t.Fatalf("listed from the hidden folder: %q", it.Name)
		}
	}
	if got := foundNames(t, c, "Secret"); len(got) != 0 {
		t.Fatalf("found in the hidden folder: %q", got)
	}
}

// Nothing put on the storage makes a place or gives anyone access: a folder in "Shared folders" or "My files" that no
// shared folder or member has is seen by nobody, and the walk's log counts it; a file or folder named like the Drive's
// own, inside a place, is just a file or folder there. Names the Drive would refuse are left out: never listed, found,
// zipped or counted.
func TestNothingOnTheStorageMakesAPlaceOrGivesAccess(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: fastWalk("300ms")})
	b := d.As(bence)
	x := bencesFiles(t, d)
	rekas, _ := myFiles(t, d, reka)
	walks := strings.Count(d.Logs(), "the walk through the storage ended")
	d.Storage.Put(t, "Shared folders/Secret Plans/Plans.txt", "plans")
	d.Storage.Put(t, "My files/dora@example.com/Hers.txt", "not a member yet")
	d.Storage.Put(t, "My files/bence@example.com/.superit-drive/storage-id", "his own file")
	d.Storage.Put(t, "My files/bence@example.com/.superit-drive/trash/2026-11-01 0badf00d/Old.txt", "his own file")
	d.Storage.PutEscaped(t, "My%20files/bence%40example.com/Line%0Abreak.txt", "a name with a line break")
	d.Storage.PutEscaped(t, "My%20files/bence%40example.com/Bad%FFname.txt", "a name that is not text")
	d.Storage.Put(t, "My files/bence@example.com/Fine.txt", "fine")
	walked(t, d, walks+2)

	for _, m := range []harness.Member{reka, bence} {
		for _, p := range placesOf(t, d, m) {
			if p.Name == "Secret Plans" {
				t.Fatalf("%s sees a shared folder made on the storage", m.Email)
			}
		}
		for _, q := range []string{"Plans", "Hers"} {
			if got := foundNames(t, d.As(m), q); len(got) != 0 {
				t.Fatalf("%s searching for %q finds %q", m.Email, q, got)
			}
		}
	}
	got := names(list(t, b, x.place, 0).Items)
	if !slices.Contains(got, ".superit-drive") || !slices.Contains(got, "Fine.txt") || slices.ContainsFunc(got, func(n string) bool {
		return strings.Contains(n, "Line") || strings.Contains(n, "name.txt")
	}) {
		t.Fatalf("Bence's My files lists %q", got)
	}
	if found := foundNames(t, d.As(reka), "storage-id"); len(found) != 0 {
		t.Fatalf("Réka finds Bence's file named like the Drive's own: %q", found)
	}
	if trash := trashOf(t, b, x.place); len(trash) != 0 {
		t.Fatalf("a folder named like the Drive's trash inside Bence's My files fills his trash: %+v", trash)
	}
	if trash := trashOf(t, d.As(reka), rekas); len(trash) != 0 {
		t.Fatalf("Réka's trash: %+v", trash)
	}
	logs := d.Logs()
	if !strings.Contains(logs, "belong to no shared folder") || !strings.Contains(logs, "Secret Plans") || !strings.Contains(logs, "belong to nobody's My files") {
		t.Fatalf("the log does not say which folders belong to no place:\n%s", logs)
	}
	if strings.Contains(logs, "dora@example.com") {
		t.Fatal("the log names an email address")
	}
}

// The walk looks at every folder now and then, so what changes on the storage in a folder nobody opens is found by
// search, and goes from Shared with me, without anyone opening it.
func TestTheWalkFindsChangesInFoldersNobodyOpens(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: fastWalk("300ms")})
	x := bencesFiles(t, d)
	o := d.As(dora)
	ok(t, "sharing Ledger.xlsx with Dóra", share(t, d.As(bence), x.place, x.ledger.ID, dora, "viewer"))
	d.Storage.Put(t, "My files/bence@example.com/Clients/Bakery/2026/Q4/Found by the walk.txt", "deep")
	d.Storage.Delete(t, "My files/bence@example.com/Clients/Bakery/2026/Ledger.xlsx", false)

	harness.Eventually(t, "a search finds what was put deep in a folder nobody opened", func() bool {
		return slices.Equal(foundNames(t, d.As(bence), "Found by the walk"), []string{"Found by the walk.txt"})
	})
	harness.Eventually(t, "Dóra's Shared with me no longer lists the file gone from the storage", func() bool {
		return len(sharedNames(t, o)) == 0
	})
	if !strings.Contains(d.Logs(), "the walk through the storage ended") {
		t.Fatalf("the log does not say the walk ended:\n%s", d.Logs())
	}
}

// A walk cut short by a restart goes on from where it was, and the log says so.
func TestAWalkCutShortGoesOnWhereItWas(t *testing.T) {
	t.Parallel()
	env := map[string]string{"SUPERIT_DRIVE_TEST_WALK_EVERY": "200ms", "SUPERIT_DRIVE_TEST_WALK_PAUSE": "150ms"}
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: env})
	c := d.As(reka)
	myFiles(t, d, reka)
	for i := range 12 {
		d.Storage.Put(t, fmt.Sprintf("%s/Folder %02d/File %02d.txt", rekasTop, i, i), "x")
	}
	harness.Eventually(t, "the walk is under way, past the first folders", func() bool {
		return len(foundNames(t, c, "File 01")) == 1
	})
	d.Restart(t, nil)
	c = d.As(reka)
	if !strings.Contains(d.Logs(), "goes on from where it was") {
		harness.Eventually(t, "the walk goes on from where it was", func() bool {
			return strings.Contains(d.Logs(), "goes on from where it was")
		})
	}
	harness.Eventually(t, "the walk that went on finds the last folder", func() bool {
		return len(foundNames(t, c, "File 11")) == 1
	})
	harness.Eventually(t, "the walk that went on ends", func() bool {
		return strings.Contains(d.Logs(), `the walk through the storage ended" resumed=true`)
	})
}

// A folder on the storage with more in it than the Drive reads at once is not read: nothing in it changes in the index,
// the Drive stays up, and the log says so. One just within the bound is read whole.
func TestAFolderTooLargeIsLeftAsItIs(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	d.Storage.Fill(t, rekasTop+"/Large", 10_000, "file ")
	d.Storage.Fill(t, rekasTop+"/Too large", 10_001, "file ")
	top := list(t, c, placeID, 0)
	if l := list(t, c, placeID, named(t, top.Items, "Large").ID); len(l.Items) != 10_000 {
		t.Fatalf("a folder of 10,000 files lists %d", len(l.Items))
	}
	if l := list(t, c, placeID, named(t, top.Items, "Too large").ID); len(l.Items) != 0 {
		t.Fatalf("a folder of 10,001 files lists %d", len(l.Items))
	}
	if res := c.Get(t, "/api/me"); res.Status != http.StatusOK {
		t.Fatalf("the Drive after a folder too large: %d", res.Status)
	}
	harness.Eventually(t, "the log says the folder is too large", func() bool {
		return strings.Contains(d.Logs(), "holds more than the Drive reads at once")
	})
}

// Folders go at most 255 deep below a place's top folder: the walk takes in a tree deeper than that down to there, and
// nothing below.
func TestADeepTreeIsTakenInDownToTheDeepestFolder(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: fastWalk("200ms")})
	c := d.As(reka)
	myFiles(t, d, reka)
	var chain []string
	for i := 1; i <= 260; i++ {
		chain = append(chain, fmt.Sprintf("d%d", i))
	}
	d.Storage.Fill(t, rekasTop+"/"+strings.Join(chain, "/"), 1, "deepest ")
	// The walk takes in one more folder at each step, and ends after the deepest: some seconds for all of them.
	for deadline := time.Now().Add(time.Minute); !strings.Contains(d.Logs(), "added=256"); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no walk took in 256 folders within a minute:\n%s", d.Logs())
		}
	}
	if got := foundNames(t, c, "d256"); !slices.Equal(got, []string{"d256"}) {
		t.Fatalf("searching for d256 finds %q", got)
	}
	for _, q := range []string{"d257", "deepest"} {
		if got := foundNames(t, c, q); len(got) != 0 {
			t.Fatalf("searching for %q finds %q, deeper than folders go", q, got)
		}
	}
}
