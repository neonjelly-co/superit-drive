package drive

import (
	"context"
	"errors"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Errors about where things go.
var (
	// ErrNotAFolder: something can only be put into a folder, and this is a file.
	ErrNotAFolder = errors.New("not a folder")
	// ErrIntoItself: a folder cannot go into itself or into a folder inside it.
	ErrIntoItself = errors.New("a folder cannot go into itself")
	// ErrTooDeep: the folder would be more than index.MaxDepth folders deep.
	ErrTooDeep = errors.New("too many folders deep")
)

// NewFolder makes an empty folder called name in a folder of the place (nil: its top folder), by by: one a guest makes
// is labelled with their address.
func (d *Drive) NewFolder(ctx context.Context, p index.Place, in *index.Item, name string, by By) (index.Item, error) {
	if err := CheckName(name); err != nil {
		return index.Item{}, err
	}
	if in != nil && len(in.Above)+2 > index.MaxDepth {
		return index.Item{}, ErrTooDeep
	}
	dir, ok := folderPath(p, in)
	if !ok {
		return index.Item{}, ErrGone
	}
	made, ok := pathIn(dir, name)
	if !ok {
		return index.Item{}, &BadName{"This name cannot be used. Choose another name."}
	}
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Item{}, err
	}
	defer leave()
	release, ok := d.claim(made)
	if !ok {
		return index.Item{}, ErrBusy
	}
	defer release()
	if err := d.ensureTop(ctx, p, in); err != nil {
		return index.Item{}, err
	}
	if err := d.free(ctx, made, name); err != nil {
		return index.Item{}, err
	}

	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err = d.storage.CreateFolder(sctx, made)
	cancel()
	switch {
	case errors.Is(err, storage.ErrExists):
		return index.Item{}, &Taken{Name: name, Folder: true}
	case errors.Is(err, storage.ErrNotFound):
		return index.Item{}, ErrGone
	case errors.Is(err, storage.ErrFull):
		return index.Item{}, ErrFull
	case err != nil:
		return index.Item{}, storageError(err)
	}
	d.changed(folderKey{p.ID, folderID(in)})
	it, err := d.index.PutItem(ctx, p.ID, folderID(in), index.Seen{Name: name, Folder: true, Changed: d.now().UTC()}, d.now(), by.Guest.Address)
	if errors.Is(err, index.ErrNoFolder) {
		return index.Item{}, ErrGone // its folder moved away, or went, meanwhile; a listing finds the new one there
	}
	return it, err
}

// free says, with a *Taken error, whether something is already at the storage path p, called name.
func (d *Drive) free(ctx context.Context, p, name string) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	e, err := d.storage.Stat(ctx, p)
	switch {
	case err == nil:
		return &Taken{Name: name, Folder: e.Folder}
	case errors.Is(err, storage.ErrNotFound):
		return nil
	}
	return storageError(err)
}

// Rename gives a live item of the place a new name, in the folder it is in. It keeps its id.
func (d *Drive) Rename(ctx context.Context, p index.Place, it index.Item, name string) (index.Item, error) {
	if err := CheckName(name); err != nil {
		return index.Item{}, err
	}
	if name == it.Name {
		return it, nil
	}
	var in *index.Item
	if len(it.Above) > 0 {
		in = &it.Above[len(it.Above)-1]
	}
	return d.relocate(ctx, p, it, in, name)
}

// Move puts a live item of the place, with everything in it, into another folder of the place (nil: its top
// folder), under its own name. It keeps its id. A folder cannot go into itself or a folder inside it.
func (d *Drive) Move(ctx context.Context, p index.Place, it index.Item, to *index.Item) (index.Item, error) {
	if err := CanGoInto(it, to); err != nil {
		return index.Item{}, err
	}
	if folderID(to) == it.ParentID {
		return it, nil
	}
	return d.relocate(ctx, p, it, to, it.Name)
}

// CanGoInto says whether the item can be put into the folder to (nil: a top folder), wherever that is: ErrNotAFolder
// when to is a file, ErrIntoItself when it is the item or inside it, ErrTooDeep when it is too deep.
func CanGoInto(it index.Item, to *index.Item) error {
	if to == nil {
		return nil
	}
	if !to.Folder {
		return ErrNotAFolder
	}
	if to.ID == it.ID {
		return ErrIntoItself
	}
	for _, above := range to.Above {
		if above.ID == it.ID {
			return ErrIntoItself
		}
	}
	if len(to.Above)+2 > index.MaxDepth {
		return ErrTooDeep
	}
	return nil
}

// MoveToPlace puts a live item of the place from, with everything in it, into a folder of another place (nil: its top
// folder), under its own name: one move on the storage, then the index. It keeps its id, and so does everything in it;
// what was given on them goes with them, and what was given on the folders above it in from stays there. Nothing is
// ever replaced: a name taken there is a *Taken error. An upload in progress into it, or into a folder in it, makes it
// wait (ErrBusy), as one under way does.
//
// It is a change inside both places, so it holds both places' locks, always the one with the lower id first: two moves
// in opposite directions cannot each hold one and wait for the other. Neither place may have gone to the trash
// meanwhile (ErrPlaceGone).
func (d *Drive) MoveToPlace(ctx context.Context, from index.Place, it index.Item, to index.Place, into *index.Item) (index.Item, error) {
	if from.ID == to.ID {
		return index.Item{}, errors.New("a move inside one place is Move")
	}
	if err := CanGoInto(it, into); err != nil {
		return index.Item{}, err
	}
	src, ok := itemPath(from, it)
	if !ok {
		return index.Item{}, ErrGone
	}
	dir, ok := folderPath(to, into)
	if !ok {
		return index.Item{}, ErrGone
	}
	dest, ok := pathIn(dir, it.Name)
	if !ok {
		return index.Item{}, ErrGone
	}
	leave, err := d.inPlaces(ctx, from, to)
	if err != nil {
		return index.Item{}, err
	}
	defer leave()
	release, ok := d.claim(src, dest)
	if !ok || (it.Folder && d.busyInside(src)) {
		if ok {
			release()
		}
		return index.Item{}, ErrBusy
	}
	defer release()
	stale := d.since(folderKey{from.ID, it.ParentID})
	if busy, err := d.index.Uploading(ctx, from.ID, it.ID); err != nil {
		return index.Item{}, err
	} else if busy {
		return index.Item{}, ErrBusy
	}
	if err := d.ensureTop(ctx, to, into); err != nil {
		return index.Item{}, err
	}
	if err := d.free(ctx, dest, it.Name); err != nil {
		return index.Item{}, err
	}

	err = d.moveOnStorage(ctx, it.Folder, src, dest)
	switch {
	case errors.Is(err, storage.ErrExists):
		return index.Item{}, &Taken{Name: it.Name}
	case errors.Is(err, storage.ErrNotFound):
		// The item has gone from the storage, or the folder it was going to has.
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		_, err := d.storage.Stat(sctx, src)
		cancel()
		if errors.Is(err, storage.ErrNotFound) {
			if err := d.index.MarkMissing(ctx, from.ID, it.ParentID, it.ID, it.Name, d.now(), stale); err != nil {
				return index.Item{}, err
			}
		}
		return index.Item{}, ErrGone
	case err != nil:
		return index.Item{}, storageError(err)
	}
	// A listing of the item itself under its old place, under way, is out of date too.
	d.changed(folderKey{from.ID, it.ParentID}, folderKey{to.ID, folderID(into)}, folderKey{from.ID, it.ID})
	moved, err := d.index.MoveToPlace(ctx, from.ID, it.ID, to.ID, folderID(into), it.Name)
	switch {
	case errors.Is(err, index.ErrUploading):
		// An upload started between the look above and now: put the item back where it was, so the two agree.
		d.putBack(ctx, it.Folder, src, dest)
		return index.Item{}, ErrBusy
	case errors.Is(err, index.ErrNoFolder):
		// The folder it went into moved away, or went, meanwhile.
		d.putBack(ctx, it.Folder, src, dest)
		return index.Item{}, ErrGone
	}
	return moved, err
}

// relocate moves a live item on the storage to the folder to (nil: the place's top folder) under name, then in the
// index. Nothing is ever replaced: a name taken there is a *Taken error.
func (d *Drive) relocate(ctx context.Context, p index.Place, it index.Item, to *index.Item, name string) (index.Item, error) {
	from, ok := itemPath(p, it)
	if !ok {
		return index.Item{}, ErrGone
	}
	dir, ok := folderPath(p, to)
	if !ok {
		return index.Item{}, ErrGone
	}
	dest, ok := pathIn(dir, name)
	if !ok {
		return index.Item{}, &BadName{"This name cannot be used. Choose another name."}
	}
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Item{}, err
	}
	defer leave()
	release, ok := d.claim(from, dest)
	if !ok || (it.Folder && d.busyInside(from)) {
		if ok {
			release()
		}
		return index.Item{}, ErrBusy
	}
	defer release()
	stale := d.since(folderKey{p.ID, it.ParentID})
	if err := d.ensureTop(ctx, p, to); err != nil {
		return index.Item{}, err
	}
	if err := d.free(ctx, dest, name); err != nil {
		return index.Item{}, err
	}

	err = d.moveOnStorage(ctx, it.Folder, from, dest)
	switch {
	case errors.Is(err, storage.ErrExists):
		return index.Item{}, &Taken{Name: name}
	case errors.Is(err, storage.ErrNotFound):
		// The item has gone from the storage, or the folder it was going to has.
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		_, err := d.storage.Stat(sctx, from)
		cancel()
		if errors.Is(err, storage.ErrNotFound) {
			if err := d.index.MarkMissing(ctx, p.ID, it.ParentID, it.ID, it.Name, d.now(), stale); err != nil {
				return index.Item{}, err
			}
		}
		return index.Item{}, ErrGone
	case err != nil:
		return index.Item{}, storageError(err)
	}
	d.changed(folderKey{p.ID, it.ParentID}, folderKey{p.ID, folderID(to)})
	moved, err := d.index.Relocate(ctx, p.ID, it.ID, folderID(to), name)
	if errors.Is(err, index.ErrNoFolder) {
		// The folder it went into moved to another place, or went, meanwhile: put it back, so the two agree.
		d.putBack(ctx, it.Folder, from, dest)
		return index.Item{}, ErrGone
	}
	return moved, err
}

// putBack moves an item back on the storage from to, where a move had just put it, to from, after the index refused
// the move; logged when it cannot (a look at either folder then sets the index right).
func (d *Drive) putBack(ctx context.Context, folder bool, from, to string) {
	if err := d.moveOnStorage(context.WithoutCancel(ctx), folder, to, from); err != nil {
		d.log.Error("an item moved on the storage could not be put back after the index refused the move; a look at either folder sets the index right",
			"from", from, "to", to, "error", err)
	}
}
