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
	"github.com/neonjelly-co/superit-drive/internal/text"
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

// A BadName is a name the Drive will not store. Why is written for the person who chose the name, in each language.
type BadName struct{ Why text.Message }

func (b *BadName) Error() string { return b.Why.English() }

// Why a name is not stored, as the person who chose it reads it. The interface says the same before it sends anything
// (nameProblem in web/src/names.ts, with its words in web/src/i18n).
var (
	nameNeeded  = text.M("A name is needed.", "Adj meg egy nevet.")
	nameNotText = text.M("This name is not valid text. Choose another name.", "Ez a név nem érvényes szöveg. Válassz másik nevet.")
	// TooLong is what a person is told about a name longer than MaxNameBytes.
	TooLong = text.M("This name is too long. Names can be up to 255 characters, and fewer when they have accented letters, "+
		"other alphabets or symbols. Shorten the name and try again.",
		"Ez a név túl hosszú. Egy név legfeljebb 255 karakter lehet, ékezetes betűkkel, más írásokkal vagy jelekkel kevesebb. "+
			"Rövidítsd le, és próbáld újra.")
	nameDots    = text.M("“.” and “..” cannot be used as names. Choose another name.", "A „.” és a „..” nem lehet név. Válassz másik nevet.")
	nameSlash   = text.M("A name cannot contain “/”. Choose another name.", "A név nem tartalmazhat „/” jelet. Válassz másik nevet.")
	nameControl = text.M("A name cannot contain line breaks, tabs or other control characters. Choose another name.",
		"A név nem tartalmazhat sortörést, tabulátort vagy más vezérlőkaraktert. Válassz másik nevet.")
	nameUnusable = text.M("This name cannot be used. Choose another name.", "Ez a név nem használható. Válassz másik nevet.")
	// nameRefused is for a name the storage refused (nameError).
	nameRefused = text.M("The Drive storage does not take this name. Choose another name.",
		"A Drive-tárhely nem fogadja el ezt a nevet. Válassz másik nevet.")
	nameTurns = text.M("This name holds characters that change the direction of text. Rename the file and send it again.",
		"Ebben a névben olyan karakterek vannak, amelyek megfordítják a szöveg irányát. Nevezd át a fájlt, és küldd el újra.")
)

// CheckName says whether name can be the name of a file or folder on the storage. Names are kept exactly as given
// (no change of case or Unicode form): they are the real names on the storage. The interface sends names in Unicode's
// composed form (NFC), so a name typed or chosen in one browser matches the same name from another.
func CheckName(name string) error {
	switch {
	case name == "":
		return &BadName{nameNeeded}
	case !utf8.ValidString(name):
		return &BadName{nameNotText}
	case len(name) > MaxNameBytes:
		return &BadName{TooLong}
	case name == "." || name == "..":
		return &BadName{nameDots}
	case strings.Contains(name, "/"):
		return &BadName{nameSlash}
	case strings.ContainsFunc(name, unicode.IsControl):
		return &BadName{nameControl}
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
// holds it. body must hold exactly size bytes. They stream straight through to a file of their own in the uploads
// folder on the storage, in one PUT, which writes the whole file or nothing; only then is it moved into its folder. A
// cut-off upload leaves nothing in the folder.
//
// When the name is taken, existing decides: ask (a *Taken error before any byte is sent), replace a file, or keep both
// under a new name. A folder is never replaced. Another upload of the same name still in flight is ErrBusy. by is who
// uploads: a file a member replaces goes to the trash as put there by them; a file a guest uploads is labelled with
// their address.
func (d *Drive) Upload(ctx context.Context, p index.Place, folder *index.Item, name string, body io.Reader, size int64, existing Existing, by By) (Upload, error) {
	if err := by.checkName(name); err != nil {
		return Upload{}, err
	}
	dir, ok := folderPath(p, folder)
	if !ok {
		return Upload{}, ErrGone
	}
	file, ok := pathIn(dir, name)
	if !ok {
		return Upload{}, &BadName{nameUnusable} // CheckName refuses these first
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
	if err := d.askFirst(ctx, file, name, existing); err != nil {
		return Upload{}, err
	}
	if err := d.makeUploadsFolder(ctx); err != nil {
		return Upload{}, err
	}

	// The upload's own length bounds it, and the person's leaving ends it; no other deadline applies.
	partial := partialOf(newUploadID())
	from := &bodyReader{r: body}
	err = d.storage.Put(ctx, partial, from, size)
	switch {
	case from.err != nil:
		d.removePartial(ctx, partial) // a storage other than Apache may keep what arrived
		return Upload{}, fmt.Errorf("%w: %w", ErrCutOff, from.err)
	case errors.Is(err, storage.ErrFull):
		return Upload{}, ErrFull
	case err != nil:
		return Upload{}, storageError(err)
	}
	up, err := d.settle(ctx, p, folder, dir, name, existing, partial, by)
	if err != nil {
		d.removePartial(ctx, partial)
		return Upload{}, err
	}
	return up, nil
}

// removePartial removes an upload's file from the uploads folder, after an upload that did not end in its folder.
func (d *Drive) removePartial(ctx context.Context, partial string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storageTimeout)
	defer cancel()
	if err := d.storage.Delete(ctx, partial); err != nil && !errors.Is(err, storage.ErrNotFound) {
		d.log.Warn("an upload's file could not be removed from the uploads folder; it is removed within a day", "file", partial, "error", err)
	}
}

// freeName finds a name in the folder at dir that nothing on the storage or in flight uses: name with " (2)", " (3)"
// and so on added before its extension (at the end, for a folder), and holds it until release is called. It asks the
// storage about each name in turn, rather than list the folder, which can hold any number of things.
func (d *Drive) freeName(ctx context.Context, dir, name string, folder bool) (free string, release func(), err error) {
	for n := 2; n < 1000; n++ {
		candidate := numbered(name, n, folder)
		p, ok := pathIn(dir, candidate)
		if !ok {
			continue
		}
		release, ok := d.claim(p)
		if !ok {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		_, err := d.storage.Stat(sctx, p)
		cancel()
		switch {
		case errors.Is(err, storage.ErrNotFound):
			return candidate, release, nil
		case err != nil:
			release()
			return "", nil, d.nameError(err, candidate)
		}
		release()
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

// A NoSuchRange error says that the part of a file a download asked for starts after its end. Size is the file's
// size, or -1 when the storage could not say.
type NoSuchRange struct{ Size int64 }

func (n *NoSuchRange) Error() string { return "the part asked for starts after the end of the file" }

// Download starts reading the file item in the place: whole when r is nil, or the part r asks for, unless ifRange (the
// If-Range a browser sent, or "") says the browser's copy is of an older version of the file; then it is read whole.
// Close its Body when done. A part that starts after the file's end is a *NoSuchRange error.
func (d *Drive) Download(ctx context.Context, p index.Place, it index.Item, r *storage.Range, ifRange string) (*storage.Reading, error) {
	if it.Folder {
		return nil, ErrNotAFile
	}
	file, ok := itemPath(p, it)
	if !ok {
		return nil, ErrGone
	}
	f, err := d.storage.Read(ctx, file, r, ifRange)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		// Gone from the storage: its folder is looked at, which hides it from the list.
		var in *index.Item
		if len(it.Above) > 0 {
			in = &it.Above[len(it.Above)-1]
		}
		d.Recheck(p, in)
		return nil, ErrGone
	case errors.Is(err, storage.ErrNoSuchRange):
		size := int64(-1)
		sctx, cancel := context.WithTimeout(ctx, storageTimeout)
		defer cancel()
		if e, err := d.storage.Stat(sctx, file); err == nil && !e.Folder {
			size = e.Size
		}
		return nil, &NoSuchRange{Size: size}
	}
	return f, storageError(err)
}
