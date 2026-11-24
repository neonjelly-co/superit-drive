package drive

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Guests: someone outside the project invited by email address to a file or folder, who signs in on the public prefix
// with a code mailed to them. The access package decides what an invitation lets a guest do; these invite, find, sign
// in and out, and count uses.
//
// A code is what a sign-in link carries: 32 bytes from crypto/rand, in base64url, far too long to guess. The index
// keeps only its HMAC-SHA-256, keyed with Options.CodeKey (which the index never holds), over the guest's address and
// the code: a copy of the index tells no code. A session's token is 32 random bytes; the index keeps its SHA-256.
//
// Because a code cannot be guessed, nothing a stranger does at the sign-in page takes a guest's sign-in away: wrong
// tries are not counted (there is nothing to protect by counting them), asking again leaves the codes already sent
// working, and the only bound a stranger can reach for one address is how often it is mailed (CodeEvery, CodesPerHour), which is
// shorter than a code works for (CodeValidFor), so that the guest always has a link that works in their mail.

// The rules of guests' codes and sessions.
const (
	// CodeValidFor is how long a code works after it was sent.
	CodeValidFor = 30 * time.Minute
	// CodeEvery is how long after a code was sent to an address the next one may be.
	CodeEvery = 5 * time.Minute
	// CodesPerHour is how many codes one address is sent in an hour, used or not: as many as CodeEvery lets through when
	// none is used. A guest who uses each link as it comes is sent no more than that either.
	CodesPerHour = 12
	// CodesPerHourOverall is how many codes all addresses together are sent in an hour: a bound on the mail the
	// sign-in page can make the Drive send. It takes more than eighty guests' addresses, asked for as often as they are
	// answered, to reach it.
	CodesPerHourOverall = 1000
	// SessionLasts is how long a guest stays signed in on a browser.
	SessionLasts = 30 * 24 * time.Hour
	// SessionsKept is how many browsers one guest stays signed in on at once: signing in on another ends the oldest.
	SessionsKept = 10
)

var codeLimits = index.CodeLimits{ValidFor: CodeValidFor, Every: CodeEvery, PerGuest: CodesPerHour, Overall: CodesPerHourOverall, Window: time.Hour}

// Errors about guests.
var (
	// ErrWrongCode: the code is not right, has ended, was used, or there is no code for the address.
	ErrWrongCode = errors.New("the code is not right")
)

// GuestByAddress finds the guest whose address is address, as members.Address writes it (index.ErrNoGuest otherwise).
func (d *Drive) GuestByAddress(ctx context.Context, address string) (index.Guest, error) {
	return d.index.GuestByAddress(ctx, members.Address(address))
}

// Invite invites the guest whose address is given to a live item of the place, as role, until ends (zero: no end date),
// by the member by, and returns them. invited says whether they had no invitation there: they are mailed about it. It
// is a change inside the place, so it takes the place's lock, as Give does.
func (d *Drive) Invite(ctx context.Context, p index.Place, it index.Item, address, role string, ends time.Time, by string) (guest index.Guest, invited bool, err error) {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return index.Guest{}, false, err
	}
	defer leave()
	return d.index.Invite(ctx, p.ID, it.ID, members.Address(address), role, ends, d.now(), by)
}

// ChangeInvitation gives the guest's invitation on a live item of the place a new role and end date (zero: none), by
// the member by, who becomes its giver when either changes. It takes the place's lock, as Give does.
func (d *Drive) ChangeInvitation(ctx context.Context, p index.Place, it index.Item, guest int64, role string, ends time.Time, by string) error {
	leave, err := d.inPlace(ctx, p)
	if err != nil {
		return err
	}
	defer leave()
	return d.index.ChangeInvitation(ctx, p.ID, it.ID, guest, role, ends, by)
}

// GrantedToGuest returns every item an invitation to the guest is on, the most recently given first: where their
// "Shared with me" looks. Whether each counts is for the access package to say.
func (d *Drive) GrantedToGuest(ctx context.Context, guest int64) ([]index.Granted, error) {
	return d.index.GrantedToGuest(ctx, guest)
}

// WithUses is grants with the uses of invitations counted and not yet written added.
func (d *Drive) WithUses(grants []index.Grant) []index.Grant { return d.withInvitationUses(grants) }

// HasLiveInvitation says whether something was given to the guest that has not ended.
func (d *Drive) HasLiveInvitation(ctx context.Context, guest int64) (bool, error) {
	return d.index.HasLiveInvitation(ctx, guest, d.now())
}

// AdmitMembers makes every guest whose address is in the member list a member: what was given to them as a guest and
// still works is given to that member, by user id, and the guest is gone (their sessions end). working says which
// invitations to a guest those are, as the access package judges them: the rest are dropped with the guest, so that
// nothing that was taken away comes back. A member who leaves never becomes a guest. It returns how many it made
// members.
func (d *Drive) AdmitMembers(ctx context.Context, list *members.List, working func(context.Context, index.Guest) ([]index.Granted, error)) (int, error) {
	guests, err := d.index.Guests(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, g := range guests {
		m, ok := list.ByEmail(g.Address)
		if !ok {
			continue
		}
		keep, err := working(ctx, g)
		if err != nil {
			return n, fmt.Errorf("what guest %d, joining the project as %s, was given: %w", g.ID, m.UserID, err)
		}
		if err := d.index.BecomeMember(ctx, g.ID, m.UserID, keep, d.now()); err != nil {
			return n, fmt.Errorf("guest %d joining the project as %s: %w", g.ID, m.UserID, err)
		}
		d.log.Info("a guest joined the project: what was given to them and still worked is theirs as a member now",
			"guest", g.ID, "member", m.UserID, "kept", len(keep))
		n++
	}
	return n, nil
}

// NewCode makes a code for the guest and records its HMAC, unless one was sent to them within CodeEvery
// (index.TooSoon), or CodesPerHourOverall to everyone in the last hour (index.TooMany): then no code may be sent. The
// codes sent to them before go on working.
func (d *Drive) NewCode(ctx context.Context, guest index.Guest, key []byte) (code string, result index.IssueResult, err error) {
	code = newCode()
	result, err = d.index.IssueCode(ctx, guest.ID, codeHash(key, guest.Address, code), d.now(), codeLimits)
	if err != nil || result != index.Issued {
		return "", result, err
	}
	return code, index.Issued, nil
}

// SignIn uses a code for an address, and with the right one starts a session for its guest: the session's token, for
// the browser's cookie, and when it ends. ErrWrongCode for anything else.
func (d *Drive) SignIn(ctx context.Context, address, code string, key []byte) (guest index.Guest, token string, ends time.Time, err error) {
	address = members.Address(address)
	now := d.now()
	g, ok, err := d.index.UseCode(ctx, address, codeHash(key, address, code), now, codeLimits)
	switch {
	case err != nil:
		return index.Guest{}, "", time.Time{}, err
	case !ok:
		return index.Guest{}, "", time.Time{}, ErrWrongCode
	}
	token = newSessionToken()
	ends = now.Add(SessionLasts)
	if err := d.index.StartSession(ctx, g.ID, tokenHash(token), now, ends, SessionsKept); err != nil {
		return index.Guest{}, "", time.Time{}, err
	}
	return g, token, ends, nil
}

// SessionOf finds the guest whose session token is given, and when it ends: only while it has not ended, and while
// something given to them has not (index.ErrNoSession otherwise).
func (d *Drive) SessionOf(ctx context.Context, token string) (index.Guest, time.Time, error) {
	if len(token) != sessionTokenLength {
		return index.Guest{}, time.Time{}, index.ErrNoSession
	}
	return d.index.SessionOf(ctx, tokenHash(token), d.now())
}

// SignOut ends the session whose token is given.
func (d *Drive) SignOut(ctx context.Context, token string) error {
	return d.index.EndSession(ctx, tokenHash(token))
}

// EndSessionsOf ends every session of the guest.
func (d *Drive) EndSessionsOf(ctx context.Context, guest int64) error {
	return d.index.EndSessionsOf(ctx, guest)
}

// tidyGuests removes sessions that ended (with all they had given ended too) and codes older than the hour they count
// for.
func (d *Drive) tidyGuests(ctx context.Context) error {
	return d.index.TidyGuests(ctx, d.now(), time.Hour)
}

// CodeLength is how many characters a code has: 32 random bytes in base64url.
const CodeLength = 43

// newCode is 32 bytes from crypto/rand, in base64url: what a sign-in link carries.
func newCode() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// codeHash is what the index keeps of a code mailed to address: an HMAC-SHA-256 keyed with key.
func codeHash(key []byte, address, code string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(address))
	m.Write([]byte{0})
	m.Write([]byte(code))
	return m.Sum(nil)
}

// sessionTokenLength is how many characters a session's token has in its cookie: 32 random bytes in base64url.
const sessionTokenLength = 43

func newSessionToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
