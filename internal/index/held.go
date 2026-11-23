package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A held My files is one whose owner is no longer in the member list. The index keeps nothing about being held (the
// member list says it), only the acts that end it: a project admin hands the files, whole, to another member, or
// deletes them. Each act is kept for good, as the record of who did it, when, and what. Its storage steps come first and
// the index's last; until the index's step it is under way, and its My files stays held whatever the member list says
// (Place.Acting).

// The kinds of act on a held My files.
const (
	// HandOver moves the files, whole, into a folder of their own in another member's My files.
	HandOver = "hand-over"
	// DeleteHeld removes them for good.
	DeleteHeld = "delete"
)

// An Act is a hand-over or a deletion of one held My files, by a project admin.
type Act struct {
	ID int64
	// Kind is HandOver or DeleteHeld.
	Kind string
	// Place is the held My files while the act is under way; 0 once it is done (the place is gone).
	Place int64
	// Owner is the removed member's user id; Folder their My files' top folder on the storage (named after their email
	// address when they first opened the Drive).
	Owner, Folder string
	// For a hand-over: ToMember is the member it goes to, ToPlace their My files (0 once that is gone itself), and ToName
	// the folder it becomes in its top folder.
	ToMember string
	ToPlace  int64
	ToName   string
	// Files and Size are how many files, and how many bytes, were in it (its trash aside) when the act began.
	Files, Size int64
	// By is the project admin who did it, At when they did; Done when the Drive finished it (zero while it is under way).
	By   string
	At   time.Time
	Done time.Time
}

// ErrNoAct is returned when a place has no act under way.
var ErrNoAct = errors.New("no act under way")

// ErrActing is returned when an act cannot begin because another one is under way on one of its places.
var ErrActing = errors.New("another act is under way there")

const actColumns = "id, act, IFNULL(place_id, 0), owner, folder, IFNULL(to_member, ''), IFNULL(to_place, 0), IFNULL(to_name, ''), " +
	"files, size, by, at, done_at"

func scanAct(row interface{ Scan(...any) error }) (Act, error) {
	var a Act
	var at int64
	var done sql.NullInt64
	err := row.Scan(&a.ID, &a.Kind, &a.Place, &a.Owner, &a.Folder, &a.ToMember, &a.ToPlace, &a.ToName, &a.Files, &a.Size, &a.By, &at, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return Act{}, ErrNoAct
	}
	a.At = time.UnixMilli(at).UTC()
	if done.Valid {
		a.Done = time.UnixMilli(done.Int64).UTC()
	}
	return a, err
}

func scanActs(rows *sql.Rows, err error) ([]Act, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Act
	for rows.Next() {
		a, err := scanAct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActOn returns the act under way on a place (ErrNoAct when there is none).
func (x *Index) ActOn(ctx context.Context, placeID int64) (Act, error) {
	return scanAct(x.db.QueryRowContext(ctx, "SELECT "+actColumns+" FROM held_acts WHERE place_id = ? AND done_at IS NULL", placeID))
}

// Act returns the act with the id given.
func (x *Index) Act(ctx context.Context, id int64) (Act, error) {
	return scanAct(x.db.QueryRowContext(ctx, "SELECT "+actColumns+" FROM held_acts WHERE id = ?", id))
}

// ActsUnderWay returns every act not yet done, the oldest first.
func (x *Index) ActsUnderWay(ctx context.Context) ([]Act, error) {
	return scanActs(x.db.QueryContext(ctx, "SELECT "+actColumns+" FROM held_acts WHERE done_at IS NULL ORDER BY id"))
}

// Acts returns every act, the most recent first: the record.
func (x *Index) Acts(ctx context.Context) ([]Act, error) {
	return scanActs(x.db.QueryContext(ctx, "SELECT "+actColumns+" FROM held_acts ORDER BY at DESC, id DESC"))
}

// liveInside is every live item of a place (not in the trash, not inside a folder in the trash, not missing from the
// storage nor inside a missing folder), from its top folder down, at most MaxDepth deep: its id. One place's argument.
const liveInside = `
	WITH RECURSIVE live (id, depth) AS (
		SELECT id, 0 FROM items WHERE place_id = ? AND parent_id IS NULL AND trashed_at IS NULL AND missing_at IS NULL
		UNION ALL
		SELECT items.id, live.depth + 1 FROM items JOIN live ON items.parent_id = live.id
		WHERE items.missing_at IS NULL AND live.depth < ?
	)`

// Contents is how many files a place holds, and how many bytes, outside its trash, as far as the index knows them.
func (x *Index) Contents(ctx context.Context, placeID int64) (files, size int64, err error) {
	return contents(ctx, x.db, placeID)
}

func contents(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, placeID int64) (files, size int64, err error) {
	err = q.QueryRowContext(ctx, liveInside+`
		SELECT COUNT(*), IFNULL(SUM(items.size), 0) FROM live JOIN items ON items.id = live.id WHERE items.is_folder = 0`,
		placeID, MaxDepth).Scan(&files, &size)
	return files, size, err
}

// StartAct records that an act on a held My files (a.Place) begins: a.Kind, a.By, a.At, and for a hand-over a.ToMember,
// a.ToPlace (their My files) and a.ToName. Whose files they are and where (Owner, Folder) and what is in them (Files,
// Size) are taken from the place. ErrActing when an act is under way on it already, or when it is the receiver of a
// hand-over under way, or (for a hand-over) when the receiver's My files is being handed over or deleted itself;
// ErrNoPlace when it is not a My files.
func (x *Index) StartAct(ctx context.Context, a Act) (Act, error) {
	if a.Kind != HandOver && a.Kind != DeleteHeld {
		return Act{}, fmt.Errorf("an act of an unknown kind %q", a.Kind)
	}
	var id int64
	err := x.write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT owner, folder FROM places WHERE id = ? AND kind = ? AND deleted_at IS NULL",
			a.Place, MyFiles).Scan(&a.Owner, &a.Folder)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoPlace
		}
		if err != nil {
			return err
		}
		var busy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM held_acts WHERE done_at IS NULL AND (place_id = ? OR to_place = ? OR place_id = ?)`,
			a.Place, a.Place, a.ToPlace).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return ErrActing
		}
		var toMember, toPlace, toName any
		if a.Kind == HandOver {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM places WHERE id = ? AND kind = ? AND id != ?", a.ToPlace, MyFiles, a.Place).Scan(&n); err != nil {
				return err
			}
			if n == 0 || a.ToMember == "" || a.ToName == "" {
				return fmt.Errorf("a hand-over needs another member's My files and a name: %w", ErrNoPlace)
			}
			toMember, toPlace, toName = a.ToMember, a.ToPlace, a.ToName
		}
		if a.Files, a.Size, err = contents(ctx, tx, a.Place); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO held_acts (act, place_id, owner, folder, to_member, to_place, to_name, files, size, by, at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			a.Kind, a.Place, a.Owner, a.Folder, toMember, toPlace, toName, a.Files, a.Size, a.By, a.At.UnixMilli()).Scan(&id)
	})
	if err != nil {
		return Act{}, err
	}
	return x.Act(ctx, id)
}

// NameUsed says whether an item of the index, live or missing from the storage (not one in the trash), has name in a
// folder of the place (folderID 0: its top folder).
func (x *Index) NameUsed(ctx context.Context, placeID, folderID int64, name string) (bool, error) {
	var n int
	err := x.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND name = ? AND trashed_at IS NULL`,
		placeID, folderID, name).Scan(&n)
	return n > 0, err
}

// EntriesIn is how many items the index holds directly in a folder of a place (folderID 0: its top folder), those
// missing from the storage included, those in the trash not: counted up to limit.
//
// Those there and those missing are counted apart, each through the index that holds only them (items_in_list_order,
// items_missing): asked for one count of both, SQLite reads every item of the Drive, and a visitor asks this with
// every file they send in.
func (x *Index) EntriesIn(ctx context.Context, placeID, folderID int64, limit int) (int, error) {
	var n int
	err := x.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM (SELECT 1 FROM items
			WHERE place_id = ?1 AND IFNULL(parent_id, 0) = ?2 AND trashed_at IS NULL AND missing_at IS NULL LIMIT ?3)) +
		(SELECT COUNT(*) FROM (SELECT 1 FROM items
			WHERE place_id = ?1 AND IFNULL(parent_id, 0) = ?2 AND trashed_at IS NULL AND missing_at IS NOT NULL LIMIT ?3))`,
		placeID, folderID, limit).Scan(&n)
	return min(n, limit), err
}

// RenameAct gives a hand-over under way another folder name in the receiver's top folder: the one it had was taken on
// the storage before the files moved there.
func (x *Index) RenameAct(ctx context.Context, id int64, toName string) error {
	res, err := x.db.ExecContext(ctx, "UPDATE held_acts SET to_name = ? WHERE id = ? AND act = ? AND done_at IS NULL", toName, id, HandOver)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("renaming hand-over %d: %w", id, ErrNoAct)
	}
	return nil
}

// actBatch is how many items one transaction of a hand-over or a deletion moves or removes, so that no transaction
// holds the index for long (other writers wait at most five seconds), however large the held My files is.
const actBatch = 20_000

// HandedOver records that a hand-over under way is done: its files are on the storage in the folder ToName of the
// receiver's top folder (whose time of last change is changed), and its held My files' trash is gone from it:
//
//   - nothing given on the held My files or on anything in it comes along: every grant (to members, to everyone, to
//     guests: a guest whose last invitation it was goes with it, and their sessions), every public link, and every upload
//     in progress into it are removed;
//   - its trash, with everything in it, is removed: it is not handed over;
//   - every item below its top folder moves into the receiver's My files, keeping its id and its folder, actBatch at a
//     time: out of reach there until the folder it is in follows, as everything is reached from a place's top folder
//     down;
//   - last, in one change: a folder item ToName is made in the receiver's top folder (a live row of that name there is
//     out of date, and goes first), the items of the held top folder move into it, the act is done, and the held My files
//     is gone.
//
// Each step is a transaction of its own that can be done again: a stop between them leaves the act under way, and
// calling HandedOver again goes on. It reads and writes rows in the database only: nothing is loaded into memory.
func (x *Index) HandedOver(ctx context.Context, id int64, changed, at time.Time) error {
	a, err := x.Act(ctx, id)
	if err != nil {
		return err
	}
	if a.Kind != HandOver || !a.Done.IsZero() || a.Place == 0 {
		return fmt.Errorf("hand-over %d: %w", id, ErrNoAct)
	}
	err = x.write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM places WHERE id = ? AND kind = ? AND deleted_at IS NULL", a.ToPlace, MyFiles).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("hand-over %d: %w", id, ErrNoPlace)
		}
		for _, q := range []string{
			"DELETE FROM grants WHERE place_id = ?",
			"DELETE FROM links WHERE item_id IN (SELECT id FROM items WHERE place_id = ?)",
			"DELETE FROM uploads WHERE place_id = ?",
			// What is inside a folder in the trash goes with it (parent_id cascades).
			"DELETE FROM items WHERE place_id = ? AND trashed_at IS NOT NULL",
		} {
			if _, err := tx.ExecContext(ctx, q, a.Place); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for {
		var moved int64
		err := x.write(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `
				UPDATE items SET place_id = ? WHERE id IN (SELECT id FROM items WHERE place_id = ? AND parent_id IS NOT NULL LIMIT ?)`,
				a.ToPlace, a.Place, actBatch)
			if err != nil {
				return err
			}
			moved, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if moved < actBatch {
			break
		}
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM items WHERE place_id = ? AND parent_id IS NULL AND name = ? AND trashed_at IS NULL AND missing_at IS NULL`,
			a.ToPlace, a.ToName); err != nil {
			return err
		}
		var folder int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at) VALUES (?, NULL, ?, 1, 0, ?) RETURNING id`,
			a.ToPlace, a.ToName, changed.Unix()).Scan(&folder); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items SET place_id = ?, parent_id = ? WHERE place_id = ?`, a.ToPlace, folder, a.Place); err != nil {
			return err
		}
		// Done first: the place going sets place_id to NULL, which only a done act may have.
		res, err := tx.ExecContext(ctx, "UPDATE held_acts SET done_at = ? WHERE id = ? AND done_at IS NULL", at.UnixMilli(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("hand-over %d: %w", id, ErrNoAct)
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM places WHERE id = ?", a.Place)
		return err
	})
}

// DeletedHeld records that a deletion under way is done: the held My files' top folder and its trash are gone from the
// storage. The held My files goes, with everything in it (actBatch items at a time, files first, each a transaction of
// its own: a stop between them leaves the act under way, and calling DeletedHeld again goes on), what was given on it,
// its links, its guests' invitations (a guest whose last invitation it was goes with it) and uploads in progress into it.
func (x *Index) DeletedHeld(ctx context.Context, id int64, at time.Time) error {
	a, err := x.Act(ctx, id)
	if err != nil {
		return err
	}
	if a.Kind != DeleteHeld || !a.Done.IsZero() || a.Place == 0 {
		return fmt.Errorf("deletion %d: %w", id, ErrNoAct)
	}
	// Files first: removing a folder takes everything still in it along, in the same transaction.
	for _, which := range []string{"is_folder = 0", "1"} {
		for {
			var removed int64
			err := x.write(ctx, func(tx *sql.Tx) error {
				res, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id IN (SELECT id FROM items WHERE place_id = ? AND `+which+` LIMIT ?)`,
					a.Place, actBatch)
				if err != nil {
					return err
				}
				removed, err = res.RowsAffected()
				return err
			})
			if err != nil {
				return err
			}
			if removed < actBatch {
				break
			}
		}
	}
	return x.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE held_acts SET done_at = ? WHERE id = ? AND done_at IS NULL", at.UnixMilli(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("deletion %d: %w", id, ErrNoAct)
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM places WHERE id = ? AND kind = ?", a.Place, MyFiles)
		return err
	})
}
