package test

import (
	"net/http"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// A folder that holds as many entries as the Drive keeps in step with (10,000) takes no file through an upload-only
// link: each is refused as a full link's is, stored nowhere, and not counted against the link.
func TestAFullFolderTakesNoFileThroughAnUploadOnlyLink(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	d.Storage.Fill(t, rekasTop+"/Inbox", 9_999, "file ")
	top := list(t, c, placeID, 0)
	inbox := named(t, top.Items, "Inbox")
	if l := list(t, c, placeID, inbox.ID); len(l.Items) != 9_999 {
		t.Fatalf("Inbox lists %d", len(l.Items))
	}
	_, page := madeUploadOnly(t, d, c, placeID, inbox.ID, false)
	v := visitor(d)
	sentThrough(t, v, page, "the 10000th.bin", nil)
	if l := list(t, c, placeID, inbox.ID); len(l.Items) != 10_000 {
		t.Fatalf("Inbox lists %d after one more", len(l.Items))
	}
	for i := 0; i < 5; i++ {
		res := sendThrough(t, v, page, "one too many.bin", []byte("xyz"))
		refusedAsTooLarge(t, "a file into a full folder", res, true)
	}
	got := linkOn(t, c, placeID, inbox.ID)
	if *got.Files != 1 || *got.TakenIn != 0 {
		t.Fatalf("the link counts %d files and %d bytes, want only the one file it took, of no bytes", *got.Files, *got.TakenIn)
	}
	if l := list(t, c, placeID, inbox.ID); len(l.Items) != 10_000 {
		t.Fatalf("Inbox lists %d after refusals", len(l.Items))
	}
	if res := c.Get(t, "/api/me"); res.Status != http.StatusOK {
		t.Fatal(res.Status)
	}
}
