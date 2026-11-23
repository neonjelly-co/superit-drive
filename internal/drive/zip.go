package drive

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"slices"
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
// Each folder is looked at on the storage on the way (Refresh), so the zip holds what is there now. Files are stored
// as they are, not compressed: most large files are compressed already, and compressing costs memory.
// A backslash in a name becomes "_" in the zip, where some programs would take it for a folder.
//
// start is called once, just before the first byte, and returns where the zip goes. An error before that (an id in
// only that is not directly in the folder is index.ErrNoItem; a folder with too much in it, ErrTooMany) means nothing
// was written; an error after it means the zip was cut short.
//
// still is asked before each file and folder is added, a folder before it is looked at: whether the person may still
// download the folder. Its error ends the zip there, cut short, so nothing put into the folder after they no longer
// may is ever in it.
func (d *Drive) Zip(ctx context.Context, p index.Place, folder *index.Item, name string, only []int64,
	still func(context.Context) error, start func() io.Writer) error {
	prefix := zipName(name) + "/"
	var items []index.Item
	var err error
	if only == nil {
		if items, err = d.listed(ctx, p, folder); err != nil {
			return err
		}
	} else {
		if err := d.Refresh(ctx, p, folder); err != nil {
			return err
		}
		picked := make(map[int64]bool, len(only))
		for _, id := range only {
			if picked[id] {
				continue
			}
			picked[id] = true
			it, err := d.index.Item(ctx, p.ID, id)
			if err != nil {
				return err
			}
			if it.ParentID != folderID(folder) {
				return index.ErrNoItem
			}
			items = append(items, it)
		}
		slices.SortFunc(items, func(a, b index.Item) int {
			if a.Folder != b.Folder {
				if a.Folder {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		prefix = ""
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

	z := &zipping{d: d, place: p, still: still, w: zip.NewWriter(start())}
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
	d     *Drive
	place index.Place
	// still says whether the person may still download what is zipped: an error when not.
	still   func(context.Context) error
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
//
// No two entries have the same name: a name changed for the zip (zipName) that is then another item's in the folder,
// or another changed name, gets a number, as a name kept beside a taken one does. Unpacked, neither replaces the other.
func (z *zipping) items(ctx context.Context, prefix string, in *index.Item, items []index.Item) error {
	taken := make(map[string]bool, len(items))
	for _, it := range items {
		if zipName(it.Name) == it.Name {
			taken[it.Name] = true
		}
	}
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := z.still(ctx); err != nil {
			return err
		}
		// Items as the index lists them do not carry the folders above them; the path on the storage needs them.
		if in != nil {
			it.Above = append(append(make([]index.Item, 0, len(in.Above)+1), in.Above...), *in)
		}
		entry := zipName(it.Name)
		if entry != it.Name {
			for n := 2; taken[entry]; n++ {
				entry = numbered(zipName(it.Name), n, it.Folder)
			}
			taken[entry] = true
		}
		name := prefix + entry
		if it.Folder {
			if err := z.folder(name+"/", &it); err != nil {
				return err
			}
			inside, err := z.d.listed(ctx, z.place, &it)
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

// listed looks at a folder of the place (nil: its top folder) on the storage, and returns what the index then holds in
// it: ErrTooMany when that is more than a zip can hold, and ErrGone when the folder is gone from the storage.
func (d *Drive) listed(ctx context.Context, p index.Place, folder *index.Item) ([]index.Item, error) {
	if err := d.Refresh(ctx, p, folder); err != nil {
		return nil, err
	}
	items, more, err := d.index.FirstChildren(ctx, p.ID, folderID(folder), MaxZipEntries)
	if err == nil && more {
		err = ErrTooMany
	}
	return items, err
}

// zipName is a name as a zip holds it: some programs take a backslash for a folder, which could put a file outside
// the folder it is unpacked into.
func zipName(name string) string { return strings.ReplaceAll(name, `\`, "_") }
