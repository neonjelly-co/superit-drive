// Package index is the Drive's index: one SQLite file in the data folder. It holds the places, the folder tree with
// every file's size (the space used is summed from them), the trash, the roles given on shared folders and, as later
// tickets add them, other access, public links and guests. The storage wins for which files exist and what is in them;
// the index alone holds access.
//
// index.go opens the file and holds the places; shared.go holds shared folders and the roles given on them, items.go
// the folder tree, and trash.go the trash.
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
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	conn, err := x.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
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
		n, err := strconv.Atoi(strings.SplitN(filepath.Base(name), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s does not start with its number", name)
		}
		if n <= version {
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
}

// ErrNoPlace is returned when a place does not exist.
var ErrNoPlace = errors.New("no such place")

const placeColumns = "id, kind, IFNULL(owner, ''), IFNULL(name, ''), folder, deleted_at, IFNULL(deleted_entry, '')"

func scanPlace(row interface{ Scan(...any) error }) (Place, error) {
	var p Place
	var deleted sql.NullInt64
	err := row.Scan(&p.ID, &p.Kind, &p.Owner, &p.Name, &p.Folder, &deleted, &p.Entry)
	if errors.Is(err, sql.ErrNoRows) {
		return Place{}, ErrNoPlace
	}
	if deleted.Valid {
		p.Deleted = time.UnixMilli(deleted.Int64).UTC()
	}
	return p, err
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

// FolderTaken says whether a place, in the trash or not, already uses folder as its top folder.
func (x *Index) FolderTaken(ctx context.Context, folder string) (bool, error) {
	var n int
	err := x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM places WHERE folder = ?", folder).Scan(&n)
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
