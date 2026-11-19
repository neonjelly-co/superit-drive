package test

import (
	"net/http"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

func usage(t *testing.T, d *harness.Drive) usageJSON {
	t.Helper()
	res := d.As(reka).Get(t, "/api/usage")
	if res.Status != http.StatusOK {
		t.Fatalf("usage: got %d %s", res.Status, res.Body)
	}
	var u usageJSON
	res.JSON(t, &u)
	if u.Used == nil {
		t.Fatalf("usage without a used figure: %s", res.Body)
	}
	return u
}

// The meter in the sidebar shows the Drive's own sum of the sizes of the files it knows, in every place and the trash,
// against DRIVE_CAPACITY when it is set.
func TestSpaceUsedIsTheDrivesOwnSumOfFileSizesTrashIncluded(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: map[string]string{"DRIVE_CAPACITY": "1TB"}})
	rekasPlace, _ := myFiles(t, d, reka)
	bencesPlace, _ := myFiles(t, d, bence)

	u := usage(t, d)
	if *u.Used != 0 || u.Capacity == nil || *u.Capacity != 1_000_000_000_000 {
		t.Fatalf("a new Drive: used %v of %v, want 0 of 1 TB", *u.Used, u.Capacity)
	}

	drafts := madeFolder(t, d.As(reka), rekasPlace, 0, "Drafts")
	a := uploadedInto(t, d.As(reka), rekasPlace, drafts.ID, "a.bin", randomBytes(t, 1000))
	uploadedInto(t, d.As(bence), bencesPlace, 0, "b.bin", randomBytes(t, 500))
	if u := usage(t, d); *u.Used != 1500 {
		t.Fatalf("used %d, want 1500: every member's files", *u.Used)
	}
	// What is in the trash still takes space; a replaced file is in the trash too.
	deleted(t, d.As(reka), rekasPlace, a.ID)
	if u := usage(t, d); *u.Used != 1500 {
		t.Fatalf("used %d after a delete, want 1500: the trash counts", *u.Used)
	}
	uploadedInto(t, d.As(bence), bencesPlace, 0, "c.bin", randomBytes(t, 10))
	if res := uploadInto(t, d.As(bence), bencesPlace, 0, "c.bin", randomBytes(t, 20), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing: %d %s", res.Status, res.Body)
	}
	if u := usage(t, d); *u.Used != 1530 {
		t.Fatalf("used %d after a replace, want 1530: the new file and the replaced one in the trash", *u.Used)
	}
	// A file put on the storage directly counts once the Drive has seen it.
	d.Storage.Put(t, rekasTop+"/Drafts/From outside.bin", string(randomBytes(t, 70)))
	list(t, d.As(reka), rekasPlace, drafts.ID)
	if u := usage(t, d); *u.Used != 1600 {
		t.Fatalf("used %d, want 1600 once the Drive has listed the file put on the storage", *u.Used)
	}
	// Every member sees the same figure: it is the project's, not their own.
	res := d.As(bence).Get(t, "/api/usage")
	var theirs usageJSON
	res.JSON(t, &theirs)
	if res.Status != http.StatusOK || *theirs.Used != 1600 {
		t.Fatalf("Bence sees %d %s", res.Status, res.Body)
	}
}

func TestWithoutACapacityOnlyTheUsedFigureIsGiven(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	uploadedInto(t, d.As(reka), placeID, 0, "a.bin", randomBytes(t, 123))
	if u := usage(t, d); *u.Used != 123 || u.Capacity != nil {
		t.Fatalf("used %d of %v, want 123 and no capacity", *u.Used, u.Capacity)
	}
}
