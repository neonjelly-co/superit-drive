package index

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"math"
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

// ErrTooManyUploads is returned when a person who starts an upload already has as many in progress as they may.
var ErrTooManyUploads = errors.New("too many uploads in progress")

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

// AddUpload records an upload that has just started, unless the person who starts it (u.Member, or u.Guest) already
// has most uploads in progress, in every place together: then it is ErrTooManyUploads, and nothing is recorded. Counting
// and recording are one statement, so uploads started at once never pass the limit together.
func (x *Index) AddUpload(ctx context.Context, u Upload, most int) error {
	res, err := x.db.ExecContext(ctx, `
		INSERT INTO uploads (id, place_id, folder_id, member, guest_id, name, size, started_at, touched_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE (SELECT COUNT(*) FROM uploads WHERE member = ? AND IFNULL(guest_id, 0) = ?) < ?`,
		u.ID, u.PlaceID, parent(u.FolderID), u.Member, parent(u.Guest), u.Name, u.Size, u.Started.UnixMilli(), u.Touched.UnixMilli(),
		u.Member, u.Guest, most)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrTooManyUploads)
	}
	return nil
}

// UploadsBy is how many uploads in progress member (a member's user id) or guest (a guest's id, with member "") has,
// in every place together.
func (x *Index) UploadsBy(ctx context.Context, member string, guest int64) (n int, err error) {
	err = x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM uploads WHERE member = ? AND IFNULL(guest_id, 0) = ?", member, guest).Scan(&n)
	return n, err
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

// UploadsTouchedBefore returns uploads no part of which arrived since the time given, the oldest first: at most limit
// of them, after the upload after (nil: from the first). Whoever works through them all asks a page at a time, each
// after the last of the one before, and so never holds more than a page, however many there are.
func (x *Index) UploadsTouchedBefore(ctx context.Context, before time.Time, after *Upload, limit int) ([]Upload, error) {
	from, fromID := int64(math.MinInt64), ""
	if after != nil {
		from, fromID = after.Touched.UnixMilli(), after.ID
	}
	rows, err := x.db.QueryContext(ctx, "SELECT "+uploadColumns+` FROM uploads
		WHERE touched_at < ? AND (touched_at, id) > (?, ?) ORDER BY touched_at, id LIMIT ?`, before.UnixMilli(), from, fromID, limit)
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

// UploadKnown says whether the index knows an upload in progress with id.
func (x *Index) UploadKnown(ctx context.Context, id string) (known bool, err error) {
	err = x.db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM uploads WHERE id = ?)", id).Scan(&known)
	return known, err
}
