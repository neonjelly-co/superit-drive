package drive

import (
	"context"
	"errors"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Access given on places and items, as the index holds it. The access package decides what it is worth; these only
// read and change it.

// GrantsOn returns what was given on the place's top folder and on the items given by id: for an item, the folders
// above it and the item itself.
func (d *Drive) GrantsOn(ctx context.Context, p index.Place, items []int64) ([]index.Grant, error) {
	return d.index.GrantsOn(ctx, p.ID, items)
}

// GrantsInside returns what was given on the items directly in a folder of the place (nil: its top folder).
func (d *Drive) GrantsInside(ctx context.Context, p index.Place, folder *index.Item) ([]index.Grant, error) {
	return d.index.GrantsInside(ctx, p.ID, folderID(folder))
}

// TopGrantsFor returns, by place id, what was given on places as a whole to the member or to everyone in the project.
func (d *Drive) TopGrantsFor(ctx context.Context, member string) (map[int64][]index.Grant, error) {
	return d.index.TopGrantsFor(ctx, member)
}

// Give gives to a role on an item of the place (nil: on the place as a whole, which only a shared folder can be), in
// place of what they had there; by is the member giving it. check, when not nil, is shown everything given there
// afterwards and can refuse with an error. It is a change inside the place, so it takes the place's lock: nothing is
// given in a shared folder that went to the trash (ErrPlaceGone), nor on an item that did (index.ErrNoItem).
func (d *Drive) Give(ctx context.Context, p index.Place, item *index.Item, to index.Grantee, role, by string, check func(after []index.Grant) error) error {
	if item == nil && p.Kind != index.SharedFolder {
		return errors.New("access is given on a shared folder as a whole, and on items; not on a My files as a whole")
	}
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return err
	}
	defer leave()
	return d.index.Give(ctx, p.ID, folderID(item), to, role, d.now(), by, check)
}

// TakeAway takes away what was given to from on an item of the place (nil: on the place as a whole). check, when not
// nil, is shown what is left given there and can refuse with an error. It takes the place's lock, as Give does.
func (d *Drive) TakeAway(ctx context.Context, p index.Place, item *index.Item, from index.Grantee, check func(after []index.Grant) error) error {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return err
	}
	defer leave()
	return d.index.TakeAway(ctx, p.ID, folderID(item), from, check)
}

// RestoreTo is the folder an item of the place's trash goes back to when restored: the one it was in, while that is
// still a folder in the place outside the trash, and otherwise the place's top folder (nil).
func (d *Drive) RestoreTo(ctx context.Context, p index.Place, t index.TrashItem) (*index.Item, error) {
	return d.restoreTo(ctx, p, t)
}
