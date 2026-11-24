package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// A visitor who hangs up while their whole file is being moved into the folder: the storage has moved it, so it is
// stored, labelled and counted by its link all the same. Left uncounted, hanging up at that moment would put any
// number of files through an upload-only link, past its limits.
func TestAFileMovedAsItsSenderHangsUpIsCounted(t *testing.T) {
	bg := context.Background()
	ctx, hangUp := context.WithCancel(bg)
	defer hangUp()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		case "PUT":
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
		case "MOVE":
			hangUp() // the storage has the request; the visitor's connection ends now
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusCreated)
		default: // PROPFIND, DELETE: nothing there
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	x, err := index.Open(bg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	d := New(x, storage.New(u, "u", "p", "drive"), Options{})
	p, err := x.AddMyFiles(bg, "u-bence", "My files/bence@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	inbox, err := x.PutItem(bg, p.ID, 0, index.Seen{Name: "Inbox", Folder: true, Changed: now}, now, index.AddedBy{})
	if err != nil {
		t.Fatal(err)
	}
	l, _, err := d.MakeLink(bg, p, inbox, index.UploadLink, "u-bence", false)
	if err != nil {
		t.Fatal(err)
	}
	it, err := d.SendIn(ctx, p, l.ID, "a.txt", strings.NewReader("hello"), 5,
		func(context.Context) (index.Place, index.Item, error) { return p, inbox, nil })
	if err != nil || it.Name != "a.txt" {
		t.Fatalf("the file moved as its sender hung up: %+v, %v", it, err)
	}
	if got, err := d.LinkByID(bg, l.ID); err != nil || got.TakenIn != 5 || got.TakenFiles != 1 {
		t.Fatalf("what the link took in: %d bytes in %d files, %v; want 5 in 1", got.TakenIn, got.TakenFiles, err)
	}
	if used, err := x.NameUsed(bg, p.ID, inbox.ID, "a.txt"); err != nil || !used {
		t.Fatalf("the index knows the file: %v, %v", used, err)
	}
}
