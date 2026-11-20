package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Keeping the index in step with the storage. The storage wins for which files exist and what is in them; the index
// alone holds access. People and programs with the storage login can change the files behind the Drive's back, and the
// storage can be put back to an earlier state, so the Drive looks again:
//
//   - a folder is listed from the index at once, and looked at on the storage in the background (Recheck), every time it
//     is opened;
//   - a slow walk goes through every folder of every place now and then (walk.go);
//   - a zip looks at each folder it puts in (Refresh).
//
// One look at a folder (reread) lists it on the storage, then brings the index in step (index.Reconcile): what is new
// is added, with nothing given on it, so it has the access of the folder it is in; what is gone is hidden at once, and
// dropped (with what was given on it, its public link, uploads into it and its trash records' way back) only when a
// look at least ConfirmAfter later still misses it and a request for its own address finds nothing. Found again before
// that exactly as the Drive knew it (same name, kind, size and time of last change), it is back, the same item; anything
// else under its name is a new item (see index.Reconcile for what makes an item the same). A look changes nothing at all unless the storage holds the Drive's mark with the id
// its index keeps (mark below), so a storage that is empty, mounted wrongly or someone else's never empties the Drive.
//
// A look reads the storage without holding the place's lock, then takes it only to compare and apply: anything the Drive
// itself changed in the folder meanwhile (the generations), or is changing now (a claim on the folder, a folder above
// it, or a name in it), makes the look out of date, and that part of it is not applied.

// MaxFolderEntries is the most entries one look reads from a folder on the storage. A folder holding more is not looked
// at (nothing in it changes in the index), and the walk logs it: the entries a look keeps cost memory until it is
// applied, about 200 bytes each.
const MaxFolderEntries = 10_000

// ConfirmAfter is how long an item must have been missing from the storage before a look may drop it from the index.
// Until then it is hidden, and comes back as it was when it is seen again: a program saving a file by writing a new one
// and removing the old, a storage briefly answering wrong, a look made just as something was moved.
const ConfirmAfter = 10 * time.Minute

// maxConfirms bounds the requests one look makes to confirm, item by item, that what has been missing long enough is
// still gone; the rest wait for the next look.
const maxConfirms = 64

// maxAdoptions bounds how many entries of a trash that the index does not know one look opens.
const maxAdoptions = 32

// errUntrusted: the storage does not hold the Drive's mark, so a look changes nothing.
var errUntrusted = errors.New("the storage does not hold the Drive's mark")

// ErrFolderTooLarge: a folder on the storage holds more than MaxFolderEntries entries, and was not looked at.
var ErrFolderTooLarge = errors.New("the folder on the storage holds more than the Drive reads at once")

// Refresh looks at one folder of the place (nil: its top folder) on the storage now, and brings the index in step with
// it, before a zip reads it. A folder gone from the storage is ErrGone; any other trouble leaves the index as it was and
// is not an error: the zip then holds what the index knows.
func (d *Drive) Refresh(ctx context.Context, p index.Place, folder *index.Item) error {
	_, err := d.reread(ctx, p, folderID(folder))
	if errors.Is(err, ErrGone) || errors.Is(err, index.ErrNoItem) {
		return ErrGone
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, index.ErrChanged) {
		d.log.Warn("a folder could not be looked at on the storage; the index is used as it is", "place", p.ID,
			"folder", folderID(folder), "error", err)
	}
	return ctx.Err()
}

// reread looks at one folder of the place (folderID 0: its top folder) on the storage and brings the index in step with
// it. One look at a time runs per folder. A folder no longer in the place, or in the trash, or missing, is
// index.ErrNoItem; a folder the look finds gone from the storage is marked missing, and is ErrGone.
func (d *Drive) reread(ctx context.Context, p index.Place, id int64) (index.Outcome, error) {
	release := d.rereading.hold(folderKey{p.ID, id})
	defer release()
	// Taken before the index and the storage are read: a change the Drive makes from here on means the look is out of date.
	stale := d.since(folderKey{p.ID, id})
	var folder *index.Item
	var path []string
	if id != 0 {
		it, err := d.index.Item(ctx, p.ID, id)
		if err != nil {
			return index.Outcome{}, err
		}
		if !it.Folder {
			return index.Outcome{}, index.ErrNoItem
		}
		folder = &it
		for _, f := range it.Above {
			path = append(path, f.Name)
		}
		path = append(path, it.Name)
	}
	dir, ok := folderPath(p, folder)
	if !ok {
		return index.Outcome{}, ErrGone
	}
	var parentStale func() bool
	if folder != nil {
		parentStale = d.since(folderKey{p.ID, folder.ParentID})
	}

	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	entries, err := d.storage.List(sctx, dir, MaxFolderEntries)
	cancel()
	switch {
	case errors.Is(err, storage.ErrNotFound) && folder != nil:
		// The folder itself is gone: it is marked missing in the folder above it, unless the Drive is moving it, or
		// something above it, right now.
		if !d.ours(ctx) {
			return index.Outcome{}, errUntrusted
		}
		leave, err := d.inPlace(ctx, p)
		if err != nil {
			return index.Outcome{}, err
		}
		defer leave()
		busy := func() bool { return parentStale() || d.busyAt(dir) }
		if err := d.index.MarkMissing(ctx, p.ID, folder.ParentID, folder.ID, folder.Name, d.now(), busy); err != nil {
			return index.Outcome{}, err
		}
		return index.Outcome{}, ErrGone
	case errors.Is(err, storage.ErrNotFound):
		entries = nil // the place's top folder is gone, and with it everything in it; it is made again when needed
	case errors.Is(err, storage.ErrTooMany):
		return index.Outcome{}, ErrFolderTooLarge
	case err != nil:
		return index.Outcome{}, storageError(err)
	}

	seen := make([]index.Seen, 0, len(entries))
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		// A name the Drive would refuse (CheckName) is left out: never listed, opened or counted, and left on the storage.
		if CheckName(e.Name) != nil {
			continue
		}
		seen = append(seen, index.Seen{Name: e.Name, Folder: e.Folder, Size: e.Size, Changed: e.Changed})
		names[e.Name] = true
	}
	entries = nil
	leaveName := func(name string) bool {
		at, ok := pathIn(dir, name)
		return !ok || CheckName(name) != nil || d.busyIn(at)
	}

	// Most looks find the folder as the index has it: nothing more is asked or written.
	now := d.now()
	old := now.Add(-ConfirmAfter)
	known, err := d.index.KnownIn(ctx, p.ID, id)
	if err != nil {
		return index.Outcome{}, err
	}
	if !index.Differs(known, seen, leaveName, old) {
		return index.Outcome{}, nil
	}
	known = nil
	if !d.ours(ctx) {
		return index.Outcome{}, errUntrusted
	}

	// What has been missing long enough is dropped if a request for its own address finds nothing there.
	missing, err := d.index.MissingIn(ctx, p.ID, id, old, maxConfirms)
	if err != nil {
		return index.Outcome{}, err
	}
	confirmed := map[int64]time.Time{}
	for _, m := range missing {
		if names[m.Name] {
			continue // the storage shows something there: back, or another item that has its name now
		}
		if at, ok := pathIn(dir, m.Name); ok && d.goneFrom(ctx, at) {
			confirmed[m.ID] = m.Since
		}
	}

	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Outcome{}, err
	}
	defer leave()
	return d.index.Reconcile(ctx, p.ID, id, index.Reread{
		Path:      path,
		Seen:      seen,
		Leave:     leaveName,
		Busy:      func() bool { return d.busyAt(dir) },
		Stale:     stale,
		At:        now,
		Hide:      true,
		Old:       old,
		Confirmed: confirmed,
		Shallow:   folder != nil && len(folder.Above)+2 > index.MaxDepth,
	})
}

// goneFrom asks the storage for what is at p, and says whether it answered that nothing is there. Any other answer,
// an error included, is not taken for gone.
func (d *Drive) goneFrom(ctx context.Context, p string) bool {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	_, err := d.storage.Stat(ctx, p)
	return errors.Is(err, storage.ErrNotFound)
}

// entryName is the form of an entry's name in a trash: the day, a space, and eight hex digits.
var entryName = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9a-f]{8}$`)

// rereadTrash looks at the place's trash on the storage and brings the index's record of it in step, entry by entry
// (index.ReconcileTrash): an item whose entry is gone, or no longer holds it, is hidden from the trash, and dropped when
// a later look confirms it; an entry the index does not know that holds one item is shown in the trash as an item
// deleted on the day the entry names, by nobody the Drive knows, going back to the place's top folder.
func (d *Drive) rereadTrash(ctx context.Context, p index.Place) (index.Outcome, error) {
	release := d.rereading.hold(folderKey{p.ID, trashFolder})
	defer release()
	stale := d.since(folderKey{p.ID, trashFolder})
	top := trashTop(p)
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	entries, err := d.storage.List(sctx, top, MaxFolderEntries)
	cancel()
	switch {
	case errors.Is(err, storage.ErrNotFound):
		entries = nil // no trash folder: nothing in the trash
	case errors.Is(err, storage.ErrTooMany):
		return index.Outcome{}, ErrFolderTooLarge
	case err != nil:
		return index.Outcome{}, storageError(err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		if e.Folder {
			present[e.Name] = true
		}
	}
	entries = nil

	// Most looks find the trash as the index has it: nothing more is asked or written.
	now := d.now()
	old := now.Add(-ConfirmAfter)
	missing, err := d.index.TrashMissing(ctx, p.ID, now, maxConfirms)
	if err != nil {
		return index.Outcome{}, err
	}
	known, err := d.index.TrashEntries(ctx, p.ID)
	if err != nil {
		return index.Outcome{}, err
	}
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m.Entry] = true
	}
	changes := len(missing) > 0
	for entry := range known {
		changes = changes || (!present[entry] && !gone[entry])
	}
	for entry := range present {
		changes = changes || (!known[entry] && entryName.MatchString(entry))
	}
	if !changes {
		return index.Outcome{}, nil
	}
	if !d.ours(ctx) {
		return index.Outcome{}, errUntrusted
	}

	// Missing items of the trash: back only when their entry holds them again; dropped when, missing long enough, their
	// entry is gone or holds them no more.
	notBack := map[string]bool{}
	confirmed := map[int64]time.Time{}
	for _, m := range missing {
		at, ok := pathIn(top, m.Entry)
		if !ok {
			continue
		}
		if present[m.Entry] {
			// Back only when its entry holds it exactly as the Drive last knew it.
			sctx, cancel := context.WithTimeout(ctx, storageTimeout)
			inside, err := d.storage.List(sctx, at, 16)
			cancel()
			if err != nil {
				continue // nothing is decided
			}
			if slices.ContainsFunc(inside, func(e storage.Entry) bool {
				return e.Name == m.Name && index.Same(m, index.Seen{Folder: e.Folder, Size: e.Size, Changed: e.Changed})
			}) {
				continue
			}
			notBack[m.Entry] = true
			if !m.Since.After(old) {
				confirmed[m.ID] = m.Since
			}
		} else if !m.Since.After(old) && d.goneFrom(ctx, at) {
			confirmed[m.ID] = m.Since
		}
	}

	// Entries the index does not know: shown in the trash when each holds exactly one item it can use.
	var adopt []index.Adopted
	for entry := range present {
		if len(adopt) >= maxAdoptions {
			break
		}
		at, ok := pathIn(top, entry)
		if known[entry] || !ok || !entryName.MatchString(entry) || d.busyIn(at) {
			continue
		}
		day, err := time.Parse(entryDay, entry[:len(entryDay)])
		if err != nil {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		inside, err := d.storage.List(sctx, at, 1)
		cancel()
		if err != nil || len(inside) != 1 || CheckName(inside[0].Name) != nil {
			continue // empty, holding several things, or not to be read: left as it is, removed 30 days after its day
		}
		e := inside[0]
		adopt = append(adopt, index.Adopted{Entry: entry, Deleted: day,
			Item: index.Seen{Name: e.Name, Folder: e.Folder, Size: e.Size, Changed: e.Changed}})
	}

	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Outcome{}, err
	}
	defer leave()
	return d.index.ReconcileTrash(ctx, p.ID, index.TrashReread{
		Present: func(entry string) bool { return present[entry] && !notBack[entry] },
		Leave: func(entry string) bool {
			at, ok := pathIn(top, entry)
			return !ok || d.busyIn(at)
		},
		Stale:     stale,
		At:        now,
		Hide:      true,
		Old:       old,
		Confirmed: confirmed,
		Adopt:     adopt,
	})
}

// stripes holds one look at a folder at a time per folder, in a fixed number of locks that folders share (two folders
// sharing one only wait for each other).
type stripes [64]sync.Mutex

func (s *stripes) hold(k folderKey) (release func()) {
	h := uint64(k.place)*0x9e3779b97f4a7c15 + uint64(k.folder)
	h ^= h >> 29
	m := &s[h%uint64(len(s))]
	m.Lock()
	return m.Unlock
}

// The looks made in the background when folders are opened: at most maxRechecks at once, and at most maxQueued folders
// waiting for one; a folder opened again while it is being looked at gets one more look after it, never more.
const (
	maxRechecks = 4
	maxQueued   = 64
	// recheckTimeout bounds one look in the background.
	recheckTimeout = 2 * time.Minute
)

type rechecks struct {
	mu      sync.Mutex
	folders map[folderKey]*recheck
	queue   []folderKey
	running int
}

// A recheck is one folder being looked at in the background, or waiting to be.
type recheck struct {
	place index.Place
	again bool          // opened again meanwhile: one more look follows this one
	done  chan struct{} // closed when no look at the folder is under way or due
}

// Recheck has a folder of the place (nil: its top folder) looked at on the storage in the background, because a person
// opened it, and the index brought in step. checking says that a look is under way or due (Rechecked waits for it);
// false when too many are waiting already, and nothing was started.
func (d *Drive) Recheck(p index.Place, folder *index.Item) (checking bool) {
	return d.recheck(p, folderID(folder))
}

// RecheckTrash has the place's trash looked at on the storage in the background, as Recheck does for a folder.
func (d *Drive) RecheckTrash(p index.Place) bool { return d.recheck(p, trashFolder) }

func (d *Drive) recheck(p index.Place, id int64) bool {
	if d.background.Err() != nil {
		return false
	}
	key := folderKey{p.ID, id}
	r := &d.rechecks
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.folders[key]; c != nil {
		c.again = true
		return true
	}
	if len(r.queue) >= maxQueued {
		return false
	}
	if r.folders == nil {
		r.folders = map[folderKey]*recheck{}
	}
	r.folders[key] = &recheck{place: p, done: make(chan struct{})}
	r.queue = append(r.queue, key)
	if r.running < maxRechecks {
		r.running++
		d.working.Add(1)
		go d.rechecking()
	}
	return true
}

// rechecking looks at the folders in line, one after another, until none is left.
func (d *Drive) rechecking() {
	defer d.working.Done()
	r := &d.rechecks
	for {
		r.mu.Lock()
		if len(r.queue) == 0 || d.background.Err() != nil {
			r.running--
			if r.running == 0 {
				for _, c := range r.folders { // stopping: nobody waits for the rest
					close(c.done)
				}
				r.folders, r.queue = nil, nil
			}
			r.mu.Unlock()
			return
		}
		key := r.queue[0]
		r.queue = r.queue[1:]
		c := r.folders[key]
		c.again = false
		r.mu.Unlock()

		ctx, cancel := context.WithTimeout(d.background, recheckTimeout)
		var err error
		if key.folder == trashFolder {
			_, err = d.rereadTrash(ctx, c.place)
		} else {
			_, err = d.reread(ctx, c.place, key.folder)
		}
		cancel()
		d.lookFailed(c.place, key.folder, err)

		r.mu.Lock()
		if c.again && d.background.Err() == nil {
			r.queue = append(r.queue, key)
		} else if r.folders[key] == c {
			delete(r.folders, key)
			close(c.done)
		}
		r.mu.Unlock()
	}
}

// Rechecked waits until no look at the folder of the place (nil: its top folder) is under way or due, at most max:
// still is true when one still is.
func (d *Drive) Rechecked(ctx context.Context, p index.Place, folder *index.Item, max time.Duration) (still bool) {
	return d.rechecked(ctx, folderKey{p.ID, folderID(folder)}, max)
}

// TrashRechecked is Rechecked for the place's trash.
func (d *Drive) TrashRechecked(ctx context.Context, p index.Place, max time.Duration) bool {
	return d.rechecked(ctx, folderKey{p.ID, trashFolder}, max)
}

func (d *Drive) rechecked(ctx context.Context, key folderKey, max time.Duration) bool {
	d.rechecks.mu.Lock()
	c := d.rechecks.folders[key]
	d.rechecks.mu.Unlock()
	if c == nil {
		return false
	}
	t := time.NewTimer(max)
	defer t.Stop()
	select {
	case <-c.done:
		return false
	case <-ctx.Done():
	case <-t.C:
	}
	return true
}

// lookFailed logs what went wrong with a look at a folder in the background, now and then: a look that was out of date,
// or at something gone, is no failure.
func (d *Drive) lookFailed(p index.Place, id int64, err error) {
	switch {
	case err == nil, errors.Is(err, index.ErrChanged), errors.Is(err, index.ErrNoItem), errors.Is(err, ErrGone),
		errors.Is(err, ErrPlaceGone), errors.Is(err, context.Canceled), errors.Is(err, errUntrusted):
	case errors.Is(err, ErrFolderTooLarge):
		if d.logged.due(fmt.Sprintf("too many %d:%d", p.ID, id), time.Hour) {
			d.log.Warn("a folder on the storage holds more than the Drive reads at once; changes made in it on the storage directly are not seen",
				"place", p.ID, "folder", id, "most", MaxFolderEntries)
		}
	default:
		if d.logged.due("look failed", 10*time.Minute) {
			d.log.Warn("a folder could not be looked at on the storage; it is looked at again when it is next opened, and by the walk",
				"place", p.ID, "folder", id, "error", err)
		}
	}
}

// logged is when each kind of warning was last logged.
type logged struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due says whether a warning of that kind may be logged now: not within every of the last time.
func (l *logged) due(kind string, every time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	if t, ok := l.last[kind]; ok && time.Since(t) < every {
		return false
	}
	l.last[kind] = time.Now()
	return true
}

// StorageIDFile is the Drive's mark on the storage: a file holding the id of the storage, which the index keeps too.
// While it is there with that id, the storage is the one the index belongs to, and looks at it may hide and drop what is
// gone. Without it (a storage empty, mounted wrongly, someone else's, or put back to a day before the Drive first
// marked it), looks change nothing in the index, and the Drive logs why.
const StorageIDFile = HiddenFolder + "/storage-id"

// mark serialises the checks of the Drive's mark on the storage.
type mark struct{ mu sync.Mutex }

// ours says whether the storage holds the Drive's mark with the id its index keeps. It is asked afresh by every look
// that would change the index (most change nothing, and do not ask). The first time, with an index that keeps none, the
// Drive writes its mark (or takes the one there, when its index was made anew against a storage that holds one).
func (d *Drive) ours(ctx context.Context) bool {
	d.mark.mu.Lock()
	defer d.mark.mu.Unlock()
	ok, why, err := d.checkMark(ctx)
	if err != nil {
		if ctx.Err() == nil && d.logged.due("mark unread", 10*time.Minute) {
			d.log.Warn("the Drive's mark on the storage could not be read; changes made on the storage directly wait", "error", err)
		}
		return false
	}
	if !ok && d.logged.due("mark missing", 10*time.Minute) {
		d.log.Error("the storage does not hold the Drive's mark, so it may be empty, mounted wrongly, someone else's, or put back to before the Drive marked it: "+
			"nothing is added, hidden or dropped from the index until the mark is back (the file holds the id, on one line)", "file", StorageIDFile, "why", why)
	}
	return ok
}

// marked makes sure the storage holds the Drive's mark, as a place is made on it: the Drive's first change to a storage
// marks it as its own, before any look has to tell whether it still is. It is logged, not refused, when it cannot.
func (d *Drive) marked(ctx context.Context) {
	d.mark.mu.Lock()
	defer d.mark.mu.Unlock()
	if _, _, err := d.checkMark(ctx); err != nil && ctx.Err() == nil {
		d.log.Warn("the Drive could not mark the storage as its own; it tries again at the next look", "error", err)
	}
}

// checkMark reads the mark on the storage and compares it with the id the index keeps; why says what differs.
func (d *Drive) checkMark(ctx context.Context) (ok bool, why string, err error) {
	kept, haveKept, err := d.index.State(ctx, "storage_id")
	if err != nil {
		return false, "", err
	}
	there, err := d.readMark(ctx)
	switch {
	case err != nil:
		return false, "", err
	case haveKept && there == kept:
		return true, "", nil
	case haveKept && there == "":
		return false, "the file is not there (id " + kept + ")", nil
	case haveKept:
		return false, "the file holds the id of another Drive's index (id " + kept + ")", nil
	case there != "":
		kept, err = d.index.AddStorageID(ctx, there)
		return err == nil && kept == there, "", err
	}
	// The first time: this storage is the one the index is being built from.
	id := newStorageID()
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	if err := d.storage.MakeFolder(sctx, HiddenFolder); err != nil {
		return false, "", err
	}
	err = d.storage.Put(sctx, StorageIDFile, strings.NewReader(id+"\n"), int64(len(id)+1))
	if errors.Is(err, storage.ErrExists) {
		return false, "", errors.New("another mark was written meanwhile; it is read next time")
	}
	if err != nil {
		return false, "", err
	}
	kept, err = d.index.AddStorageID(ctx, id)
	if err == nil {
		d.log.Info("the Drive marked the storage as its own", "file", StorageIDFile, "id", id)
	}
	return err == nil && kept == id, "", err
}

// readMark reads the id in the Drive's mark on the storage: "" when there is none.
func (d *Drive) readMark(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	f, err := d.storage.Read(ctx, StorageIDFile, nil, "")
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Body.Close()
	data, err := io.ReadAll(io.LimitReader(f.Body, 256))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func newStorageID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
