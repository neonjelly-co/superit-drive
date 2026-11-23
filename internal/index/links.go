package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// A Link is a public link on an item, as the index holds it: never its token, which only its address carries, and
// only the token's SHA-256 inside the index. The index only holds links; internal/access decides what each lets
// visitors do.
type Link struct {
	ID int64
	// Item is the id of the item the link is on, and Place the place that item is in now; Folder says whether that item
	// is a folder.
	Item, Place int64
	Folder      bool
	// Kind is what it lets visitors do: ViewLink or UploadLink.
	Kind string
	// Made is when it was made, and MadeBy the user id of the member who made it.
	Made   time.Time
	MadeBy string
	// Uses is how often it was used, and Used when it last was (zero before its first use), as written so far.
	Uses int64
	Used time.Time
	// TakenIn is, for an upload-only link, how many bytes of files it has taken in since it was made or last reset, and
	// TakenFiles how many files.
	TakenIn, TakenFiles int64
}

// The kinds of link: a view link lets visitors see and download what it is on; an upload-only link, on a folder only,
// lets them put new files into it and see nothing.
const (
	ViewLink   = "view"
	UploadLink = "upload-only"
)

// ErrNoLink is returned when there is no such link.
var ErrNoLink = errors.New("no such link")

// ErrLinkExists is returned when an item that has a link is given another without replacing it.
var ErrLinkExists = errors.New("the item has a public link already")

// ErrLinkKind is returned for a link of a kind that cannot be on its item: an upload-only link on a file, or a kind
// there is not.
var ErrLinkKind = errors.New("a link of this kind cannot be on this item")

// linkColumns are read by scanLink, in this order. Every query of links joins the item it is on, for its place.
const linkColumns = "links.id, links.item_id, items.place_id, items.is_folder, links.kind, links.made_at, links.made_by, links.uses, " +
	"links.used_at, links.taken_in, links.taken_files"

func scanLink(row interface{ Scan(...any) error }, more ...any) (Link, error) {
	var l Link
	var made int64
	var used sql.NullInt64
	err := row.Scan(append([]any{&l.ID, &l.Item, &l.Place, &l.Folder, &l.Kind, &made, &l.MadeBy, &l.Uses, &used, &l.TakenIn, &l.TakenFiles}, more...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNoLink
	}
	l.Made = time.UnixMilli(made).UTC()
	if used.Valid {
		l.Used = time.UnixMilli(used.Int64).UTC()
	}
	return l, err
}

func scanLinks(rows *sql.Rows, err error) ([]Link, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LinkByToken finds the link whose token's SHA-256 is hash, and returns it with the hash it holds, for the caller to
// compare. It is found wherever its item is, in the trash too: whether it lets anyone in is not the index's to say.
func (x *Index) LinkByToken(ctx context.Context, hash []byte) (Link, []byte, error) {
	var held []byte
	l, err := scanLink(x.db.QueryRowContext(ctx, "SELECT "+linkColumns+`, links.token_hash
		FROM links JOIN items ON items.id = links.item_id WHERE links.token_hash = ?`, hash), &held)
	return l, held, err
}

// LinkOn finds the link on the item, if it has one (ErrNoLink otherwise).
func (x *Index) LinkOn(ctx context.Context, itemID int64) (Link, error) {
	return scanLink(x.db.QueryRowContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id WHERE links.item_id = ?`, itemID))
}

// LinksOn returns the links on the items given by id, such as an item and the folders above it.
func (x *Index) LinksOn(ctx context.Context, items []int64) ([]Link, error) {
	if len(items) == 0 {
		return nil, nil
	}
	list, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return scanLinks(x.db.QueryContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id
		WHERE links.item_id IN (SELECT value FROM json_each(?)) ORDER BY links.id`, string(list)))
}

// LinksInside returns the links on the items directly in one folder of a place (folderID 0: its top folder), leaving
// out items in the trash.
func (x *Index) LinksInside(ctx context.Context, placeID, folderID int64) ([]Link, error) {
	return scanLinks(x.db.QueryContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id
		WHERE items.place_id = ? AND IFNULL(items.parent_id, 0) = ? AND items.trashed_at IS NULL AND items.missing_at IS NULL
		ORDER BY links.id`,
		placeID, folderID))
}

// linkFits says whether a link of kind can be on it: a view link on anything, an upload-only link on a folder only.
func linkFits(kind string, it Item) bool {
	return kind == ViewLink || (kind == UploadLink && it.Folder)
}

// MakeLink records a new link of kind on a live item of the place (ErrNoItem otherwise), whose token's SHA-256 is hash,
// made by the member by at the time given. An upload-only link goes on a folder only (ErrLinkKind otherwise). When the
// item has a link already, replace says to remove it first, and so end its address; without it, nothing changes and the
// error is ErrLinkExists.
func (x *Index) MakeLink(ctx context.Context, placeID, itemID int64, hash []byte, kind, by string, at time.Time, replace bool) (Link, error) {
	var id int64
	err := x.write(ctx, func(tx *sql.Tx) error {
		it, err := item(ctx, tx, placeID, itemID)
		if err != nil {
			return err
		}
		if !linkFits(kind, it) {
			return ErrLinkKind
		}
		if replace {
			if _, err := tx.ExecContext(ctx, "DELETE FROM links WHERE item_id = ?", itemID); err != nil {
				return err
			}
		} else {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM links WHERE item_id = ?", itemID).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrLinkExists
			}
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO links (item_id, token_hash, kind, made_at, made_by) VALUES (?, ?, ?, ?, ?) RETURNING id`,
			itemID, hash, kind, at.UnixMilli(), by).Scan(&id)
	})
	if err != nil {
		return Link{}, err
	}
	return scanLink(x.db.QueryRowContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id WHERE links.id = ?`, id))
}

// SetLinkKind changes what the link on a live item of the place lets visitors do, keeping its address, its uses and what
// it has taken in: ErrNoLink when the item has none, ErrLinkKind for an upload-only link on a file.
func (x *Index) SetLinkKind(ctx context.Context, placeID, itemID int64, kind string) (Link, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		it, err := item(ctx, tx, placeID, itemID)
		if err != nil {
			return err
		}
		if !linkFits(kind, it) {
			return ErrLinkKind
		}
		// Only when it changes: the same kind again is no access change.
		_, err = tx.ExecContext(ctx, "UPDATE links SET kind = ? WHERE item_id = ? AND kind != ?", kind, itemID, kind)
		return err
	})
	if err != nil {
		return Link{}, err
	}
	return x.LinkOn(ctx, itemID)
}

// ResetLink starts the count of what the link on a live item of the place has taken in again from 0: ErrNoLink when the
// item has none.
func (x *Index) ResetLink(ctx context.Context, placeID, itemID int64) (Link, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		if _, err := item(ctx, tx, placeID, itemID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE links SET taken_in = 0, taken_files = 0 WHERE item_id = ?", itemID)
		return err
	})
	if err != nil {
		return Link{}, err
	}
	return x.LinkOn(ctx, itemID)
}

// LinkByID finds a link by its id, wherever its item is (ErrNoLink when there is none).
func (x *Index) LinkByID(ctx context.Context, id int64) (Link, error) {
	return scanLink(x.db.QueryRowContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id WHERE links.id = ?`, id))
}

// TakenIn adds one file of n bytes to what the link with id has taken in: an empty file is a file too. A link removed
// meanwhile is left out. It is not an access change: the trigger that counts those leaves both counts out.
func (x *Index) TakenIn(ctx context.Context, id, n int64) error {
	if n < 0 {
		return nil
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE links SET taken_in = taken_in + ?, taken_files = taken_files + 1 WHERE id = ?", n, id)
		return err
	})
}

// RemoveLink removes the link on a live item of the place, if it has one: its address is dead for good.
func (x *Index) RemoveLink(ctx context.Context, placeID, itemID int64) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := item(ctx, tx, placeID, itemID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM links WHERE item_id = ?", itemID)
		return err
	})
}

// A Use is how often a link was used since its uses were last written, and when last.
type Use struct {
	Count int64
	Last  time.Time
}

// AddUses adds to links (by id) how often each was used, and when last, in one change. A link removed meanwhile is
// left out.
func (x *Index) AddUses(ctx context.Context, uses map[int64]Use) error {
	if len(uses) == 0 {
		return nil
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		for id, u := range uses {
			if u.Count <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE links SET uses = uses + ?, used_at = MAX(IFNULL(used_at, 0), ?) WHERE id = ?`,
				u.Count, u.Last.UnixMilli(), id); err != nil {
				return fmt.Errorf("counting the uses of link %d: %w", id, err)
			}
		}
		return nil
	})
}
