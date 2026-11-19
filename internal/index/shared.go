package index

import (
	"context"
	"database/sql"
	"errors"
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
		_, err = tx.ExecContext(ctx, "INSERT INTO grants (place_id, member, role, given_at) VALUES (?, ?, 'manager', ?)",
			id, manager, at.UnixMilli())
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

// A Grant is a role given to a member on a place.
type Grant struct {
	// Member is the member's stable user id. They may since have left the project.
	Member string
	// Role is "manager", so far.
	Role string
	// Given is when it was given.
	Given time.Time
}

// Grants returns the roles given on a place, the earliest first.
func (x *Index) Grants(ctx context.Context, placeID int64) ([]Grant, error) {
	return grants(ctx, x.db, placeID)
}

func grants(ctx context.Context, q querier, placeID int64) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, "SELECT member, role, given_at FROM grants WHERE place_id = ? ORDER BY given_at, member", placeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		var given int64
		if err := rows.Scan(&g.Member, &g.Role, &given); err != nil {
			return nil, err
		}
		g.Given = time.UnixMilli(given).UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

// Given is the role given to the member on a place, or "" for none.
func (x *Index) Given(ctx context.Context, placeID int64, member string) (string, error) {
	var role string
	err := x.db.QueryRowContext(ctx, "SELECT role FROM grants WHERE place_id = ? AND member = ?", placeID, member).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// GivenTo returns the role given to the member on each place they were given one on, by place id.
func (x *Index) GivenTo(ctx context.Context, member string) (map[int64]string, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT place_id, role FROM grants WHERE member = ?", member)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var role string
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		out[id] = role
	}
	return out, rows.Err()
}

// Give gives the member a role on a place, in place of any role they had there.
func (x *Index) Give(ctx context.Context, placeID int64, member, role string, at time.Time) error {
	_, err := x.db.ExecContext(ctx, `
		INSERT INTO grants (place_id, member, role, given_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (place_id, member) DO UPDATE SET role = excluded.role, given_at = excluded.given_at
		WHERE grants.role != excluded.role`,
		placeID, member, role, at.UnixMilli())
	return err
}

// TakeAway takes away the role given to the member on a place, if they have one. check is shown the roles that would
// be left, while nobody else can change them; an error from it changes nothing and is returned.
func (x *Index) TakeAway(ctx context.Context, placeID int64, member string, check func(left []Grant) error) error {
	return x.write(ctx, func(tx *sql.Tx) error {
		all, err := grants(ctx, tx, placeID)
		if err != nil {
			return err
		}
		left := make([]Grant, 0, len(all))
		for _, g := range all {
			if g.Member != member {
				left = append(left, g)
			}
		}
		if len(left) == len(all) {
			return nil // nothing to take away
		}
		if err := check(left); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM grants WHERE place_id = ? AND member = ?", placeID, member)
		return err
	})
}
