package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Errors the server tells the person about.
var (
	// ErrExists: something of that name is already there. Uploading never replaces anything (asking "replace or keep
	// both" comes with folders and the trash).
	ErrExists = errors.New("something of that name is already there")
	// ErrGone: the file, or the folder an upload was going into, is no longer on the storage.
	ErrGone = errors.New("no longer on the storage")
	// ErrNotAFile: the item is a folder, and only a file can be downloaded on its own.
	ErrNotAFile = errors.New("not a file")
	// ErrFull: the storage has no room for the file.
	ErrFull = errors.New("the storage is full")
	// ErrCutOff: the upload ended before the whole file arrived. Nothing was stored.
	ErrCutOff = errors.New("the upload was cut off")
)

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
// (no change of case or Unicode form): they are the real names on the storage.
func CheckName(name string) error {
	switch {
	case name == "":
		return &BadName{"A file needs a name."}
	case !utf8.ValidString(name):
		return &BadName{"This name is not valid text. Rename the file and try again."}
	case len(name) > MaxNameBytes:
		return &BadName{TooLong}
	case name == "." || name == "..":
		return &BadName{"“.” and “..” cannot be used as names. Rename the file and try again."}
	case strings.Contains(name, "/"):
		return &BadName{"A name cannot contain “/”. Rename the file and try again."}
	case strings.ContainsFunc(name, unicode.IsControl):
		return &BadName{"A name cannot contain line breaks, tabs or other control characters. Rename the file and try again."}
	}
	return nil
}

// pathIn is the storage path of the item called name directly in the place's top folder. ok is false for a name that
// is not one plain segment ("", ".", "..", or one holding a "/", which only a misbehaving server could list): such a
// name never becomes a path, so no name in the index can reach outside its place.
func pathIn(p index.Place, name string) (file string, ok bool) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return "", false
	}
	return p.Folder + "/" + name, true
}

// Item finds the item with id in the place's top folder, and nowhere else.
func (d *Drive) Item(ctx context.Context, p index.Place, id int64) (index.Item, error) {
	return d.index.TopItem(ctx, p.ID, id)
}

// Upload stores a new file called name in the place's top folder and returns it as the index now holds it. body must
// hold exactly size bytes; they stream straight through to the storage in one PUT, which writes the whole file or
// nothing, so a cut-off upload leaves nothing behind.
//
// An upload never replaces anything: a name that is taken, on the storage or by another upload still in flight, is
// refused with ErrExists before any byte is sent.
func (d *Drive) Upload(ctx context.Context, p index.Place, name string, body io.Reader, size int64) (index.Item, error) {
	if err := CheckName(name); err != nil {
		return index.Item{}, err
	}
	file, ok := pathIn(p, name)
	if !ok {
		return index.Item{}, &BadName{"This name cannot be used. Rename the file and try again."} // CheckName refuses these first
	}
	release, ok := d.claim(file)
	if !ok {
		return index.Item{}, ErrExists
	}
	defer release()

	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	_, err := d.storage.Stat(sctx, file)
	cancel()
	if err == nil {
		return index.Item{}, ErrExists
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return index.Item{}, storageError(err)
	}

	// The upload's own length bounds it, and the person's leaving ends it; no other deadline applies.
	from := &bodyReader{r: body}
	err = d.storage.Put(ctx, file, from, size)
	switch {
	case from.err != nil:
		return index.Item{}, fmt.Errorf("%w: %w", ErrCutOff, from.err)
	case errors.Is(err, storage.ErrExists):
		return index.Item{}, ErrExists
	case errors.Is(err, storage.ErrNotFound):
		return index.Item{}, ErrGone
	case errors.Is(err, storage.ErrFull):
		return index.Item{}, ErrFull
	case err != nil:
		return index.Item{}, storageError(err)
	}

	// Size and time as the storage records them. If it cannot say right now, the file is there all the same: use what
	// the Drive knows, and the next listing corrects it.
	seen := index.Seen{Name: name, Size: size, Changed: time.Now().UTC()}
	sctx, cancel = context.WithTimeout(ctx, storageTimeout)
	if e, err := d.storage.Stat(sctx, file); err == nil && !e.Folder {
		seen.Size, seen.Changed = e.Size, e.Changed
	}
	cancel()
	return d.index.PutTopItem(ctx, p.ID, seen)
}

// claim marks the storage path file as being written. ok is false when another upload holds it.
func (d *Drive) claim(file string) (release func(), ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writing[file] {
		return nil, false
	}
	d.writing[file] = true
	return func() {
		d.mu.Lock()
		delete(d.writing, file)
		d.mu.Unlock()
	}, true
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
	file, ok := pathIn(p, it.Name)
	if !ok {
		return nil, ErrGone
	}
	f, err := d.storage.Open(ctx, file)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrGone
	}
	return f, storageError(err)
}
