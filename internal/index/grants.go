package index

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// A Grant is access given to a member, to everyone in the project, or to a guest (an invitation), on a place's top
// folder or on one item in it. The index only holds grants; internal/access decides what each is worth.
type Grant struct {
	// ID is the grant's own id, never given to another.
	ID int64
	// Item is the id of the item it was given on, or 0 for the place's top folder.
	Item int64
	Grantee
	// GuestAddress is, for an invitation, the guest's address.
	GuestAddress string
	// Role is "viewer", "editor" or "manager".
	Role string
	// Given is when it was given; By is the user id of the member who gave it, or "" when that is not known.
	Given time.Time
	By    string
	// Ends is when it stops counting, and zero for never.
	Ends time.Time
	// Uses is, for an invitation, how often its guest used it, and Used when last (zero before the first use), as
	// written so far.
	Uses int64
	Used time.Time
}

// A Grantee is who access is given to: everyone in the project, one member, or one guest.
type Grantee struct {
	// Everyone is true for everyone in the project; Member and Guest are then empty.
	Everyone bool
	// Member is the member's stable user id. They may since have left the project.
	Member string
	// Guest is the guest's id.
	Guest int64
}

// columns are the grantee as the grants table holds it.
func (g Grantee) columns() (grantee string, member, guest any) {
	switch {
	case g.Everyone:
		return "everyone", nil, nil
	case g.Guest != 0:
		return "guest", nil, g.Guest
	}
	return "member", g.Member, nil
}

// grantColumns are read by scanGrants, in this order; every query of them joins the guest a grant may be to.
const grantColumns = `grants.id, IFNULL(grants.item_id, 0), grants.grantee = 'everyone', IFNULL(grants.member, ''),
	IFNULL(grants.guest_id, 0), IFNULL(guests.address, ''), grants.role, grants.given_at, IFNULL(grants.given_by, ''),
	grants.ends_at, grants.uses, grants.used_at`

// grantsJoined is the grants with the guests they may be to.
const grantsJoined = "grants LEFT JOIN guests ON guests.id = grants.guest_id"

func scanGrant(row interface{ Scan(...any) error }, more ...any) (Grant, error) {
	var g Grant
	var given int64
	var ends, used sql.NullInt64
	if err := row.Scan(append([]any{&g.ID, &g.Item, &g.Everyone, &g.Member, &g.Guest, &g.GuestAddress, &g.Role, &given, &g.By,
		&ends, &g.Uses, &used}, more...)...); err != nil {
		return Grant{}, err
	}
	g.Given = time.UnixMilli(given).UTC()
	if ends.Valid {
		g.Ends = time.UnixMilli(ends.Int64).UTC()
	}
	if used.Valid {
		g.Used = time.UnixMilli(used.Int64).UTC()
	}
	return g, nil
}

func scanGrants(rows *sql.Rows, err error) ([]Grant, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GrantsOn returns what was given on a place's top folder and on the items given by id, in the order it was given.
// Items in the trash keep theirs.
func (x *Index) GrantsOn(ctx context.Context, placeID int64, items []int64) ([]Grant, error) {
	return grantsOn(ctx, x.db, placeID, items)
}

func grantsOn(ctx context.Context, q querier, placeID int64, items []int64) ([]Grant, error) {
	query := "SELECT " + grantColumns + " FROM " + grantsJoined + " WHERE grants.place_id = ? AND (grants.item_id IS NULL"
	args := []any{placeID}
	if len(items) > 0 {
		query += " OR grants.item_id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(items)), ", ") + ")"
		for _, id := range items {
			args = append(args, id)
		}
	}
	return scanGrants(q.QueryContext(ctx, query+") ORDER BY grants.given_at, grants.id", args...))
}

// grantsOnOne returns what was given on one item of a place, or on its top folder (itemID 0).
func grantsOnOne(ctx context.Context, q querier, placeID, itemID int64) ([]Grant, error) {
	return scanGrants(q.QueryContext(ctx, "SELECT "+grantColumns+" FROM "+grantsJoined+`
		WHERE grants.place_id = ? AND IFNULL(grants.item_id, 0) = ? ORDER BY grants.given_at, grants.id`, placeID, itemID))
}

// GrantsInside returns what was given on the items directly in one folder of a place (folderID 0: its top folder),
// leaving out items in the trash.
func (x *Index) GrantsInside(ctx context.Context, placeID, folderID int64) ([]Grant, error) {
	return scanGrants(x.db.QueryContext(ctx, "SELECT "+grantColumns+" FROM "+grantsJoined+`
		WHERE grants.place_id = ? AND grants.item_id IN (
			SELECT id FROM items WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL AND missing_at IS NULL
		)
		ORDER BY grants.given_at, grants.id`, placeID, placeID, folderID))
}

// TopGrantsFor returns, by place id, what was given on places' top folders (shared folders as a whole) to the member
// or to everyone in the project.
func (x *Index) TopGrantsFor(ctx context.Context, member string) (map[int64][]Grant, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT "+grantColumns+", grants.place_id FROM "+grantsJoined+`
		WHERE grants.item_id IS NULL AND (grants.grantee = 'everyone' OR grants.member = ?) ORDER BY grants.given_at, grants.id`, member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Grant{}
	for rows.Next() {
		var place int64
		g, err := scanGrant(rows, &place)
		if err != nil {
			return nil, err
		}
		out[place] = append(out[place], g)
	}
	return out, rows.Err()
}

// Give gives to (a member, or everyone; a guest is invited with Invite) a role on one item of a place, or on its top
// folder (itemID 0), in place of what was given to them there; by is the member who gives it. The item must be in the
// place and not in the trash (ErrNoItem otherwise). check, when not nil, is shown everything given there afterwards,
// while nobody else can change it; an error from it changes nothing and is returned. added says whether nothing was
// given to them there before: a new share, rather than a role changed or given again. Given again, a grant keeps no
// end date (a guest who joined the project keeps theirs until then).
func (x *Index) Give(ctx context.Context, placeID, itemID int64, to Grantee, role string, at time.Time, by string, check func(after []Grant) error) (added bool, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		if itemID != 0 {
			if _, err := item(ctx, tx, placeID, itemID); err != nil {
				return err
			}
		}
		before, err := grantsOnOne(ctx, tx, placeID, itemID)
		if err != nil {
			return err
		}
		added = true
		after := make([]Grant, 0, len(before)+1)
		for _, g := range before {
			if g.Grantee != to {
				after = append(after, g)
			} else {
				added = false
			}
		}
		after = append(after, Grant{Item: itemID, Grantee: to, Role: role, Given: at, By: by})
		if check != nil {
			if err := check(after); err != nil {
				return err
			}
		}
		if to.Guest != 0 {
			return errors.New("a guest is invited with Invite")
		}
		grantee, member, _ := to.columns()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO grants (place_id, item_id, grantee, member, role, given_at, given_by) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (place_id, IFNULL(item_id, 0), grantee, IFNULL(member, ''), IFNULL(guest_id, 0)) DO UPDATE
			SET role = excluded.role, given_at = excluded.given_at, given_by = excluded.given_by, ends_at = NULL
			WHERE grants.role != excluded.role OR grants.ends_at IS NOT NULL`,
			placeID, parent(itemID), grantee, member, role, at.UnixMilli(), nullable(by))
		return err
	})
	return added && err == nil, err
}

// A Granted item is one that something was given on, to a member or to everyone in the project: where to look when
// working out what was shared with them.
type Granted struct {
	Place, Item int64
}

// GrantedTo returns every item that something was given on to the member, or to everyone in the project, each once,
// the most recently given first. It says nothing about whether it counts, whether the item is in the trash, or whether
// the person may open it: internal/access decides that, on the whole way down to each.
func (x *Index) GrantedTo(ctx context.Context, member string) ([]Granted, error) {
	rows, err := x.db.QueryContext(ctx, `
		SELECT place_id, item_id FROM grants
		WHERE item_id IS NOT NULL AND (grantee = 'everyone' OR member = ?)
		GROUP BY place_id, item_id
		ORDER BY MAX(given_at) DESC, MAX(rowid) DESC -- given in the same millisecond: the one given last first
		`, member)
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

// TakeAway takes away what was given to from on one item of a place, or on its top folder (itemID 0), if anything
// was. check, when not nil, is shown what is left given there, while nobody else can change it; an error from it
// changes nothing and is returned.
func (x *Index) TakeAway(ctx context.Context, placeID, itemID int64, from Grantee, check func(after []Grant) error) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		before, err := grantsOnOne(ctx, tx, placeID, itemID)
		if err != nil {
			return err
		}
		after := make([]Grant, 0, len(before))
		for _, g := range before {
			if g.Grantee != from {
				after = append(after, g)
			}
		}
		if len(after) == len(before) {
			return nil // nothing to take away
		}
		if check != nil {
			if err := check(after); err != nil {
				return err
			}
		}
		grantee, member, guest := from.columns()
		_, err = tx.ExecContext(ctx, `
			DELETE FROM grants WHERE place_id = ? AND IFNULL(item_id, 0) = ? AND grantee = ? AND IFNULL(member, '') = IFNULL(?, '')
			AND IFNULL(guest_id, 0) = IFNULL(?, 0)`,
			placeID, itemID, grantee, member, guest)
		return err
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
