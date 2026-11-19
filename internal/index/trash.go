package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// A TrashItem is an item in a place's trash.
type TrashItem struct {
	Item
	// Deleted is when it went to the trash.
	Deleted time.Time
	// Replaced is true when an upload replaced it, and false when a person deleted it.
	Replaced bool
	// Entry is its folder in the place's trash on the storage; the item is in it under its own name.
	Entry string
	// RestoreTo is the folder it was in when it went to the trash, or 0 for the place's top folder (and when that
	// folder has been removed for good).
	RestoreTo int64
	// DeletedBy is the user id of the member who put it in the trash (by deleting it, or replacing it with an upload),
	// or "" when that is not known.
	DeletedBy string
}

const trashColumns = itemColumns + ", items.trashed_at, items.trashed_why = 'replaced', items.trash_folder, IFNULL(items.restore_to, 0), IFNULL(items.trashed_by, '')"

func scanTrashItem(row interface{ Scan(...any) error }) (TrashItem, error) {
	var t TrashItem
	var deleted int64
	it, err := scanItem(row, &deleted, &t.Replaced, &t.Entry, &t.RestoreTo, &t.DeletedBy)
	t.Item = it
	t.Deleted = time.UnixMilli(deleted).UTC()
	return t, err
}

func scanTrashItems(rows *sql.Rows, err error) ([]TrashItem, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrashItem
	for rows.Next() {
		t, err := scanTrashItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Trash records that a live item of the place is now in the place's trash, in the folder entry there, put there by the
// member by. It keeps its id, and everything in it stays in it.
func (x *Index) Trash(ctx context.Context, placeID, id int64, at time.Time, entry, by string) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE items
			SET trashed_at = ?, trash_folder = ?, trashed_why = 'deleted', trashed_by = ?, restore_to = parent_id, parent_id = NULL
			WHERE id = ? AND place_id = ? AND trashed_at IS NULL`,
			at.UnixMilli(), entry, nullable(by), id, placeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("moving item %d to the trash in the index: %w", id, ErrNoItem)
		}
		return nil
	})
}

// AddReplaced records a file an upload by the member by replaced, now in the place's trash in the folder entry there,
// as a new item. The item at the file's name keeps its id: it is the same file with new content.
func (x *Index) AddReplaced(ctx context.Context, placeID, folderID int64, s Seen, at time.Time, entry, by string) (int64, error) {
	var id int64
	err := x.db.QueryRowContext(ctx, `
		INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at, trashed_at, trash_folder, trashed_why, trashed_by, restore_to)
		VALUES (?, NULL, ?, 0, ?, ?, ?, ?, 'replaced', ?, ?)
		RETURNING id`,
		placeID, s.Name, s.Size, s.Changed.Unix(), at.UnixMilli(), entry, nullable(by), parent(folderID)).Scan(&id)
	return id, err
}

// Restore records that an item of the place's trash is back in a folder of the place (folderID 0: the top folder)
// under name. The storage has already moved it there, so a row the index still holds under that name there is out of
// date and is dropped first.
func (x *Index) Restore(ctx context.Context, placeID, id, folderID int64, name string) (Item, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		if err := liveFolder(ctx, tx, placeID, folderID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM items WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL`,
			placeID, folderID, name)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE items
			SET parent_id = ?, name = ?, trashed_at = NULL, trash_folder = NULL, trashed_why = NULL, trashed_by = NULL, restore_to = NULL
			WHERE id = ? AND place_id = ? AND trashed_at IS NOT NULL`,
			parent(folderID), name, id, placeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("restoring item %d in the index: %w", id, ErrNoItem)
		}
		return nil
	})
	if err != nil {
		return Item{}, err
	}
	return x.Item(ctx, placeID, id)
}

// TrashItems returns what is in the place's trash, the most recently deleted first. A folder's Size is the sum of the
// files in it, as far as the index knows them.
func (x *Index) TrashItems(ctx context.Context, placeID int64) ([]TrashItem, error) {
	items, err := scanTrashItems(x.db.QueryContext(ctx, `
		SELECT `+trashColumns+` FROM items
		WHERE place_id = ? AND trashed_at IS NOT NULL
		ORDER BY trashed_at DESC, id DESC`, placeID))
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Folder {
			if items[i].Size, err = x.sizeInside(ctx, items[i].ID); err != nil {
				return nil, err
			}
		}
	}
	return items, nil
}

// sizeInside is the sum of the sizes of the files inside the folder id, at any depth.
func (x *Index) sizeInside(ctx context.Context, id int64) (int64, error) {
	var size int64
	err := x.db.QueryRowContext(ctx, `
		WITH RECURSIVE inside (id, depth) AS (
			SELECT ?, 0
			UNION ALL
			SELECT items.id, inside.depth + 1 FROM items JOIN inside ON items.parent_id = inside.id
			WHERE inside.depth < ?
		)
		SELECT IFNULL(SUM(items.size), 0) FROM inside JOIN items ON items.id = inside.id WHERE items.is_folder = 0`,
		id, MaxDepth).Scan(&size)
	return size, err
}

// CountInside counts the live items of the place with the ids given and everything inside them, at any depth: up to
// limit, where it stops counting.
func (x *Index) CountInside(ctx context.Context, placeID int64, ids []int64, limit int) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	list, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	var n int
	err = x.db.QueryRowContext(ctx, `
		WITH RECURSIVE inside (id, depth) AS (
			SELECT id, 0 FROM items
			WHERE place_id = ? AND trashed_at IS NULL AND id IN (SELECT value FROM json_each(?))
			UNION ALL
			SELECT items.id, inside.depth + 1 FROM items JOIN inside ON items.parent_id = inside.id
			WHERE inside.depth < ?
		)
		SELECT COUNT(*) FROM (SELECT 1 FROM inside LIMIT ?)`, placeID, string(list), MaxDepth, limit).Scan(&n)
	return n, err
}

// TrashItem finds the item with id in the place's trash, a folder's Size summed as in TrashItems. Something inside a
// folder in the trash is not found: it goes and comes back with its folder.
func (x *Index) TrashItem(ctx context.Context, placeID, id int64) (TrashItem, error) {
	t, err := scanTrashItem(x.db.QueryRowContext(ctx, `
		SELECT `+trashColumns+` FROM items WHERE id = ? AND place_id = ? AND trashed_at IS NOT NULL`, id, placeID))
	if err == nil && t.Folder {
		t.Size, err = x.sizeInside(ctx, t.ID)
	}
	return t, err
}

// TrashedBefore returns what went to the trash before the time given, in every place, the oldest first.
func (x *Index) TrashedBefore(ctx context.Context, before time.Time) ([]TrashItem, error) {
	return scanTrashItems(x.db.QueryContext(ctx, `
		SELECT `+trashColumns+` FROM items
		WHERE trashed_at IS NOT NULL AND trashed_at < ?
		ORDER BY trashed_at, id`, before.UnixMilli()))
}

// RemoveFromTrash removes an item of the trash from the index for good, with everything in it.
func (x *Index) RemoveFromTrash(ctx context.Context, placeID, id int64) error {
	_, err := x.db.ExecContext(ctx, "DELETE FROM items WHERE id = ? AND place_id = ? AND trashed_at IS NOT NULL", id, placeID)
	return err
}

// ForgetTrash removes from the index the items of the place's trash whose folders gone says are no longer on the
// storage, unless stale says the trash changed since the Drive looked.
func (x *Index) ForgetTrash(ctx context.Context, placeID int64, gone func(entry string) bool, stale func() bool) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		if stale() {
			return nil
		}
		rows, err := tx.QueryContext(ctx, "SELECT id, trash_folder FROM items WHERE place_id = ? AND trashed_at IS NOT NULL", placeID)
		if err != nil {
			return err
		}
		var drop []int64
		for rows.Next() {
			var id int64
			var entry string
			if err := rows.Scan(&id, &entry); err != nil {
				rows.Close()
				return err
			}
			if gone(entry) {
				drop = append(drop, id)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range drop {
			if _, err := tx.ExecContext(ctx, "DELETE FROM items WHERE id = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
}

// TrashEntries says which folders of the place's trash on the storage the index knows: those of its items, and the one
// holding the place's own top folder while a shared folder is in the trash.
func (x *Index) TrashEntries(ctx context.Context, placeID int64) (map[string]bool, error) {
	rows, err := x.db.QueryContext(ctx, `
		SELECT trash_folder FROM items WHERE place_id = ? AND trashed_at IS NOT NULL
		UNION ALL
		SELECT deleted_entry FROM places WHERE id = ? AND deleted_entry IS NOT NULL`, placeID, placeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			return nil, err
		}
		known[entry] = true
	}
	return known, rows.Err()
}

// Used is the sum of the sizes of every file the index knows, in every place, the trash included.
func (x *Index) Used(ctx context.Context) (int64, error) {
	var used int64
	err := x.db.QueryRowContext(ctx, "SELECT IFNULL(SUM(size), 0) FROM items WHERE is_folder = 0").Scan(&used)
	return used, err
}
