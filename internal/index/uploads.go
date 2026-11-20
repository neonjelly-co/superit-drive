package index

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// An Upload is a resumable upload in progress: started, and not yet finished or cancelled.
type Upload struct {
	// ID is random; it also names the upload's file in the hidden uploads folder on the storage.
	ID      string
	PlaceID int64
	// FolderID is the folder it goes into, or 0 for the place's top folder.
	FolderID int64
	// Member is the user id of the member uploading, or Guest the id of the guest (Member is then ""). Nobody else may
	// continue, finish or cancel it.
	Member string
	Guest  int64
	// Name is the name it was started under; Size the length of the whole file, in bytes.
	Name string
	Size int64
	// Started is when it started, Touched when a part last arrived.
	Started, Touched time.Time
}

// ErrNoUpload is returned when an upload in progress does not exist where it was looked for.
var ErrNoUpload = errors.New("no such upload")

const uploadColumns = "id, place_id, IFNULL(folder_id, 0), member, IFNULL(guest_id, 0), name, size, started_at, touched_at"

func scanUpload(row interface{ Scan(...any) error }) (Upload, error) {
	var u Upload
	var started, touched int64
	err := row.Scan(&u.ID, &u.PlaceID, &u.FolderID, &u.Member, &u.Guest, &u.Name, &u.Size, &started, &touched)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNoUpload
	}
	u.Started, u.Touched = time.UnixMilli(started).UTC(), time.UnixMilli(touched).UTC()
	return u, err
}

// AddUpload records an upload that has just started.
func (x *Index) AddUpload(ctx context.Context, u Upload) error {
	_, err := x.db.ExecContext(ctx, `
		INSERT INTO uploads (id, place_id, folder_id, member, guest_id, name, size, started_at, touched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.PlaceID, parent(u.FolderID), u.Member, parent(u.Guest), u.Name, u.Size, u.Started.UnixMilli(), u.Touched.UnixMilli())
	return err
}

// UploadOf finds the upload with id in the place, if member (a member's user id) or guest (a guest's id, with member
// "") started it. Another person's upload, or one of another place, is not found.
func (x *Index) UploadOf(ctx context.Context, placeID int64, id, member string, guest int64) (Upload, error) {
	if (member == "") == (guest == 0) {
		return Upload{}, ErrNoUpload // nobody, or two people at once
	}
	return scanUpload(x.db.QueryRowContext(ctx, `SELECT `+uploadColumns+` FROM uploads
		WHERE id = ? AND place_id = ? AND member = ? AND IFNULL(guest_id, 0) = ?`, id, placeID, member, guest))
}

// TouchUpload records that a part of the upload arrived at the time given.
func (x *Index) TouchUpload(ctx context.Context, id string, at time.Time) error {
	_, err := x.db.ExecContext(ctx, "UPDATE uploads SET touched_at = ? WHERE id = ?", at.UnixMilli(), id)
	return err
}

// RemoveUpload forgets an upload: it finished, was cancelled, or was abandoned.
func (x *Index) RemoveUpload(ctx context.Context, id string) error {
	_, err := x.db.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id)
	return err
}

// UploadsTouchedBefore returns the uploads no part of which arrived since the time given, the oldest first.
func (x *Index) UploadsTouchedBefore(ctx context.Context, before time.Time) ([]Upload, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT "+uploadColumns+" FROM uploads WHERE touched_at < ? ORDER BY touched_at", before.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UploadIDs says which uploads the index knows, by id.
func (x *Index) UploadIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT id FROM uploads")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		known[id] = true
	}
	return known, rows.Err()
}
