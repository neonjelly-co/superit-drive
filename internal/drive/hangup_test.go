package drive

import (
	"context"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// hangUpAt gives a context that ends when the storage gets its first request with the method whose path holds part: the
// person asking hangs up once the storage has the change.
func hangUpAt(t *testing.T, f *fakeDAV, method, part string) context.Context {
	t.Helper()
	ctx, hangUp := context.WithCancel(context.Background())
	t.Cleanup(hangUp)
	f.mu.Lock()
	f.hook = func(m, p string) {
		if m == method && strings.Contains(p, part) {
			hangUp()
		}
	}
	f.mu.Unlock()
	return ctx
}

// A change, once the storage has it, is finished without whoever asked: left half done, the storage would hold the
// change and the index not, and the next look at the folder would take the item for one put there directly, without
// its label and without what was given on it.
func TestAChangeIsFinishedWhoeverHangsUp(t *testing.T) {
	bg := context.Background()
	d, x, f := driveOnFake(t)
	p, err := d.MyFiles(bg, memberOf("u-reka", "reka@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	decide := func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil }
	up, err := d.Upload(bg, p, nil, "Contract.pdf", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"}, decide)
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := d.Rename(hangUpAt(t, f, "MOVE", "Contract.pdf"), p, up.Item, "Contract final.pdf", By{Member: "u-reka"})
	if err != nil || renamed.ID != up.Item.ID || renamed.Name != "Contract final.pdf" {
		t.Fatalf("a rename whose asker hung up at the move: %+v, %v", renamed, err)
	}
	if it, err := x.Item(bg, p.ID, up.Item.ID); err != nil || it.Name != "Contract final.pdf" {
		t.Fatalf("the index after it: %+v, %v", it, err)
	}

	guest := index.Guest{ID: 7, Address: "anna@client.example"}
	made, err := d.NewFolder(hangUpAt(t, f, "MKCOL", "From Anna"), p, nil, "From Anna", By{Guest: guest})
	if err != nil || made.AddedByGuest != guest.Address {
		t.Fatalf("a guest's new folder whose asker hung up as it was made: %+v, %v", made, err)
	}

	entry, err := d.Delete(hangUpAt(t, f, "MOVE", "Contract final.pdf"), p, renamed, "u-reka")
	if err != nil || entry.Item.ID != up.Item.ID {
		t.Fatalf("a delete whose asker hung up at the move: %+v, %v", entry, err)
	}
	trashed, err := x.TrashItem(bg, p.ID, up.Item.ID)
	if err != nil {
		t.Fatalf("the trash after it: %v", err)
	}

	back, err := d.Restore(hangUpAt(t, f, "MOVE", "Contract final.pdf"), p, trashed, func(*index.Item) error { return nil })
	if err != nil || back.ID != up.Item.ID {
		t.Fatalf("a restore whose asker hung up at the move: %+v, %v", back, err)
	}
	if at := f.holds("PRECIOUS"); len(at) != 1 || !strings.HasSuffix(at[0], "/My files/reka@example.com/Contract final.pdf") {
		t.Fatalf("the file after it all: %v", at)
	}
	// The emptied entry went with it, hung up or not.
	for _, path := range f.all() {
		if _, inside, _ := strings.Cut(path, "/trash/My files/reka@example.com/"); inside != "" {
			t.Fatalf("left in the trash on the storage: %s", path)
		}
	}
}
