package test

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// An upload whose name is taken is not stored until the person says what to do: the Drive answers 409 saying what
// kind of item has the name, and the interface asks "replace or keep both".
func TestAnUploadOfATakenNameAsksReplaceOrKeepBoth(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	drafts := madeFolder(t, c, placeID, 0, "Drafts")
	report := uploadedInto(t, c, placeID, drafts.ID, "Report.pdf", []byte("first"))

	res := uploadInto(t, c, placeID, drafts.ID, "Report.pdf", []byte("second"), "")
	var conflict conflictJSON
	res.JSON(t, &conflict)
	if res.Status != http.StatusConflict || conflict.Exists != "file" || conflict.Name != "Report.pdf" || !strings.Contains(conflict.Error, "Report.pdf") {
		t.Fatalf("without an answer: got %d %s, want 409 saying a file has the name", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop+"/Drafts"); !slices.Equal(got, []string{"Report.pdf"}) {
		t.Fatalf("Drafts on the storage: %q", got)
	}

	// Keep both: the upload gets the first free name.
	for _, want := range []string{"Report (2).pdf", "Report (3).pdf"} {
		res := uploadInto(t, c, placeID, drafts.ID, "Report.pdf", []byte(want), "keep-both")
		var it itemJSON
		res.JSON(t, &it)
		if res.Status != http.StatusCreated || it.Name != want || it.Replaced {
			t.Fatalf("keep both: got %d %s, want %q", res.Status, res.Body, want)
		}
		if got, _ := d.Storage.Get(t, rekasTop+"/Drafts/"+want); string(got) != want {
			t.Fatalf("%s holds %q", want, got)
		}
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Drafts/Report.pdf"); string(got) != "first" {
		t.Fatalf("keep both changed Report.pdf: %q", got)
	}

	// Replace: the file keeps its name and its id, with the new content; the replaced one goes to the trash.
	res = uploadInto(t, c, placeID, drafts.ID, "Report.pdf", []byte("second"), "replace")
	var it itemJSON
	res.JSON(t, &it)
	if res.Status != http.StatusCreated || it.ID != report.ID || it.Name != "Report.pdf" || !it.Replaced || *it.Size != 6 {
		t.Fatalf("replace: got %d %s, want Report.pdf with id %d", res.Status, res.Body, report.ID)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Drafts/Report.pdf"); string(got) != "second" {
		t.Fatalf("Report.pdf holds %q after the replace", got)
	}
	trash := trashOf(t, c, placeID)
	if len(trash) != 1 || trash[0].Name != "Report.pdf" || !trash[0].Replaced || trash[0].Size != 5 || trash[0].ID == report.ID ||
		len(trash[0].From) != 1 || trash[0].From[0].ID != drafts.ID {
		t.Fatalf("the trash lists %+v, want the replaced Report.pdf, from Drafts", trash)
	}
	if got, _ := d.Storage.Get(t, entryOf(t, d, "Report.pdf")+"/Report.pdf"); string(got) != "first" {
		t.Fatalf("the replaced file in the trash holds %q", got)
	}
	// It can be restored: beside the new one, under a free name.
	r := restored(t, c, placeID, trash[0].ID)
	if r.Item.Name != "Report (4).pdf" {
		t.Fatalf("the replaced file came back as %q", r.Item.Name)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Drafts/Report (4).pdf"); string(got) != "first" {
		t.Fatalf("the restored replaced file holds %q", got)
	}

	// Replace or keep both with a free name is a plain upload.
	for _, existing := range []string{"replace", "keep-both"} {
		name := "Fresh " + existing + ".txt"
		res := uploadInto(t, c, placeID, drafts.ID, name, []byte("x"), existing)
		var it itemJSON
		res.JSON(t, &it)
		if res.Status != http.StatusCreated || it.Name != name || it.Replaced {
			t.Errorf("%s with a free name: got %d %s", existing, res.Status, res.Body)
		}
	}
	if res := uploadInto(t, c, placeID, drafts.ID, "Report.pdf", []byte("x"), "overwrite"); res.Status != http.StatusBadRequest {
		t.Errorf("an unknown answer: got %d %s, want 400", res.Status, res.Body)
	}
}

// A file never replaces a folder: a name a folder has can only be kept beside it.
func TestAFileNeverReplacesAFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	folder := madeFolder(t, c, placeID, 0, "Invoices")
	uploadedInto(t, c, placeID, folder.ID, "Inside.txt", []byte("inside"))

	for _, existing := range []string{"", "replace"} {
		res := uploadInto(t, c, placeID, 0, "Invoices", []byte("a file"), existing)
		var conflict conflictJSON
		res.JSON(t, &conflict)
		if res.Status != http.StatusConflict || conflict.Exists != "folder" {
			t.Errorf("a file called like a folder, %q: got %d %s, want 409 saying a folder has the name", existing, res.Status, res.Body)
		}
	}
	res := uploadInto(t, c, placeID, 0, "Invoices", []byte("a file"), "keep-both")
	var it itemJSON
	res.JSON(t, &it)
	if res.Status != http.StatusCreated || it.Name != "Invoices (2)" {
		t.Fatalf("keep both: got %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Invoices (2)", "Invoices/"}) {
		t.Fatalf("Réka's top folder on the storage: %q", got)
	}
	if got := onStorage(t, d, rekasTop+"/Invoices"); !slices.Equal(got, []string{"Inside.txt"}) {
		t.Fatalf("Invoices on the storage: %q", got)
	}
	if trash := trashOf(t, c, placeID); len(trash) != 0 {
		t.Fatalf("the trash lists %+v", trash)
	}
}

// A replace whose upload is cut off leaves the file that was there in its place, and nothing in the trash.
func TestAReplaceThatIsCutOffKeepsTheFileThatWasThere(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	budget := uploadedInto(t, c, placeID, 0, "Budget.xlsx", []byte("the budget"))

	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"name": {"Budget.xlsx"}, "existing": {"replace"}}
	fmt.Fprintf(conn, "POST /api/places/%d/files?%s HTTP/1.1\r\nHost: %s\r\nDrive-Secret: %s\r\nRemote-User-Id: %s\r\nOrigin: %s\r\n"+
		"Content-Length: %d\r\n\r\n", placeID, q.Encode(), address.Host, d.Secret, reka.UserID, d.Address, 1<<20)
	if _, err := conn.Write(randomBytes(t, 1000)); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	harness.Eventually(t, "Budget.xlsx is back with its content and the trash is empty", func() bool {
		got, ok := d.Storage.Get(t, rekasTop+"/Budget.xlsx")
		trash, _ := d.Storage.List(t, rekasTrash)
		return ok && string(got) == "the budget" && len(trash) == 0
	})
	if trash := trashOf(t, c, placeID); len(trash) != 0 {
		t.Fatalf("the trash lists %+v", trash)
	}
	if _, items := myFiles(t, d, reka); len(items) != 1 || items[0].ID != budget.ID || *items[0].Size != int64(len("the budget")) {
		t.Fatalf("My files lists %+v", items)
	}
}
