package drive

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// The walk: now and then the Drive looks at every folder of every place on the storage, and at every place's trash, one
// after another, as a folder is looked at when it is opened (reread), so that what was changed on the storage directly
// in a folder nobody opens is found too, by search and in Shared with me.
//
// It is slow on purpose. One folder at a time, never two walks at once; after each folder it rests for WalkPause, or
// for three times as long as the folder took when that is longer (at most a minute), so a slow storage is asked less
// often. A folder is one listing (PROPFIND with Depth 1, a few hundred bytes for each entry) and seldom more (a request
// for each item missing long enough, at most maxConfirms). It moves no file's bytes, so it takes no transfer slot: it
// holds one connection to the storage at a time, besides the at most maxRechecks of the looks at folders being opened.
//
// Where it is (the last folder it finished) is kept in the index after every folder, so a walk interrupted by a restart
// goes on from there. Its memory does not grow with the storage: it keeps one folder's entries at a time, at most
// MaxFolderEntries.

// Walking says when the walk runs.
type Walking struct {
	// First is how long after the Drive starts the first walk begins (or an interrupted one goes on).
	First time.Duration
	// Every is how long after one walk ends the next begins.
	Every time.Duration
	// Pause is the least the walk rests after each folder.
	Pause time.Duration
}

// The walk's timing in a released Drive: a walk of 100,000 files in 5,000 folders takes at least 5,000 × 0.5 s, about 42
// minutes, and more on a slow storage.
var DefaultWalking = Walking{First: time.Minute, Every: 6 * time.Hour, Pause: 500 * time.Millisecond}

// maxRest is the longest the walk rests after one folder.
const maxRest = time.Minute

// What the index keeps about the walk (index.State).
const (
	walkCursor   = "walk_cursor"   // the last step finished, while a walk is under way
	walkStarted  = "walk_started"  // when the walk under way started (unix milliseconds)
	walkFinished = "walk_finished" // when the last walk ended (unix milliseconds)
)

// Walk walks the storage now and then, as w says, until ctx ends.
func (d *Drive) Walk(ctx context.Context, w Walking) {
	wait := w.First
	if _, under, _ := d.index.State(ctx, walkCursor); !under {
		if v, ok, _ := d.index.State(ctx, walkFinished); ok {
			if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
				wait = max(w.First, time.Until(time.UnixMilli(ms).Add(w.Every)))
			}
		}
	}
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if err := d.walk(ctx, w.Pause); err != nil && ctx.Err() == nil {
			d.log.Error("the walk through the storage stopped; it goes on from where it was next time", "error", err)
		}
		wait = w.Every
	}
}

// walked counts what one walk did, for its log line.
type walked struct {
	folders, trashes, unread, tooLarge, untrusted int
	index.Outcome
}

// walk walks every place on the storage once, from where an interrupted walk stopped, or from the start.
func (d *Drive) walk(ctx context.Context, pause time.Duration) error {
	var step index.WalkStep
	began := time.Now()
	v, resuming, err := d.index.State(ctx, walkCursor)
	if err != nil {
		return err
	}
	if resuming {
		var ok bool
		if step, ok = index.ParseWalkStep(v); !ok {
			step, resuming = index.WalkStep{}, false
		}
	}
	if resuming {
		d.log.Info("the walk through the storage goes on from where it was", "after", step.String())
	} else {
		if err := d.index.SetState(ctx, walkStarted, strconv.FormatInt(d.now().UnixMilli(), 10)); err != nil {
			return err
		}
		if err := d.index.SetState(ctx, walkCursor, step.String()); err != nil {
			return err
		}
		d.log.Info("the walk through the storage started")
	}

	var w walked
	for {
		next, ok, err := d.index.NextWalkStep(ctx, step)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		started := time.Now()
		d.walkStep(ctx, next, &w)
		if err := ctx.Err(); err != nil {
			return err
		}
		step = next
		if err := d.index.SetState(ctx, walkCursor, step.String()); err != nil {
			return err
		}
		rest := min(max(pause, 3*time.Since(started)), maxRest)
		t := time.NewTimer(rest)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	d.unknownTopFolders(ctx)

	if err := d.index.SetState(ctx, walkFinished, strconv.FormatInt(d.now().UnixMilli(), 10)); err != nil {
		return err
	}
	if err := d.index.SetState(ctx, walkCursor, ""); err != nil {
		return err
	}
	d.log.Info("the walk through the storage ended", "resumed", resuming, "took", time.Since(began).Round(time.Second).String(),
		"folders", w.folders, "trashes", w.trashes, "added", w.Added, "changed", w.Changed, "back", w.Back,
		"hidden", w.Hidden, "dropped", w.Dropped, "unread", w.unread, "tooLarge", w.tooLarge, "untrusted", w.untrusted)
	return nil
}

// walkStep looks at one folder, or one trash, for the walk.
func (d *Drive) walkStep(ctx context.Context, s index.WalkStep, w *walked) {
	p, err := d.index.PlaceByID(ctx, s.Place)
	if err != nil {
		return // gone to the trash, or removed, since: nothing to look at
	}
	var o index.Outcome
	if s.Folder == index.TrashStep {
		o, err = d.rereadTrash(ctx, p)
		w.trashes++
	} else {
		o, err = d.reread(ctx, p, s.Folder)
		w.folders++
	}
	w.Add(o)
	switch {
	case err == nil, errors.Is(err, index.ErrChanged), errors.Is(err, index.ErrNoItem), errors.Is(err, ErrPlaceGone):
	case errors.Is(err, ErrGone):
		w.Hidden++
	case errors.Is(err, ErrFolderTooLarge):
		w.tooLarge++
		d.log.Warn("a folder on the storage holds more than the Drive reads at once; changes made in it on the storage directly are not seen",
			"place", p.ID, "folder", s.Folder, "most", MaxFolderEntries)
	case errors.Is(err, errUntrusted):
		w.untrusted++
	case ctx.Err() != nil:
	default:
		w.unread++
		if d.logged.due("walk unread", 10*time.Minute) {
			d.log.Warn("the walk could not look at a folder on the storage; it is looked at again on the next walk",
				"place", p.ID, "folder", s.Folder, "error", err)
		}
	}
}

// unknownTopFolders logs how many folders directly in "My files" and "Shared folders" on the storage belong to no
// place: nobody sees them in the Drive, and nothing is made of them. The names of shared folders are logged; those in
// My files are named after email addresses, and only counted.
func (d *Drive) unknownTopFolders(ctx context.Context) {
	places, err := d.index.Places(ctx)
	if err != nil {
		return
	}
	held := make(map[string]bool, len(places))
	for _, p := range places {
		held[p.Folder] = true
	}
	for _, top := range []string{MyFilesFolder, SharedFoldersFolder} {
		var names []string
		count := 0
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		err := d.storage.Each(sctx, top, func(e storage.Entry) error {
			if p, ok := pathIn(top, e.Name); ok && held[p] {
				return nil
			}
			count++
			if top == SharedFoldersFolder && len(names) < 20 {
				names = append(names, e.Name)
			}
			return nil
		})
		cancel()
		if err != nil || count == 0 {
			continue
		}
		if top == SharedFoldersFolder {
			d.log.Warn("folders in Shared folders on the storage belong to no shared folder: nobody sees them in the Drive",
				"count", count, "first", names)
		} else {
			d.log.Warn("folders in My files on the storage belong to nobody's My files: nobody sees them in the Drive", "count", count)
		}
	}
}
