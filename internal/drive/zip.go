package drive

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// MaxZipEntries is the most files and folders one zip may hold. A zip keeps a line about every entry in memory until
// its end, where it writes them all (the central directory): about 300 bytes each, so a zip of this many holds about
// 3 MiB. A larger folder is downloaded a folder inside it at a time.
const MaxZipEntries = 10_000

// ErrTooMany: a folder holds more than MaxZipEntries files and folders, too many for one zip.
var ErrTooMany = errors.New("too many files and folders for one zip")

// Zip writes a folder of the place (nil: its top folder), with everything in it at any depth, as one zip, streamed
// from the storage a file at a time: nothing is held on the server but the file being read. The folder itself is at
// the top of the zip, called name. With only (ids of items directly in the folder), the zip holds just those items
// (each a file, or a folder with everything in it), at its top.
//
// Each folder is listed from the storage on the way, as when it is opened, so the zip holds what is there now. Files
// are stored as they are, not compressed: most large files are compressed already, and compressing costs memory.
// A backslash in a name becomes "_" in the zip, where some programs would take it for a folder.
//
// start is called once, just before the first byte, and returns where the zip goes. An error before that (an id in
// only that is not directly in the folder is index.ErrNoItem; a folder with too much in it, ErrTooMany) means nothing
// was written; an error after it means the zip was cut short.
func (d *Drive) Zip(ctx context.Context, p index.Place, folder *index.Item, name string, only []int64, start func() io.Writer) error {
	items, err := d.List(ctx, p, folder)
	if err != nil {
		return err
	}
	prefix := zipName(name) + "/"
	if only != nil {
		pick := make(map[int64]bool, len(only))
		for _, id := range only {
			pick[id] = true
		}
		var chosen []index.Item
		for _, it := range items {
			if pick[it.ID] {
				chosen = append(chosen, it)
				delete(pick, it.ID)
			}
		}
		if len(pick) > 0 {
			return index.ErrNoItem
		}
		items, prefix = chosen, ""
	}
	// What the index knows is in it, before anything is written; the walk counts again as it goes.
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	if n, err := d.index.CountInside(ctx, p.ID, ids, MaxZipEntries+1); err != nil {
		return err
	} else if n > MaxZipEntries {
		return ErrTooMany
	}

	z := &zipping{d: d, place: p, w: zip.NewWriter(start())}
	if prefix != "" {
		if err := z.folder(prefix, folder); err != nil {
			return err
		}
	}
	if err := z.items(ctx, prefix, folder, items); err != nil {
		return err
	}
	return z.w.Close()
}

// zipping is one zip being written.
type zipping struct {
	d       *Drive
	place   index.Place
	w       *zip.Writer
	entries int
}

func (z *zipping) entry(h *zip.FileHeader) (io.Writer, error) {
	if z.entries++; z.entries > MaxZipEntries {
		return nil, ErrTooMany
	}
	return z.w.CreateHeader(h)
}

// folder adds a folder's own entry, so an empty folder is in the zip too.
func (z *zipping) folder(name string, it *index.Item) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	if it != nil && it.Changed.Unix() > 0 {
		h.Modified = it.Changed
	}
	h.SetMode(0o755 | 1<<31) // a directory
	_, err := z.entry(h)
	return err
}

// items adds items, which are directly in the folder in (nil: the place's top folder), and everything in them, each
// under prefix.
func (z *zipping) items(ctx context.Context, prefix string, in *index.Item, items []index.Item) error {
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Items as the index lists them do not carry the folders above them; the path on the storage needs them.
		if in != nil {
			it.Above = append(append(make([]index.Item, 0, len(in.Above)+1), in.Above...), *in)
		}
		name := prefix + zipName(it.Name)
		if it.Folder {
			if err := z.folder(name+"/", &it); err != nil {
				return err
			}
			inside, err := z.d.List(ctx, z.place, &it)
			if errors.Is(err, ErrGone) {
				continue // gone from the storage since its folder was listed
			}
			if err != nil {
				return err
			}
			if err := z.items(ctx, name+"/", &it, inside); err != nil {
				return err
			}
			continue
		}
		if err := z.file(ctx, name, it); err != nil {
			return err
		}
	}
	return nil
}

func (z *zipping) file(ctx context.Context, name string, it index.Item) error {
	p, ok := itemPath(z.place, it)
	if !ok {
		return nil
	}
	f, err := z.d.storage.Read(ctx, p, nil, "")
	if errors.Is(err, storage.ErrNotFound) {
		return nil // gone from the storage since its folder was listed
	}
	if err != nil {
		return storageError(err)
	}
	defer f.Body.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	if it.Changed.Unix() > 0 {
		h.Modified = it.Changed
	}
	w, err := z.entry(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f.Body)
	return err
}

// zipName is a name as a zip holds it: some programs take a backslash for a folder, which could put a file outside
// the folder it is unpacked into.
func zipName(name string) string { return strings.ReplaceAll(name, `\`, "_") }
