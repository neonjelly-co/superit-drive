package drive

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// A rollback: the storage put back to a snapshot. On Superit's Drive storage a project admin does it from the
// dashboard; the platform stops the Drive meanwhile, and starts it again with settings that say what happened
// (settings.Rollback), and keeps starting it with the same ones until the next rollback. The files on the storage are
// then as they were when the snapshot was taken; the index is not put back, so sharing stays as it is.
//
// TakeIn takes a declared rollback in once, when the Drive starts: one it took in already (the same time) is old news.
// Taking it in keeps it in the index (drive_state) and has the walk through the storage start at once, from the top,
// so the index comes in step with the storage as the looks always bring it (reread.go): what is gone is hidden at once
// and dropped, with its sharing, when a later look confirms it; what is back is a new item with its folder's access;
// nothing is ever given from the storage, and nothing taken away comes back. Until that walk has ended, with the
// storage holding the Drive's mark throughout, the Drive also:
//
//   - first looks at every place's trash, and at every shared folder in the trash: a shared folder deleted since the
//     snapshot has its top folder back at its own path, and its entry no longer holds it, so it is out of the trash
//     again, as a manager restoring it would have it (backFromTheTrash);
//   - takes an entry of a trash that holds exactly an item the place has outside the trash, gone from where the index
//     has it, for that item, back in the trash as it was at the snapshot (restored since): it goes into the trash as
//     itself, with what was given on it and its link, to come back with them when restored (rereadTrash);
//   - writes its mark back when the storage no longer holds it and the snapshot was taken before the Drive marked the
//     storage (checkMark): the rollback says why it is gone. A mark that is there with another id is never replaced.
//
// Members see a notice for NoticeFor after it happened (Notice), saying who put the files back and to when.

// NoticeFor is how long members see that the storage was put back, from when it was.
const NoticeFor = 7 * 24 * time.Hour

// A Rollback is the storage put back to a snapshot, as the settings declared it.
type Rollback struct {
	// At is when it was put back; To is when the snapshot was taken; By is who did it, as members read it.
	At, To time.Time
	By     string
	// Until is when members stop seeing the notice: NoticeFor after At (after the Drive took it in, when At is later).
	Until time.Time
}

// What the index keeps about rollbacks (index.State).
const (
	rollbackKey   = "rollback"       // the last rollback taken in (JSON)
	rollbackWalk  = "rollback_walk"  // its time (unix milliseconds) while the walk that takes it in is due or under way
	storageMarked = "storage_marked" // when the Drive last wrote its mark on the storage (unix milliseconds)
)

type rollbackJSON struct {
	At    int64  `json:"at"` // unix milliseconds, as are the others
	To    int64  `json:"to"`
	By    string `json:"by"`
	Until int64  `json:"until"`
}

func (r Rollback) encode() string {
	b, _ := json.Marshal(rollbackJSON{At: r.At.UnixMilli(), To: r.To.UnixMilli(), By: r.By, Until: r.Until.UnixMilli()})
	return string(b)
}

func decodeRollback(v string) (Rollback, bool) {
	var j rollbackJSON
	if err := json.Unmarshal([]byte(v), &j); err != nil || j.At == 0 {
		return Rollback{}, false
	}
	return Rollback{At: time.UnixMilli(j.At).UTC(), To: time.UnixMilli(j.To).UTC(), By: j.By, Until: time.UnixMilli(j.Until).UTC()}, true
}

// lastRollback is the last rollback the Drive took in; ok is false when it took in none.
func (d *Drive) lastRollback(ctx context.Context) (Rollback, bool, error) {
	v, ok, err := d.index.State(ctx, rollbackKey)
	if err != nil || !ok {
		return Rollback{}, false, err
	}
	r, ok := decodeRollback(v)
	return r, ok, nil
}

// TakeIn takes in the rollback the settings declare (nil: none), when the Drive starts, before the walk. taken is false
// when there is nothing new to take in: none declared, the one taken in already (the same time, to the second; whoever
// runs the Drive declares it on every start until the next one), or one older than that, which is left aside.
func (d *Drive) TakeIn(ctx context.Context, declared *Rollback) (taken bool, err error) {
	if declared == nil {
		return false, nil
	}
	last, ok, err := d.lastRollback(ctx)
	if err != nil {
		return false, err
	}
	at := declared.At.Truncate(time.Second)
	switch {
	case ok && at.Equal(last.At.Truncate(time.Second)):
		d.log.Info("the rollback of the storage in the settings was taken in already", "at", at.Format(time.RFC3339))
		return false, nil
	case ok && at.Before(last.At):
		d.log.Warn("the settings declare a rollback of the storage older than the last one taken in; it is left aside",
			"at", at.Format(time.RFC3339), "last", last.At.Format(time.RFC3339))
		return false, nil
	}
	// The seven days count from when it happened; from now, when the time given is still to come (clocks differ).
	from := declared.At
	if now := d.now(); now.Before(from) {
		from = now
	}
	r := Rollback{At: declared.At.UTC(), To: declared.To.UTC(), By: declared.By, Until: from.Add(NoticeFor).UTC()}
	// One change: the rollback, the walk it needs, and no walk to go on with (it would skip the folders before it).
	if err := d.index.SetStates(ctx, map[string]string{
		rollbackKey:  r.encode(),
		rollbackWalk: strconv.FormatInt(r.At.UnixMilli(), 10),
		walkCursor:   "",
	}); err != nil {
		return false, err
	}
	d.log.Info("the storage was put back to a snapshot: the Drive walks through it now, and members see a notice until the time given",
		"at", r.At.Format(time.RFC3339), "to", r.To.Format(time.RFC3339), "until", r.Until.Format(time.RFC3339))
	return true, nil
}

// Notice is the last rollback while members are to see it; ok is false when there is none to see.
func (d *Drive) Notice(ctx context.Context) (Rollback, bool, error) {
	r, ok, err := d.lastRollback(ctx)
	if err != nil || !ok || !d.now().Before(r.Until) {
		return Rollback{}, false, err
	}
	return r, true, nil
}

// rollingBack is the rollback the Drive took in while the walk that brings the index in step with it has not ended;
// ok is false otherwise.
func (d *Drive) rollingBack(ctx context.Context) (Rollback, bool) {
	if _, ok, err := d.index.State(ctx, rollbackWalk); err != nil || !ok {
		return Rollback{}, false
	}
	r, ok, err := d.lastRollback(ctx)
	return r, ok && err == nil
}

// rolledBack records that a walk through the whole storage ended after the rollback at, with the Drive's mark there
// for every look: the rollback is taken in.
func (d *Drive) rolledBack(ctx context.Context, at time.Time) error {
	if v, ok, err := d.index.State(ctx, rollbackWalk); err != nil || !ok || v != strconv.FormatInt(at.UnixMilli(), 10) {
		return err
	}
	if err := d.index.SetState(ctx, rollbackWalk, ""); err != nil {
		return err
	}
	d.log.Info("the index is in step with the storage put back to a snapshot", "at", at.Format(time.RFC3339))
	return nil
}

// beforeTheMark says whether the storage was put back to a snapshot taken before the Drive last wrote its mark there:
// then the mark's absence is explained, and checkMark may write it back.
func (d *Drive) beforeTheMark(ctx context.Context) (Rollback, bool) {
	r, ok := d.rollingBack(ctx)
	if !ok {
		return Rollback{}, false
	}
	v, ok, err := d.index.State(ctx, storageMarked)
	if err != nil || !ok {
		return Rollback{}, false
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	return r, err == nil && r.To.Before(time.UnixMilli(ms))
}

// afterRollback is the first part of the walk after a rollback: every shared folder in the trash, then every place's
// trash, before any folder, so that what the rollback put back into a trash, or took out of one, is found before a look
// at a folder hides it. done is false when the storage did not hold the Drive's mark (nothing changed), or a shared folder
// in the trash could not be looked at: then the rollback stays to be taken in, and the next walk begins with this again.
func (d *Drive) afterRollback(ctx context.Context) (done bool) {
	if !d.ours(ctx) {
		return false
	}
	done = true
	back := 0
	deleted, err := d.index.DeletedSharedFolders(ctx)
	if err != nil {
		return false
	}
	for _, p := range deleted {
		ok, err := d.backFromTheTrash(ctx, p)
		if err != nil {
			done = false
			if ctx.Err() == nil {
				d.log.Warn("a shared folder in the trash could not be looked at after the rollback; it is looked at again on the next walk",
					"place", p.ID, "error", err)
			}
		}
		if ok {
			back++
		}
	}
	var o index.Outcome
	if places, err := d.index.Places(ctx); err == nil {
		for _, p := range places {
			if !p.Deleted.IsZero() || ctx.Err() != nil {
				continue
			}
			got, err := d.rereadTrash(ctx, p)
			o.Add(got)
			d.lookFailed(p, trashFolder, err)
		}
	}
	d.log.Info("after the rollback, the Drive looked at every trash on the storage", "sharedFoldersBack", back,
		"backInTheTrash", o.Back, "added", o.Added, "hidden", o.Hidden)
	return done
}

// backFromTheTrash takes a shared folder out of the trash, as it is, when the storage shows its top folder at its own
// path again and its entry in the trash no longer holds it: a rollback to before it was deleted put it back there.
// It comes back as restoring it would bring it back: with its managers and the roles given on it, what is in it with
// what was given on that, and its links. Nothing is moved on the storage; an empty entry left is removed.
func (d *Drive) backFromTheTrash(ctx context.Context, p index.Place) (bool, error) {
	release, err := d.wholePlace(ctx, p.ID)
	if err != nil {
		return false, err
	}
	defer release()
	if p, err = d.index.DeletedPlace(ctx, p.ID); errors.Is(err, index.ErrNoPlace) {
		return false, nil // restored, or removed for good, meanwhile
	} else if err != nil {
		return false, err
	}
	back, err := d.topFolderBack(ctx, p)
	if err != nil || !back {
		return false, err
	}
	if err := d.index.RestoreSharedFolder(ctx, p.ID); err != nil {
		return false, err
	}
	d.log.Info("a shared folder in the trash is back: its top folder is on the storage at its own path again, and its entry in the trash no longer holds it",
		"place", p.ID, "folder", p.Folder)
	return true, nil
}

// topFolderBack says whether a shared folder in the trash has its top folder back at its own path on the storage, while
// its entry in the trash no longer holds it; if so, it records that the place's top folder and trash change, and
// removes the entry when it is empty. It holds the place alone (wholePlace).
func (d *Drive) topFolderBack(ctx context.Context, p index.Place) (bool, error) {
	dir, ok := pathIn(trashTop(p), p.Entry)
	if !ok {
		return false, nil
	}
	inEntry, _ := pathIn(dir, path.Base(p.Folder))
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	top, err := d.storage.Stat(sctx, p.Folder)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return false, nil
	case err != nil:
		return false, storageError(err)
	case !top.Folder:
		return false, nil
	}
	if _, err := d.storage.Stat(sctx, inEntry); !errors.Is(err, storage.ErrNotFound) {
		return false, storageError(err) // nil when the entry still holds it: two of it, and neither is taken
	}
	d.changed(folderKey{p.ID, 0}, folderKey{p.ID, trashFolder})
	if entries, err := d.storage.List(sctx, dir, 1); err == nil && len(entries) == 0 {
		_ = d.removeEntry(ctx, dir)
	}
	return true, nil
}

// restoredSince finds the item of the place, outside the trash, that is exactly what an entry of its trash holds and is
// no longer where the index has it: after a rollback, an item restored since the snapshot is back in its entry. ok is
// false unless there is exactly one, in a folder that is live, and nothing the Drive is changing.
func (d *Drive) restoredSince(ctx context.Context, p index.Place, s index.Seen) (id, from int64, ok bool) {
	twins, err := d.index.ExactlyAs(ctx, p.ID, s, 2)
	if err != nil || len(twins) != 1 {
		return 0, 0, false
	}
	t := twins[0]
	var folder *index.Item
	if t.ParentID != 0 {
		f, err := d.index.Item(ctx, p.ID, t.ParentID)
		if err != nil || !f.Folder {
			return 0, 0, false // in a folder in the trash, or gone itself
		}
		folder = &f
	}
	dir, ok := folderPath(p, folder)
	if !ok {
		return 0, 0, false
	}
	at, ok := pathIn(dir, t.Name)
	if !ok || d.busyIn(at) || (!t.Missing && !d.goneFrom(ctx, at)) {
		return 0, 0, false
	}
	return t.ID, t.ParentID, true
}
