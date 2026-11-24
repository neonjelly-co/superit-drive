package drive

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// A change is asked for with the item as it was read when the request was decided. The item moved elsewhere
// meanwhile, and something else put where it was, the storage would move that other thing, and the index the item
// itself, with what was given on it and what is in it, to where the other thing went: whoever could no longer reach a
// folder had it back, and what was later put into it by its id. Such a change is not made.
func TestAChangeIsNotMadeToAnItemThatMovedMeanwhile(t *testing.T) {
	bg := context.Background()
	by := By{Member: "u-reka"}
	for _, change := range []string{"move", "rename", "delete", "new folder"} {
		t.Run(change, func(t *testing.T) {
			d, x, f := driveOnFake(t)
			p, err := d.MyFiles(bg, memberOf("u-reka", "reka@example.com"))
			if err != nil {
				t.Fatal(err)
			}
			folder := func(in *index.Item, name string) index.Item {
				t.Helper()
				it, err := d.NewFolder(bg, p, in, name, by)
				if err != nil {
					t.Fatal(err)
				}
				if it, err = x.Item(bg, p.ID, it.ID); err != nil { // as a request reads it: with the folders above it
					t.Fatal(err)
				}
				return it
			}
			team, elsewhere := folder(nil, "Team"), folder(nil, "Confidential")
			a, b := folder(&team, "A"), folder(&team, "B")
			x1 := folder(&a, "X")
			decide := func(context.Context) (index.Place, *index.Item, error) { return p, &x1, nil }
			if _, err := d.Upload(bg, p, &x1, "Salaries.txt", strings.NewReader("PRECIOUS"), 8, Ask, by, decide); err != nil {
				t.Fatal(err)
			}

			// The request is decided, with X where it is. Then X is moved out of Team, and another X made in its place.
			stale, err := x.Item(bg, p.ID, x1.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Move(bg, p, stale, &elsewhere); err != nil {
				t.Fatal(err)
			}
			other := folder(&a, "X")

			switch change {
			case "move":
				_, err = d.Move(bg, p, stale, &b)
			case "rename":
				_, err = d.Rename(bg, p, stale, "Y", by)
			case "delete":
				_, err = d.Delete(bg, p, stale, "u-reka")
			case "new folder":
				_, err = d.NewFolder(bg, p, &stale, "Inside", by)
			}
			if !errors.Is(err, ErrGone) {
				t.Fatalf("the change was answered %v, want ErrGone", err)
			}

			// X is where it was moved to, in the index and on the storage, with its file; the other X was not touched.
			now, err := x.Item(bg, p.ID, x1.ID)
			if err != nil || now.ParentID != elsewhere.ID || now.Name != "X" {
				t.Fatalf("the item is now %+v (%v), want X in Confidential", now, err)
			}
			if got := f.holds("PRECIOUS"); !slices.Equal(got, []string{"/drive/My files/reka@example.com/Confidential/X/Salaries.txt"}) {
				t.Fatalf("the file is at %v", got)
			}
			if o, err := x.Item(bg, p.ID, other.ID); err != nil || o.ParentID != a.ID || o.Name != "X" {
				t.Fatalf("the other X is now %+v (%v), want X in A", o, err)
			}
			if !f.exists("/drive/My files/reka@example.com/Team/A/X") || f.exists("/drive/My files/reka@example.com/Team/A/X/Inside") {
				t.Fatalf("the storage now holds %v", f.all())
			}
		})
	}
}
