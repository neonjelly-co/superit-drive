package drive

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// A shared folder deleted whole lies in the trash of its top folder, which is gone. A Drive whose index no longer
// knows it (built anew from the files) must not give that trash to the next shared folder made under its name:
// whoever made it would see the deleted one there, and restore it as its manager.
func TestANewSharedFolderDoesNotTakeOverTheTrashOfOneBefore(t *testing.T) {
	bg := context.Background()
	f := newFakeDAV()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	on := func() *Drive {
		x, err := index.Open(bg, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { x.Close() })
		d := New(x, storage.New(u, "u", "p", "drive"), Options{})
		t.Cleanup(d.Stop)
		return d
	}

	first := on()
	payroll, err := first.NewSharedFolder(bg, "Payroll", "u-reka")
	if err != nil {
		t.Fatal(err)
	}
	decide := func(context.Context) (index.Place, *index.Item, error) { return payroll, nil, nil }
	if _, err := first.Upload(bg, payroll, nil, "Salaries.txt", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"}, decide); err != nil {
		t.Fatal(err)
	}
	if _, err := first.DeleteSharedFolder(bg, payroll); err != nil {
		t.Fatal(err)
	}
	if at := f.holds("PRECIOUS"); len(at) != 1 || !strings.Contains(at[0], "/trash/Shared folders/Payroll/") {
		t.Fatalf("the deleted shared folder's file is at %v", at)
	}

	// A Drive with a new index, on the same storage: someone else makes a shared folder of the same name.
	second := on()
	p, err := second.NewSharedFolder(bg, "Payroll", "u-emil")
	if err != nil {
		t.Fatal(err)
	}
	if p.Folder != "Shared folders/Payroll (2)" {
		t.Fatalf("the new shared folder's top folder is %q, want one whose trash holds nothing", p.Folder)
	}
	entries, err := second.Trash(bg, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the new shared folder's trash shows %d entries of the one deleted before: %+v", len(entries), entries)
	}
}
