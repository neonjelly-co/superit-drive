package drive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Names in the index come from storage listings, which a misbehaving WebDAV server could fill with anything, and from
// people. Only plain names become a path, at any depth, so no name can reach outside its place.
func TestOnlyPlainNamesBecomeAPath(t *testing.T) {
	top := "My files/reka@example.com"
	ok := map[string][]string{
		"My files/reka@example.com":                          nil,
		"My files/reka@example.com/Tax notes.pdf":            {"Tax notes.pdf"},
		"My files/reka@example.com/.hidden":                  {".hidden"},
		"My files/reka@example.com/...":                      {"..."},
		`My files/reka@example.com/back\slash`:               {`back\slash`},
		"My files/reka@example.com/line\nbreak.txt":          {"line\nbreak.txt"}, // odd, but a name a storage can hold
		"My files/reka@example.com/Drafts/2026/Offer.docx":   {"Drafts", "2026", "Offer.docx"},
		"My files/reka@example.com/Drafts/..x/x../Offer.pdf": {"Drafts", "..x", "x..", "Offer.pdf"},
	}
	for want, names := range ok {
		if got, ok := pathIn(top, names...); !ok || got != want {
			t.Errorf("pathIn(%q) = %q, %v; want %q", names, got, ok, want)
		}
	}
	bad := [][]string{
		{""}, {"."}, {".."}, {"../bence@example.com/Payslip.pdf"}, {"a/b"}, {"/"}, {"x/../../y"},
		{"Drafts", ".."}, {"Drafts", "..", "..", "bence@example.com"}, {"Drafts", "", "x"}, {"Drafts", "2026/../.."},
		{"..", "Drafts"},
	}
	for _, names := range bad {
		if got, ok := pathIn(top, names...); ok {
			t.Errorf("pathIn(%q) = %q; want no path", names, got)
		}
	}
	if got, ok := pathIn("", "a"); ok {
		t.Errorf(`pathIn("", "a") = %q; want no path`, got)
	}

	// An item's path is made from the names of the folders above it, so a bad name anywhere above stops it too.
	rekas := index.Place{ID: 1, Kind: index.MyFiles, Owner: "u-reka", Folder: top}
	deep := index.Item{Name: "Payslip.pdf", Above: []index.Item{{Name: "Drafts"}, {Name: ".."}, {Name: ".."}}}
	if got, ok := itemPath(rekas, deep); ok {
		t.Errorf("itemPath = %q; want no path", got)
	}
	if got, ok := pathIn(trashTop(rekas), "2026-11-19 7f3a9c1e", "Offer.docx"); !ok ||
		got != ".superit-drive/trash/My files/reka@example.com/2026-11-19 7f3a9c1e/Offer.docx" {
		t.Errorf("a trash path: %q, %v", got, ok)
	}
}

// "Keep both" and a restore into a taken name use the first free name of these.
func TestNumberedNames(t *testing.T) {
	cases := []struct {
		name   string
		n      int
		folder bool
		want   string
	}{
		{"Report.pdf", 2, false, "Report (2).pdf"},
		{"Report.pdf", 3, false, "Report (3).pdf"},
		{"Report (2).pdf", 3, false, "Report (3).pdf"},
		{"Report (2) (2).pdf", 2, false, "Report (2) (2).pdf"},
		{"Meeting (2026).pdf", 2, false, "Meeting (2026) (2).pdf"},
		{"archive.tar.gz", 2, false, "archive.tar (2).gz"},
		{".bashrc", 2, false, ".bashrc (2)"},
		{"no extension", 2, false, "no extension (2)"},
		{"Photos.2026", 2, true, "Photos.2026 (2)"},
		{"Drafts (4)", 5, true, "Drafts (5)"},
		{"trailing dot.", 2, false, "trailing dot (2)."},
	}
	for _, c := range cases {
		if got := numbered(c.name, c.n, c.folder); got != c.want {
			t.Errorf("numbered(%q, %d, %v) = %q, want %q", c.name, c.n, c.folder, got, c.want)
		}
	}

	// Never longer than 255 bytes, never a broken character, and the extension kept.
	long := strings.Repeat("é", 125) + "x.pdf" // 255 bytes
	got := numbered(long, 12, false)
	if len(got) > MaxNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, " (12).pdf") {
		t.Errorf("numbered(255 bytes) = %q (%d bytes)", got, len(got))
	}
	longExtension := "a." + strings.Repeat("x", 252)
	if got := numbered(longExtension, 2, false); len(got) > MaxNameBytes || !strings.HasSuffix(got, " (2)") {
		t.Errorf("numbered(long extension) = %q (%d bytes)", got, len(got))
	}
}

// A look at a folder that read the storage before an upload ended must not hide the upload's new item when it writes to
// the index afterwards. Nor may a look that read the storage before a rename bring back the old name.
func TestALookThatStartedBeforeAChangeLeavesTheIndexAlone(t *testing.T) {
	ctx := context.Background()
	x, err := index.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	d := New(x, nil, Options{})
	p, err := x.AddMyFiles(ctx, "u-reka", "My files/reka@example.com")
	if err != nil {
		t.Fatal(err)
	}
	key := folderKey{p.ID, 0}
	reread := func(stale func() bool, seen ...index.Seen) error {
		_, err := x.Reconcile(ctx, p.ID, 0, index.Reread{Seen: seen, Leave: func(string) bool { return false },
			Busy: func() bool { return false }, Stale: stale, At: time.Now(), Hide: true})
		return err
	}

	// The look starts and reads the storage: nothing there yet.
	stale := d.since(key)
	// Meanwhile an upload ends: on the storage, then in the index, as Upload does it.
	d.changed(key)
	uploaded, err := x.PutItem(ctx, p.ID, 0, index.Seen{Name: "a.txt", Size: 1, Changed: time.Now()}, time.Now(), index.AddedBy{})
	if err != nil {
		t.Fatal(err)
	}
	// The look writes what it saw.
	if err := reread(stale); !errors.Is(err, index.ErrChanged) {
		t.Fatalf("a look that started before the upload ended: %v, want ErrChanged", err)
	}
	items, _ := x.Children(ctx, p.ID, 0)
	if len(items) != 1 || items[0].ID != uploaded.ID {
		t.Fatalf("after a look that started before the upload ended: %+v, want a.txt with id %d", items, uploaded.ID)
	}

	// A look that starts after the change is applied: what the storage shows wins.
	if err := reread(d.since(key), index.Seen{Name: "a.txt", Size: 5}); err != nil {
		t.Fatal(err)
	}
	items, _ = x.Children(ctx, p.ID, 0)
	if len(items) != 1 || items[0].ID != uploaded.ID || items[0].Size != 5 {
		t.Fatalf("after a fresh look: %+v", items)
	}

	// A rename while a look is under way: the look saw the old name, and must not bring it back.
	stale = d.since(key)
	d.changed(key)
	if _, err := x.Relocate(ctx, p.ID, uploaded.ID, 0, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if err := reread(stale, index.Seen{Name: "a.txt", Size: 5}); !errors.Is(err, index.ErrChanged) {
		t.Fatalf("a look that started before a rename: %v, want ErrChanged", err)
	}
	items, _ = x.Children(ctx, p.ID, 0)
	if len(items) != 1 || items[0].Name != "b.txt" || items[0].ID != uploaded.ID {
		t.Fatalf("after a look that started before a rename: %+v", items)
	}
}

// A path a change of the Drive's holds (claim) is busy for the folder it is in and everything below it, and a folder
// is busy while it, or a folder above it, is held.
func TestWhatIsBusy(t *testing.T) {
	d := New(nil, nil, Options{})
	release, ok := d.claim("My files/r/Clients/Bakery")
	if !ok {
		t.Fatal("not claimed")
	}
	for p, want := range map[string]bool{
		"My files/r/Clients/Bakery": true, "My files/r/Clients/Bakery/2026": true, "My files/r/Clients": false,
		"My files/r/Clients/Bakery Ltd": false,
	} {
		if got := d.busyAt(p); got != want {
			t.Errorf("busyAt(%q) = %v, want %v", p, got, want)
		}
	}
	for p, want := range map[string]bool{
		"My files/r/Clients/Bakery": true, "My files/r/Clients": true, "My files/r": true,
		"My files/r/Clients/Bakery/2026": false, "My files/r/Clients/Bak": false,
	} {
		if got := d.busyIn(p); got != want {
			t.Errorf("busyIn(%q) = %v, want %v", p, got, want)
		}
	}
	release()
	if d.busyAt("My files/r/Clients/Bakery/2026") || d.busyIn("My files/r") {
		t.Fatal("still busy after the change ended")
	}
}

// A change inside a shared folder that passed its checks just before the shared folder went to the trash must not
// write into it afterwards: it would make the top folder again, under a place nobody can open. Moving the whole
// shared folder waits for the changes inside it, and a change that starts after finds the place gone.
func TestNothingIsWrittenIntoASharedFolderOnItsWayToTheTrash(t *testing.T) {
	ctx := context.Background()
	x, err := index.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	d := New(x, nil, Options{})
	p, err := x.AddSharedFolder(ctx, "Clients", "Shared folders/Clients", "u-bence", time.Now())
	if err != nil {
		t.Fatal(err)
	}

	leave, err := d.inPlace(ctx, p) // an upload under way
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func(), 1)
	go func() {
		release, err := d.wholePlace(ctx, p.ID)
		if err != nil {
			t.Error(err)
			close(got)
			return
		}
		got <- release
	}()
	select {
	case <-got:
		t.Fatal("the shared folder was moved while a change inside it was under way")
	case <-time.After(200 * time.Millisecond):
	}
	leave()
	release, ok := <-got
	if !ok {
		t.FailNow()
	}
	// While it is held, the shared folder goes to the trash, as DeleteSharedFolder does it.
	if err := x.DeleteSharedFolder(ctx, p.ID, time.Now(), "entry"); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := d.inPlace(ctx, p); !errors.Is(err, ErrPlaceGone) {
		t.Fatalf("a change after the shared folder went to the trash: got %v, want ErrPlaceGone", err)
	}

	// Moving it gives up, rather than wait for ever, when a change inside does not end.
	other, err := x.AddSharedFolder(ctx, "Payroll", "Shared folders/Payroll", "u-bence", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.inPlace(ctx, other); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := d.wholePlace(short, other.ID); err == nil {
		t.Fatal("the shared folder was held alone while a change inside it was under way")
	}
}
