package index

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SharedFolders returns every shared folder that is not in the trash, by name.
func (x *Index) SharedFolders(ctx context.Context) ([]Place, error) {
	return scanPlaces(x.db.QueryContext(ctx, "SELECT "+placeColumns+` FROM places
		WHERE kind = ? AND deleted_at IS NULL ORDER BY name, id`, SharedFolder))
}

// DeletedSharedFolders returns every shared folder in the trash, the most recently deleted first.
func (x *Index) DeletedSharedFolders(ctx context.Context) ([]Place, error) {
	return scanPlaces(x.db.QueryContext(ctx, "SELECT "+placeColumns+` FROM places
		WHERE kind = ? AND deleted_at IS NOT NULL ORDER BY deleted_at DESC, id DESC`, SharedFolder))
}

// SharedFoldersDeletedBefore returns the shared folders that went to the trash before the time given, the oldest first.
func (x *Index) SharedFoldersDeletedBefore(ctx context.Context, before time.Time) ([]Place, error) {
	return scanPlaces(x.db.QueryContext(ctx, "SELECT "+placeColumns+` FROM places
		WHERE kind = ? AND deleted_at IS NOT NULL AND deleted_at < ? ORDER BY deleted_at, id`, SharedFolder, before.UnixMilli()))
}

// AddSharedFolder records a new shared folder called name, whose top folder on the storage is folder, and gives
// manager (a member's user id) the manager role on it, in one change.
func (x *Index) AddSharedFolder(ctx context.Context, name, folder, manager string, at time.Time) (Place, error) {
	var id int64
	err := x.write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
			INSERT INTO places (kind, name, folder, created_at) VALUES (?, ?, ?, ?) RETURNING id`,
			SharedFolder, name, folder, at.Unix()).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO grants (place_id, item_id, grantee, member, role, given_at, given_by)
			VALUES (?, NULL, 'member', ?, 'manager', ?, ?)`, id, manager, at.UnixMilli(), manager)
		return err
	})
	if err != nil {
		return Place{}, err
	}
	return x.PlaceByID(ctx, id)
}

// DeleteSharedFolder records that a shared folder is now in the trash: its top folder is in the entry called entry
// in its own trash folder on the storage. Its items, its own trash and its grants stay as they are.
func (x *Index) DeleteSharedFolder(ctx context.Context, id int64, at time.Time, entry string) error {
	res, err := x.db.ExecContext(ctx, `
		UPDATE places SET deleted_at = ?, deleted_entry = ? WHERE id = ? AND kind = ? AND deleted_at IS NULL`,
		at.UnixMilli(), entry, id, SharedFolder)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("moving shared folder %d to the trash in the index: %w", id, ErrNoPlace)
	}
	return nil
}

// RestoreSharedFolder records that a shared folder in the trash is back in its own top folder.
func (x *Index) RestoreSharedFolder(ctx context.Context, id int64) error {
	res, err := x.db.ExecContext(ctx, `
		UPDATE places SET deleted_at = NULL, deleted_entry = NULL WHERE id = ? AND deleted_at IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("restoring shared folder %d in the index: %w", id, ErrNoPlace)
	}
	return nil
}

// RemoveSharedFolder removes a shared folder in the trash from the index for good, with its items, its trash and its
// grants.
func (x *Index) RemoveSharedFolder(ctx context.Context, id int64) error {
	_, err := x.db.ExecContext(ctx, "DELETE FROM places WHERE id = ? AND kind = ? AND deleted_at IS NOT NULL", id, SharedFolder)
	return err
}

// SizeOfPlace is the sum of the sizes of the files the index knows in a place, its trash included.
func (x *Index) SizeOfPlace(ctx context.Context, id int64) (int64, error) {
	var size int64
	err := x.db.QueryRowContext(ctx, "SELECT IFNULL(SUM(size), 0) FROM items WHERE place_id = ? AND is_folder = 0", id).Scan(&size)
	return size, err
}
