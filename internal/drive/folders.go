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

// NewFolder makes an empty folder called name in a folder of the place (nil: its top folder).
func (d *Drive) NewFolder(ctx context.Context, p index.Place, in *index.Item, name string) (index.Item, error) {
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
	return d.index.PutItem(ctx, p.ID, folderID(in), index.Seen{Name: name, Folder: true, Changed: d.now().UTC()})
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
	if to != nil {
		if !to.Folder {
			return index.Item{}, ErrNotAFolder
		}
		if to.ID == it.ID {
			return index.Item{}, ErrIntoItself
		}
		for _, above := range to.Above {
			if above.ID == it.ID {
				return index.Item{}, ErrIntoItself
			}
		}
		if len(to.Above)+2 > index.MaxDepth {
			return index.Item{}, ErrTooDeep
		}
	}
	if folderID(to) == it.ParentID {
		return it, nil
	}
	return d.relocate(ctx, p, it, to, it.Name)
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
			if err := d.index.DropGone(ctx, p.ID, it.ParentID, it.ID, it.Name, stale); err != nil {
				return index.Item{}, err
			}
		}
		return index.Item{}, ErrGone
	case err != nil:
		return index.Item{}, storageError(err)
	}
	d.changed(folderKey{p.ID, it.ParentID}, folderKey{p.ID, folderID(to)})
	return d.index.Relocate(ctx, p.ID, it.ID, folderID(to), name)
}
