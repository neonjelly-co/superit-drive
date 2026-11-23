// Package index is the Drive's index: one SQLite file in the data folder. It holds the places, the folder tree with
// every file's size (the space used is summed from them), the trash, the access given on places and items and, as
// later tickets add them, public links and guests. The storage wins for which files exist and what is in them; the
// index alone holds access.
//
// index.go opens the file and holds the places; shared.go holds shared folders, grants.go the access given on them and
// on items, items.go the folder tree, trash.go the trash, and copies.go copies of the whole index.
//
// The schema is changed only by adding a numbered file to migrations/. Each runs once, in order, and the database's
// user_version records how far it got.
package index

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver, so the binary stays static
)

// FileName is the name of the index file in the data folder.
const FileName = "index.sqlite"

//go:embed migrations/*.sql
var migrations embed.FS

// Index is the open index.
type Index struct {
	db *sql.DB
}

// Open opens the index in dataDir, making it if needed, and brings its schema up to date.
func Open(ctx context.Context, dataDir string) (*Index, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("making the data folder: %w", err)
	}
	if err := registerSearchFunctions(); err != nil {
		return nil, fmt.Errorf("preparing the index's search: %w", err)
	}
	file := filepath.Join(dataDir, FileName)
	pragmas := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		pragmas.Add("_pragma", p)
	}
	pragmas.Set("_txlock", "immediate") // writers take the write lock at BEGIN, so two writers never deadlock
	db, err := sql.Open("sqlite", "file:"+file+"?"+pragmas.Encode())
	if err != nil {
		return nil, fmt.Errorf("opening the index %s: %w", file, err)
	}
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("opening the index %s: %w", file, err)
	}
	x := &Index{db: db}
	if err := x.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("updating the index %s: %w", file, err)
	}
	return x, nil
}

// Close closes the index.
func (x *Index) Close() error { return x.db.Close() }

// migrate runs the migrations the index has not had yet, each in its own transaction.
//
// They run on one connection with foreign keys off, as SQLite's own recipe for changing a table asks: a table is
// changed by copying it into a new one and dropping the old, and with foreign keys on, dropping a table deletes its
// rows first, with everything that refers to them (ON DELETE CASCADE): rebuilding places would empty items. Each
// migration's foreign keys are checked before it commits instead, and the connection gets them back after.
func (x *Index) migrate(ctx context.Context) (err error) {
	conn, err := x.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// An index a newer Drive made is not opened: this Drive would skip every migration it does not know, and work on
	// tables whose rules it does not keep (a restriction a newer version added would quietly stop holding).
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > Version() {
		return fmt.Errorf("%w (its schema is version %d, this Drive's %d)", ErrNewer, version, Version())
	}
	return migrateConn(ctx, conn, Version())
}

// ErrNewer: the index in the data folder was made by a Drive whose schema is newer than this one's migrations make, as
// after the image was put back to an older version without the data folder. The Drive does not start on it; a copy
// made by a newer Drive is refused the same way (ErrCopyNewer).
var ErrNewer = errors.New("the index was made by a newer version of the Drive")

// Version is the schema version this Drive's migrations bring an index to: the number of the last one.
func Version() int {
	names, _ := migrationNames()
	if len(names) == 0 {
		return 0
	}
	n, _ := migrationNumber(names[len(names)-1])
	return n
}

func migrationNames() ([]string, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	sort.Strings(names)
	return names, err
}

func migrationNumber(name string) (int, error) {
	n, err := strconv.Atoi(strings.SplitN(filepath.Base(name), "_", 2)[0])
	if err != nil {
		return 0, fmt.Errorf("migration %s does not start with its number", name)
	}
	return n, nil
}

// migrateConn runs, on conn, the migrations up to and including number upTo that the database has not had yet.
func migrateConn(ctx context.Context, conn *sql.Conn, upTo int) (err error) {
	names, err := migrationNames()
	if err != nil {
		return err
	}
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() {
		// The connection goes back to the pool: it must enforce foreign keys again, or the index is not opened.
		var on int
		_, e := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
		if e == nil {
			e = conn.QueryRowContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys").Scan(&on)
		}
		if err == nil && (e != nil || on != 1) {
			err = fmt.Errorf("turning foreign keys back on after the migrations: %v", e)
		}
	}()
	for _, name := range names {
		n, err := migrationNumber(name)
		if err != nil {
			return err
		}
		if n <= version || n > upTo {
			continue
		}
		script, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := migrateOne(ctx, tx, name, string(script), n); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = n
	}
	return nil
}

func migrateOne(ctx context.Context, tx *sql.Tx, name, script string, n int) error {
	if _, err := tx.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	broken := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if broken {
		return fmt.Errorf("%s: a row refers to one that is not there (PRAGMA foreign_key_check)", name)
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n))
	return err
}

// write runs change in one transaction.
func (x *Index) write(ctx context.Context, change func(*sql.Tx) error) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := change(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// PlaceKind is the kind of a place.
type PlaceKind string

const (
	// MyFiles is a member's own place.
	MyFiles PlaceKind = "my-files"
	// SharedFolder is a place owned by the project.
	SharedFolder PlaceKind = "shared-folder"
)

// A Place is a top-level place in the Drive.
type Place struct {
	ID   int64
	Kind PlaceKind
	// Owner is the user id of the member whose "My files" this is. A shared folder has none: the project owns it.
	Owner string
	// Name is a shared folder's name, as people see it. "My files" has none.
	Name string
	// Folder is the place's top folder on the storage, relative to the Drive's folder.
	Folder string
	// Deleted is when a shared folder went to the trash, and zero for a place that is not in the trash.
	Deleted time.Time
	// Entry is, while it is in the trash, the entry in its own trash folder on the storage that holds its top folder.
	Entry string
	// OutsideOff says whether sharing outside the project is switched off for this shared folder by its managers, and
	// ProjectOutsideOff whether it is for the whole project by a project admin: read with the place, so that one reading
	// decides about both. Each is the zero Switch while it is on.
	OutsideOff, ProjectOutsideOff Switch
	// Acting is, for a held My files being handed over or deleted (an act under way, held.go), what is being done to it:
	// HandOver or DeleteHeld. It stays held meanwhile, whatever the member list says. "" for any other place.
	Acting string
}

// A Switch is sharing outside the project switched off: when, and by whom (a member's user id). The zero Switch is on.
type Switch struct {
	At time.Time
	By string
}

// Off says whether it is switched off.
func (s Switch) Off() bool { return !s.At.IsZero() }

// ErrNoPlace is returned when a place does not exist.
var ErrNoPlace = errors.New("no such place")

const placeColumns = "id, kind, IFNULL(owner, ''), IFNULL(name, ''), folder, deleted_at, IFNULL(deleted_entry, ''), " +
	"(SELECT off_at FROM outside_off WHERE IFNULL(place_id, 0) = places.id), " +
	"(SELECT off_by FROM outside_off WHERE IFNULL(place_id, 0) = places.id), " +
	"(SELECT off_at FROM outside_off WHERE IFNULL(place_id, 0) = 0), " +
	"(SELECT off_by FROM outside_off WHERE IFNULL(place_id, 0) = 0), " +
	"(SELECT act FROM held_acts WHERE held_acts.place_id = places.id AND held_acts.done_at IS NULL)"

func scanPlace(row interface{ Scan(...any) error }) (Place, error) {
	var p Place
	var deleted, offAt, projectOffAt sql.NullInt64
	var offBy, projectOffBy, acting sql.NullString
	err := row.Scan(&p.ID, &p.Kind, &p.Owner, &p.Name, &p.Folder, &deleted, &p.Entry, &offAt, &offBy, &projectOffAt, &projectOffBy, &acting)
	if errors.Is(err, sql.ErrNoRows) {
		return Place{}, ErrNoPlace
	}
	if deleted.Valid {
		p.Deleted = time.UnixMilli(deleted.Int64).UTC()
	}
	p.OutsideOff, p.ProjectOutsideOff = switchOf(offAt, offBy), switchOf(projectOffAt, projectOffBy)
	p.Acting = acting.String
	return p, err
}

func switchOf(at sql.NullInt64, by sql.NullString) Switch {
	if !at.Valid {
		return Switch{}
	}
	return Switch{At: time.UnixMilli(at.Int64).UTC(), By: by.String}
}

// ProjectOutsideOff says whether sharing outside the project is switched off for the whole project, and since when and
// by whom.
func (x *Index) ProjectOutsideOff(ctx context.Context) (Switch, error) {
	var at sql.NullInt64
	var by sql.NullString
	err := x.db.QueryRowContext(ctx, "SELECT off_at, off_by FROM outside_off WHERE IFNULL(place_id, 0) = 0").Scan(&at, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return Switch{}, nil
	}
	return switchOf(at, by), err
}

// SwitchOutside switches sharing outside the project off (off: at the time given, by the member by) or back on, for
// the whole project (placeID 0) or for a shared folder that is not in the trash (ErrNoPlace otherwise). Switching off
// what is off already keeps when and by whom it was. changed says whether it was switched.
func (x *Index) SwitchOutside(ctx context.Context, placeID int64, off bool, at time.Time, by string) (changed bool, err error) {
	err = x.write(ctx, func(tx *sql.Tx) error {
		if placeID != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM places WHERE id = ? AND kind = ? AND deleted_at IS NULL",
				placeID, SharedFolder).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrNoPlace
			}
		}
		var res sql.Result
		var err error
		if off {
			var place any // NULL: the whole project
			if placeID != 0 {
				place = placeID
			}
			res, err = tx.ExecContext(ctx, `INSERT INTO outside_off (place_id, off_at, off_by) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
				place, at.UnixMilli(), by)
		} else {
			res, err = tx.ExecContext(ctx, "DELETE FROM outside_off WHERE IFNULL(place_id, 0) = ?", placeID)
		}
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		changed = n > 0
		return nil
	})
	return changed, err
}

func scanPlaces(rows *sql.Rows, err error) ([]Place, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Place
	for rows.Next() {
		p, err := scanPlace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaceByID finds a place by its id. A shared folder in the trash is not found: see DeletedPlace.
func (x *Index) PlaceByID(ctx context.Context, id int64) (Place, error) {
	return scanPlace(x.db.QueryRowContext(ctx, "SELECT "+placeColumns+" FROM places WHERE id = ? AND deleted_at IS NULL", id))
}

// DeletedPlace finds a shared folder in the trash by its id. A place that is not in the trash is not found.
func (x *Index) DeletedPlace(ctx context.Context, id int64) (Place, error) {
	return scanPlace(x.db.QueryRowContext(ctx, "SELECT "+placeColumns+" FROM places WHERE id = ? AND deleted_at IS NOT NULL", id))
}

// MyFiles finds the member's "My files". It returns ErrNoPlace when the member has none yet.
func (x *Index) MyFiles(ctx context.Context, userID string) (Place, error) {
	return scanPlace(x.db.QueryRowContext(ctx,
		"SELECT "+placeColumns+" FROM places WHERE kind = ? AND owner = ?", MyFiles, userID))
}

// Places returns every place, those in the trash included.
func (x *Index) Places(ctx context.Context) ([]Place, error) {
	return scanPlaces(x.db.QueryContext(ctx, "SELECT "+placeColumns+" FROM places ORDER BY id"))
}

// FolderTaken says whether a place, in the trash or not, already uses folder as its top folder, or a held My files that
// was handed over or deleted used it (held_acts): such a folder is never given to a new place, so nothing that may be
// left of it on the storage is ever taken over.
func (x *Index) FolderTaken(ctx context.Context, folder string) (bool, error) {
	var n int
	err := x.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM places WHERE folder = ?) + (SELECT COUNT(*) FROM held_acts WHERE folder = ?)`,
		folder, folder).Scan(&n)
	return n > 0, err
}

// AddMyFiles records the member's "My files" with its top folder. If the member already has one (another request
// got there first), that one is returned instead.
func (x *Index) AddMyFiles(ctx context.Context, userID, folder string) (Place, error) {
	_, err := x.db.ExecContext(ctx,
		"INSERT INTO places (kind, owner, folder, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
		MyFiles, userID, folder, time.Now().Unix())
	if err != nil {
		return Place{}, err
	}
	return x.MyFiles(ctx, userID)
}
