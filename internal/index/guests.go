package index

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Guests, as the index holds them: who they are (by address), the invitations to them (grants to a guest), the codes
// mailed to them to sign in with, and their sessions. internal/access decides what an invitation lets a guest do; these
// only read and change what is held.

// A Guest is someone outside the project something was given to by email address.
type Guest struct {
	ID int64
	// Address is their email address, as members.Address writes it.
	Address string
}

// ErrNoGuest is returned when there is no such guest.
var ErrNoGuest = errors.New("no such guest")

// ErrNoSession is returned for a session that does not exist, has ended, or whose guest has nothing that has not ended.
var ErrNoSession = errors.New("no such session")

func scanGuest(row interface{ Scan(...any) error }, more ...any) (Guest, error) {
	var g Guest
	err := row.Scan(append([]any{&g.ID, &g.Address}, more...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Guest{}, ErrNoGuest
	}
	return g, err
}

// GuestByAddress finds the guest whose address is address (as members.Address writes it).
func (x *Index) GuestByAddress(ctx context.Context, address string) (Guest, error) {
	return scanGuest(x.db.QueryRowContext(ctx, "SELECT id, address FROM guests WHERE address = ?", address))
}

// Guests returns every guest, by id.
func (x *Index) Guests(ctx context.Context) ([]Guest, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT id, address FROM guests ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Guest
	for rows.Next() {
		g, err := scanGuest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Invite invites the guest whose address is address (as members.Address writes it; a guest is recorded for it when
// there is none) to a live item of the place (ErrNoItem otherwise) as role, until ends (zero: no end date), given by the
// member by at the time given. An invitation that is there already gets the new role and end date, and is given anew
// by by when either changes. added says whether there was none: a new invitation, which its guest is told of.
func (x *Index) Invite(ctx context.Context, placeID, itemID int64, address, role string, ends, at time.Time, by string) (guest Guest, added bool, err error) {
	if itemID == 0 || by == "" {
		return Guest{}, false, errors.New("a guest is invited to an item, by a member")
	}
	err = x.write(ctx, func(tx *sql.Tx) error {
		if _, err := item(ctx, tx, placeID, itemID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO guests (address, added_at) VALUES (?, ?) ON CONFLICT (address) DO NOTHING",
			address, at.UnixMilli()); err != nil {
			return err
		}
		guest, err = scanGuest(tx.QueryRowContext(ctx, "SELECT id, address FROM guests WHERE address = ?", address))
		if err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM grants WHERE place_id = ? AND item_id = ? AND guest_id = ?",
			placeID, itemID, guest.ID).Scan(&n); err != nil {
			return err
		}
		added = n == 0
		_, err = tx.ExecContext(ctx, `
			INSERT INTO grants (place_id, item_id, grantee, guest_id, role, given_at, given_by, ends_at) VALUES (?, ?, 'guest', ?, ?, ?, ?, ?)
			ON CONFLICT (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''), IFNULL(guest_id, 0)) DO UPDATE
			SET role = excluded.role, ends_at = excluded.ends_at, given_at = excluded.given_at, given_by = excluded.given_by
			WHERE grants.role != excluded.role OR grants.ends_at IS NOT excluded.ends_at`,
			placeID, itemID, guest.ID, role, at.UnixMilli(), by, unixMilli(ends))
		return err
	})
	return guest, added && err == nil, err
}

// ChangeInvitation gives the invitation to guest on a live item of the place a new role and end date (zero: none).
// Whoever gave it stays its giver. ErrNoItem when there is no such invitation there.
func (x *Index) ChangeInvitation(ctx context.Context, placeID, itemID, guest int64, role string, ends time.Time) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := item(ctx, tx, placeID, itemID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE grants SET role = ?, ends_at = ? WHERE place_id = ? AND item_id = ? AND guest_id = ?`,
			role, unixMilli(ends), placeID, itemID, guest)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("no invitation of guest %d on item %d: %w", guest, itemID, ErrNoItem)
		}
		return nil
	})
}

// GrantedToGuest returns every item an invitation to the guest is on, each once, the most recently given first. It says
// nothing about whether it counts: internal/access decides that, on the whole way down to each.
func (x *Index) GrantedToGuest(ctx context.Context, guest int64) ([]Granted, error) {
	rows, err := x.db.QueryContext(ctx, `
		SELECT place_id, item_id FROM grants WHERE guest_id = ? AND item_id IS NOT NULL
		ORDER BY given_at DESC, id DESC`, guest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Granted
	for rows.Next() {
		var g Granted
		if err := rows.Scan(&g.Place, &g.Item); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// HasLiveInvitation says whether something was given to the guest that has not ended at the time given: what keeps a
// guest's sessions going, and their codes coming. (Whether it lets them in anywhere is internal/access's to say.)
func (x *Index) HasLiveInvitation(ctx context.Context, guest int64, now time.Time) (bool, error) {
	var n int
	err := x.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM grants WHERE guest_id = ? AND (ends_at IS NULL OR ends_at > ?)`,
		guest, now.UnixMilli()).Scan(&n)
	return n > 0, err
}

// BecomeMember gives every invitation to the guest to the member userID instead, as a grant to them by name (keeping
// its end date, and the higher role and the later end, or none, where the member was given something there already),
// and removes the guest, with their codes, sessions and uploads in progress: a guest who joined the project keeps their
// access as a member, tied to their user id from then on.
func (x *Index) BecomeMember(ctx context.Context, guest int64, userID string) error {
	if userID == "" {
		return errors.New("a guest becomes a member with a user id")
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO grants (place_id, item_id, grantee, member, role, given_at, given_by, ends_at)
			SELECT place_id, item_id, 'member', ?, role, given_at, given_by, ends_at FROM grants WHERE guest_id = ?
			ON CONFLICT (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''), IFNULL(guest_id, 0)) DO UPDATE
			SET role = CASE WHEN excluded.role = 'editor' OR grants.role = 'editor' THEN 'editor' ELSE grants.role END,
				ends_at = CASE WHEN grants.ends_at IS NULL OR excluded.ends_at IS NULL THEN NULL
					ELSE MAX(grants.ends_at, excluded.ends_at) END`, userID, guest); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM guests WHERE id = ?", guest)
		return err
	})
}

// Limits on guests' codes, which the caller passes.
type CodeLimits struct {
	// ValidFor is how long a code works after it was sent; Tries how many wrong tries kill it.
	ValidFor time.Duration
	Tries    int
	// PerGuest is how many codes one guest may be sent within Window; Overall how many all guests together.
	PerGuest, Overall int
	Window            time.Duration
	// Wrong is how many wrong tries of live codes the Drive takes within Window, from everyone together.
	Wrong int
}

// IssueCode records a code just mailed to the guest, whose hash (an HMAC) is given, at the time given, and spends
// every other code of theirs, if fewer than limits.PerGuest codes were sent to them, and fewer than limits.Overall to
// everyone, within limits.Window before it. ok is false otherwise, and nothing changes: the code must not be sent.
func (x *Index) IssueCode(ctx context.Context, guest int64, hash []byte, at time.Time, limits CodeLimits) (ok bool, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		since := at.Add(-limits.Window).UnixMilli()
		var mine, all int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_codes WHERE guest_id = ? AND sent_at > ?`, guest, since).Scan(&mine); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_codes WHERE sent_at > ?`, since).Scan(&all); err != nil {
			return err
		}
		if mine >= limits.PerGuest || all >= limits.Overall {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE guest_codes SET spent = 1 WHERE guest_id = ? AND spent = 0`, guest); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO guest_codes (guest_id, code_hash, sent_at) VALUES (?, ?, ?)`,
			guest, hash, at.UnixMilli()); err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok && err == nil, err
}

// TryResult is what a try of a code came to.
type TryResult int

const (
	// Wrong: the code is not the guest's newest live code, or there is none (or no such guest).
	Wrong TryResult = iota
	// SignedIn: it was, and it is spent now.
	SignedIn
	// TooManyTries: so many wrong codes were tried lately, by everyone together, that no try is taken now.
	TooManyTries
)

// wrongTries is the row of guest_limits that counts the wrong tries of live codes.
const wrongTries = "wrong codes"

// TryCode tries a code for the address, whose hash (an HMAC, made as IssueCode's was) is typed, at the time given.
//
// Every try does the same work, whatever the address: one change, which holds the count of wrong tries of everyone
// together, so that how long it takes does not tell whether the address has a code (or anything shared with it). A try
// of the guest's newest live code (not spent, sent within limits.ValidFor, fewer than limits.Tries wrong tries) that
// matches signs them in and spends it; one that does not counts as a wrong try of it, and towards limits.Wrong. While
// limits.Wrong wrong tries were made within limits.Window, no try is taken.
func (x *Index) TryCode(ctx context.Context, address string, typed []byte, at time.Time, limits CodeLimits) (guest Guest, result TryResult, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		guest, result = Guest{}, Wrong
		windowStart, count := at.UnixMilli(), 0
		var start int64
		var n int
		switch err := tx.QueryRowContext(ctx, `SELECT window_start, count FROM guest_limits WHERE name = ?`, wrongTries).Scan(&start, &n); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case start > at.Add(-limits.Window).UnixMilli():
			windowStart, count = start, n
		}
		save := func() error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO guest_limits (name, window_start, count) VALUES (?, ?, ?)
				ON CONFLICT (name) DO UPDATE SET window_start = excluded.window_start, count = excluded.count`,
				wrongTries, windowStart, count)
			return err
		}
		if count >= limits.Wrong {
			result = TooManyTries
			return save()
		}
		var id int64
		var held []byte
		g, err := scanGuest(tx.QueryRowContext(ctx, `
			SELECT guests.id, guests.address, IFNULL(c.id, 0), IFNULL(c.code_hash, zeroblob(32)) FROM guests
			LEFT JOIN guest_codes AS c ON c.id = (
				SELECT id FROM guest_codes WHERE guest_id = guests.id AND spent = 0 AND sent_at > ? AND tries < ?
				ORDER BY sent_at DESC, id DESC LIMIT 1)
			WHERE guests.address = ?`, at.Add(-limits.ValidFor).UnixMilli(), limits.Tries, address), &id, &held)
		if err != nil && !errors.Is(err, ErrNoGuest) {
			return err
		}
		if len(held) != len(typed) {
			held = make([]byte, len(typed))
		}
		matches := subtle.ConstantTimeCompare(held, typed) == 1
		switch {
		case id != 0 && matches:
			if _, err := tx.ExecContext(ctx, `UPDATE guest_codes SET spent = 1 WHERE guest_id = ?`, g.ID); err != nil {
				return err
			}
			guest, result = g, SignedIn
		case id != 0:
			if _, err := tx.ExecContext(ctx, `UPDATE guest_codes SET tries = tries + 1 WHERE id = ?`, id); err != nil {
				return err
			}
			count++
		}
		return save()
	})
	if err != nil {
		return Guest{}, Wrong, err
	}
	return guest, result, nil
}

// StartSession records a session of the guest, whose token's SHA-256 is hash, from started until ends, and keeps at
// most keep of their sessions: the oldest others end.
func (x *Index) StartSession(ctx context.Context, guest int64, hash []byte, started, ends time.Time, keep int) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO guest_sessions (token_hash, guest_id, started_at, ends_at) VALUES (?, ?, ?, ?)`,
			hash, guest, started.UnixMilli(), ends.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM guest_sessions WHERE guest_id = ? AND token_hash NOT IN (
				SELECT token_hash FROM guest_sessions WHERE guest_id = ? ORDER BY started_at DESC, token_hash LIMIT ?)`,
			guest, guest, keep)
		return err
	})
}

// SessionOf finds the guest whose session's token has hash as its SHA-256, and when the session ends: only while it has
// not ended at the time given, and while something given to the guest has not ended either (ErrNoSession otherwise).
func (x *Index) SessionOf(ctx context.Context, hash []byte, now time.Time) (Guest, time.Time, error) {
	var ends int64
	var held []byte
	g, err := scanGuest(x.db.QueryRowContext(ctx, `
		SELECT guests.id, guests.address, s.ends_at, s.token_hash FROM guest_sessions AS s JOIN guests ON guests.id = s.guest_id
		WHERE s.token_hash = ? AND s.ends_at > ?
		AND EXISTS (SELECT 1 FROM grants WHERE grants.guest_id = guests.id AND (grants.ends_at IS NULL OR grants.ends_at > ?))`,
		hash, now.UnixMilli(), now.UnixMilli()), &ends, &held)
	if errors.Is(err, ErrNoGuest) || (err == nil && subtle.ConstantTimeCompare(held, hash) != 1) {
		return Guest{}, time.Time{}, ErrNoSession
	}
	if err != nil {
		return Guest{}, time.Time{}, err
	}
	return g, time.UnixMilli(ends).UTC(), nil
}

// EndSession ends the session whose token's SHA-256 is hash, if there is one.
func (x *Index) EndSession(ctx context.Context, hash []byte) error {
	_, err := x.db.ExecContext(ctx, "DELETE FROM guest_sessions WHERE token_hash = ?", hash)
	return err
}

// TidyGuests removes what is of no use at the time given: sessions that have ended, the sessions of guests with nothing
// given to them that has not ended (they ended with it), and codes sent more than keepCodes ago.
func (x *Index) TidyGuests(ctx context.Context, now time.Time, keepCodes time.Duration) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM guest_sessions WHERE ends_at <= ?
			OR NOT EXISTS (SELECT 1 FROM grants WHERE grants.guest_id = guest_sessions.guest_id AND (grants.ends_at IS NULL OR grants.ends_at > ?))`,
			now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM guest_codes WHERE sent_at <= ?`, now.Add(-keepCodes).UnixMilli())
		return err
	})
}

// EndSessionsOf ends every session of the guest.
func (x *Index) EndSessionsOf(ctx context.Context, guest int64) error {
	_, err := x.db.ExecContext(ctx, "DELETE FROM guest_sessions WHERE guest_id = ?", guest)
	return err
}

// AddInvitationUses adds to invitations (by grant id) how often each was used, and when last, in one change. An
// invitation taken away meanwhile is left out.
func (x *Index) AddInvitationUses(ctx context.Context, uses map[int64]Use) error {
	if len(uses) == 0 {
		return nil
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		for id, u := range uses {
			if u.Count <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE grants SET uses = uses + ?, used_at = MAX(IFNULL(used_at, 0), ?) WHERE id = ? AND grantee = 'guest'`,
				u.Count, u.Last.UnixMilli(), id); err != nil {
				return fmt.Errorf("counting the uses of invitation %d: %w", id, err)
			}
		}
		return nil
	})
}

// unixMilli is a time as the index keeps it, NULL for zero.
func unixMilli(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}
