package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Errors the server tells the person about.
var (
	// ErrBusy: another change of the same name, such as an upload, is still on its way.
	ErrBusy = errors.New("something of that name is being changed right now")
	// ErrNotAFile: the item is a folder, and only a file can be downloaded on its own.
	ErrNotAFile = errors.New("not a file")
	// ErrFull: the storage has no room for the file.
	ErrFull = errors.New("the storage is full")
	// ErrCutOff: the upload ended before the whole file arrived. Nothing was stored.
	ErrCutOff = errors.New("the upload was cut off")
)

// A Taken error says that something called Name is already where a file or folder was to go, and whether it is a
// folder: a file can replace a file, never a folder.
type Taken struct {
	Name   string
	Folder bool
}

func (t *Taken) Error() string { return fmt.Sprintf("something called %q is already there", t.Name) }

// MaxNameBytes is the longest name the Drive stores, in bytes of UTF-8. It is the limit of a Storage Box, of Apache's
// mod_dav and of most file systems.
const MaxNameBytes = 255

// A BadName is a name the Drive will not store. Its text is written for the person who chose the name.
type BadName struct{ reason string }

func (b *BadName) Error() string { return b.reason }

// TooLong is what a person is told about a name longer than MaxNameBytes. The interface says the same before it
// sends anything (web/src/names.ts).
const TooLong = "This name is too long. Names can be up to 255 characters, and fewer when they have accented letters, " +
	"other alphabets or symbols. Shorten the name and try again."

// CheckName says whether name can be the name of a file or folder on the storage. Names are kept exactly as given
// (no change of case or Unicode form): they are the real names on the storage. The interface sends names in Unicode's
// composed form (NFC), so a name typed or chosen in one browser matches the same name from another.
func CheckName(name string) error {
	switch {
	case name == "":
		return &BadName{"A name is needed."}
	case !utf8.ValidString(name):
		return &BadName{"This name is not valid text. Choose another name."}
	case len(name) > MaxNameBytes:
		return &BadName{TooLong}
	case name == "." || name == "..":
		return &BadName{"“.” and “..” cannot be used as names. Choose another name."}
	case strings.Contains(name, "/"):
		return &BadName{"A name cannot contain “/”. Choose another name."}
	case strings.ContainsFunc(name, unicode.IsControl):
		return &BadName{"A name cannot contain line breaks, tabs or other control characters. Choose another name."}
	}
	return nil
}

// Existing says what an upload does when its name is taken by a file.
type Existing string

const (
	// Ask refuses with a *Taken error, so the person can be asked "replace or keep both".
	Ask Existing = ""
	// Replace puts the file that is there into the trash and stores the upload under its name. The item keeps its id:
	// it is the same file with new content.
	Replace Existing = "replace"
	// KeepBoth stores the upload under the first free name made by adding " (2)", " (3)" and so on to its own.
	KeepBoth Existing = "keep-both"
)

// An Upload is a file just stored.
type Upload struct {
	index.Item
	// Replaced is the file the upload replaced, now in the trash, or nil.
	Replaced *index.TrashItem
}

// Upload stores a new file called name in a folder of the place (nil: its top folder) and returns it as the index now
// holds it. body must hold exactly size bytes; they stream straight through to the storage in one PUT, which writes
// the whole file or nothing, so a cut-off upload leaves nothing behind.
//
// When the name is taken, existing decides: ask (a *Taken error before any byte is sent), replace a file, or keep both
// under a new name. A folder is never replaced. Another upload of the same name still in flight is ErrBusy.
func (d *Drive) Upload(ctx context.Context, p index.Place, folder *index.Item, name string, body io.Reader, size int64, existing Existing) (Upload, error) {
	if err := CheckName(name); err != nil {
		return Upload{}, err
	}
	dir, ok := folderPath(p, folder)
	if !ok {
		return Upload{}, ErrGone
	}
	file, ok := pathIn(dir, name)
	if !ok {
		return Upload{}, &BadName{"This name cannot be used. Choose another name."} // CheckName refuses these first
	}
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return Upload{}, err
	}
	defer leave()
	release, ok := d.claim(file)
	if !ok {
		return Upload{}, ErrBusy
	}
	defer release()

	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	there, err := d.storage.Stat(sctx, file)
	cancel()
	var replacing bool
	switch {
	case errors.Is(err, storage.ErrNotFound):
	case err != nil:
		return Upload{}, storageError(err)
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
		// The file there goes to the trash first, and comes back if the upload fails. (With the hidden uploads folder,
		// a later ticket, the upload is stored first and the two swap places at the end.)
		replaced, undo, err := d.putAside(ctx, p, folder, file, there)
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

	// The upload's own length bounds it, and the person's leaving ends it; no other deadline applies.
	from := &bodyReader{r: body}
	err = d.storage.Put(ctx, file, from, size)
	switch {
	case from.err != nil:
		return Upload{}, fmt.Errorf("%w: %w", ErrCutOff, from.err)
	case errors.Is(err, storage.ErrExists):
		return Upload{}, &Taken{Name: name}
	case errors.Is(err, storage.ErrNotFound):
		// The folder is gone from the storage. A place's top folder is made again, so the next try works.
		if err := d.ensureTop(context.WithoutCancel(ctx), p, folder); err != nil {
			d.log.Error("a place's top folder could not be made again", "folder", p.Folder, "error", err)
		}
		return Upload{}, ErrGone
	case errors.Is(err, storage.ErrFull):
		return Upload{}, ErrFull
	case err != nil:
		return Upload{}, storageError(err)
	}

	// Size and time as the storage records them. If it cannot say right now, the file is there all the same: use what
	// the Drive knows, and the next listing corrects it.
	seen := index.Seen{Name: name, Size: size, Changed: d.now().UTC()}
	sctx, cancel = context.WithTimeout(ctx, storageTimeout)
	if e, err := d.storage.Stat(sctx, file); err == nil && !e.Folder {
		seen.Size, seen.Changed = e.Size, e.Changed
	}
	cancel()
	d.changed(folderKey{p.ID, folderID(folder)})
	it, err := d.index.PutItem(ctx, p.ID, folderID(folder), seen)
	if err != nil {
		return Upload{}, err
	}
	out.Item = it
	return out, nil
}

// freeName finds a name in the folder at dir that nothing on the storage or in flight uses: name with " (2)", " (3)"
// and so on added before its extension (at the end, for a folder), and holds it until release is called.
func (d *Drive) freeName(ctx context.Context, dir, name string, folder bool) (free string, release func(), err error) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	entries, err := d.storage.List(sctx, dir)
	cancel()
	if errors.Is(err, storage.ErrNotFound) {
		return "", nil, ErrGone
	}
	if err != nil {
		return "", nil, storageError(err)
	}
	taken := make(map[string]bool, len(entries))
	for _, e := range entries {
		taken[e.Name] = true
	}
	for n := 2; n < 1000; n++ {
		candidate := numbered(name, n, folder)
		p, ok := pathIn(dir, candidate)
		if !ok || taken[candidate] {
			continue
		}
		if release, ok := d.claim(p); ok {
			return candidate, release, nil
		}
	}
	return "", nil, &Taken{Name: name, Folder: folder}
}

// numberedSuffix is a number in brackets at the end of a name's stem, as numbered adds it: "Report (2)".
var numberedSuffix = regexp.MustCompile(` \(([1-9][0-9]{0,2})\)$`)

// numbered is name with " (n)" added: before the extension of a file ("Report.pdf" → "Report (2).pdf"), at the end of
// a folder's name or of a file name without one (".bashrc" → ".bashrc (2)"). A number numbered added before is
// replaced rather than added to ("Report (2).pdf" → "Report (3).pdf"); larger numbers, such as a year, stay. The
// stem is shortened when the result would be longer than MaxNameBytes.
func numbered(name string, n int, folder bool) string {
	stem, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); !folder && i > 0 {
		stem, ext = name[:i], name[i:]
	}
	stem = numberedSuffix.ReplaceAllString(stem, "")
	suffix := " (" + strconv.Itoa(n) + ")"
	if len(suffix)+len(ext) >= MaxNameBytes {
		stem, ext = stem+ext, "" // an extension too long to keep whole is part of the name
	}
	for len(stem)+len(suffix)+len(ext) > MaxNameBytes {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return stem + suffix + ext
}

// bodyReader keeps the first error reading an upload's body, so a person's dropped connection is told apart from
// the storage failing.
type bodyReader struct {
	r   io.Reader
	err error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && err != io.EOF && b.err == nil {
		b.err = err
	}
	return n, err
}

// Download starts reading the file item in the place, whole. Close the file when done.
func (d *Drive) Download(ctx context.Context, p index.Place, it index.Item) (*storage.File, error) {
	if it.Folder {
		return nil, ErrNotAFile
	}
	file, ok := itemPath(p, it)
	if !ok {
		return nil, ErrGone
	}
	f, err := d.storage.Open(ctx, file)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrGone
	}
	return f, storageError(err)
}
