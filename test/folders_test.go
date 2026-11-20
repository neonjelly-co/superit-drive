package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's calls for folders, as the interface makes them. folderID 0 is the place's top folder.

func listPath(placeID, folderID int64) string {
	if folderID == 0 {
		return fmt.Sprintf("/api/places/%d/items", placeID)
	}
	return fmt.Sprintf("/api/places/%d/items/%d/items", placeID, folderID)
}

// list opens a folder as the interface does, and fails the test unless the Drive answered with it: the list from the
// index, and when the Drive says it is looking at the folder on the storage, the list again once it has (?wait).
func list(t *testing.T, c *harness.Client, placeID, folderID int64) itemsJSON {
	t.Helper()
	res := opened(t, c, placeID, folderID)
	if res.Status != http.StatusOK {
		t.Fatalf("listing folder %d: got %d %s", folderID, res.Status, res.Body)
	}
	var l itemsJSON
	res.JSON(t, &l)
	return l
}

// opened opens a folder as the interface does, and returns the last answer: the list from the index, then, while the
// Drive says it is looking at the folder on the storage, the list once it has.
func opened(t *testing.T, c *harness.Client, placeID, folderID int64) harness.Response {
	t.Helper()
	res := c.Get(t, listPath(placeID, folderID))
	for range 3 {
		var l struct {
			Checking bool `json:"checking"`
		}
		if res.Status != http.StatusOK || json.Unmarshal(res.Body, &l) != nil || !l.Checking {
			return res
		}
		res = c.Get(t, listPath(placeID, folderID)+"?wait")
	}
	return res
}

func sendJSON(t *testing.T, c *harness.Client, method, path string, body any) harness.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(t, method, path, bytes.NewReader(data))
}

func newFolder(t *testing.T, c *harness.Client, placeID, inID int64, name string) harness.Response {
	t.Helper()
	path := fmt.Sprintf("/api/places/%d/folders", placeID)
	if inID != 0 {
		path = fmt.Sprintf("/api/places/%d/items/%d/folders", placeID, inID)
	}
	return sendJSON(t, c, http.MethodPost, path, map[string]string{"name": name})
}

// madeFolder makes a folder and fails the test unless the Drive made it.
func madeFolder(t *testing.T, c *harness.Client, placeID, inID int64, name string) itemJSON {
	t.Helper()
	res := newFolder(t, c, placeID, inID, name)
	if res.Status != http.StatusCreated {
		t.Fatalf("making the folder %q: got %d %s", name, res.Status, res.Body)
	}
	var it itemJSON
	res.JSON(t, &it)
	if it.Kind != "folder" || it.Name != name {
		t.Fatalf("making the folder %q: got %+v", name, it)
	}
	return it
}

func rename(t *testing.T, c *harness.Client, placeID, itemID int64, name string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/rename", placeID, itemID), map[string]string{"name": name})
}

// move moves an item into the folder to; 0 is the place's top folder.
func move(t *testing.T, c *harness.Client, placeID, itemID, to int64) harness.Response {
	t.Helper()
	var dest any
	if to != 0 {
		dest = to
	}
	return sendJSON(t, c, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/move", placeID, itemID), map[string]any{"to": dest})
}

// uploadInto uploads a file into a folder (0: the top folder), saying what to do when its name is taken.
func uploadInto(t *testing.T, c *harness.Client, placeID, folderID int64, name string, content []byte, existing string) harness.Response {
	t.Helper()
	path := fmt.Sprintf("/api/places/%d/files", placeID)
	if folderID != 0 {
		path = fmt.Sprintf("/api/places/%d/items/%d/files", placeID, folderID)
	}
	q := url.Values{"name": {name}}
	if existing != "" {
		q.Set("existing", existing)
	}
	return c.Do(t, http.MethodPost, path+"?"+q.Encode(), bytes.NewReader(content))
}

func uploadedInto(t *testing.T, c *harness.Client, placeID, folderID int64, name string, content []byte) itemJSON {
	t.Helper()
	res := uploadInto(t, c, placeID, folderID, name, content, "")
	if res.Status != http.StatusCreated {
		t.Fatalf("uploading %q: got %d %s", name, res.Status, res.Body)
	}
	var it itemJSON
	res.JSON(t, &it)
	return it
}

// named finds the item called name, and fails the test when there is none.
func named(t *testing.T, items []itemJSON, name string) itemJSON {
	t.Helper()
	i := slices.IndexFunc(items, func(it itemJSON) bool { return it.Name == name })
	if i < 0 {
		t.Fatalf("no %q among %q", name, names(items))
	}
	return items[i]
}

// onStorage lists a folder on the storage by name.
func onStorage(t *testing.T, d *harness.Drive, p string) []string {
	t.Helper()
	entries, ok := d.Storage.List(t, p)
	if !ok {
		t.Fatalf("%q is not a folder on the storage", p)
	}
	var out []string
	for _, e := range entries {
		n := e.Name
		if e.Folder {
			n += "/"
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func TestNewFolderMakesAFolderOnTheStorageWhereThePersonIsLooking(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	clients := madeFolder(t, c, placeID, 0, "Clients")
	bakery := madeFolder(t, c, placeID, clients.ID, "Bakery Ltd")
	year := madeFolder(t, c, placeID, bakery.ID, "2026 Árvíztűrő")
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Clients/"}) {
		t.Fatalf("Réka's top folder on the storage: %q", got)
	}
	if got := onStorage(t, d, rekasTop+"/Clients/Bakery Ltd"); !slices.Equal(got, []string{"2026 Árvíztűrő/"}) {
		t.Fatalf("Bakery Ltd on the storage: %q", got)
	}

	// Opening a folder lists it, with the path down to it for the breadcrumb, and keeps every id.
	l := list(t, c, placeID, bakery.ID)
	if len(l.Items) != 1 || l.Items[0].ID != year.ID || l.Items[0].Kind != "folder" {
		t.Fatalf("Bakery Ltd holds %+v", l.Items)
	}
	if want := []folderJSON{{clients.ID, "Clients"}, {bakery.ID, "Bakery Ltd"}}; !slices.Equal(l.Path, want) {
		t.Fatalf("the path to Bakery Ltd is %+v, want %+v", l.Path, want)
	}
	if top := list(t, c, placeID, 0); len(top.Path) != 0 || named(t, top.Items, "Clients").ID != clients.ID {
		t.Fatalf("the top folder: %+v", top)
	}

	// Files go into the folder being looked at, and download from there.
	content := randomBytes(t, 20_000)
	ledger := uploadedInto(t, c, placeID, year.ID, "Ledger Q3.xlsx", content)
	if got, _ := d.Storage.Get(t, rekasTop+"/Clients/Bakery Ltd/2026 Árvíztűrő/Ledger Q3.xlsx"); !bytes.Equal(got, content) {
		t.Fatal("the file is not on the storage in its folder")
	}
	if res := c.Get(t, downloadPath(placeID, ledger.ID)); res.Status != http.StatusOK || !bytes.Equal(res.Body, content) {
		t.Fatalf("downloading from three folders deep: %d", res.Status)
	}
	if l := list(t, c, placeID, year.ID); len(l.Items) != 1 || l.Items[0].ID != ledger.ID {
		t.Fatalf("2026 holds %+v", l.Items)
	}

	// A name that is taken, by a folder or a file, is refused and nothing changes.
	for _, name := range []string{"Bakery Ltd"} {
		res := newFolder(t, c, placeID, clients.ID, name)
		var conflict conflictJSON
		res.JSON(t, &conflict)
		if res.Status != http.StatusConflict || !strings.Contains(conflict.Error, name) {
			t.Errorf("a second %q: got %d %s, want 409", name, res.Status, res.Body)
		}
	}
	if res := newFolder(t, c, placeID, year.ID, "Ledger Q3.xlsx"); res.Status != http.StatusConflict {
		t.Errorf("a folder with a file's name: got %d %s, want 409", res.Status, res.Body)
	}
	// So are names that cannot be stored, and making a folder inside a file.
	for _, name := range []string{"", ".", "..", "a/b", "../Escape", strings.Repeat("a", 256), "tab\there"} {
		if res := newFolder(t, c, placeID, clients.ID, name); res.Status != http.StatusBadRequest || errorOf(t, res) == "" {
			t.Errorf("a folder called %q: got %d %s, want 400 with a reason", name, res.Status, res.Body)
		}
	}
	if res := newFolder(t, c, placeID, ledger.ID, "Inside a file"); res.Status != http.StatusBadRequest {
		t.Errorf("a folder inside a file: got %d %s, want 400", res.Status, res.Body)
	}
	if res := c.Get(t, listPath(placeID, ledger.ID)); res.Status != http.StatusBadRequest {
		t.Errorf("listing a file as a folder: got %d %s, want 400", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop+"/Clients"); !slices.Equal(got, []string{"Bakery Ltd/"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Clients/"}) {
		t.Fatalf("something escaped into Réka's top folder: %q", got)
	}
}

// Folders and files put on the storage directly open like any other, at any depth.
func TestFoldersPutOnTheStorageDirectlyOpen(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	d.Storage.Put(t, rekasTop+"/Archive/2025/Q4/Report.pdf", "%PDF report")
	placeID, top := myFiles(t, d, reka)
	c := d.As(reka)

	archive := named(t, top, "Archive")
	y2025 := named(t, list(t, c, placeID, archive.ID).Items, "2025")
	q4 := named(t, list(t, c, placeID, y2025.ID).Items, "Q4")
	l := list(t, c, placeID, q4.ID)
	report := named(t, l.Items, "Report.pdf")
	if res := c.Get(t, downloadPath(placeID, report.ID)); res.Status != http.StatusOK || string(res.Body) != "%PDF report" {
		t.Fatalf("got %d %q", res.Status, res.Body)
	}
	if want := []string{"Archive", "2025", "Q4"}; len(l.Path) != 3 || l.Path[0].Name != want[0] || l.Path[2].Name != want[2] {
		t.Fatalf("path %+v", l.Path)
	}

	// A folder removed from the storage directly is listed from the index once more as it is opened, then, once the
	// Drive has looked, answers as one that is not there, and leaves the list of the folder above it.
	d.Storage.Delete(t, rekasTop+"/Archive/2025", true)
	res := opened(t, c, placeID, y2025.ID)
	if res.Status != http.StatusNotFound || errorOf(t, res) != notHere {
		t.Fatalf("a folder gone from the storage: got %d %s", res.Status, res.Body)
	}
	if res := c.Get(t, downloadPath(placeID, report.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("a file inside a folder gone from the storage: got %d %s", res.Status, res.Body)
	}
	if l := list(t, c, placeID, archive.ID); len(l.Items) != 0 {
		t.Fatalf("Archive still lists %q", names(l.Items))
	}

	// The place's own top folder removed directly: the next change into it makes it again, an upload included (it
	// arrives in the hidden uploads folder, and is moved into the top folder made again).
	d.Storage.Delete(t, rekasTop, true)
	if it := madeFolder(t, c, placeID, 0, "New"); it.Name != "New" {
		t.Fatalf("got %+v", it)
	}
	d.Storage.Delete(t, rekasTop, true)
	uploadedInto(t, c, placeID, 0, "a.txt", []byte("a"))
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("Réka's top folder on the storage holds %q", got)
	}
}

func TestRenameIsAMoveOnTheStorageAndKeepsTheId(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	drafts := madeFolder(t, c, placeID, 0, "Drafts")
	offer := uploadedInto(t, c, placeID, drafts.ID, "Offer.docx", []byte("offer"))
	notes := uploadedInto(t, c, placeID, 0, "notes.txt", []byte("notes"))

	// A file.
	res := rename(t, c, placeID, notes.ID, "Notes for Bence – 2026.txt")
	var renamed itemJSON
	res.JSON(t, &renamed)
	if res.Status != http.StatusOK || renamed.ID != notes.ID || renamed.Name != "Notes for Bence – 2026.txt" {
		t.Fatalf("renaming a file: got %d %s", res.Status, res.Body)
	}
	// A folder, with what is in it.
	res = rename(t, c, placeID, drafts.ID, "Offers")
	res.JSON(t, &renamed)
	if res.Status != http.StatusOK || renamed.ID != drafts.ID || renamed.Name != "Offers" {
		t.Fatalf("renaming a folder: got %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Notes for Bence – 2026.txt", "Offers/"}) {
		t.Fatalf("Réka's top folder on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Offers/Offer.docx"); string(got) != "offer" {
		t.Fatalf("Offer.docx did not move with its folder: %q", got)
	}
	l := list(t, c, placeID, drafts.ID)
	if len(l.Items) != 1 || l.Items[0].ID != offer.ID || l.Path[0].Name != "Offers" {
		t.Fatalf("the renamed folder lists %+v with path %+v", l.Items, l.Path)
	}
	if res := c.Get(t, downloadPath(placeID, offer.ID)); res.Status != http.StatusOK || string(res.Body) != "offer" {
		t.Fatalf("the file in the renamed folder: %d %s", res.Status, res.Body)
	}
	// Changing only the case is a rename too.
	if res := rename(t, c, placeID, offer.ID, "offer.docx"); res.Status != http.StatusOK {
		t.Fatalf("a rename that changes the case: %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop+"/Offers"); !slices.Equal(got, []string{"offer.docx"}) {
		t.Fatalf("Offers on the storage: %q", got)
	}
	// Deeper down, a file and a folder.
	bakery := madeFolder(t, c, placeID, drafts.ID, "Bakery")
	y2026 := madeFolder(t, c, placeID, bakery.ID, "2026")
	ledger := uploadedInto(t, c, placeID, y2026.ID, "Ledger.xlsx", []byte("ledger"))
	if res := rename(t, c, placeID, ledger.ID, "Ledger Q3.xlsx"); res.Status != http.StatusOK {
		t.Fatalf("renaming a file three folders deep: %d %s", res.Status, res.Body)
	}
	if res := rename(t, c, placeID, y2026.ID, "Year 2026"); res.Status != http.StatusOK {
		t.Fatalf("renaming a folder two folders deep: %d %s", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Offers/Bakery/Year 2026/Ledger Q3.xlsx"); string(got) != "ledger" {
		t.Fatalf("Ledger Q3.xlsx is not where the renames put it: %q", got)
	}

	// The same name is no change; a taken one, or one that cannot be stored, is refused and nothing moves.
	if res := rename(t, c, placeID, notes.ID, "Notes for Bence – 2026.txt"); res.Status != http.StatusOK {
		t.Errorf("renaming to the same name: got %d %s", res.Status, res.Body)
	}
	if res := rename(t, c, placeID, notes.ID, "Offers"); res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "Offers") {
		t.Errorf("renaming to a taken name: got %d %s, want 409", res.Status, res.Body)
	}
	for _, name := range []string{"", "..", "../Escape.txt", "a/b.txt", strings.Repeat("é", 128)} {
		if res := rename(t, c, placeID, notes.ID, name); res.Status != http.StatusBadRequest {
			t.Errorf("renaming to %q: got %d %s, want 400", name, res.Status, res.Body)
		}
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Notes for Bence – 2026.txt", "Offers/"}) {
		t.Fatalf("after refused renames, Réka's top folder on the storage: %q", got)
	}
	if res := d.As(reka).Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/rename", placeID, notes.ID), strings.NewReader("not json")); res.Status != http.StatusBadRequest {
		t.Errorf("a rename that is not JSON: got %d", res.Status)
	}
}

func TestMoveIsAMoveOnTheStorageAndNeverIntoItself(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	clients := madeFolder(t, c, placeID, 0, "Clients")
	bakery := madeFolder(t, c, placeID, clients.ID, "Bakery Ltd")
	archive := madeFolder(t, c, placeID, 0, "Archive")
	invoice := uploadedInto(t, c, placeID, bakery.ID, "Invoice 12.pdf", []byte("invoice"))
	loose := uploadedInto(t, c, placeID, 0, "Loose.txt", []byte("loose"))

	// A file into a folder.
	res := move(t, c, placeID, loose.ID, archive.ID)
	var moved itemJSON
	res.JSON(t, &moved)
	if res.Status != http.StatusOK || moved.ID != loose.ID {
		t.Fatalf("moving a file: got %d %s", res.Status, res.Body)
	}
	// A folder, with everything in it, into another.
	if res := move(t, c, placeID, clients.ID, archive.ID); res.Status != http.StatusOK {
		t.Fatalf("moving a folder: got %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Archive/"}) {
		t.Fatalf("Réka's top folder on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Archive/Clients/Bakery Ltd/Invoice 12.pdf"); string(got) != "invoice" {
		t.Fatalf("the invoice did not move with its folders: %q", got)
	}
	l := list(t, c, placeID, bakery.ID)
	if len(l.Items) != 1 || l.Items[0].ID != invoice.ID {
		t.Fatalf("Bakery Ltd after the move: %+v", l.Items)
	}
	if want := []string{"Archive", "Clients", "Bakery Ltd"}; len(l.Path) != 3 || l.Path[0].Name != want[0] || l.Path[1].Name != want[1] {
		t.Fatalf("the path to Bakery Ltd: %+v", l.Path)
	}
	// Back to the top folder.
	if res := move(t, c, placeID, loose.ID, 0); res.Status != http.StatusOK {
		t.Fatalf("moving to the top: got %d %s", res.Status, res.Body)
	}
	if _, ok := d.Storage.Get(t, rekasTop+"/Loose.txt"); !ok {
		t.Fatal("Loose.txt is not back at the top")
	}
	// Where it already is: no change.
	if res := move(t, c, placeID, loose.ID, 0); res.Status != http.StatusOK {
		t.Fatalf("moving to where it is: got %d %s", res.Status, res.Body)
	}

	// Never into itself, or into a folder inside it, at any depth.
	for what, to := range map[string]int64{"itself": clients.ID, "a folder inside it": bakery.ID} {
		res := move(t, c, placeID, clients.ID, to)
		if res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "into itself") {
			t.Errorf("moving Clients into %s: got %d %s, want 409", what, res.Status, res.Body)
		}
	}
	if res := move(t, c, placeID, archive.ID, bakery.ID); res.Status != http.StatusConflict {
		t.Errorf("moving Archive two levels down into itself: got %d %s, want 409", res.Status, res.Body)
	}
	// Not into a file, nor onto a name that is taken there.
	if res := move(t, c, placeID, loose.ID, invoice.ID); res.Status != http.StatusBadRequest {
		t.Errorf("moving into a file: got %d %s, want 400", res.Status, res.Body)
	}
	uploadedInto(t, c, placeID, archive.ID, "Loose.txt", []byte("another"))
	if res := move(t, c, placeID, loose.ID, archive.ID); res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "Loose.txt") {
		t.Errorf("moving onto a taken name: got %d %s, want 409", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Archive/Loose.txt"); string(got) != "another" {
		t.Errorf("a refused move replaced what was there: %q", got)
	}
	// Not into a folder that does not exist, and a request without a destination is not read as the top folder.
	if res := move(t, c, placeID, loose.ID, invoice.ID+loose.ID+100); res.Status != http.StatusNotFound {
		t.Errorf("moving into a folder that does not exist: got %d %s, want 404", res.Status, res.Body)
	}
	if res := sendJSON(t, c, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/move", placeID, invoice.ID), map[string]any{}); res.Status != http.StatusBadRequest {
		t.Errorf("a move without a destination: got %d %s, want 400", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop+"/Archive"); !slices.Equal(got, []string{"Clients/", "Loose.txt"}) {
		t.Fatalf("Archive on the storage after the refused moves: %q", got)
	}
	if got := onStorage(t, d, rekasTop+"/Archive/Clients"); !slices.Equal(got, []string{"Bakery Ltd/"}) {
		t.Fatalf("Clients on the storage after the refused moves: %q", got)
	}
}

// "My files" is closed to everyone else, admins included, for every folder action: each answers as a place that does
// not exist. Nor does anyone reach another member's folder through their own place.
func TestNobodyElseCanChangeOrLookIntoAMembersFolders(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	bencesPlace, _ := myFiles(t, d, bence)
	rekasPlace, _ := myFiles(t, d, reka)
	payroll := madeFolder(t, d.As(bence), bencesPlace, 0, "Payroll")
	slip := uploadedInto(t, d.As(bence), bencesPlace, payroll.ID, "November.xlsx", []byte("private"))
	gone := uploadedInto(t, d.As(bence), bencesPlace, 0, "Deleted.txt", []byte("private too"))
	if res := d.As(bence).Do(t, http.MethodDelete, fmt.Sprintf("/api/places/%d/items/%d", bencesPlace, gone.ID), nil); res.Status != http.StatusOK {
		t.Fatalf("deleting: %d %s", res.Status, res.Body)
	}
	mine := madeFolder(t, d.As(reka), rekasPlace, 0, "Mine")

	missingPlace := d.As(reka).Get(t, listPath(rekasPlace+bencesPlace+100, 1))
	for what, res := range map[string]harness.Response{
		"list a folder":     d.As(reka).Get(t, listPath(bencesPlace, payroll.ID)),
		"make a folder":     newFolder(t, d.As(reka), bencesPlace, payroll.ID, "Planted"),
		"make a top folder": newFolder(t, d.As(reka), bencesPlace, 0, "Planted"),
		"upload":            uploadInto(t, d.As(reka), bencesPlace, payroll.ID, "Planted.txt", []byte("x"), "replace"),
		"rename":            rename(t, d.As(reka), bencesPlace, slip.ID, "Renamed.xlsx"),
		"move":              move(t, d.As(reka), bencesPlace, slip.ID, 0),
		"delete":            d.As(reka).Do(t, http.MethodDelete, fmt.Sprintf("/api/places/%d/items/%d", bencesPlace, slip.ID), nil),
		"see the trash":     d.As(reka).Get(t, fmt.Sprintf("/api/places/%d/trash", bencesPlace)),
		"restore":           d.As(reka).Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/trash/%d/restore", bencesPlace, gone.ID), nil),
	} {
		if res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missingPlace.Body) {
			t.Errorf("%s in Bence's My files: got %d %s; a place that does not exist answers %d %s",
				what, res.Status, res.Body, missingPlace.Status, missingPlace.Body)
		}
	}
	// Through her own place, Bence's items are not found: not as folders to look into or move into, not as items.
	for what, res := range map[string]harness.Response{
		"list his folder":      d.As(reka).Get(t, listPath(rekasPlace, payroll.ID)),
		"rename his file":      rename(t, d.As(reka), rekasPlace, slip.ID, "Renamed.xlsx"),
		"move his file":        move(t, d.As(reka), rekasPlace, slip.ID, mine.ID),
		"move hers into his":   move(t, d.As(reka), rekasPlace, mine.ID, payroll.ID),
		"restore his deleted":  d.As(reka).Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/trash/%d/restore", rekasPlace, gone.ID), nil),
		"upload into his":      uploadInto(t, d.As(reka), rekasPlace, payroll.ID, "Planted.txt", []byte("x"), ""),
		"make a folder in his": newFolder(t, d.As(reka), rekasPlace, payroll.ID, "Planted"),
	} {
		if res.Status != http.StatusNotFound {
			t.Errorf("%s through her own place: got %d %s, want 404", what, res.Status, res.Body)
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com"); !slices.Equal(got, []string{"Payroll/"}) {
		t.Errorf("Bence's top folder on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Payroll"); !slices.Equal(got, []string{"November.xlsx"}) {
		t.Errorf("Bence's Payroll on the storage: %q", got)
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Mine/"}) {
		t.Errorf("Réka's top folder on the storage: %q", got)
	}
}
