package index

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"
)

// fill puts folders folders in the top folder of the place, each holding files files of 100 bytes, in one change.
func fill(t *testing.T, x *Index, p Place, folders, files int) {
	t.Helper()
	ctx := context.Background()
	err := x.write(ctx, func(tx *sql.Tx) error {
		for f := range folders {
			var folder int64
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at) VALUES (?, NULL, ?, 1, 0, 0) RETURNING id`,
				p.ID, "Folder "+strconv.Itoa(f)).Scan(&folder); err != nil {
				return err
			}
			for i := range files {
				if _, err := tx.ExecContext(ctx, `INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at) VALUES (?, ?, ?, 0, 100, 0)`,
					p.ID, folder, "File "+strconv.Itoa(i)+".pdf"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A hand-over or a deletion of a held My files larger than one batch moves, or removes, everything in it, a batch at a
// time; one act at a time on a place, and none on the receiver of a hand-over under way; and each is done once.
func TestAHandOverOrADeletionLargerThanABatch(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	bences, doras, emils := place(t, x, "u-bence"), place(t, x, "u-dora"), place(t, x, "u-emil")
	fill(t, x, bences, 25, actBatch/20+1) // more than one batch below its top folder
	fill(t, x, emils, 3, 10)
	put(t, x, doras, 0, "Hers.txt", false, 7)

	a, err := x.StartAct(ctx, Act{Kind: HandOver, Place: bences.ID, ToMember: "u-dora", ToPlace: doras.ID, ToName: "bence", By: "u-admin", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	want := int64(25 * (actBatch/20 + 1))
	if a.Files != want || a.Size != want*100 || a.Owner != "u-bence" || a.Folder != "My files/u-bence" {
		t.Fatalf("the act begun: %+v", a)
	}
	if _, err := x.StartAct(ctx, Act{Kind: DeleteHeld, Place: bences.ID, By: "u-admin", At: time.Now()}); err != ErrActing {
		t.Fatalf("a second act on the same place: %v", err)
	}
	if _, err := x.StartAct(ctx, Act{Kind: DeleteHeld, Place: doras.ID, By: "u-admin", At: time.Now()}); err != ErrActing {
		t.Fatalf("an act on the receiver of one under way: %v", err)
	}
	// As a Drive stopped after its first batch leaves it: part of what is below the top folder in Dóra's My files.
	if _, err := x.db.ExecContext(ctx, `UPDATE items SET place_id = ? WHERE id IN (SELECT id FROM items WHERE place_id = ? AND parent_id IS NOT NULL LIMIT 1000)`,
		doras.ID, bences.ID); err != nil {
		t.Fatal(err)
	}
	if files, _, err := x.Contents(ctx, doras.ID); err != nil || files != 1 {
		t.Fatalf("what Dóra's My files shows midway: %d files, %v", files, err)
	}
	if err := x.HandedOver(ctx, a.ID, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := x.HandedOver(ctx, a.ID, time.Now(), time.Now()); err == nil {
		t.Fatal("a hand-over was done twice")
	}
	if a, err = x.Act(ctx, a.ID); err != nil || a.Done.IsZero() || a.Place != 0 {
		t.Fatalf("the hand-over, done: %+v", a)
	}
	if files, size, err := x.Contents(ctx, doras.ID); err != nil || files != want+1 || size != want*100+7 {
		t.Fatalf("Dóra's My files after the hand-over: %d files, %d bytes, %v", files, size, err)
	}
	if _, err := x.PlaceByID(ctx, bences.ID); err != ErrNoPlace {
		t.Fatalf("Bence's held My files after the hand-over: %v", err)
	}
	if taken, err := x.FolderTaken(ctx, bences.Folder); err != nil || !taken {
		t.Fatalf("Bence's old top folder is free for a new My files: %v %v", taken, err)
	}

	d, err := x.StartAct(ctx, Act{Kind: DeleteHeld, Place: emils.ID, By: "u-admin", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := x.DeletedHeld(ctx, d.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := x.PlaceByID(ctx, emils.ID); err != ErrNoPlace {
		t.Fatalf("Emil's held My files after the deletion: %v", err)
	}
	if err := x.DeletedHeld(ctx, d.ID, time.Now()); err == nil {
		t.Fatal("a deletion was done twice")
	}
	if acts, err := x.Acts(ctx); err != nil || len(acts) != 2 || acts[0].ID != d.ID {
		t.Fatalf("the record: %+v %v", acts, err)
	}
}
