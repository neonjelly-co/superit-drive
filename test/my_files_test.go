package test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// myFiles finds the member's "My files" the way the interface does and lists it.
func myFiles(t *testing.T, d *harness.Drive, m harness.Member) (placeID int64, items []itemJSON) {
	t.Helper()
	res := d.As(m).Get(t, "/api/places")
	if res.Status != http.StatusOK {
		t.Fatalf("places for %s: %d %s", m.Email, res.Status, res.Body)
	}
	var places placesJSON
	res.JSON(t, &places)
	if len(places.Places) == 0 || places.Places[0].Kind != "my-files" {
		t.Fatalf("places for %s: want their own My files first, got %s", m.Email, res.Body)
	}
	placeID = places.Places[0].ID

	res = d.As(m).Get(t, fmt.Sprintf("/api/places/%d/items", placeID))
	if res.Status != http.StatusOK {
		t.Fatalf("items for %s: %d %s", m.Email, res.Status, res.Body)
	}
	var list itemsJSON
	res.JSON(t, &list)
	return placeID, list.Items
}

func names(items []itemJSON) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func TestAMemberSeesTheFilesAndFoldersInTheirOwnTopFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)

	// Files put on the storage directly, as someone with the storage login could.
	top := "My files/reka@example.com"
	d.Storage.Put(t, top+"/Tax notes 2026.pdf", "%PDF-1.7 tax notes")
	d.Storage.Put(t, top+"/Scan_0412.jpg", strings.Repeat("x", 3800))
	d.Storage.Put(t, top+"/Réka's list #1 & 100% done?.txt", "odd name")
	d.Storage.Put(t, top+"/Drafts/Offer for Bakery.docx", "offer") // inside a folder, so not at the top
	d.Storage.Put(t, "My files/bence@example.com/Not Réka's.txt", "someone else's")

	_, items := myFiles(t, d, reka)

	wantNames := []string{"Drafts", "Réka's list #1 & 100% done?.txt", "Scan_0412.jpg", "Tax notes 2026.pdf"}
	if got := names(items); !slices.Equal(got, wantNames) {
		t.Fatalf("got %q, want %q (folders first, then by name)", got, wantNames)
	}

	onStorage, _ := d.Storage.List(t, top)
	for _, it := range items {
		i := slices.IndexFunc(onStorage, func(e harness.Entry) bool { return e.Name == it.Name })
		if i < 0 {
			t.Fatalf("%q is not on the storage", it.Name)
		}
		e := onStorage[i]

		wantKind := map[bool]string{true: "folder", false: "file"}[e.Folder]
		if it.Kind != wantKind {
			t.Errorf("%q: kind %q, want %q", it.Name, it.Kind, wantKind)
		}
		switch {
		case e.Folder && it.Size != nil:
			t.Errorf("%q: a folder has no size, got %d", it.Name, *it.Size)
		case !e.Folder && (it.Size == nil || *it.Size != e.Size):
			t.Errorf("%q: size %v, want %d", it.Name, it.Size, e.Size)
		}
		if it.Changed == nil {
			t.Errorf("%q: no changed date", it.Name)
		} else if changed, err := time.Parse(time.RFC3339, *it.Changed); err != nil || !changed.Equal(e.Modified) {
			t.Errorf("%q: changed %s, want %s", it.Name, *it.Changed, e.Modified.Format(time.RFC3339))
		}
	}
}

func TestTheStorageIsAReadableTreeWithOneTopFolderPerMember(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)

	if _, ok := d.Storage.List(t, ""); ok {
		t.Fatal("the Drive's folder should not exist before anyone uses the Drive")
	}

	for _, m := range []harness.Member{reka, bence} {
		if _, items := myFiles(t, d, m); len(items) != 0 {
			t.Errorf("%s: a new member's My files should be empty, got %q", m.Email, names(items))
		}
	}

	root, _ := d.Storage.List(t, "")
	if len(root) != 1 || root[0].Name != "My files" || !root[0].Folder {
		t.Fatalf("the Drive's folder should hold only \"My files\", got %+v", root)
	}
	tops, _ := d.Storage.List(t, "My files")
	var got []string
	for _, e := range tops {
		if !e.Folder {
			t.Errorf("%q is not a folder", e.Name)
		}
		got = append(got, e.Name)
	}
	slices.Sort(got)
	if want := []string{"bence@example.com", "reka@example.com"}; !slices.Equal(got, want) {
		t.Fatalf("one top folder per member: got %q, want %q", got, want)
	}

	// A file the Drive lists is the file of that name in the member's folder.
	d.Storage.Put(t, "My files/bence@example.com/notes.txt", "Bence's notes")
	if _, items := myFiles(t, d, bence); !slices.Equal(names(items), []string{"notes.txt"}) {
		t.Fatalf("got %q", names(items))
	}
}

func TestTheListFollowsTheStorage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	top := "My files/reka@example.com"
	d.Storage.Put(t, top+"/a.txt", "a")

	firstID, items := myFiles(t, d, reka)
	if !slices.Equal(names(items), []string{"a.txt"}) {
		t.Fatalf("got %q", names(items))
	}
	idOfA := items[0].ID

	d.Storage.Put(t, top+"/b.txt", "b")
	d.Storage.Put(t, top+"/a.txt", "a, longer now")
	secondID, items := myFiles(t, d, reka)
	if firstID != secondID {
		t.Fatalf("My files changed its id from %d to %d", firstID, secondID)
	}
	if !slices.Equal(names(items), []string{"a.txt", "b.txt"}) {
		t.Fatalf("got %q", names(items))
	}
	if items[0].ID != idOfA || items[0].Size == nil || *items[0].Size != int64(len("a, longer now")) {
		t.Fatalf("a.txt should keep its id and show its new size, got %+v", items[0])
	}

	d.Storage.Delete(t, top+"/a.txt", false)
	if _, items := myFiles(t, d, reka); !slices.Equal(names(items), []string{"b.txt"}) {
		t.Fatalf("got %q", names(items))
	}

	// If the whole folder goes, My files is empty and the folder is made again.
	d.Storage.Delete(t, top, true)
	if _, items := myFiles(t, d, reka); len(items) != 0 {
		t.Fatalf("got %q", names(items))
	}
	if _, ok := d.Storage.List(t, top); !ok {
		t.Fatal("the member's folder was not made again")
	}
}

func TestNobodyElseCanListAMembersFiles(t *testing.T) {
	t.Parallel()
	// Réka is a project admin: "My files" is closed to admins too.
	d := harness.Start(t, reka, bence)
	d.Storage.Put(t, "My files/bence@example.com/Payslip November.pdf", "private")

	bencesID, _ := myFiles(t, d, bence)
	rekasID, _ := myFiles(t, d, reka)

	res := d.As(reka).Get(t, fmt.Sprintf("/api/places/%d/items", bencesID))
	if res.Status != http.StatusNotFound || strings.Contains(string(res.Body), "Payslip") {
		t.Fatalf("Réka listing Bence's My files: got %d %s, want 404 without his files", res.Status, res.Body)
	}
	// The same answer as a place that does not exist, so nobody learns which places exist.
	missing := d.As(reka).Get(t, fmt.Sprintf("/api/places/%d/items", rekasID+bencesID+100))
	if missing.Status != res.Status || !bytes.Equal(missing.Body, res.Body) {
		t.Fatalf("a missing place answers %d %s; another member's answers %d %s", missing.Status, missing.Body, res.Status, res.Body)
	}
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		if res := d.As(reka).Get(t, "/api/places/"+bad+"/items"); res.Status != http.StatusNotFound {
			t.Errorf("place %q: got %d, want 404", bad, res.Status)
		}
	}
}

func TestTheIndexIsOneSQLiteFileInTheDataFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	d.Storage.Put(t, "My files/reka@example.com/a.txt", "a")
	myFiles(t, d, reka)

	entries, err := os.ReadDir(d.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// SQLite keeps its write-ahead log next to the file while the Drive runs.
		if e.Name() != "index.sqlite" && e.Name() != "index.sqlite-wal" && e.Name() != "index.sqlite-shm" {
			t.Errorf("the data folder holds %q; it should hold only the index", e.Name())
		}
	}
	head := make([]byte, 16)
	f, err := os.Open(filepath.Join(d.DataDir, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(head); err != nil || string(head) != "SQLite format 3\x00" {
		t.Fatalf("index.sqlite is not a SQLite file: %q", head)
	}
}
