package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Adopting a copy of the index. A Drive whose data folder holds no index (a new server, or the server's data restored
// from a backup that holds only the copy, as the README asks a backup to hold) makes it from a copy, before it opens it:
//
//   - the data folder's own copy (index.BackupFile), when a backup of the data folder was put back: it is the whole
//     index as it was, guests' sessions included;
//   - the latest copy on the storage, in the folder of the id in the Drive's mark (CopiesFolder): only when the storage
//     holds the mark, and only a copy whose index keeps that same id; when the data folder's copy belongs to that storage
//     too and is as new or newer, that one is taken instead.
//
// A copy becomes the index only once index.CheckCopy finds it whole, of a schema this Drive's migrations make (an older
// one is brought up to date as any index is), with every place where the Drive puts places. Then it is cleared
// (index.ClearCopy): a copy from the storage of guests' sessions, codes and limits and uploads in progress, which only
// the server they were on can honour (guests sign in again, with a new code); any copy of where the walk was. And the
// walk starts at once, as after a rollback (adoptionWalk): what changed on the storage since the copy was made comes in
// as the looks always bring it, and an item deleted since, back in its entry of the trash, goes there as itself.
//
// A copy made by a newer Drive stops the Drive from starting: building an index without it would soon put a copy without
// the shares in its place. A damaged copy, or one that belongs to another storage, is moved aside on the storage and
// kept, and the Drive builds its index from the files, with no shares, and says so loudly. So does a Drive whose storage
// holds neither copy nor mark (a new storage, or one whose Drive never wrote a copy). When the storage cannot be read,
// the Drive does not start: it cannot tell whether there is a copy to adopt.
//
// A running Drive, and one that starts with its index, never adopts a copy: after a rollback, the storage's copy is the
// snapshot's, older than the index, which keeps the sharing as it is (rollback.go); its next copy replaces it.

// adoptionWalk is kept in drive_state from when the index is made from a copy until a walk through the storage has
// brought it in step, as rollbackWalk is for a rollback.
const adoptionWalk = "adoption_walk"

// A candidate is a copy of the index the Drive may make its index from.
type candidate struct {
	file    string // a file in the data folder
	info    index.CopyInfo
	sum     index.CopySummary
	storage string // where on the storage it was; "" for the data folder's own copy
	changed time.Time
}

// Adopt makes the index in dataDir from a copy when there is none there yet (see above); nothing otherwise. now is the
// Drive's clock.
func Adopt(ctx context.Context, dataDir string, s *storage.Client, log *slog.Logger, now func() time.Time) error {
	live := filepath.Join(dataDir, index.FileName)
	if _, err := os.Stat(live); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("making the data folder: %w", err)
	}
	// What is beside a missing index is not the next one's: a write-ahead log left there would be laid over it.
	for _, beside := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(live + beside); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	removeTemporary(dataDir)

	local, err := localCopy(ctx, dataDir, log)
	if err != nil {
		return err
	}
	mark, err := patiently(ctx, log, func(ctx context.Context) (string, error) { return readMarkOn(ctx, s) })
	if err != nil {
		return fmt.Errorf("the data folder holds no index, and the storage could not be read to find whether it holds a copy of one; "+
			"the Drive does not start without knowing (an index made without the copy would replace it): %w", err)
	}
	var stored *candidate
	if mark == "" {
		foreignCopies(ctx, s, log)
	} else {
		stored, err = storageCopy(ctx, dataDir, s, log, mark, local)
		if err != nil {
			return err
		}
	}

	chosen := local
	if stored != nil && (local == nil || local.info.StorageID != mark || stored.info.Seq > local.info.Seq) {
		chosen = stored
	}
	if stored != nil && chosen != stored {
		os.Remove(stored.file)
	}
	if chosen == nil {
		log.Info("the data folder holds no index, and there is no copy of one to make it from: the Drive builds its index from the files on the storage, with no shares")
		return nil
	}

	file := chosen.file
	if chosen.storage == "" {
		// The data folder keeps its copy: the index is made from a copy of it.
		file = temporary(dataDir)
		if err := copyFile(chosen.file, file); err != nil {
			return err
		}
	}
	defer os.Remove(file)
	if err := index.ClearCopy(ctx, file, index.Clearing{
		Outsiders: chosen.storage != "",
		State:     walkState,
		Set:       map[string]string{adoptionWalk: strconv.FormatInt(now().UnixMilli(), 10)},
	}); err != nil {
		return fmt.Errorf("readying the copy of the index: %w", err)
	}
	if err := syncFile(file); err != nil {
		return err
	}
	if err := os.Rename(file, live); err != nil {
		return err
	}
	if err := syncFolder(dataDir); err != nil {
		return err
	}
	from := "the data folder's copy (" + filepath.Join(index.BackupFolder, index.FileName) + "); guests stay signed in"
	if chosen.storage != "" {
		from = "the copy on the storage (" + chosen.storage + "); guests sign in again with a new code"
	}
	log.Warn("the data folder held no index: the Drive made it from "+from+". The index is as it was when the copy was "+
		"made; a walk through the storage now takes in what changed there since",
		"number", chosen.info.Seq, "copied", chosen.changed.Format(time.RFC3339), "storageId", chosen.info.StorageID,
		"version", chosen.info.Version, "places", chosen.sum.Places, "sharedFolders", chosen.sum.SharedFolders,
		"items", chosen.sum.Items, "shares", chosen.sum.Grants, "managers", chosen.sum.Managers, "links", chosen.sum.Links,
		"guests", chosen.sum.Guests, "acts", chosen.sum.Acts)
	return nil
}

// topFolderAllowed says whether folder is where the Drive puts the top folder of a place of that kind: one name in "My
// files" or "Shared folders".
func topFolderAllowed(kind index.PlaceKind, folder string) bool {
	var top string
	switch kind {
	case index.MyFiles:
		top = MyFilesFolder
	case index.SharedFolder:
		top = SharedFoldersFolder
	default:
		return false
	}
	name, ok := strings.CutPrefix(folder, top+"/")
	if _, inside := pathIn(top, name); !ok || !inside || CheckName(name) != nil {
		return false
	}
	return true
}

// localCopy is the data folder's copy of the index when it passes the check; nil when there is none, or it does not
// (it is then set aside, kept, and the log says why). A copy made by a newer Drive is an error.
func localCopy(ctx context.Context, dataDir string, log *slog.Logger) (*candidate, error) {
	file := index.BackupFile(dataDir)
	st, err := os.Stat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	info, sum, err := index.CheckCopy(ctx, file, topFolderAllowed)
	switch {
	case errors.Is(err, index.ErrCopyNewer):
		return nil, fmt.Errorf("the data folder holds no index, and its copy (%s) %w: the Drive does not start, so that it "+
			"does not make an index without it; start a version of the Drive as new as the one that made it", file, err)
	case errors.Is(err, index.ErrCopyDamaged):
		aside := file + ".refused-" + time.Now().UTC().Format("20060102T150405Z")
		_ = os.Rename(file, aside)
		log.Error("THE DATA FOLDER'S COPY OF THE INDEX IS NOT USED: "+err.Error()+". It is kept beside it", "file", aside)
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &candidate{file: file, info: info, sum: sum, changed: st.ModTime().UTC()}, nil
}

// storageCopy is the latest copy on the storage for the storage id mark, downloaded into the data folder, when it
// passes the check and belongs to that id; nil when there is none, or when the data folder's copy (local) is as new.
// One that does not pass is moved aside on the storage and kept, and the log says loudly why. A copy made by a newer
// Drive is an error.
func storageCopy(ctx context.Context, dataDir string, s *storage.Client, log *slog.Logger, mark string, local *candidate) (*candidate, error) {
	folder, ok := copiesFolderOf(mark)
	if !ok {
		log.Error("THE STORAGE'S MARK CANNOT NAME A FOLDER OF COPIES OF AN INDEX: none is adopted, and the Drive builds its index from the files, with no shares",
			"mark", StorageIDFile)
		return nil, nil
	}
	// The newest copy, read as the listing arrives: a folder of any size costs no more than one entry.
	type found struct {
		newest storage.Entry
		seq    int64
	}
	f, err := patiently(ctx, log, func(ctx context.Context) (found, error) {
		f := found{seq: -1}
		err := s.Each(ctx, folder, func(e storage.Entry) error {
			if n, ok := copySeqOf(e.Name); ok && !e.Folder && n > f.seq {
				f.newest, f.seq = e, n
			}
			return nil
		})
		if errors.Is(err, storage.ErrNotFound) {
			return found{seq: -1}, nil
		}
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("the data folder holds no index, and the copies of the index on the storage could not be listed; "+
			"the Drive does not start without knowing whether one is there: %w", err)
	}
	newest, seq := f.newest, f.seq
	if seq < 0 {
		return nil, nil
	}
	if local != nil && local.info.StorageID == mark && local.info.Seq >= seq {
		return nil, nil // the data folder's copy is as new: nothing to download
	}
	if newest.Size > maxCopy {
		log.Error("THE COPY OF THE INDEX ON THE STORAGE IS NOT USED: it is larger than any copy the Drive makes. The Drive "+
			"builds its index from the files on the storage, and starts with no shares; the copy is left there", "copy", folder+"/"+newest.Name,
			"bytes", newest.Size)
		return nil, nil
	}
	where := folder + "/" + newest.Name
	file := temporary(dataDir)
	if _, err := patiently(ctx, log, func(ctx context.Context) (struct{}, error) {
		os.Remove(file)
		return struct{}{}, download(ctx, s, where, file)
	}); err != nil {
		os.Remove(file)
		return nil, fmt.Errorf("the data folder holds no index, and the copy of it on the storage (%s) could not be read; "+
			"the Drive does not start without it: %w", where, err)
	}
	info, sum, err := index.CheckCopy(ctx, file, topFolderAllowed)
	if err == nil && info.StorageID != mark {
		err = fmt.Errorf("it belongs to another storage (its index keeps the id %q, the storage's mark says %q)", info.StorageID, mark)
	}
	switch {
	case errors.Is(err, index.ErrCopyNewer):
		os.Remove(file)
		return nil, fmt.Errorf("the data folder holds no index, and the copy of it on the storage (%s) %w: the Drive does not "+
			"start, so that it does not make an index without it and then replace it; start a version of the Drive as new as "+
			"the one that made it", where, err)
	case err != nil:
		os.Remove(file)
		aside := folder + "/refused-" + time.Now().UTC().Format("20060102T150405Z") + "-" + newest.Name
		mctx, cancel := context.WithTimeout(ctx, storageTimeout)
		if merr := s.Move(mctx, where, aside); merr != nil {
			aside = where + " (it could not be moved aside: " + merr.Error() + ")"
		}
		cancel()
		log.Error("THE COPY OF THE INDEX ON THE STORAGE IS NOT USED: "+err.Error()+
			". The Drive builds its index from the files on the storage, and starts with no shares, no links, no guests "+
			"and no shared folders but those it finds as folders; the copy is kept on the storage", "copy", aside)
		return nil, nil
	}
	return &candidate{file: file, info: info, sum: sum, storage: where, changed: newest.Changed}, nil
}

// foreignCopies logs loudly when the storage holds copies of an index but not the Drive's mark: none is adopted.
func foreignCopies(ctx context.Context, s *storage.Client, log *slog.Logger) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	var ids []string
	_ = s.Each(sctx, CopiesFolder, func(e storage.Entry) error {
		if e.Folder && len(ids) < 10 {
			ids = append(ids, e.Name)
		}
		return nil
	})
	if len(ids) > 0 {
		log.Error("THE STORAGE HOLDS COPIES OF AN INDEX BUT NOT THE DRIVE'S MARK: none is adopted, and the Drive builds its "+
			"index from the files, with no shares. If the storage is this Drive's own, stop it, write the id of the copies "+
			"to adopt (the name of their folder) into the mark, and start it again with an empty data folder",
			"mark", StorageIDFile, "copies", CopiesFolder, "ids", ids)
	}
}

// maxCopy is the largest copy of the index the Drive downloads to adopt: far more than an index of millions of items
// takes, and a bound on what a file planted there can fill the server's disk with.
const maxCopy = 4 << 30

// download reads the file at p on the storage into a new file at to, chunk by chunk, synced to disk (copies.go).
func download(ctx context.Context, s *storage.Client, p, to string) error {
	f, err := s.Open(ctx, p)
	if err != nil {
		return err
	}
	defer f.Body.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	w := &syncingWriter{f: out}
	n, err := io.Copy(w, io.LimitReader(f.Body, maxCopy+1))
	switch {
	case err != nil:
	case n > maxCopy:
		err = fmt.Errorf("it is larger than %d bytes, more than any copy the Drive makes", int64(maxCopy))
	case f.Size >= 0 && n != f.Size:
		err = fmt.Errorf("read %d bytes of %d", n, f.Size)
	}
	if err == nil {
		err = w.flush()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// patiently asks the storage, and asks again twice, a little later each time, when it does not answer.
func patiently[T any](ctx context.Context, log *slog.Logger, ask func(context.Context) (T, error)) (T, error) {
	var v T
	var err error
	for try, wait := 0, 2*time.Second; ; try, wait = try+1, wait*2 {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		v, err = ask(sctx)
		cancel()
		if err == nil || try == 2 || ctx.Err() != nil {
			return v, err
		}
		log.Warn("the storage could not be read; it is asked again", "in", wait.String(), "error", err)
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(wait):
		}
	}
}
