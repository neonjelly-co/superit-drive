package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MaxDepth is how many folders deep an item can be. A WebDAV server's paths run out long before (Apache's are limited
// to 4096 bytes on Linux); the limit also stops a walk up the tree from ever running on.
const MaxDepth = 256

// An Item is a file or folder inside a place.
type Item struct {
	ID      int64
	PlaceID int64
	// ParentID is the folder the item is in: 0 when it is directly in the place's top folder, and while it is in the
	// trash.
	ParentID int64
	Name     string
	Folder   bool
	Size     int64
	Changed  time.Time
	// Above is every folder the item is in, from the top of the place down to its own folder, each with its own
	// Above filled the same way. Only Item fills it.
	Above []Item
}

// A Seen item is a file or folder as the storage showed it.
type Seen struct {
	Name    string
	Folder  bool
	Size    int64
	Changed time.Time
}

// ErrNoItem is returned when an item does not exist where it was looked for.
var ErrNoItem = errors.New("no such item")

// ErrNoFolder is returned when something was to go into a folder that is not, or no longer, a live folder of the place:
// it went to the trash, was removed, or moved to another place meanwhile. It is an ErrNoItem too.
var ErrNoFolder = fmt.Errorf("%w: the folder is not in the place", ErrNoItem)

// itemColumns are read by scanItem, in this order.
const itemColumns = "items.id, items.place_id, IFNULL(items.parent_id, 0), items.name, items.is_folder, items.size, items.changed_at"

func scanItem(row interface{ Scan(...any) error }, more ...any) (Item, error) {
	var it Item
	var changed int64
	err := row.Scan(append([]any{&it.ID, &it.PlaceID, &it.ParentID, &it.Name, &it.Folder, &it.Size, &changed}, more...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNoItem
	}
	it.Changed = time.Unix(changed, 0).UTC()
	return it, err
}

func scanItems(rows *sql.Rows, err error) ([]Item, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// parent is a folder id as the items table holds it: NULL for the place's top folder.
func parent(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// Children returns what is directly in one folder of a place (folderID 0: its top folder), folders first, then by
// name. Items in the trash are not in any folder, and missing ones (the storage no longer shows them) are not listed.
func (x *Index) Children(ctx context.Context, placeID, folderID int64) ([]Item, error) {
	return scanItems(x.db.QueryContext(ctx, `
		SELECT `+itemColumns+` FROM items
		WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL AND missing_at IS NULL
		ORDER BY is_folder DESC, name`, placeID, folderID))
}

// FirstChildren is Children, at most limit of them, in the same order; more says there are others.
func (x *Index) FirstChildren(ctx context.Context, placeID, folderID int64, limit int) (items []Item, more bool, err error) {
	items, err = scanItems(x.db.QueryContext(ctx, `
		SELECT `+itemColumns+` FROM items
		WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL AND missing_at IS NULL
		ORDER BY is_folder DESC, name LIMIT ?`, placeID, folderID, limit+1))
	if len(items) > limit {
		return items[:limit], true, err
	}
	return items, false, err
}

// Item finds the item with id in the place, at any depth, with the folders above it in Above. It returns ErrNoItem for
// an id that is not there: one in another place, one in the trash or inside a folder in the trash, one missing from the
// storage or inside a missing folder, and one that does not exist. An item is only ever found inside the place it
// belongs to.
func (x *Index) Item(ctx context.Context, placeID, id int64) (Item, error) {
	return item(ctx, x.db, placeID, id)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func item(ctx context.Context, q querier, placeID, id int64) (Item, error) {
	// Walk up from the item to the top: each step adds the folder the last one is in.
	rows, err := q.QueryContext(ctx, `
		WITH RECURSIVE up (depth, id) AS (
			SELECT 0, ?
			UNION ALL
			SELECT up.depth + 1, items.parent_id FROM items JOIN up ON items.id = up.id
			WHERE items.parent_id IS NOT NULL AND up.depth <= ?
		)
		SELECT `+itemColumns+`, items.trashed_at IS NOT NULL OR items.missing_at IS NOT NULL FROM up JOIN items ON items.id = up.id
		ORDER BY up.depth DESC`, id, MaxDepth)
	if err != nil {
		return Item{}, err
	}
	defer rows.Close()
	var chain []Item // from the top down to the item
	for rows.Next() {
		var gone bool // in the trash, or missing from the storage
		it, err := scanItem(rows, &gone)
		if err != nil {
			return Item{}, err
		}
		if it.PlaceID != placeID || gone {
			return Item{}, ErrNoItem
		}
		chain = append(chain, it)
	}
	if err := rows.Err(); err != nil {
		return Item{}, err
	}
	if len(chain) == 0 || len(chain) > MaxDepth || chain[0].ParentID != 0 || chain[len(chain)-1].ID != id {
		return Item{}, ErrNoItem
	}
	// Each folder on the way down carries the folders above it too, so it can be used as a folder on its own.
	for i := range chain {
		chain[i].Above = chain[:i:i]
	}
	return chain[len(chain)-1], nil
}

// PutItem records one item the Drive itself just put in a folder of the place (folderID 0: its top folder), such as a
// file just uploaded or a folder just made, at the time given, and returns it. A live item of that name keeps its id, as
// long as it is the same kind of item: a file where a folder was (or the other way round) is a new item, and the other
// is marked missing. An item missing from the storage is never taken over: the new one has an id of its own.
func (x *Index) PutItem(ctx context.Context, placeID, folderID int64, s Seen, at time.Time) (Item, error) {
	var it Item
	err := x.write(ctx, func(tx *sql.Tx) error {
		if err := liveFolder(ctx, tx, placeID, folderID); err != nil {
			return err
		}
		if err := upsert(ctx, tx, placeID, folderID, s, at); err != nil {
			return err
		}
		var err error
		it, err = scanItem(tx.QueryRowContext(ctx, `
			SELECT `+itemColumns+` FROM items
			WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL AND missing_at IS NULL`,
			placeID, folderID, s.Name))
		return err
	})
	return it, err
}

// liveFolder checks that folderID is a folder of the place, not in the trash and not missing from the storage (0, the
// top folder, always is), before anything is put into it: ErrNoItem otherwise. A change decided on a folder that has
// since moved to another place, or gone, never records an item in one place under a folder of another.
func liveFolder(ctx context.Context, tx *sql.Tx, placeID, folderID int64) error {
	if folderID == 0 {
		return nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM items WHERE id = ? AND place_id = ? AND is_folder = 1 AND trashed_at IS NULL AND missing_at IS NULL`,
		folderID, placeID).Scan(&n)
	if err == nil && n == 0 {
		return fmt.Errorf("folder %d of place %d: %w", folderID, placeID, ErrNoFolder)
	}
	return err
}

// upsert adds or updates the live item called s.Name in the folder, keeping the id of a live item of that name and
// kind. A live item of the other kind there is marked missing at the time given, as the storage no longer has it: it is
// dropped later, as anything missing is (Reconcile).
func upsert(ctx context.Context, tx *sql.Tx, placeID, folderID int64, s Seen, at time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE items SET missing_at = ?
		WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL AND missing_at IS NULL AND is_folder != ?`,
		at.UnixMilli(), placeID, folderID, s.Name, s.Folder)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (place_id, IFNULL(parent_id, 0), name) WHERE trashed_at IS NULL AND missing_at IS NULL DO UPDATE
		SET size = excluded.size, changed_at = excluded.changed_at`,
		placeID, parent(folderID), s.Name, s.Folder, s.Size, s.Changed.Unix())
	return err
}

// MarkMissing marks an item the storage no longer has as missing, at the time given, if it is still where the Drive
// looked for it (in folderID, called name, not in the trash) and stale does not say the folder changed since the Drive
// looked. It is hidden from then on, and dropped only when a later look still finds it gone (Reconcile).
func (x *Index) MarkMissing(ctx context.Context, placeID, folderID, id int64, name string, at time.Time, stale func() bool) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if stale() {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE items SET missing_at = ?
			WHERE id = ? AND place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL AND missing_at IS NULL`,
			at.UnixMilli(), id, placeID, folderID, name)
		return err
	})
}

// ErrUploading: an upload in progress goes into the folder, or into a folder inside it.
var ErrUploading = errors.New("an upload in progress goes into it")

// MoveToPlace moves a live item of one place, with everything in it, into a folder of another place (folderID 0: its
// top folder) under name. The storage has already moved it there, so a row the index still holds under that name there
// is out of date and is dropped first. It keeps its id, and so do the items inside it.
//
// What was given on the item and on everything inside it goes with them (the foreign key on the grants' item and
// place follows the item). What was given on the folders above it in its old place stays with them, so it no longer
// reaches the item. An item in the old place's trash that was deleted out of it stays there, and goes back to that
// place's top folder. It refuses (ErrUploading) while an upload in progress goes into it or into a folder inside it:
// that upload belongs to the old place.
func (x *Index) MoveToPlace(ctx context.Context, fromPlace, id, toPlace, folderID int64, name string) (Item, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		if busy, err := uploading(ctx, tx, fromPlace, id); err != nil {
			return err
		} else if busy {
			return ErrUploading
		}
		if err := liveFolder(ctx, tx, toPlace, folderID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM items
			WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL AND missing_at IS NULL`,
			toPlace, folderID, name); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, insideItem+`
			UPDATE items SET place_id = ? WHERE id IN (SELECT id FROM inside)`, id, fromPlace, MaxDepth, toPlace)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n < 1 {
			return fmt.Errorf("moving item %d to another place in the index: %w", id, ErrNoItem)
		}
		_, err = tx.ExecContext(ctx, `UPDATE items SET parent_id = ?, name = ? WHERE id = ? AND place_id = ?`,
			parent(folderID), name, id, toPlace)
		return err
	})
	if err != nil {
		return Item{}, err
	}
	return x.Item(ctx, toPlace, id)
}

// insideItem is a live item of a place (its id, the place's id) and everything inside it, at any depth (MaxDepth).
const insideItem = `
	WITH RECURSIVE inside (id, depth) AS (
		SELECT id, 0 FROM items WHERE id = ? AND place_id = ? AND trashed_at IS NULL
		UNION ALL
		SELECT items.id, inside.depth + 1 FROM items JOIN inside ON items.parent_id = inside.id
		WHERE inside.depth < ?
	)`

// Uploading says whether an upload in progress goes into the live item of the place, or into a folder inside it.
func (x *Index) Uploading(ctx context.Context, placeID, id int64) (bool, error) {
	return uploading(ctx, x.db, placeID, id)
}

func uploading(ctx context.Context, q querier, placeID, id int64) (bool, error) {
	rows, err := q.QueryContext(ctx, insideItem+`
		SELECT 1 FROM uploads WHERE place_id = ? AND folder_id IN (SELECT id FROM inside) LIMIT 1`, id, placeID, MaxDepth, placeID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// Relocate gives a live item a new folder in its place (folderID 0: the top folder) and a new name: a rename, a move,
// or both. The storage has already moved it, so a live row the index still holds under the new name there is out of
// date and is dropped first. A missing one is left to be dropped as anything missing is: the moved item is not it.
func (x *Index) Relocate(ctx context.Context, placeID, id, folderID int64, name string) (Item, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		if err := liveFolder(ctx, tx, placeID, folderID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM items
			WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL AND missing_at IS NULL AND id != ?`,
			placeID, folderID, name, id)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE items SET parent_id = ?, name = ? WHERE id = ? AND place_id = ? AND trashed_at IS NULL AND missing_at IS NULL`,
			parent(folderID), name, id, placeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("moving item %d in the index: %w", id, ErrNoItem)
		}
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return x.Item(ctx, placeID, id)
}
