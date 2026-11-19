package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// TrashFolder holds every place's trash on the storage: the trash of the place whose top folder is "My files/x" is
// TrashFolder + "/My files/x". In it, each item that went to the trash has a folder of its own (an entry), named
// after the day it went there and a random part, such as "2026-11-19 7f3a9c1e", and holding the item under its real
// name. Two items of the same name can be in the trash at once, and the storage alone shows when each was deleted.
const TrashFolder = HiddenFolder + "/trash"

// KeptFor is how long an item stays in the trash before the Drive removes it for good.
const KeptFor = 30 * 24 * time.Hour

// entryDay is the layout of the day at the start of an entry's name.
const entryDay = "2006-01-02"

func trashTop(p index.Place) string { return TrashFolder + "/" + p.Folder }

// A TrashEntry is an item in a place's trash, as a person sees it.
type TrashEntry struct {
	index.TrashItem
	// From is the folder it goes back to when restored, from the top of the place down: the folder it was deleted
	// from, while that folder is still in the place. Empty for the top folder.
	From []index.Item
	// Removed is when the Drive removes it for good.
	Removed time.Time
}

// newEntry is the name of a new entry in a trash: today's date and a random part.
func (d *Drive) newEntry() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return d.now().UTC().Format(entryDay) + " " + hex.EncodeToString(b)
}

// makeEntry makes a new entry in the place's trash on the storage, and the trash's folders above it when missing.
func (d *Drive) makeEntry(ctx context.Context, p index.Place) (entry, dir string, err error) {
	entry = d.newEntry()
	dir, ok := pathIn(trashTop(p), entry)
	if !ok {
		return "", "", errors.New("the trash's folder for this place cannot be named") // the entry is the Drive's own name
	}
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	err = d.storage.CreateFolder(ctx, dir)
	if errors.Is(err, storage.ErrNotFound) {
		err = d.storage.MakeFolder(ctx, dir)
	}
	return entry, dir, storageError(err)
}

// moveOnStorage moves a file or folder on the storage, the way its kind needs. When the folder it was going into is
// missing, the error is storage.ErrNotFound, whatever the server answered (Apache answers 500).
func (d *Drive) moveOnStorage(ctx context.Context, folder bool, from, to string) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	var err error
	if folder {
		err = d.storage.MoveFolder(ctx, from, to)
	} else {
		err = d.storage.Move(ctx, from, to)
	}
	var status *storage.StatusError
	if errors.As(err, &status) {
		if e, serr := d.storage.Stat(ctx, path.Dir(to)); errors.Is(serr, storage.ErrNotFound) || (serr == nil && !e.Folder) {
			return fmt.Errorf("%w (the folder it was going into is missing)", storage.ErrNotFound)
		}
	}
	return err
}

// removeEntry removes an entry's folder from the trash on the storage, with whatever is still in it.
func (d *Drive) removeEntry(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	err := d.storage.DeleteFolder(ctx, dir)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return err
}

// Delete moves a live item of the place, with everything in it, into the place's trash. It keeps its id.
func (d *Drive) Delete(ctx context.Context, p index.Place, it index.Item) (TrashEntry, error) {
	from, ok := itemPath(p, it)
	if !ok {
		return TrashEntry{}, ErrGone
	}
	release, ok := d.claim(from)
	if !ok || (it.Folder && d.busyInside(from)) {
		if ok {
			release()
		}
		return TrashEntry{}, ErrBusy
	}
	defer release()
	stale := d.since(folderKey{p.ID, it.ParentID})

	entry, dir, err := d.makeEntry(ctx, p)
	if err != nil {
		return TrashEntry{}, err
	}
	to, _ := pathIn(dir, it.Name)
	if err := d.moveOnStorage(ctx, it.Folder, from, to); err != nil {
		_ = d.removeEntry(ctx, dir)
		if errors.Is(err, storage.ErrNotFound) {
			// Gone from the storage already: the index follows.
			if err := d.index.DropGone(ctx, p.ID, it.ParentID, it.ID, it.Name, stale); err != nil {
				return TrashEntry{}, err
			}
			return TrashEntry{}, ErrGone
		}
		return TrashEntry{}, storageError(err)
	}
	d.changed(folderKey{p.ID, it.ParentID}, folderKey{p.ID, trashFolder})
	if err := d.index.Trash(ctx, p.ID, it.ID, d.now(), entry); err != nil {
		return TrashEntry{}, err
	}
	t, err := d.index.TrashItem(ctx, p.ID, it.ID)
	if err != nil {
		return TrashEntry{}, err
	}
	return d.entry(ctx, p, t)
}

// putAside moves the file at file, which an upload is about to replace, into the place's trash, as a new item. undo
// puts it back, for when the upload fails. replaced is nil when the file had gone meanwhile.
func (d *Drive) putAside(ctx context.Context, p index.Place, folder *index.Item, file string, there storage.Entry) (replaced *index.TrashItem, undo func(), err error) {
	entry, dir, err := d.makeEntry(ctx, p)
	if err != nil {
		return nil, nil, err
	}
	to, _ := pathIn(dir, there.Name)
	if err := d.moveOnStorage(ctx, false, file, to); err != nil {
		_ = d.removeEntry(ctx, dir)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, storageError(err)
	}
	d.changed(folderKey{p.ID, trashFolder})
	id, err := d.index.AddReplaced(ctx, p.ID, folderID(folder), index.Seen{Name: there.Name, Size: there.Size, Changed: there.Changed}, d.now(), entry)
	if err != nil {
		return nil, nil, err
	}
	t, err := d.index.TrashItem(ctx, p.ID, id)
	if err != nil {
		return nil, nil, err
	}
	undo = func() {
		ctx := context.WithoutCancel(ctx)
		if err := d.moveOnStorage(ctx, false, to, file); err != nil {
			// It stays in the trash, where it can be restored.
			d.log.Error("a file an upload was to replace could not be put back; it is in the trash", "file", file, "error", err)
			return
		}
		d.changed(folderKey{p.ID, trashFolder})
		_ = d.removeEntry(ctx, dir)
		if err := d.index.RemoveFromTrash(ctx, p.ID, id); err != nil {
			d.log.Error("a file put back after a failed upload is still in the trash's index", "file", file, "error", err)
		}
	}
	return &t, undo, nil
}

// TrashItem finds the item with id in the place's trash.
func (d *Drive) TrashItem(ctx context.Context, p index.Place, id int64) (index.TrashItem, error) {
	return d.index.TrashItem(ctx, p.ID, id)
}

// Trash returns what is in the place's trash, the most recently deleted first. Anything that has been there for 30
// days is removed for good first, and anything no longer in the trash on the storage is dropped.
func (d *Drive) Trash(ctx context.Context, p index.Place) ([]TrashEntry, error) {
	if err := d.removeExpired(ctx, &p); err != nil {
		return nil, err
	}
	stale := d.since(folderKey{p.ID, trashFolder})
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	entries, err := d.storage.List(sctx, trashTop(p))
	cancel()
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return nil, storageError(err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name] = e.Folder
	}
	if err := d.index.ForgetTrash(ctx, p.ID, func(entry string) bool { return !present[entry] }, stale); err != nil {
		return nil, err
	}

	items, err := d.index.TrashItems(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	out := make([]TrashEntry, 0, len(items))
	for _, t := range items {
		e, err := d.entry(ctx, p, t)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// entry describes an item of the trash as a person sees it.
func (d *Drive) entry(ctx context.Context, p index.Place, t index.TrashItem) (TrashEntry, error) {
	e := TrashEntry{TrashItem: t, Removed: t.Deleted.Add(KeptFor)}
	if f, err := d.restoreTo(ctx, p, t); err != nil {
		return TrashEntry{}, err
	} else if f != nil {
		e.From = append(append([]index.Item{}, f.Above...), *f)
	}
	return e, nil
}

// restoreTo is the folder an item of the trash goes back to: the one it was in, while that is still a folder in the
// place outside the trash, and otherwise the place's top folder (nil).
func (d *Drive) restoreTo(ctx context.Context, p index.Place, t index.TrashItem) (*index.Item, error) {
	if t.RestoreTo == 0 {
		return nil, nil
	}
	f, err := d.index.Item(ctx, p.ID, t.RestoreTo)
	if errors.Is(err, index.ErrNoItem) || (err == nil && !f.Folder) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// Restore moves an item of the place's trash back to the folder it was deleted from (or the place's top folder, when
// that folder is in the trash itself or gone), under its own name, or with " (2)" and so on added when the name is
// taken there. It keeps its id.
func (d *Drive) Restore(ctx context.Context, p index.Place, t index.TrashItem) (index.Item, error) {
	dir, ok := pathIn(trashTop(p), t.Entry)
	if !ok {
		return index.Item{}, ErrGone
	}
	from, ok := pathIn(dir, t.Name)
	if !ok {
		return index.Item{}, ErrGone
	}
	dest, err := d.restoreTo(ctx, p, t)
	if err != nil {
		return index.Item{}, err
	}
	for {
		it, err := d.restoreInto(ctx, p, t, from, dest)
		if errors.Is(err, ErrGone) && dest != nil {
			dest = nil // the folder it was in has gone from the storage: the top folder instead
			continue
		}
		if errors.Is(err, errNotInTrash) {
			_ = d.removeEntry(ctx, dir)
			if err := d.index.RemoveFromTrash(ctx, p.ID, t.ID); err != nil {
				return index.Item{}, err
			}
			return index.Item{}, ErrGone
		}
		if err != nil {
			return index.Item{}, err
		}
		if err := d.removeEntry(ctx, dir); err != nil {
			d.log.Warn("an emptied folder in the trash could not be removed", "folder", dir, "error", err)
		}
		return it, nil
	}
}

var errNotInTrash = errors.New("no longer in the trash on the storage")

func (d *Drive) restoreInto(ctx context.Context, p index.Place, t index.TrashItem, from string, dest *index.Item) (index.Item, error) {
	dir, ok := folderPath(p, dest)
	if !ok {
		return index.Item{}, ErrGone
	}
	if err := d.ensureTop(ctx, p, dest); err != nil {
		return index.Item{}, err
	}
	name := t.Name
	to, ok := pathIn(dir, name)
	if !ok {
		return index.Item{}, ErrGone
	}
	release, ok := d.claim(to)
	if !ok {
		return index.Item{}, ErrBusy
	}
	defer release()
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	_, err := d.storage.Stat(sctx, to)
	cancel()
	switch {
	case err == nil:
		var releaseOther func()
		name, releaseOther, err = d.freeName(ctx, dir, name, t.Folder)
		if err != nil {
			return index.Item{}, err
		}
		defer releaseOther()
		to, _ = pathIn(dir, name)
	case !errors.Is(err, storage.ErrNotFound):
		return index.Item{}, storageError(err)
	}

	if err := d.moveOnStorage(ctx, t.Folder, from, to); err != nil {
		if errors.Is(err, storage.ErrExists) {
			return index.Item{}, &Taken{Name: name} // put there directly since the Drive looked
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return index.Item{}, storageError(err)
		}
		// Either the item is no longer in the trash, or the folder it was going to is gone.
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		_, err := d.storage.Stat(sctx, from)
		cancel()
		if errors.Is(err, storage.ErrNotFound) {
			return index.Item{}, errNotInTrash
		}
		return index.Item{}, ErrGone
	}
	d.changed(folderKey{p.ID, folderID(dest)}, folderKey{p.ID, trashFolder})
	return d.index.Restore(ctx, p.ID, t.ID, folderID(dest), name)
}

// EmptyTrash removes for good everything that has been in a trash for 30 days, in every place, then waits for the
// next run, every interval, until ctx ends.
func (d *Drive) EmptyTrash(ctx context.Context, every time.Duration) {
	for {
		if err := d.removeExpired(ctx, nil); err != nil && ctx.Err() == nil {
			d.log.Error("emptying the trash failed; it is tried again later", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// removeExpired removes for good everything that has been in the trash for 30 days: in the one place given, or in
// every place when only is nil. On the storage that includes entries the index does not know (left by a change the
// Drive did not finish), judged by the day in their names.
func (d *Drive) removeExpired(ctx context.Context, only *index.Place) error {
	cutoff := d.now().Add(-KeptFor)
	due, err := d.index.TrashedBefore(ctx, cutoff)
	if err != nil {
		return err
	}
	var places []index.Place
	if only != nil {
		places = []index.Place{*only}
	} else if places, err = d.index.Places(ctx); err != nil {
		return err
	}
	byID := map[int64]index.Place{}
	for _, p := range places {
		byID[p.ID] = p
	}

	var failed error
	removed := 0
	for _, t := range due {
		p, ok := byID[t.PlaceID]
		if !ok {
			continue
		}
		if dir, ok := pathIn(trashTop(p), t.Entry); ok {
			if err := d.removeEntry(ctx, dir); err != nil {
				failed = storageError(err) // kept, and tried again on the next run
				continue
			}
		}
		if err := d.index.RemoveFromTrash(ctx, p.ID, t.ID); err != nil {
			return err
		}
		removed++
	}

	for _, p := range places {
		n, err := d.removeLeftovers(ctx, p, cutoff)
		removed += n
		if err != nil {
			failed = err
		}
	}
	if removed > 0 {
		d.log.Info("removed from the trash for good after 30 days", "items", removed)
	}
	return failed
}

// removeLeftovers removes the entries of the place's trash on the storage that the index does not know and whose
// names say they are older than cutoff.
func (d *Drive) removeLeftovers(ctx context.Context, p index.Place, cutoff time.Time) (int, error) {
	known, err := d.index.TrashEntries(ctx, p.ID)
	if err != nil {
		return 0, err
	}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	entries, err := d.storage.List(sctx, trashTop(p))
	cancel()
	if errors.Is(err, storage.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, storageError(err)
	}
	removed := 0
	for _, e := range entries {
		if !e.Folder || known[e.Name] || len(e.Name) < len(entryDay) {
			continue
		}
		day, err := time.Parse(entryDay, e.Name[:len(entryDay)])
		if err != nil || !day.Add(24*time.Hour).Before(cutoff) {
			continue
		}
		dir, ok := pathIn(trashTop(p), e.Name)
		if !ok {
			continue
		}
		if err := d.removeEntry(ctx, dir); err != nil {
			return removed, storageError(err)
		}
		removed++
	}
	return removed, nil
}
