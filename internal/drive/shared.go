package drive

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// SharedFoldersFolder is the folder on the storage that holds every shared folder's top folder.
const SharedFoldersFolder = "Shared folders"

// SharedFolders returns every shared folder that is not in the trash, by name.
func (d *Drive) SharedFolders(ctx context.Context) ([]index.Place, error) {
	return d.index.SharedFolders(ctx)
}

// NewSharedFolder makes a shared folder called name, owned by the project, with its own top folder on the storage,
// and makes creator (a member's user id) its first manager.
//
// The top folder is "Shared folders/<name>", or "<name> (2)" and so on when another shared folder holds that folder
// (one in the trash included) or something is already there on the storage, or in the trash of that folder there: a
// new shared folder never takes over files nobody gave it. People see the name either way, so two shared folders can share a name; a person who could
// see the other one is refused before it gets here.
func (d *Drive) NewSharedFolder(ctx context.Context, name, creator string) (index.Place, error) {
	if err := CheckName(name); err != nil {
		return index.Place{}, err
	}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err := d.storage.MakeFolder(sctx, SharedFoldersFolder)
	cancel()
	if err != nil {
		return index.Place{}, storageError(err)
	}
	for n := 1; n < 1000; n++ {
		base := name
		if n > 1 {
			base = numbered(name, n, true)
		}
		folder, ok := pathIn(SharedFoldersFolder, base)
		if !ok {
			return index.Place{}, &BadName{nameUnusable} // CheckName refuses these first
		}
		if taken, err := d.index.FolderTaken(ctx, folder); err != nil {
			return index.Place{}, err
		} else if taken {
			continue
		}
		release, ok := d.claim(folder)
		if !ok {
			continue // another shared folder is being made there right now
		}
		p, made, err := d.makeSharedFolder(ctx, name, folder, creator)
		release()
		if made || err != nil {
			return p, err
		}
	}
	return index.Place{}, &Taken{Name: name, Folder: true}
}

// makeSharedFolder makes the top folder at folder and records the shared folder. made is false, with no error, when
// something is already there on the storage.
func (d *Drive) makeSharedFolder(ctx context.Context, name, folder, creator string) (p index.Place, made bool, err error) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	_, err = d.storage.Stat(sctx, folder)
	switch {
	case err == nil:
		return index.Place{}, false, nil
	case !errors.Is(err, storage.ErrNotFound):
		return index.Place{}, false, d.nameError(err, name)
	}
	// Nor is anything in the trash of a place that had this top folder: a shared folder deleted whole lies there, and
	// what was deleted from it, while its top folder is gone. An index that no longer knows that shared folder (built
	// anew from the files, or made from a copy older than it) would have the next look at the new one's trash take all
	// of it for the new one's own, for whoever made it to restore.
	_, err = d.storage.Stat(sctx, trashTop(index.Place{Folder: folder}))
	switch {
	case err == nil:
		return index.Place{}, false, nil
	case !errors.Is(err, storage.ErrNotFound):
		return index.Place{}, false, d.nameError(err, name)
	}
	err = d.storage.CreateFolder(sctx, folder)
	switch {
	case errors.Is(err, storage.ErrExists):
		return index.Place{}, false, nil
	case errors.Is(err, storage.ErrFull):
		return index.Place{}, false, ErrFull
	case err != nil:
		return index.Place{}, false, d.nameError(err, name)
	}
	d.marked(ctx)
	p, err = d.index.AddSharedFolder(ctx, name, folder, creator, d.now())
	if err != nil {
		d.log.Error("a shared folder's top folder was made, but the shared folder could not be recorded", "folder", folder, "error", err)
	}
	return p, err == nil, err
}

// A DeletedSharedFolder is a shared folder in the trash, as a person sees it.
type DeletedSharedFolder struct {
	index.Place
	// Removed is when the Drive removes it for good.
	Removed time.Time
	// Size is the sum of the files in it as far as the Drive knows them, its own trash included.
	Size int64
}

// DeleteSharedFolder moves a shared folder, with everything in it, into the trash: its top folder goes into an entry
// of its own trash folder, beside what was deleted from it, and the shared folder is hidden from everyone until a
// manager restores it, or the Drive removes it for good with its trash 30 days later. Its items, its trash and the
// roles given on it are kept as they are meanwhile. It waits for changes inside it to end (ErrBusy when they do not).
func (d *Drive) DeleteSharedFolder(ctx context.Context, p index.Place) (DeletedSharedFolder, error) {
	if p.Kind != index.SharedFolder {
		return DeletedSharedFolder{}, errors.New("only a shared folder can be deleted as a whole")
	}
	release, err := d.wholePlace(ctx, p.ID)
	if err != nil {
		return DeletedSharedFolder{}, err
	}
	defer release()
	if p, err = d.index.PlaceByID(ctx, p.ID); errors.Is(err, index.ErrNoPlace) {
		return DeletedSharedFolder{}, ErrPlaceGone
	} else if err != nil {
		return DeletedSharedFolder{}, err
	}

	entry, dir, held, err := d.makeEntry(ctx, p)
	if err != nil {
		return DeletedSharedFolder{}, err
	}
	defer held()
	to, ok := pathIn(dir, path.Base(p.Folder))
	if !ok {
		return DeletedSharedFolder{}, errors.New("a shared folder's top folder has no name") // the Drive made it
	}
	// Not found: its top folder had already gone from the storage. The empty entry stands for it, and restoring makes
	// the top folder again.
	// From here on, whoever asked hanging up changes nothing (as in putInPlace): a move the storage made and the index
	// never heard of would be taken for one made on the storage directly, and lose what was given on the item.
	ctx = context.WithoutCancel(ctx)
	if err := d.moveOnStorage(ctx, true, p.Folder, to); err != nil && !errors.Is(err, storage.ErrNotFound) {
		if !unsure(err) { // unsure, the entry may hold the whole shared folder, now or in a moment: it is left
			_ = d.removeEntry(ctx, dir)
		}
		return DeletedSharedFolder{}, storageError(err)
	}
	d.changed(folderKey{p.ID, 0}, folderKey{p.ID, trashFolder})
	if err := d.recorded(func() error { return d.index.DeleteSharedFolder(ctx, p.ID, d.now(), entry) }); err != nil {
		d.putBack(ctx, true, p.Folder, to) // the index did not take it: back where it was, so the two agree
		return DeletedSharedFolder{}, err
	}
	gone, err := d.index.DeletedPlace(ctx, p.ID)
	if err != nil {
		return DeletedSharedFolder{}, err
	}
	return d.deleted(ctx, gone)
}

// RestoreSharedFolder brings a shared folder out of the trash, back to its own top folder, with everything in it, its
// own trash and the roles given on it as they were. Its folder is held for it while it is in the trash, so only
// something put there directly on the storage can be in the way; that is never replaced (a *Taken error), unless its
// entry in the trash no longer holds its top folder: then what is there is its top folder, put back by a rollback, and
// it is restored as it is.
func (d *Drive) RestoreSharedFolder(ctx context.Context, p index.Place) (index.Place, error) {
	release, err := d.wholePlace(ctx, p.ID)
	if err != nil {
		return index.Place{}, err
	}
	defer release()
	if p, err = d.index.DeletedPlace(ctx, p.ID); errors.Is(err, index.ErrNoPlace) {
		return index.Place{}, ErrPlaceGone
	} else if err != nil {
		return index.Place{}, err
	}
	dir, ok := pathIn(trashTop(p), p.Entry)
	if !ok {
		return index.Place{}, ErrGone
	}
	base := path.Base(p.Folder)
	from, _ := pathIn(dir, base)

	// Its top folder may be back at its own path already, and its entry no longer hold it: a rollback to before it was
	// deleted put it there. Then it is restored as it is, without a move.
	if back, err := d.topFolderBack(ctx, p); err != nil {
		return index.Place{}, err
	} else if back {
		if err := d.index.RestoreSharedFolder(ctx, p.ID); err != nil {
			return index.Place{}, err
		}
		return d.index.PlaceByID(ctx, p.ID)
	}

	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	if err := d.storage.MakeFolder(sctx, path.Dir(p.Folder)); err != nil {
		return index.Place{}, storageError(err)
	}
	switch _, err := d.storage.Stat(sctx, p.Folder); {
	case err == nil:
		return index.Place{}, &Taken{Name: base, Folder: true}
	case !errors.Is(err, storage.ErrNotFound):
		return index.Place{}, storageError(err)
	}
	// From here on, whoever asked hanging up changes nothing (as in putInPlace): a move the storage made and the index
	// never heard of would be taken for one made on the storage directly, and lose what was given on the item.
	ctx = context.WithoutCancel(ctx)
	err = d.moveOnStorage(ctx, true, from, p.Folder)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		// Its top folder is not in its entry (it had gone before it was deleted): an empty one, as a listing would make.
		// Only when a look says so too: the entry is removed below with whatever is in it, and an answer of "not there"
		// that was wrong would take the whole shared folder with it.
		fctx, done := context.WithTimeout(ctx, storageTimeout)
		_, serr := d.storage.Stat(fctx, from)
		if serr == nil {
			done()
			return index.Place{}, fmt.Errorf("%w: the storage would not move a shared folder out of the trash, and still holds it there", ErrStorage)
		}
		if !errors.Is(serr, storage.ErrNotFound) {
			done()
			return index.Place{}, storageError(serr)
		}
		err := d.storage.CreateFolder(fctx, p.Folder)
		done()
		if err != nil && !errors.Is(err, storage.ErrExists) {
			return index.Place{}, storageError(err)
		}
	case errors.Is(err, storage.ErrExists):
		return index.Place{}, &Taken{Name: base, Folder: true}
	case err != nil:
		return index.Place{}, storageError(err)
	}
	d.changed(folderKey{p.ID, 0}, folderKey{p.ID, trashFolder})
	if err := d.removeEntry(ctx, dir); err != nil {
		d.log.Warn("an emptied folder in the trash could not be removed", "folder", dir, "error", err)
	}
	if err := d.recorded(func() error { return d.index.RestoreSharedFolder(ctx, p.ID) }); err != nil {
		return index.Place{}, err // its top folder is back at its own path: restoring again finds it there (topFolderBack)
	}
	return d.index.PlaceByID(ctx, p.ID)
}

// DeletedSharedFolders returns every shared folder in the trash, the most recently deleted first. Those that have been
// there for 30 days are removed for good first.
func (d *Drive) DeletedSharedFolders(ctx context.Context) ([]DeletedSharedFolder, error) {
	if _, err := d.removeExpiredSharedFolders(ctx, d.now().Add(-KeptFor)); err != nil {
		return nil, err
	}
	places, err := d.index.DeletedSharedFolders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DeletedSharedFolder, 0, len(places))
	for _, p := range places {
		e, err := d.deleted(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (d *Drive) deleted(ctx context.Context, p index.Place) (DeletedSharedFolder, error) {
	size, err := d.index.SizeOfPlace(ctx, p.ID)
	return DeletedSharedFolder{Place: p, Removed: p.Deleted.Add(KeptFor), Size: size}, err
}

// removeExpiredSharedFolders removes for good the shared folders that went to the trash before cutoff: their trash
// folder on the storage, which holds their top folder and everything deleted from them, then their place in the index
// with its items and roles. One that is being restored right now is left for the next run.
func (d *Drive) removeExpiredSharedFolders(ctx context.Context, cutoff time.Time) (removed int, failed error) {
	due, err := d.index.SharedFoldersDeletedBefore(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	for _, p := range due {
		release, err := d.wholePlace(ctx, p.ID)
		if err != nil {
			failed = err
			continue
		}
		if p, err = d.index.DeletedPlace(ctx, p.ID); err != nil || !p.Deleted.Before(cutoff) {
			release() // restored meanwhile
			continue
		}
		if err := d.removeEntry(ctx, trashTop(p)); err != nil {
			release()
			failed = storageError(err) // kept, and tried again on the next run
			continue
		}
		err = d.index.RemoveSharedFolder(ctx, p.ID)
		release()
		if err != nil {
			return removed, err
		}
		d.log.Info("removed a shared folder from the trash for good after 30 days", "folder", p.Folder)
		removed++
	}
	return removed, failed
}
