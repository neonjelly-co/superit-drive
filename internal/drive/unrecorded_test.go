package drive

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// A rename whose index write fails (the write lock is held past busy_timeout by something else): the storage has moved
// the file. What becomes of the item's public link?
func TestARenameIsRecordedOnceTheIndexIsFree(t *testing.T) {
	bg := context.Background()
	f := newFakeDAV()
	srv := httptest.NewServer(f)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	dir := t.TempDir()
	x, err := index.Open(bg, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	var shift atomic.Int64
	d := New(x, storage.New(u, "u", "p", "drive"), Options{Now: func() time.Time { return time.Now().Add(time.Duration(shift.Load())) }})
	defer d.Stop()
	p, err := d.MyFiles(bg, memberOf("u-reka", "reka@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	decide := func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil }
	up, err := d.Upload(bg, p, nil, "Contract.pdf", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"}, decide)
	if err != nil {
		t.Fatal(err)
	}
	l, _, err := d.MakeLink(bg, p, up.Item, index.ViewLink, "u-reka", false)
	if err != nil {
		t.Fatal(err)
	}

	// Another writer holds the index's write lock for 7 seconds (as one long transaction would).
	other, err := sql.Open("sqlite", "file:"+filepath.Join(dir, index.FileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(bg, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(7 * time.Second)
		_, _ = conn.ExecContext(bg, "ROLLBACK")
	}()
	_, err = d.Rename(bg, p, up.Item, "Contract final.pdf", By{Member: "u-reka"})
	t.Logf("Rename answered: %v", err)
	t.Logf("storage: %v", f.holds("PRECIOUS"))

	o, err := d.reread(bg, p, 0)
	t.Logf("first look: %+v %v", o, err)
	shift.Store(int64(11 * time.Minute))
	o, err = d.reread(bg, p, 0)
	t.Logf("look 11 minutes later: %+v %v", o, err)
	_, err = d.LinkByID(bg, l.ID)
	t.Logf("the link now: %v", err)
	if errors.Is(err, index.ErrNoLink) {
		t.Fatalf("SHARING LOST: the renamed file is a new item, and the item's public link is gone")
	}
}

// RestoreSharedFolder: the storage answers the MOVE out of the trash with 404 once, wrongly.
func TestASharedFolderIsNotRestoredOnAWrongNotFound(t *testing.T) {
	bg := context.Background()
	f := newFakeDAV()
	var wrong atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MOVE" && wrong.CompareAndSwap(true, false) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	x, err := index.Open(bg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	d := New(x, storage.New(u, "u", "p", "drive"), Options{})
	defer d.Stop()
	p, err := d.NewSharedFolder(bg, "Clients", "u-reka")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Upload(bg, p, nil, "f.txt", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"},
		func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil }); err != nil {
		t.Fatal(err)
	}
	gone, err := d.DeleteSharedFolder(bg, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("in the trash: %v", f.holds("PRECIOUS"))
	wrong.Store(true)
	_, err = d.RestoreSharedFolder(bg, gone.Place)
	t.Logf("RestoreSharedFolder answered: %v", err)
	t.Logf("after: %v; storage %v", f.holds("PRECIOUS"), f.all())
	if len(f.holds("PRECIOUS")) == 0 {
		t.Fatalf("DATA LOST: the shared folder's files are nowhere")
	}
}
