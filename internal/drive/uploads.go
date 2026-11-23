package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// UploadsFolder holds every upload while its bytes arrive: one file per upload, named after a random id. Nothing is
// written into a place's folders until an upload is whole; then it is moved into place, so a half-finished file never
// shows in a folder.
//
// A small file arrives in one request, as one PUT (which writes the whole file or nothing) and is moved into place at
// once. A large one is a resumable upload: it is started, its parts arrive one request each (written into its file
// from where the file ends, with PUT and Content-Range), and it is finished when the whole file is there. An upload cut
// off midway goes on from where its file ends. Where the storage cannot write into part of a file, a resumable upload
// is sent as one part, and starts again from the beginning when cut off.
const UploadsFolder = HiddenFolder + "/uploads"

// PartSize is the most one part of a resumable upload may hold, when the storage can resume. Each part is one request,
// and one transfer: with parts of this size, the transfer slots go round, and who may upload is decided again often.
const PartSize = 32 << 20

// AbandonedAfter is how long an upload in progress is kept with no part arriving: then it is removed, with its file. A
// day covers a connection that dropped, a laptop shut overnight or a router restarted, and nobody sees or counts the
// space an unfinished upload takes, so it is not kept longer.
const AbandonedAfter = 24 * time.Hour

// maxLeftOvers is the most files left in the uploads folder (by an upload the Drive did not finish, or put there
// directly) one run removes.
const maxLeftOvers = 1000

// moveIntoPlace bounds moving a whole upload into its folder. A Storage Box renames, at once; another WebDAV server may
// copy the bytes.
const moveIntoPlace = 10 * time.Minute

// Errors about uploads in progress.
var (
	// ErrRestart: the storage turned out not to keep a part written into the middle of a file. The upload's file was
	// removed; it starts again from the beginning, as one part.
	ErrRestart = errors.New("the upload must start again from the beginning")
	// ErrBadPart: a part that does not fit the file, or is larger than a part may be.
	ErrBadPart = errors.New("the part does not fit the file")
)

// A WrongOffset error says that a part was sent for another place in the file than where the file now ends: Offset.
type WrongOffset struct{ Offset int64 }

func (w *WrongOffset) Error() string {
	return fmt.Sprintf("the file on the storage ends at %d", w.Offset)
}

// An Incomplete error says that an upload was to be finished before the whole file arrived: Offset bytes have.
type Incomplete struct{ Offset int64 }

func (i *Incomplete) Error() string {
	return fmt.Sprintf("only %d bytes of the file have arrived", i.Offset)
}

// uploadID is the form of an upload's id: 16 random bytes, in hex.
var uploadID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func newUploadID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// partialOf is where an upload's bytes are on the storage while they arrive.
func partialOf(id string) string { return UploadsFolder + "/" + id }

// makeUploadsFolder makes the uploads folder, and the folders above it, if it is missing.
func (d *Drive) makeUploadsFolder(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	err := d.storage.CreateFolder(ctx, UploadsFolder)
	switch {
	case err == nil || errors.Is(err, storage.ErrExists):
		return nil
	case errors.Is(err, storage.ErrNotFound):
		return storageError(d.storage.MakeFolder(ctx, UploadsFolder))
	}
	return storageError(err)
}

// resumeState is what the Drive knows about whether the storage can write a part into the middle of a file.
type resumeState struct {
	known, can bool
}

// CanResume says whether the storage can write a part of an upload into its file from where the file ends (PUT with
// Content-Range), so an upload cut off goes on from there. The first call finds out, in the uploads folder, the way
// `superit-drive check-storage` does; the answer is kept until the Drive stops.
func (d *Drive) CanResume(ctx context.Context) (bool, error) {
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()
	if d.resume.known {
		return d.resume.can, nil
	}
	if err := d.makeUploadsFolder(ctx); err != nil {
		return false, err
	}
	why, err := d.tryResume(ctx)
	if err != nil {
		return false, storageError(err)
	}
	d.resume = resumeState{known: true, can: why == ""}
	if why != "" {
		d.log.Warn("the storage cannot write part of a file (PUT with Content-Range): an upload cut off starts again from the beginning", "why", why)
	}
	return d.resume.can, nil
}

// cannotResume records that the storage turned out not to keep a part written into the middle of a file.
func (d *Drive) cannotResume(why string) {
	d.resumeMu.Lock()
	defer d.resumeMu.Unlock()
	if d.resume.known && !d.resume.can {
		return
	}
	d.resume = resumeState{known: true, can: false}
	d.log.Warn("the storage cannot write part of a file (PUT with Content-Range): an upload cut off starts again from the beginning", "why", why)
}

// tryResume writes a file in three steps, as a resumable upload does (empty, then two parts, each from where the file
// ends), reads it back, and removes it. It says why the storage cannot resume, or "" when it can; an error when the
// storage did not answer.
func (d *Drive) tryResume(ctx context.Context) (why string, err error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	probe := partialOf(newUploadID())
	content := make([]byte, 64)
	_, _ = rand.Read(content)
	if err := d.storage.Put(ctx, probe, nil, 0); err != nil {
		return "", err
	}
	defer func() { _ = d.storage.Delete(context.WithoutCancel(ctx), probe) }()
	total := int64(len(content))
	for _, half := range [][2]int64{{0, total / 2}, {total / 2, total}} {
		err := d.storage.PutPart(ctx, probe, half[0], bytes.NewReader(content[half[0]:half[1]]), half[1]-half[0], total)
		var status *storage.StatusError
		if errors.As(err, &status) {
			return "it refused to write part of a file: " + status.Error(), nil
		}
		if err != nil {
			return "", err
		}
	}
	f, err := d.storage.Read(ctx, probe, nil, "")
	if err != nil {
		return "", err
	}
	defer f.Body.Close()
	got, err := io.ReadAll(io.LimitReader(f.Body, total+1))
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, content) {
		return "it wrote the parts of a file somewhere else than asked (it ignores Content-Range)", nil
	}
	return "", nil
}

// StartUpload starts a resumable upload of a file called name, size bytes long, into a folder of the place (nil: its
// top folder), by by (a member or a guest), and returns it. Nothing is put into the folder until it is finished.
//
// The name is looked at now, so that a person is asked "replace or keep both" before the file travels: when it is
// taken and existing does not say what to do, it is a *Taken error. It is looked at again when the upload is finished.
func (d *Drive) StartUpload(ctx context.Context, p index.Place, folder *index.Item, name string, size int64, existing Existing, by By) (index.Upload, error) {
	if err := by.checkName(name); err != nil {
		return index.Upload{}, err
	}
	if size <= 0 {
		return index.Upload{}, ErrBadPart
	}
	dir, ok := folderPath(p, folder)
	if !ok {
		return index.Upload{}, ErrGone
	}
	file, ok := pathIn(dir, name)
	if !ok {
		return index.Upload{}, &BadName{nameUnusable}
	}
	if err := d.askFirst(ctx, file, name, existing); err != nil {
		return index.Upload{}, err
	}
	resumable, err := d.CanResume(ctx)
	if err != nil {
		return index.Upload{}, err
	}
	if err := d.makeUploadsFolder(ctx); err != nil {
		return index.Upload{}, err
	}
	now := d.now()
	u := index.Upload{ID: newUploadID(), PlaceID: p.ID, FolderID: folderID(folder), Member: by.Member, Guest: by.Guest.ID,
		Name: name, Size: size, Started: now, Touched: now}
	if resumable {
		// An empty file to write the parts into, each from where it ends. (A part written into a file that is there
		// keeps what arrived when it is cut off; a new file cut off is not kept.)
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		err := d.storage.Put(sctx, partialOf(u.ID), nil, 0)
		cancel()
		if errors.Is(err, storage.ErrFull) {
			return index.Upload{}, ErrFull
		}
		if err != nil {
			return index.Upload{}, storageError(err)
		}
	}
	if err := d.index.AddUpload(ctx, u); err != nil {
		_ = d.storage.Delete(context.WithoutCancel(ctx), partialOf(u.ID))
		return index.Upload{}, err
	}
	return u, nil
}

// askFirst looks whether the name of the file at file is taken, before an upload of it sends anything: a *Taken error
// when it is and existing does not say what to do, or says to replace a folder.
func (d *Drive) askFirst(ctx context.Context, file, name string, existing Existing) error {
	if existing == KeepBoth {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	there, err := d.storage.Stat(sctx, file)
	cancel()
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return nil
	case err != nil:
		return d.nameError(err, name)
	case existing == Replace && !there.Folder:
		return nil
	}
	return &Taken{Name: name, Folder: there.Folder}
}

// UploadOf finds the upload in progress with id in the place, if by (a member or a guest) started it: ErrNoUpload
// otherwise.
func (d *Drive) UploadOf(ctx context.Context, p index.Place, id string, by By) (index.Upload, error) {
	if !uploadID.MatchString(id) {
		return index.Upload{}, index.ErrNoUpload
	}
	return d.index.UploadOf(ctx, p.ID, id, by.Member, by.Guest.ID)
}

// UploadOffset is how much of the upload has arrived: the length of its file on the storage. Where the storage cannot
// resume, a file cut off midway is of no use: it is removed, and the upload starts again from 0.
func (d *Drive) UploadOffset(ctx context.Context, u index.Upload) (int64, error) {
	resumable, err := d.CanResume(ctx)
	if err != nil {
		return 0, err
	}
	partial := partialOf(u.ID)
	release, ok := d.claim(partial)
	if !ok {
		// A part is being written right now: what has arrived so far.
		n, _, err := d.length(ctx, partial)
		return n, err
	}
	defer release()
	n, there, err := d.length(ctx, partial)
	if err != nil || resumable || !there || n == 0 || n >= u.Size {
		return n, err
	}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	if err := d.storage.Delete(sctx, partial); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return 0, storageError(err)
	}
	return 0, nil
}

// length is the length of the file at p on the storage; there is false when there is none.
func (d *Drive) length(ctx context.Context, p string) (n int64, there bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	e, err := d.storage.Stat(ctx, p)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return 0, false, nil
	case err != nil:
		return 0, false, storageError(err)
	case e.Folder:
		return 0, false, fmt.Errorf("%w: %s is a folder", ErrStorage, p)
	}
	return e.Size, true, nil
}

// WritePart writes n bytes of the upload, read from body, into its file from offset, which must be where the file now
// ends (a *WrongOffset error says where that is), and returns where it ends after. Where the storage can resume, a part
// holds at most PartSize bytes; where it cannot, the one part is the whole file, from 0.
//
// When the person's connection ends midway, it is ErrCutOff: what arrived is kept where the storage can resume, and
// the upload goes on from there. Another part of the same upload still being written is ErrBusy.
func (d *Drive) WritePart(ctx context.Context, u index.Upload, offset int64, body io.Reader, n int64) (int64, error) {
	resumable, err := d.CanResume(ctx)
	if err != nil {
		return 0, err
	}
	if n <= 0 || offset < 0 || offset+n > u.Size || (resumable && n > PartSize) || (!resumable && (offset != 0 || n != u.Size)) {
		return 0, ErrBadPart
	}
	partial := partialOf(u.ID)
	release, ok := d.claim(partial)
	if !ok {
		return 0, ErrBusy
	}
	defer release()
	at, there, err := d.length(ctx, partial)
	if err != nil {
		return 0, err
	}
	if offset != at {
		return 0, &WrongOffset{Offset: at}
	}

	from := &bodyReader{r: body}
	switch {
	case !resumable:
		if there { // empty: a cut-off whole file was removed, or never written
			sctx, cancel := context.WithTimeout(ctx, storageTimeout)
			err = d.storage.Delete(sctx, partial)
			cancel()
			if err != nil && !errors.Is(err, storage.ErrNotFound) {
				return 0, storageError(err)
			}
		}
		err = d.storage.Put(ctx, partial, from, n)
	case !there:
		// Its file is gone (someone removed it on the storage), so offset is 0: it starts again, into a new empty one.
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		err = d.storage.Put(sctx, partial, nil, 0)
		cancel()
		if err == nil {
			err = d.storage.PutPart(ctx, partial, offset, from, n, u.Size)
		}
	default:
		err = d.storage.PutPart(ctx, partial, offset, from, n, u.Size)
	}
	if err := d.index.TouchUpload(ctx, u.ID, d.now()); err != nil {
		return 0, err
	}
	switch {
	case from.err != nil:
		return 0, fmt.Errorf("%w: %w", ErrCutOff, from.err)
	case errors.Is(err, storage.ErrFull):
		return 0, ErrFull
	case err != nil:
		return 0, storageError(err)
	}
	end, _, err := d.length(ctx, partial)
	if err != nil {
		return 0, err
	}
	if end != offset+n {
		// The storage said it wrote the part, but the file does not end after it: it ignored where the part goes.
		d.cannotResume(fmt.Sprintf("after %d bytes were written from %d, the file was %d bytes long", n, offset, end))
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		defer cancel()
		if err := d.storage.Delete(sctx, partial); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return 0, storageError(err)
		}
		return 0, ErrRestart
	}
	return end, nil
}

// FinishUpload moves a whole upload into its folder (nil: the place's top folder) under its name, as an upload by by,
// and forgets it. When the name is taken, existing says what to do, as for any upload (a *Taken error when
// it does not say); the upload stays, to be finished again. Before the whole file has arrived it is an *Incomplete
// error.
func (d *Drive) FinishUpload(ctx context.Context, p index.Place, folder *index.Item, u index.Upload, existing Existing, by By) (Upload, error) {
	dir, ok := folderPath(p, folder)
	if !ok {
		return Upload{}, ErrGone
	}
	file, ok := pathIn(dir, u.Name)
	if !ok {
		return Upload{}, &BadName{nameUnusable}
	}
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return Upload{}, err
	}
	defer leave()
	partial := partialOf(u.ID)
	release, ok := d.claim(partial, file)
	if !ok {
		return Upload{}, ErrBusy
	}
	defer release()
	n, _, err := d.length(ctx, partial)
	if err != nil {
		return Upload{}, err
	}
	if n != u.Size {
		return Upload{}, &Incomplete{Offset: n}
	}
	up, err := d.settle(ctx, p, folder, dir, u.Name, existing, partial, by)
	if err != nil {
		return Upload{}, err
	}
	if err := d.index.RemoveUpload(ctx, u.ID); err != nil {
		d.log.Error("a finished upload is still in the index; it is removed within a day", "upload", u.ID, "error", err)
	}
	return up, nil
}

// CancelUpload removes an upload in progress, with what of it has arrived.
func (d *Drive) CancelUpload(ctx context.Context, u index.Upload) error {
	return d.dropUpload(ctx, u, true)
}

// dropUpload removes an upload's file and forgets it. With wait false, an upload a part is being written to right now
// is left alone (ErrBusy).
func (d *Drive) dropUpload(ctx context.Context, u index.Upload, wait bool) error {
	partial := partialOf(u.ID)
	release, ok := d.claim(partial)
	for !ok && wait {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		release, ok = d.claim(partial)
	}
	if !ok {
		return ErrBusy
	}
	defer release()
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err := d.storage.Delete(sctx, partial)
	cancel()
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return storageError(err)
	}
	return d.index.RemoveUpload(ctx, u.ID)
}

// settle moves the whole file at partial, in the uploads folder, into a folder of the place (nil: its top folder; dir
// its path on the storage) under name, as an upload by by (labelled with a guest's address), and records it in the
// index. The caller holds
// the place (inPlace) and the claim on the name in dir.
//
// When the name is taken, existing says what to do: replace a file there (it goes to the trash, and comes back if the
// upload cannot be moved into its place), or keep both, under a new name. Otherwise it is a *Taken error, and nothing
// moved.
func (d *Drive) settle(ctx context.Context, p index.Place, folder *index.Item, dir, name string, existing Existing, partial string, by By) (Upload, error) {
	file, _ := pathIn(dir, name)
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	there, err := d.storage.Stat(sctx, file)
	cancel()
	var replacing bool
	switch {
	case errors.Is(err, storage.ErrNotFound):
	case err != nil:
		return Upload{}, d.nameError(err, name)
	case existing == Replace && !there.Folder:
		replacing = true
	case existing == KeepBoth:
		var releaseOther func()
		name, releaseOther, err = d.freeName(ctx, dir, name, false)
		if err != nil {
			return Upload{}, err
		}
		defer releaseOther()
		file, _ = pathIn(dir, name)
	default:
		return Upload{}, &Taken{Name: name, Folder: there.Folder}
	}

	var out Upload
	if replacing {
		// The file there goes to the trash, and the upload takes its place; if it cannot, the file comes back.
		replaced, undo, err := d.putAside(ctx, p, folder, file, there, by.Member)
		if err != nil {
			return Upload{}, err
		}
		if replaced != nil {
			out.Replaced = replaced
			defer func() {
				if out.Item.ID == 0 {
					undo()
				}
			}()
		}
	}

	it, err := d.putInPlace(ctx, p, folder, partial, file, name, by)
	if err != nil {
		return Upload{}, err
	}
	out.Item = it
	return out, nil
}

// putInPlace moves the whole file at partial, in the uploads folder, to file, called name, in a folder of the place
// (nil: its top folder), never over anything there (a *Taken error when something is: put there directly since the Drive
// looked), and records it in the index as added by by. The caller holds the place (inPlace) and the claim on file.
func (d *Drive) putInPlace(ctx context.Context, p index.Place, folder *index.Item, partial, file, name string, by By) (index.Item, error) {
	err := d.moveWithin(ctx, moveIntoPlace, false, partial, file)
	if errors.Is(err, storage.ErrNotFound) && folder == nil {
		// The place's top folder is gone from the storage (someone removed it): it is made again.
		if err = d.ensureTop(ctx, p, nil); err == nil {
			err = d.moveWithin(ctx, moveIntoPlace, false, partial, file)
		}
	}
	switch {
	case errors.Is(err, storage.ErrExists):
		return index.Item{}, &Taken{Name: name} // put there directly since the Drive looked
	case errors.Is(err, storage.ErrNotFound):
		if _, there, serr := d.length(ctx, partial); serr == nil && !there {
			return index.Item{}, fmt.Errorf("%w: the upload's file is gone from the uploads folder", ErrStorage)
		}
		return index.Item{}, ErrGone
	case errors.Is(err, storage.ErrFull):
		return index.Item{}, ErrFull
	case err != nil:
		return index.Item{}, d.nameError(err, name)
	}

	// Size and time as the storage records them. If it cannot say right now, the file is there all the same: use what
	// the Drive knows, and the next look at the folder corrects it.
	seen := index.Seen{Name: name, Changed: d.now().UTC()}
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	if e, err := d.storage.Stat(sctx, file); err == nil && !e.Folder {
		seen.Size, seen.Changed = e.Size, e.Changed
	}
	cancel()
	d.changed(folderKey{p.ID, folderID(folder)})
	it, err := d.index.PutItem(ctx, p.ID, folderID(folder), seen, d.now(), by.added())
	if errors.Is(err, index.ErrNoFolder) {
		// Its folder moved to another place, or went, after the upload was decided on it; the file is where the folder
		// was, and a look at it finds it there.
		return index.Item{}, ErrGone
	}
	return it, err
}

// removeAbandoned removes the uploads no part of which arrived for AbandonedAfter, with their files; and the files in
// the uploads folder the index does not know (left by an upload the Drive did not finish) that the storage says have
// not changed for as long.
func (d *Drive) removeAbandoned(ctx context.Context) error {
	cutoff := d.now().Add(-AbandonedAfter)
	old, err := d.index.UploadsTouchedBefore(ctx, cutoff)
	if err != nil {
		return err
	}
	var failed error
	removed := 0
	for _, u := range old {
		if err := d.dropUpload(ctx, u, false); err != nil {
			if !errors.Is(err, ErrBusy) {
				failed = err // kept, and tried again on the next run
			}
			continue
		}
		removed++
	}

	// The files in the uploads folder the index does not know, unchanged for a day. The folder is read as it arrives and
	// only those names are kept, at most maxLeftOvers a run: whoever put a great many files there directly costs the
	// Drive no more memory than that, and the rest go on the next runs.
	known, err := d.index.UploadIDs(ctx)
	if err != nil {
		return err
	}
	var leftOver []storage.Entry
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err = d.storage.Each(sctx, UploadsFolder, func(e storage.Entry) error {
		if len(leftOver) < maxLeftOvers && !known[e.Name] && !e.Changed.IsZero() && e.Changed.Before(cutoff) {
			leftOver = append(leftOver, e)
		}
		return nil
	})
	cancel()
	if errors.Is(err, storage.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return storageError(err)
	}
	for _, e := range leftOver {
		p, ok := pathIn(UploadsFolder, e.Name)
		if !ok || d.claimed(p) {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		if e.Folder {
			err = d.storage.DeleteFolder(sctx, p)
		} else {
			err = d.storage.Delete(sctx, p)
		}
		cancel()
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			failed = storageError(err)
			continue
		}
		removed++
	}
	if removed > 0 {
		d.log.Info("removed uploads abandoned for a day", "uploads", removed)
	}
	return failed
}
