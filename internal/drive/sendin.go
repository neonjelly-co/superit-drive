package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// Files sent in through an upload-only public link. A visitor with one puts new files into the folder it is on, and
// sees nothing of what is there: a file whose name is taken is stored beside it under a free name (" (2)" and so on),
// never in its place, and the visitor is told neither. Each file is one request with its length, streamed straight to
// a file of its own in the hidden uploads folder, as a member's upload in one request is; the link is decided on again
// once the file is whole, just before it is moved into its folder, so a link removed, made a view link, or no longer
// counting meanwhile (sharing outside the project switched off, its maker no longer able to share the folder, the folder
// in the trash) stores nothing.
//
// An upload-only link takes in at most LinkLimit bytes in all, until whoever may manage it resets it. Each file's
// length is kept for it before any of its bytes are read, so files on their way at once never take in more than the
// limit together, whatever they say about themselves: a file is exactly as long as it said (the request's length), or
// it is cut off and stores nothing.

// DefaultLinkLimit is how much an upload-only link takes in, in bytes, before it stops until it is reset: 10 GB, as
// storage is sold (1 GB is 10^9 bytes).
const DefaultLinkLimit = 10_000_000_000

// ErrLinkFull: the upload-only link has taken in all it may until it is reset (or this file would take it past that).
var ErrLinkFull = errors.New("the link has taken in all it may until it is reset")

// intake keeps, per upload-only link, the bytes of the files on their way through it, which count against its limit
// until they are stored (and then count in the index) or not.
type intake struct {
	mu       sync.Mutex
	reserved map[int64]int64
}

// LinkLimit is how much an upload-only link takes in, in bytes, before it stops until it is reset.
func (d *Drive) LinkLimit() int64 { return d.linkLimit }

// keepFor keeps n bytes of what the link with id link may take in, for one file on its way: ErrLinkFull when what the
// link has taken in, what is on its way and n would pass its limit. settle gives them back, counting them as taken in
// when stored is true; once, however often it is called.
func (d *Drive) keepFor(ctx context.Context, link, n int64) (settle func(stored bool) error, err error) {
	d.intake.mu.Lock()
	defer d.intake.mu.Unlock()
	l, err := d.index.LinkByID(ctx, link)
	if err != nil {
		return nil, err
	}
	// Written so that no sum can overflow, whatever n a request says. Once full, not even an empty file.
	if n < 0 || n > d.linkLimit-l.TakenIn-d.intake.reserved[link] || l.TakenIn >= d.linkLimit {
		return nil, ErrLinkFull
	}
	if d.intake.reserved == nil {
		d.intake.reserved = map[int64]int64{}
	}
	d.intake.reserved[link] += n
	var once sync.Once
	return func(stored bool) (err error) {
		once.Do(func() {
			d.intake.mu.Lock()
			defer d.intake.mu.Unlock()
			if stored {
				// Counted before it is given back: whoever asks next sees it in one or the other.
				ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storageTimeout)
				err = d.index.TakenIn(ctx, link, n)
				cancel()
			}
			if d.intake.reserved[link] -= n; d.intake.reserved[link] <= 0 {
				delete(d.intake.reserved, link)
			}
		})
		return err
	}, nil
}

// SendIn stores a file a visitor sends through the upload-only link with id link, called name, size bytes long, read
// from body, into the folder the link is on in the place p, and returns it. decide is the link decided on again, once
// the file is whole and the place is held: it returns the place and the folder as they are now (the place read again,
// with its switches and whether it is held), or the error to answer with. The
// file never replaces anything: when its name is taken (on the storage, or by a change on its way), it is stored under
// the first free name with " (2)", " (3)" and so on, labelled as having come through a link. Nothing about what was
// there comes back.
//
// ErrLinkFull before anything is read when the file would take the link past its limit; ErrCutOff when the visitor's
// connection ended before the whole file arrived (nothing is stored, nothing counted).
func (d *Drive) SendIn(ctx context.Context, p index.Place, link int64, name string, body io.Reader, size int64,
	decide func(context.Context) (index.Place, index.Item, error)) (index.Item, error) {
	if err := CheckName(name); err != nil {
		return index.Item{}, err
	}
	if _, ok := pathIn("x", name); !ok {
		return index.Item{}, &BadName{"This name cannot be used. Choose another name."} // CheckName refuses these first
	}
	settle, err := d.keepFor(ctx, link, size)
	if err != nil {
		return index.Item{}, err
	}
	stored := false
	defer func() {
		if err := settle(stored); err != nil {
			d.log.Error("what an upload-only link took in could not be counted", "link", link, "error", err)
		}
	}()
	if err := d.makeUploadsFolder(ctx); err != nil {
		return index.Item{}, err
	}
	partial := partialOf(newUploadID())
	from := &bodyReader{r: body}
	err = d.storage.Put(ctx, partial, from, size)
	switch {
	case from.err != nil:
		d.removePartial(ctx, partial) // a storage other than Apache may keep what arrived
		return index.Item{}, fmt.Errorf("%w: %w", ErrCutOff, from.err)
	case errors.Is(err, storage.ErrFull):
		return index.Item{}, ErrFull
	case err != nil:
		return index.Item{}, storageError(err)
	}
	it, err := d.sentInPlace(ctx, p, link, name, partial, decide)
	if err != nil {
		d.removePartial(ctx, partial)
		return index.Item{}, err
	}
	stored = true
	return it, nil
}

// sentInPlace decides on a whole file sent through a link again, holding the place, and moves it into the folder the
// link is on now, under the first free name from name.
func (d *Drive) sentInPlace(ctx context.Context, p index.Place, link int64, name, partial string,
	decide func(context.Context) (index.Place, index.Item, error)) (index.Item, error) {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Item{}, err
	}
	defer leave()
	p, folder, err := decide(ctx)
	if err != nil {
		return index.Item{}, err
	}
	dir, ok := folderPath(p, &folder)
	if !ok {
		return index.Item{}, ErrGone
	}
	// The move never replaces anything: something put on the storage directly, which the index does not know yet, makes
	// it fail, and the next free name is tried, a few times at most.
	from := 1
	for range 3 {
		n, free, release, err := d.freeSentName(ctx, p, &folder, dir, name, from)
		if err != nil {
			return index.Item{}, err
		}
		file, _ := pathIn(dir, free)
		it, err := d.putInPlace(ctx, p, &folder, partial, file, free, By{Link: link})
		release()
		var taken *Taken
		if errors.As(err, &taken) {
			from = n + 1
			continue
		}
		return it, err
	}
	return index.Item{}, &Taken{Name: name}
}

// freeSentName is the first name, from the from-th on (1: name itself, n: name with " (n)" added, as keeping both
// names it), that no item of the index has in the folder at dir (live, or missing from the storage) and no change on
// its way there holds; it holds that name until release is called, and n says which it was.
//
// It asks the index and the Drive's own claims, never the storage: a look there takes a round trip for every name that
// is taken, and how long a visitor's file took to be answered would tell them which names are in the folder. Asked of
// the index, a taken name costs a lookup that is lost in the time any request takes. (A name only the storage has, put
// there directly and not yet seen by a look, still costs the move that finds it: that is all a visitor could tell, and
// only about such a file.)
func (d *Drive) freeSentName(ctx context.Context, p index.Place, folder *index.Item, dir, name string, from int) (n int, free string, release func(), err error) {
	for n = max(from, 1); n < 1000; n++ {
		candidate := name
		if n > 1 {
			candidate = numbered(name, n, false)
		}
		path, ok := pathIn(dir, candidate)
		if !ok {
			continue
		}
		release, ok := d.claim(path)
		if !ok {
			continue
		}
		used, err := d.index.NameUsed(ctx, p.ID, folderID(folder), candidate)
		if err != nil {
			release()
			return 0, "", nil, err
		}
		if !used {
			return n, candidate, release, nil
		}
		release()
	}
	return 0, "", nil, &Taken{Name: name}
}
