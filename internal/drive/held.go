package drive

import (
	"context"
	"errors"
	"path"
	"sync"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// When a member is removed. A My files whose owner is no longer in the member list is held: the access package lets
// nobody do anything in it, so nothing shared from it opens (to members, guests or through links), and it is theirs again,
// as it was, when they come back with the same user id. Nothing changes on the storage or in the index for that. A project
// admin ends it with one of two acts, each on the held My files as a whole, opening nothing in it:
//
//   - a hand-over: its top folder moves (one MOVE) into the top folder of another member's My files, as a folder named
//     after it (the removed member's address when they first opened the Drive, with " (2)" and so on when that is
//     taken: never merged into a folder that is there). Its items keep their ids in the index, and nothing given on them
//     comes along: no grant, no public link, no guest's invitation, no upload in progress. Its trash is not handed over:
//     it is removed.
//   - a deletion: its top folder and its trash are removed from the storage for good, and it goes from the index with
//     everything in it.
//
// Each act is recorded (index.Act) before anything changes, and done in steps, the storage's first and the index's
// last, each of which can be done again: if the Drive stops midway, it finishes the act when it starts again (Tidy), and
// meanwhile the held My files stays held whatever the member list says. While an act is under way, the storage paths it
// changes are claimed (in memory, again from the record when the Drive starts, before anything else looks), so no look
// at the storage takes the moved folder for one put there directly, or the held one for one emptied.

// Errors about acts on a held My files.
var (
	// ErrNotHeld: its owner is in the member list again, so the files are theirs.
	ErrNotHeld = errors.New("the owner of these files is a member again")
	// ErrActing: another act is under way on it (or on the My files it was to go to).
	ErrActing = errors.New("these files are being handed over or deleted already")
)

// actTimeout bounds one storage step of an act: removing a large folder from a WebDAV server can take minutes.
const actTimeout = 10 * time.Minute

// acts holds the storage paths claimed for each act under way, by its id, until it is done.
type acts struct {
	mu     sync.Mutex
	claims map[int64]*actClaims
}

// actClaims releases the claims of one act: on the held top folder, and, for a hand-over, on the folder it becomes.
type actClaims struct{ folder, dest func() }

func (c *actClaims) release() {
	if c.folder != nil {
		c.folder()
	}
	if c.dest != nil {
		c.dest()
	}
}

// HeldMyFiles is a held My files as a project admin sees it: whose it was, and how much is in it.
type HeldMyFiles struct {
	index.Place
	// Files and Size are how many files, and how many bytes, are in it outside its trash; Used is all it takes, its trash
	// included.
	Files, Size, Used int64
	// Act is the act under way on it, if any.
	Act *index.Act
}

// Held returns every held My files: those whose owner is not in the member list, and those an act is under way on.
func (d *Drive) Held(ctx context.Context, list *members.List) ([]HeldMyFiles, error) {
	places, err := d.index.Places(ctx)
	if err != nil {
		return nil, err
	}
	used, err := d.index.UsedByPlace(ctx)
	if err != nil {
		return nil, err
	}
	var out []HeldMyFiles
	for _, p := range places {
		if p.Kind != index.MyFiles {
			continue
		}
		if _, stays := list.Lookup(p.Owner); stays && p.Acting == "" {
			continue
		}
		h := HeldMyFiles{Place: p, Used: used[p.ID]}
		if h.Files, h.Size, err = d.index.Contents(ctx, p.ID); err != nil {
			return nil, err
		}
		if p.Acting != "" {
			a, err := d.index.ActOn(ctx, p.ID)
			if err != nil && !errors.Is(err, index.ErrNoAct) {
				return nil, err
			}
			if err == nil {
				h.Act = &a
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// Acts returns every hand-over and deletion of a held My files, the most recent first: the record.
func (d *Drive) Acts(ctx context.Context) ([]index.Act, error) {
	return d.index.Acts(ctx)
}

// HandOver hands the held My files p, as a whole, to the member to, for the project admin by: its top folder becomes a
// folder of their My files (made for them if they have none yet), without anything given on it or in it, and its trash
// is removed. held is asked once the place is locked: whether its owner is still out of the member list (ErrNotHeld
// otherwise). It returns the act once it is done, or, when it is not done within wait (or its storage steps failed, err
// then says why), as it is under way: the Drive goes on with it in the background, and after a restart.
func (d *Drive) HandOver(ctx context.Context, p index.Place, to members.Member, by string, held func() bool, wait time.Duration) (index.Act, error) {
	if p.Kind != index.MyFiles {
		return index.Act{}, errors.New("only a My files is handed over")
	}
	mine, err := d.MyFiles(ctx, to)
	if err != nil {
		return index.Act{}, err
	}
	if mine.ID == p.ID {
		return index.Act{}, ErrNotHeld
	}
	return d.act(ctx, p, index.Act{Kind: index.HandOver, ToMember: to.UserID, ToPlace: mine.ID, By: by}, held, wait)
}

// DeleteHeld deletes the held My files p for good, its trash included, for the project admin by, as HandOver hands it
// over.
func (d *Drive) DeleteHeld(ctx context.Context, p index.Place, by string, held func() bool, wait time.Duration) (index.Act, error) {
	if p.Kind != index.MyFiles {
		return index.Act{}, errors.New("only a My files is deleted this way")
	}
	return d.act(ctx, p, index.Act{Kind: index.DeleteHeld, By: by}, held, wait)
}

// act begins the act want on the held My files p, or goes on with the same act left under way on it, and does it in
// the background, waiting for it at most wait.
func (d *Drive) act(ctx context.Context, p index.Place, want index.Act, held func() bool, wait time.Duration) (index.Act, error) {
	// Alone in the place: changes inside it that began before its owner left end first (ErrBusy when they do not).
	release, err := d.wholePlace(ctx, p.ID)
	if err != nil {
		return index.Act{}, err
	}
	handed := false
	defer func() {
		if !handed {
			release()
		}
	}()
	if p, err = d.index.PlaceByID(ctx, p.ID); errors.Is(err, index.ErrNoPlace) {
		return index.Act{}, ErrPlaceGone
	} else if err != nil {
		return index.Act{}, err
	}
	a, err := d.index.ActOn(ctx, p.ID)
	switch {
	case err == nil:
		// The same act, left under way: it goes on. Another one: refused.
		if a.Kind != want.Kind || a.ToMember != want.ToMember {
			return a, ErrActing
		}
	case errors.Is(err, index.ErrNoAct):
		if a, err = d.startAct(ctx, p, want, held); err != nil {
			return a, err
		}
	default:
		return index.Act{}, err
	}
	if err := d.actClaimed(ctx, a); err != nil {
		return a, err
	}

	done := make(chan error, 1)
	handed = true
	d.working.Add(1)
	go func() {
		defer d.working.Done()
		defer release()
		done <- d.finish(d.background, a)
	}()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case err := <-done:
		if err != nil {
			return a, err
		}
		return d.index.Act(ctx, a.ID)
	case <-t.C:
	case <-ctx.Done():
	}
	return a, nil
}

// startAct records a new act on the held My files p, which the caller holds alone, with the storage paths it changes
// claimed: for a hand-over, its top folder and the folder it becomes in the receiver's top folder (a name free there on
// the storage); for a deletion, its top folder.
func (d *Drive) startAct(ctx context.Context, p index.Place, want index.Act, stillHeld func() bool) (index.Act, error) {
	if !stillHeld() {
		return index.Act{}, ErrNotHeld
	}
	claims := &actClaims{}
	var ok bool
	if claims.folder, ok = d.claim(p.Folder); !ok {
		return index.Act{}, ErrBusy
	}
	if want.Kind == index.HandOver {
		to, err := d.index.PlaceByID(ctx, want.ToPlace)
		if err != nil {
			claims.release()
			return index.Act{}, err
		}
		if want.ToName, claims.dest, err = d.freeTopName(ctx, to, path.Base(p.Folder)); err != nil {
			claims.release()
			return index.Act{}, err
		}
	}
	want.Place, want.At = p.ID, d.now()
	a, err := d.index.StartAct(ctx, want)
	if errors.Is(err, index.ErrActing) {
		err = ErrActing
	}
	if err != nil {
		claims.release()
		return index.Act{}, err
	}
	d.holding(a.ID, claims)
	d.log.Info("a project admin began an act on the files of a member who left", "act", a.ID, "kind", a.Kind, "owner", a.Owner,
		"to", a.ToMember, "by", a.By, "files", a.Files, "size", a.Size)
	return a, nil
}

// freeTopName is name, or name with " (2)" and so on, free in the top folder of the place to, claimed until release is
// called: nothing of that name on the storage, nor in the index (not even an item missing from the storage, which could
// still come back), so that nothing the receiver has is ever taken over or merged into.
func (d *Drive) freeTopName(ctx context.Context, to index.Place, name string) (free string, release func(), err error) {
	dir, ok := pathIn(to.Folder)
	if !ok {
		return "", nil, ErrGone
	}
	if err := d.ensureTop(ctx, to, nil); err != nil {
		return "", nil, err
	}
	for n := 1; n < 1000; n++ {
		candidate := name
		if n > 1 {
			candidate = numbered(name, n, true)
		}
		at, ok := pathIn(dir, candidate)
		if !ok {
			return "", nil, errors.New("a My files' top folder has no name") // the Drive named it
		}
		if used, err := d.index.NameUsed(ctx, to.ID, 0, candidate); err != nil || used {
			if err != nil {
				return "", nil, err
			}
			continue
		}
		release, ok := d.claim(at)
		if !ok {
			continue // being uploaded, made or moved there right now
		}
		there, err := d.there(ctx, at)
		if err == nil && !there {
			return candidate, release, nil
		}
		release()
		if err != nil {
			return "", nil, err
		}
	}
	return "", nil, &Taken{Name: name, Folder: true}
}

// there says whether the storage has something at p.
func (d *Drive) there(ctx context.Context, p string) (bool, error) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	_, err := d.storage.Stat(sctx, p)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, storage.ErrNotFound):
		return false, nil
	}
	return false, storageError(err)
}

// holding keeps the claims of an act under way until it is done.
func (d *Drive) holding(id int64, c *actClaims) {
	d.acts.mu.Lock()
	defer d.acts.mu.Unlock()
	if d.acts.claims == nil {
		d.acts.claims = map[int64]*actClaims{}
	}
	if old := d.acts.claims[id]; old != nil {
		old.release()
	}
	d.acts.claims[id] = c
}

// holds says whether the act's claims are held.
func (d *Drive) holds(id int64) bool {
	d.acts.mu.Lock()
	defer d.acts.mu.Unlock()
	return d.acts.claims[id] != nil
}

// letGo releases the claims of an act that is done.
func (d *Drive) letGo(id int64) {
	d.acts.mu.Lock()
	c := d.acts.claims[id]
	delete(d.acts.claims, id)
	d.acts.mu.Unlock()
	if c != nil {
		c.release()
	}
}

// newDest puts the claim on the folder a hand-over becomes in place of the one it had.
func (d *Drive) newDest(id int64, dest func()) {
	d.acts.mu.Lock()
	defer d.acts.mu.Unlock()
	c := d.acts.claims[id]
	if c == nil {
		c = &actClaims{}
		if d.acts.claims == nil {
			d.acts.claims = map[int64]*actClaims{}
		}
		d.acts.claims[id] = c
	}
	if c.dest != nil {
		c.dest()
	}
	c.dest = dest
}

// actClaimed makes sure the storage paths an act under way changes are claimed, as startAct claims them: when the Drive
// starts (HoldActs), before anything looks at the storage, and before it goes on with an act.
func (d *Drive) actClaimed(ctx context.Context, a index.Act) error {
	if d.holds(a.ID) {
		return nil
	}
	c := &actClaims{}
	var ok bool
	if c.folder, ok = d.claim(a.Folder); !ok {
		return ErrBusy
	}
	if a.Kind == index.HandOver {
		to, err := d.index.PlaceByID(ctx, a.ToPlace)
		if err != nil {
			c.release()
			return err
		}
		dest, ok := pathIn(to.Folder, a.ToName)
		if !ok {
			c.release()
			return errors.New("a hand-over's folder cannot be named")
		}
		if c.dest, ok = d.claim(dest); !ok {
			c.release()
			return ErrBusy
		}
	}
	d.holding(a.ID, c)
	return nil
}

// HoldActs claims the storage paths of every act under way, left by a Drive that stopped midway. Call it when the Drive
// starts, before anything looks at the storage: Tidy then finishes them.
func (d *Drive) HoldActs(ctx context.Context) error {
	under, err := d.index.ActsUnderWay(ctx)
	if err != nil {
		return err
	}
	for _, a := range under {
		if err := d.actClaimed(ctx, a); err != nil {
			d.log.Error("an act on the files of a member who left could not be held; it is tried again within the hour", "act", a.ID, "error", err)
		}
	}
	return nil
}

// finishActs finishes every act under way, one at a time: those left by a Drive that stopped midway, or whose storage
// steps failed. One whose place is busy is left for the next run.
func (d *Drive) finishActs(ctx context.Context) error {
	under, err := d.index.ActsUnderWay(ctx)
	if err != nil {
		return err
	}
	var failed error
	for _, a := range under {
		release, err := d.wholePlace(ctx, a.Place)
		if err != nil {
			failed = err
			continue
		}
		if a, err = d.index.Act(ctx, a.ID); err == nil && a.Done.IsZero() {
			if err = d.actClaimed(ctx, a); err == nil {
				d.log.Info("an act on the files of a member who left goes on", "act", a.ID, "kind", a.Kind)
				err = d.finish(ctx, a)
			}
		}
		release()
		if err != nil {
			failed = err
		}
	}
	return failed
}

// finish does the steps of an act under way that are left, the storage's first, then the index's, and logs it. Each
// step can be done again: a step done before the Drive stopped is seen done on the storage. The caller holds the held
// My files alone.
func (d *Drive) finish(ctx context.Context, a index.Act) error {
	var err error
	switch {
	case !d.ours(ctx):
		// Only on the Drive's own storage: one that is empty or mounted wrongly would show the held files gone.
		err = errUntrusted
	case a.Kind == index.HandOver:
		err = d.finishHandOver(ctx, a)
	case a.Kind == index.DeleteHeld:
		err = d.finishDelete(ctx, a)
	default:
		err = errors.New("an act of an unknown kind")
	}
	if err != nil {
		if ctx.Err() == nil {
			d.log.Error("an act on the files of a member who left could not be finished; the Drive goes on with it within the hour",
				"act", a.ID, "kind", a.Kind, "error", err)
		}
		return err
	}
	d.letGo(a.ID)
	if a.Kind == index.HandOver {
		d.log.Info("the files of a member who left were handed over", "act", a.ID, "owner", a.Owner, "to", a.ToMember, "by", a.By,
			"files", a.Files, "size", a.Size)
	} else {
		d.log.Info("the files of a member who left were deleted", "act", a.ID, "owner", a.Owner, "by", a.By, "files", a.Files, "size", a.Size)
	}
	return nil
}

// finishHandOver moves the held top folder into the receiver's top folder (unless it is there already), removes the held
// My files' trash, then records it in the index.
func (d *Drive) finishHandOver(ctx context.Context, a index.Act) error {
	to, err := d.index.PlaceByID(ctx, a.ToPlace)
	if err != nil {
		return err
	}
	src := a.Folder
	var dest string
	for tries := 0; ; tries++ {
		if tries == 5 {
			return errors.New("the storage changed under the hand-over five times")
		}
		var ok bool
		if dest, ok = pathIn(to.Folder, a.ToName); !ok {
			return errors.New("a hand-over's folder cannot be named")
		}
		srcThere, err := d.there(ctx, src)
		if err != nil {
			return err
		}
		destThere, err := d.there(ctx, dest)
		if err != nil {
			return err
		}
		switch {
		case !srcThere && destThere:
			// Moved before the Drive stopped.
		case !srcThere:
			// The held top folder had gone from the storage: an empty folder stands for it, and the looks find what the index
			// knows of it gone.
			if err := d.ensureTop(ctx, to, nil); err != nil {
				return err
			}
			sctx, cancel := context.WithTimeout(ctx, storageTimeout)
			err := d.storage.CreateFolder(sctx, dest)
			cancel()
			if err != nil && !errors.Is(err, storage.ErrExists) {
				return storageError(err)
			}
		case destThere:
			// Something took the name on the storage since it was chosen (put there directly): another name.
			if err := d.renameAct(ctx, &a, to); err != nil {
				return err
			}
			continue
		default:
			if err := d.ensureTop(ctx, to, nil); err != nil {
				return err
			}
			err := d.moveWithin(ctx, actTimeout, true, src, dest)
			switch {
			case errors.Is(err, storage.ErrExists), errors.Is(err, storage.ErrNotFound):
				continue // the name was taken, or the held folder went, meanwhile: looked at again
			case err != nil:
				return storageError(err)
			}
			d.stopAt("hand-over moved")
		}
		break
	}
	// Its trash is not handed over.
	if err := d.removeWithin(ctx, trashTop(index.Place{Folder: src})); err != nil {
		return err
	}
	d.stopAt("hand-over trash removed")
	changed := d.now()
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	if e, err := d.storage.Stat(sctx, dest); err == nil && !e.Changed.IsZero() {
		changed = e.Changed
	}
	cancel()
	leave, err := d.inPlace(ctx, to)
	if err != nil {
		return err
	}
	defer leave()
	d.changed(folderKey{to.ID, 0}, folderKey{a.Place, 0}, folderKey{a.Place, trashFolder})
	return d.index.HandedOver(ctx, a.ID, changed, d.now())
}

// renameAct gives a hand-over under way another free name in the receiver's top folder, and claims it in place of the
// one it had.
func (d *Drive) renameAct(ctx context.Context, a *index.Act, to index.Place) error {
	name, release, err := d.freeTopName(ctx, to, path.Base(a.Folder))
	if err != nil {
		return err
	}
	if err := d.index.RenameAct(ctx, a.ID, name); err != nil {
		release()
		return err
	}
	d.newDest(a.ID, release)
	a.ToName = name
	return nil
}

// finishDelete removes the held My files' trash and its top folder from the storage (unless they are gone already),
// then records it in the index.
func (d *Drive) finishDelete(ctx context.Context, a index.Act) error {
	if err := d.removeWithin(ctx, trashTop(index.Place{Folder: a.Folder})); err != nil {
		return err
	}
	if err := d.removeWithin(ctx, a.Folder); err != nil {
		return err
	}
	d.stopAt("delete removed")
	d.changed(folderKey{a.Place, 0}, folderKey{a.Place, trashFolder})
	return d.index.DeletedHeld(ctx, a.ID, d.now())
}

// removeWithin removes a folder from the storage, with everything in it, given actTimeout: nothing there is no error.
func (d *Drive) removeWithin(ctx context.Context, dir string) error {
	sctx, cancel := context.WithTimeout(ctx, actTimeout)
	defer cancel()
	err := d.storage.DeleteFolder(sctx, dir)
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return storageError(err)
}
