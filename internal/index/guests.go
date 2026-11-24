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

// ChangeInvitation gives the invitation to guest on a live item of the place a new role and end date (zero: none), by
// the member by. When either changes, by becomes its giver, as when an invitation is given again (Invite): in a My
// files an invitation counts only while its giver may still share, so what one person made of another's invitation
// never outlasts their own access under the other's name. ErrNoItem when there is no such invitation there.
func (x *Index) ChangeInvitation(ctx context.Context, placeID, itemID, guest int64, role string, ends time.Time, by string) error {
	if by == "" {
		return errors.New("an invitation is changed by a member")
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := item(ctx, tx, placeID, itemID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE grants SET given_by = CASE WHEN role != ?1 OR ends_at IS NOT ?2 THEN ?3 ELSE given_by END, role = ?1, ends_at = ?2
			WHERE place_id = ?4 AND item_id = ?5 AND guest_id = ?6`,
			role, unixMilli(ends), by, placeID, itemID, guest)
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

// BecomeMember gives the invitations to the guest on the items of keep to the member userID instead, as grants to them
// by name (each with its end date), and removes the guest, with every other invitation to them, their codes, sessions
// and uploads in progress: a guest who joined the project keeps their access as a member, tied to their user id from
// then on. Which invitations still work is the caller's to say (keep): one that does not is not carried over, or what
// was taken from the guest would come back to the member.
//
// It never gives more than was given. An invitation the member gave themselves is dropped (nobody shares with
// themselves). Where the member was given something on the item already, the invitation takes its place only when it
// is no less on both counts (its role no lower, its end no earlier), or when what the member was given has ended by
// now (it gives nothing any more, and must not stand in the way of an invitation that works); otherwise the member's
// grant stays as it is: never the role of one with the end of the other.
func (x *Index) BecomeMember(ctx context.Context, guest int64, userID string, keep []Granted, now time.Time) error {
	if userID == "" {
		return errors.New("a guest becomes a member with a user id")
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		for _, k := range keep {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO grants (place_id, item_id, grantee, member, role, given_at, given_by, ends_at)
				SELECT place_id, item_id, 'member', ?1, role, given_at, given_by, ends_at FROM grants
				WHERE guest_id = ?2 AND place_id = ?3 AND item_id = ?4 AND IFNULL(given_by, '') <> ?1
				ON CONFLICT (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''), IFNULL(guest_id, 0)) DO UPDATE
				SET role = excluded.role, ends_at = excluded.ends_at, given_at = excluded.given_at, given_by = excluded.given_by
				WHERE (grants.ends_at IS NOT NULL AND grants.ends_at <= ?5)
					OR ((excluded.role = 'editor' OR grants.role = 'viewer')
						AND (excluded.ends_at IS NULL OR (grants.ends_at IS NOT NULL AND excluded.ends_at >= grants.ends_at)))`,
				userID, guest, k.Place, k.Item, now.UnixMilli()); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM guests WHERE id = ?", guest)
		return err
	})
}

// Limits on guests' codes, which the caller passes. A code is the long secret a sign-in link carries.
type CodeLimits struct {
	// ValidFor is how long a code works after it was sent.
	ValidFor time.Duration
	// Every is how long after a code was sent to a guest, and not used yet, the next one may be: asked for sooner, none
	// is sent, and the one sent then still works (ValidFor must be longer, so that the guest always has a code that
	// works within reach). Once they have used it, they may be sent another at once, up to PerGuest.
	Every time.Duration
	// PerGuest is how many codes one guest may be sent within Window, used or not: without it, a guest who uses each
	// link as it comes could be sent the next at once, without end, and take all of Overall for themselves.
	PerGuest int
	// Overall is how many codes all guests together may be sent within Window: a bound on the mail the sign-in page can
	// make the Drive send, set well above what a few addresses reach.
	Overall int
	Window  time.Duration
}

// IssueResult is what asking for a code came to.
type IssueResult int

const (
	// Issued: the code was recorded, and may be sent.
	Issued IssueResult = iota
	// TooSoon: a code was sent to the guest within limits.Every and is not used yet (it works), or limits.PerGuest were
	// sent to them within limits.Window; no other is sent.
	TooSoon
	// TooMany: limits.Overall codes were sent to everyone within limits.Window; none is sent now.
	TooMany
)

// IssueCode records a code about to be mailed to the guest, whose hash (an HMAC) is given, at the time given, unless
// one was sent to them within limits.Every and not used, or limits.PerGuest to them within limits.Window (TooSoon), or
// limits.Overall to everyone within limits.Window (TooMany):
// then nothing changes, and the code must not be sent. A guest's earlier codes go on working until they end or one of
// them is used: whoever asks in a guest's name makes none of theirs useless.
func (x *Index) IssueCode(ctx context.Context, guest int64, hash []byte, at time.Time, limits CodeLimits) (result IssueResult, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		result = Issued
		var mine, all int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_codes WHERE guest_id = ? AND spent = 0 AND sent_at > ?`,
			guest, at.Add(-limits.Every).UnixMilli()).Scan(&mine); err != nil {
			return err
		}
		if mine > 0 {
			result = TooSoon
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_codes WHERE guest_id = ? AND sent_at > ?`,
			guest, at.Add(-limits.Window).UnixMilli()).Scan(&mine); err != nil {
			return err
		}
		if mine >= limits.PerGuest {
			result = TooSoon
			return nil
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM guest_codes WHERE sent_at > ?`,
			at.Add(-limits.Window).UnixMilli()).Scan(&all); err != nil {
			return err
		}
		if all >= limits.Overall {
			result = TooMany
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO guest_codes (guest_id, code_hash, sent_at) VALUES (?, ?, ?)`,
			guest, hash, at.UnixMilli())
		return err
	})
	return result, err
}

// UseCode signs the guest with the address in with a code, whose hash (an HMAC, made as IssueCode's was) is given, at
// the time given: ok when it is one of that guest's codes, sent within limits.ValidFor and not used yet. It is used
// then, and works no more; their other codes are as they were.
//
// A code is too long to guess, so nothing counts wrong tries: no number of them, for any address, stops a right code
// from signing its guest in. A try is one look for the hash, the same whatever the address, so how long it takes does
// not tell whether the address is a guest's.
func (x *Index) UseCode(ctx context.Context, address string, hash []byte, at time.Time, limits CodeLimits) (guest Guest, ok bool, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		guest, ok = Guest{}, false
		var id int64
		g, err := scanGuest(tx.QueryRowContext(ctx, `
			SELECT guests.id, guests.address, c.id FROM guest_codes AS c JOIN guests ON guests.id = c.guest_id
			WHERE c.code_hash = ? AND guests.address = ? AND c.spent = 0 AND c.sent_at > ?`,
			hash, address, at.Add(-limits.ValidFor).UnixMilli()), &id)
		if errors.Is(err, ErrNoGuest) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE guest_codes SET spent = 1 WHERE id = ?`, id); err != nil {
			return err
		}
		guest, ok = g, true
		return nil
	})
	if err != nil {
		return Guest{}, false, err
	}
	return guest, ok, nil
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
