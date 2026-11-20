package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Copies of the index. The index alone holds access, and it lives on one server; so the Drive keeps copies of it:
//
//   - in its data folder, at index.BackupFile: the whole index, as one consistent SQLite file, replaced whole by a rename,
//     for a backup of the data folder to archive while the Drive runs (the live index and its write-ahead log are not
//     safe to archive file by file);
//   - on the storage, in CopiesFolder: the same copy without what lets guests in now or belongs to one server
//     (ClearCopy with Outsiders: guests' sessions, codes and limits, uploads in progress; and the walk's place), for a
//     Drive started on a new server with an empty data folder to adopt (adopt.go).
//
// Both are written at once: when the Drive starts, soon after access changes (index.AccessChanges: a burst of them makes
// one copy, Copying.Quiet after the last, and none waits longer than Copying.Latest), and every night (Copying.Night).
// A copy is put on the storage only while the storage holds the Drive's mark with the id the copy's index keeps: never
// on a storage that is empty, mounted wrongly or someone else's. One the storage did not take is tried again
// (Copying.Retry). The copy on the storage is written under a name of its own and moved into place when whole; the one
// before it is removed only then, so a whole copy is there throughout.
//
// Making a copy streams from disk to disk (SQLite's VACUUM INTO, through its page cache), and putting it on the storage
// streams from the file, over one connection with the capped buffers every connection to the storage has: it takes no
// transfer slot, as the walk takes none, and its memory does not grow with the index.

// CopiesFolder holds the copies of the index on the storage: a folder per storage id, holding copy-<number>.sqlite, the
// latest copy (two for a moment, while the one before it is removed), and upload-<random>.part while one arrives. A
// copy the Drive refused to adopt is moved aside there, as refused-<when>-<its name>, and kept.
const CopiesFolder = HiddenFolder + "/index"

// copiesFolderOf is the folder of the copies of the index whose storage id is id; ok is false for an id that cannot name
// one folder (a mark someone wrote by hand), whose copies are neither written nor adopted.
func copiesFolderOf(id string) (string, bool) {
	folder, ok := pathIn(CopiesFolder, id)
	return folder, ok && CheckName(id) == nil
}

// copyName is the name of the copy numbered seq in its storage id's folder.
func copyName(seq int64) string { return fmt.Sprintf("copy-%012d.sqlite", seq) }

var copyNamed = regexp.MustCompile(`^copy-([0-9]{1,19})\.sqlite$`)

// copySeqOf reads the number in a copy's name; ok is false for any other name.
func copySeqOf(name string) (int64, bool) {
	m := copyNamed.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

// Copying says when the Drive writes copies of its index.
type Copying struct {
	// Quiet is how long after the last access change nothing more must change before a copy is written; Latest the
	// longest an access change waits for its copy, however much more changes meanwhile.
	Quiet, Latest time.Duration
	// Retry is how long after a copy the storage did not take (unreachable, or not holding the Drive's mark) it is tried
	// again.
	Retry time.Duration
	// Night is the time of day, in UTC, of the copy written every night.
	Night time.Duration
	// Poll is how often the Drive looks whether the index has access changes the last copy lacks.
	Poll time.Duration
}

// DefaultCopying is when a released Drive copies its index.
var DefaultCopying = Copying{Quiet: 30 * time.Second, Latest: 5 * time.Minute, Retry: 5 * time.Minute, Night: 3 * time.Hour,
	Poll: time.Second}

// walkState are the keys of drive_state that say where the walk is, by one server's clock: a copy on the storage leaves
// them out, and an index made from a copy starts without them.
var walkState = []string{walkCursor, walkStarted, walkFinished}

// errNotOurs: the storage does not hold the Drive's mark with the id of the copy's index.
var errNotOurs = errors.New("the storage does not hold the Drive's mark with the id of its index")

// KeepCopies writes copies of the index into the data folder dataDir and onto the storage, as c says, until ctx ends:
// one at once, then after access changes and every night.
func (d *Drive) KeepCopies(ctx context.Context, dataDir string, c Copying) {
	removeTemporary(dataDir)
	k := &keeper{d: d, dir: dataDir, c: c, copied: -1}
	k.night = nextNight(d.now(), c.Night)
	reason := "the Drive started"
	for {
		if reason != "" {
			k.write(ctx, reason)
			reason = ""
		}
		t := time.NewTimer(c.Poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		now := d.now()
		if now.Before(k.notBefore) {
			continue
		}
		if n, err := d.index.AccessChanges(ctx); err == nil && n != k.copied {
			if n != k.seen {
				k.seen, k.changed = n, now
				if k.waiting.IsZero() {
					k.waiting = now
				}
			}
			if now.Sub(k.changed) >= c.Quiet || now.Sub(k.waiting) >= c.Latest {
				reason = "access changed"
			}
		}
		if !now.Before(k.night) {
			reason = "nightly"
			k.night = nextNight(now, c.Night)
		}
		if reason == "" && k.behind && !now.Before(k.retry) {
			// Only once the storage is the Drive's again: a new copy, with a new number, rather than the one it refused.
			if ok, err := d.markIs(ctx, k.behindID); err == nil && ok {
				reason = "the storage did not take the last copy"
			} else {
				k.retry = now.Add(c.Retry)
			}
		}
	}
}

// keeper is what KeepCopies keeps between copies.
type keeper struct {
	d   *Drive
	dir string
	c   Copying
	// copied is how many access changes the last copy holds (-1 before the first); seen the number last seen, changed
	// when it was, and waiting since when changes wait for a copy (zero while none does).
	copied, seen     int64
	changed, waiting time.Time
	// night is when the next nightly copy is due.
	night time.Time
	// behind says the storage did not take the last copy, for the index whose storage id is behindID; it is tried again
	// from retry on.
	behind   bool
	behindID string
	retry    time.Time
	// notBefore holds the next copy back after one that could not be made at all (a full disk), so it is not tried on
	// every poll.
	notBefore time.Time
}

// nextNight is the first time after now that is at, in UTC, from midnight.
func nextNight(now time.Time, at time.Duration) time.Time {
	u := now.UTC()
	t := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).Add(at)
	for !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}

// write writes a copy into the data folder, then onto the storage, and logs what it did.
func (k *keeper) write(ctx context.Context, reason string) {
	d := k.d
	began := time.Now()
	info, size, err := k.local(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Error("the index could not be copied into the data folder; it is tried again later", "error", err)
			k.notBefore = d.now().Add(k.c.Retry)
		}
		return
	}
	k.copied = info.Changes
	if k.seen == info.Changes {
		k.waiting = time.Time{}
	} else {
		k.waiting = d.now() // changes that came while it was being made wait for the next
	}
	stored, err := k.toStorage(ctx, info)
	took := time.Since(began).Round(time.Millisecond).String()
	switch {
	case err == nil && stored == "":
		k.behind = false
		d.log.Info("the index was copied into the data folder; none goes on the storage before the Drive has marked it",
			"why", reason, "number", info.Seq, "bytes", size, "took", took)
	case err == nil:
		k.behind = false
		d.log.Info("the index was copied into the data folder and onto the storage", "why", reason, "number", info.Seq,
			"bytes", size, "storage", stored, "took", took)
	case ctx.Err() != nil:
	default:
		k.behind, k.behindID, k.retry = true, info.StorageID, d.now().Add(k.c.Retry)
		if errors.Is(err, errNotOurs) {
			if d.logged.due("copy not ours", 10*time.Minute) {
				d.log.Warn("the index was copied into the data folder, but not onto the storage, which does not hold the Drive's mark with the id of its index; it is tried again once it does",
					"why", reason, "number", info.Seq, "id", info.StorageID)
			}
		} else if d.logged.due("copy failed", 10*time.Minute) {
			d.log.Warn("the index was copied into the data folder, but its copy could not be put on the storage; it is tried again later",
				"why", reason, "number", info.Seq, "error", err)
		}
	}
}

// local writes a new copy of the index and renames it over the data folder's copy.
func (k *keeper) local(ctx context.Context) (index.CopyInfo, int64, error) {
	backup := index.BackupFile(k.dir)
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return index.CopyInfo{}, 0, err
	}
	tmp := temporary(k.dir)
	info, err := k.d.index.WriteCopy(ctx, tmp)
	if err != nil {
		return index.CopyInfo{}, 0, err
	}
	st, err := os.Stat(tmp)
	if err == nil {
		err = os.Rename(tmp, backup)
	}
	if err == nil {
		err = syncFolder(filepath.Dir(backup))
	}
	if err != nil {
		os.Remove(tmp)
		return index.CopyInfo{}, 0, err
	}
	dropCached(backup)
	return info, st.Size(), nil
}

// toStorage puts the data folder's copy, cleared for the storage, into its storage id's folder there, and removes the
// copies before it. It answers where it put it: "" when the index keeps no storage id yet (it has marked no storage, so
// it holds nothing a copy would keep), and errNotOurs when the storage does not hold the mark with that id.
func (k *keeper) toStorage(ctx context.Context, info index.CopyInfo) (string, error) {
	d := k.d
	if info.StorageID == "" {
		return "", nil
	}
	if ok, err := d.markIs(ctx, info.StorageID); err != nil {
		return "", storageError(err)
	} else if !ok {
		return "", errNotOurs
	}
	tmp := temporary(k.dir)
	defer os.Remove(tmp)
	if err := index.CopyFile(index.BackupFile(k.dir), tmp); err != nil {
		return "", err
	}
	if err := index.ClearCopy(ctx, tmp, index.Clearing{Outsiders: true, State: walkState}); err != nil {
		return "", err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}

	folder, ok := copiesFolderOf(info.StorageID)
	if !ok {
		return "", errNotOurs
	}
	part := folder + "/upload-" + randomHex(8) + ".part"
	name := folder + "/" + copyName(info.Seq)
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err = d.storage.MakeFolder(sctx, folder)
	cancel()
	if err != nil {
		return "", storageError(err)
	}
	// At least 256 KiB a second, and two minutes besides.
	uctx, cancel := context.WithTimeout(ctx, 2*time.Minute+time.Duration(st.Size()>>18)*time.Second)
	err = d.storage.Put(uctx, part, f, st.Size())
	cancel()
	if err != nil {
		k.removeOnStorage(part)
		return "", storageError(err)
	}
	sctx, cancel = context.WithTimeout(ctx, storageTimeout)
	err = d.storage.Move(sctx, part, name)
	cancel()
	if err != nil {
		k.removeOnStorage(part)
		return "", storageError(err)
	}
	k.removeOthers(ctx, folder, copyName(info.Seq))
	return name, nil
}

// removeOthers removes from the copies' folder every copy but keep, and every upload left there. A copy moved aside
// (refused) stays.
func (k *keeper) removeOthers(ctx context.Context, folder, keep string) {
	var old []string
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	err := k.d.storage.Each(sctx, folder, func(e storage.Entry) error {
		if _, isCopy := copySeqOf(e.Name); (isCopy || strings.HasSuffix(e.Name, ".part")) && e.Name != keep && !e.Folder {
			old = append(old, folder+"/"+e.Name)
		}
		return nil
	})
	cancel()
	if err != nil && k.d.logged.due("copies not removed", 10*time.Minute) {
		k.d.log.Warn("the copies of the index before the last could not be listed on the storage; they are removed after the next copy", "error", err)
	}
	for _, p := range old {
		k.removeOnStorage(p)
	}
}

// removeOnStorage removes a file of the copies' folder, and logs (now and then) when it cannot.
func (k *keeper) removeOnStorage(p string) {
	ctx, cancel := context.WithTimeout(k.d.background, storageTimeout)
	defer cancel()
	if err := k.d.storage.Delete(ctx, p); err != nil && !errors.Is(err, storage.ErrNotFound) && k.d.logged.due("copy not removed", 10*time.Minute) {
		k.d.log.Warn("a file of the copies of the index could not be removed from the storage; it is removed after the next copy", "file", p, "error", err)
	}
}

// markIs says whether the storage holds the Drive's mark with id.
func (d *Drive) markIs(ctx context.Context, id string) (bool, error) {
	d.mark.mu.Lock()
	defer d.mark.mu.Unlock()
	there, err := d.readMark(ctx)
	return err == nil && id != "" && there == id, err
}

// temporary is the name of a new temporary file in the data folder: a copy being made, cleared or adopted. The data
// folder is on disk; /tmp may be memory (a tmpfs), which a container's memory figure counts.
func temporary(dataDir string) string {
	return filepath.Join(dataDir, ".copy-"+randomHex(8)+".sqlite")
}

// removeTemporary removes temporary files a Drive that stopped midway left in the data folder.
func removeTemporary(dataDir string) {
	left, _ := filepath.Glob(filepath.Join(dataDir, ".copy-*"))
	for _, f := range left {
		os.Remove(f)
	}
}

// syncFile writes what the file system holds of a file to disk.
func syncFile(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// syncFolder writes a folder's entries to disk, so that a file renamed into it stays renamed after a power cut.
func syncFolder(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
