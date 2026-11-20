package drive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Public links, as the index holds them. The access package decides what a link lets its visitors do; these make,
// find and remove links, and count their uses.
//
// A link's address carries a token of TokenBytes random bytes from crypto/rand, written in base64url (TokenLength
// characters): it says nothing about the item, and cannot be guessed. The index holds only its SHA-256, so that a copy
// of the index, or a backup of it, hands out no working link; the token itself is given once, to the member who makes
// the link, and never again.

// TokenBytes is how many random bytes a link's token holds: 192 bits.
const TokenBytes = 24

// TokenLength is how many characters a link's token has in its address.
const TokenLength = (TokenBytes*8 + 5) / 6

// newToken makes a link's token.
func newToken() string {
	b := make([]byte, TokenBytes)
	_, _ = rand.Read(b) // crypto/rand never fails on the systems Go supports; it would crash the program before
	return base64.RawURLEncoding.EncodeToString(b)
}

// tokenHash is what the index holds of a token.
func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// MakeLink makes a public link of kind on a live item of the place, by the member by, and returns it with its token,
// which is never shown again. When the item has a link already, replace says to remove it first (its address stops
// working); without it, the error is index.ErrLinkExists. It is a change inside the place, so it takes the place's
// lock: no link is made in a shared folder that went to the trash (ErrPlaceGone), nor on an item that did
// (index.ErrNoItem).
func (d *Drive) MakeLink(ctx context.Context, p index.Place, it index.Item, kind, by string, replace bool) (index.Link, string, error) {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Link{}, "", err
	}
	defer leave()
	token := newToken()
	l, err := d.index.MakeLink(ctx, p.ID, it.ID, tokenHash(token), kind, by, d.now(), replace)
	if err != nil {
		return index.Link{}, "", err
	}
	return l, token, nil
}

// RemoveLink removes the public link on a live item of the place, if it has one: its address is dead for good. It
// takes the place's lock, as MakeLink does.
func (d *Drive) RemoveLink(ctx context.Context, p index.Place, it index.Item) error {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return err
	}
	defer leave()
	return d.index.RemoveLink(ctx, p.ID, it.ID)
}

// LinkByToken finds the link a token from an address opens, whatever the token is: anything that is not a link's
// token (one removed, one never made, one malformed) is index.ErrNoLink, found the same way, so that none of them
// answers differently or in a different time. The link is found wherever its item is: whether it lets anyone in is the
// access package's to say.
func (d *Drive) LinkByToken(ctx context.Context, token string) (index.Link, error) {
	hash := tokenHash(token)
	l, held, err := d.index.LinkByToken(ctx, hash)
	if err != nil {
		return index.Link{}, err
	}
	if subtle.ConstantTimeCompare(held, hash) != 1 {
		return index.Link{}, index.ErrNoLink
	}
	return d.withUses(l), nil
}

// LinkOn finds the public link on an item, wherever it is (index.ErrNoLink when it has none).
func (d *Drive) LinkOn(ctx context.Context, it index.Item) (index.Link, error) {
	l, err := d.index.LinkOn(ctx, it.ID)
	return d.withUses(l), err
}

// LinksOn returns the public links on the items given by id, such as an item and the folders above it.
func (d *Drive) LinksOn(ctx context.Context, items []int64) ([]index.Link, error) {
	return d.index.LinksOn(ctx, items)
}

// LinksInside returns the public links on the items directly in a folder of the place (nil: its top folder).
func (d *Drive) LinksInside(ctx context.Context, p index.Place, folder *index.Item) ([]index.Link, error) {
	return d.index.LinksInside(ctx, p.ID, folderID(folder))
}

// FirstChildren is what the index holds in a folder of the place (nil: its top folder): at most limit items, folders
// first, then by name, and whether there are more. It does not ask the storage, and takes no lock.
func (d *Drive) FirstChildren(ctx context.Context, p index.Place, folder *index.Item, limit int) ([]index.Item, bool, error) {
	return d.index.FirstChildren(ctx, p.ID, folderID(folder), limit)
}

// uses counts the uses of links in memory, between writes to the index: counting one never writes, never waits for
// the index or a place's lock, and never fails. WriteUses writes them, every so often and when the Drive stops; a
// Drive that is killed loses those counted since.
type uses struct {
	mu      sync.Mutex
	pending map[int64]index.Use
}

// UsedLink counts one use of the link, now.
func (d *Drive) UsedLink(l index.Link) {
	d.uses.mu.Lock()
	defer d.uses.mu.Unlock()
	if d.uses.pending == nil {
		d.uses.pending = map[int64]index.Use{}
	}
	u := d.uses.pending[l.ID]
	u.Count++
	u.Last = d.now().UTC()
	d.uses.pending[l.ID] = u
}

// withUses is the link with the uses counted and not yet written added.
func (d *Drive) withUses(l index.Link) index.Link {
	d.uses.mu.Lock()
	u, ok := d.uses.pending[l.ID]
	d.uses.mu.Unlock()
	if ok && l.ID != 0 {
		l.Uses += u.Count
		if u.Last.After(l.Used) {
			l.Used = u.Last
		}
	}
	return l
}

// WriteUses writes the uses of links counted since the last time into the index. What cannot be written is kept for
// the next time.
func (d *Drive) WriteUses(ctx context.Context) error {
	d.uses.mu.Lock()
	pending := d.uses.pending
	d.uses.pending = nil
	d.uses.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	err := d.index.AddUses(ctx, pending)
	if err != nil {
		d.uses.mu.Lock()
		for id, u := range d.uses.pending {
			p := pending[id]
			p.Count += u.Count
			if u.Last.After(p.Last) {
				p.Last = u.Last
			}
			pending[id] = p
		}
		d.uses.pending = pending
		d.uses.mu.Unlock()
	}
	return err
}

// KeepUses writes the uses of links every interval until ctx ends, and once more then.
func (d *Drive) KeepUses(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if err := d.WriteUses(wctx); err != nil {
				d.log.Error("the uses of public links could not be written as the Drive stopped; they are lost", "error", err)
			}
			cancel()
			return
		case <-tick.C:
			if err := d.WriteUses(ctx); err != nil {
				d.log.Warn("the uses of public links could not be written; they are tried again later", "error", err)
			}
		}
	}
}
