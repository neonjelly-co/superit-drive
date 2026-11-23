package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Copies of the index: one SQLite file each, whole and consistent, made while the Drive runs (WriteCopy, with SQLite's
// VACUUM INTO, which reads the index in one read transaction and writes a new file from it: a file a backup can archive
// as it is, unlike the live index and its write-ahead log). The data folder keeps the latest at BackupFile, and the
// Drive puts one on the storage too, without what ClearCopy clears (internal/drive/copies.go says when, and where).
//
// A copy becomes the index of a Drive whose data folder has none (internal/drive/adopt.go): only after CheckCopy finds it
// whole and made by this Drive's own migrations, and ClearCopy has cleared what a copy must not bring.

// BackupFolder is the folder in the data folder that holds the copy of the index a backup archives.
const BackupFolder = "backup"

// BackupFile is where the data folder dataDir keeps its copy of the index: replaced whole, by renaming a new copy over
// it, so it is always a consistent copy, whatever a backup reads at any moment.
func BackupFile(dataDir string) string { return filepath.Join(dataDir, BackupFolder, FileName) }

// copyCacheKiB is the page cache, in KiB, of the connection that makes a copy; those that read or clear one have half.
const copyCacheKiB = 1024

// What drive_state keeps about copies.
const (
	copySeq = "copy_seq" // the number of the last copy made: each copy holds its own
)

// MaxCopySeq is the highest number a copy may have: far past what copies ever reach, and short of what would overflow as
// the next one is counted.
const MaxCopySeq = 1 << 53

// A CopyInfo says what a copy of the index is.
type CopyInfo struct {
	// Version is its schema version (Version for one this Drive made).
	Version int
	// StorageID is the id of the storage its index belongs to; "" when it kept none (it never marked a storage).
	StorageID string
	// Seq is which copy it is: copies of one index count up from 1.
	Seq int64
	// Changes is how many access changes its index had counted when it was made (AccessChanges).
	Changes int64
}

// What CheckCopy finds wrong with a copy.
var (
	// ErrCopyNewer: the copy was made by a Drive whose index has a newer schema than this one's migrations make.
	ErrCopyNewer = errors.New("the copy of the index was made by a newer version of the Drive")
	// ErrCopyDamaged: the copy is not a whole SQLite file, or not an index this Drive's migrations make.
	ErrCopyDamaged = errors.New("the copy of the index is damaged, or not one the Drive made")
)

// AccessChanges is how many changes to who may reach what the index has counted (migration 0011): a copy holds the
// number its index had when it was made, so a different number says that something changed since.
func (x *Index) AccessChanges(ctx context.Context) (int64, error) {
	var n int64
	err := x.db.QueryRowContext(ctx, "SELECT n FROM access_changes").Scan(&n)
	return n, err
}

// WriteCopy writes a consistent copy of the index into file, a path in the same file system as the data folder where
// nothing is yet, as the next copy (its Seq one more than the last). Writes go on meanwhile; the copy holds the index as
// it was at one moment. The file is written and synced to disk; the caller renames it into place.
func (x *Index) WriteCopy(ctx context.Context, file string) (CopyInfo, error) {
	err := x.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO drive_state (key, value) VALUES (?, '1')
			ON CONFLICT (key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)`, copySeq)
		return err
	})
	if err != nil {
		return CopyInfo{}, err
	}
	conn, err := x.db.Conn(ctx)
	if err != nil {
		return CopyInfo{}, err
	}
	// A small page cache for the copy (VACUUM INTO gives the new file the same): it reads and writes each page once.
	_, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA cache_size = -%d", copyCacheKiB))
	if err == nil {
		_, err = conn.ExecContext(ctx, "VACUUM INTO ?", file)
	}
	_, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA cache_size = -2000") // SQLite's default, for the pool
	conn.Close()
	if err != nil {
		os.Remove(file)
		return CopyInfo{}, fmt.Errorf("copying the index: %w", err)
	}
	if err := syncFile(file); err != nil {
		os.Remove(file)
		return CopyInfo{}, err
	}
	db, err := openCopy(file, true)
	if err != nil {
		return CopyInfo{}, err
	}
	defer db.Close()
	return copyInfo(ctx, db)
}

// syncFile writes what the file system holds of file to disk.
func syncFile(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// openCopy opens a copy of the index on its own, one connection, without the index's write-ahead log: read-only, or
// with no journal at all (a copy that fails midway is thrown away, never used).
func openCopy(file string, readOnly bool) (*sql.DB, error) {
	pragmas := url.Values{}
	pragmas.Add("_pragma", "busy_timeout(5000)")
	pragmas.Add("_pragma", fmt.Sprintf("cache_size(-%d)", copyCacheKiB/2))
	if readOnly {
		pragmas.Set("mode", "ro")
	} else {
		pragmas.Add("_pragma", "journal_mode(OFF)")
		pragmas.Add("_pragma", "synchronous(OFF)")
		// What is deleted is overwritten with zeros, so that a copy cleared of a secret holds no trace of it.
		pragmas.Add("_pragma", "secure_delete(ON)")
	}
	db, err := sql.Open("sqlite", "file:"+file+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// copyInfo reads what a copy says about itself.
func copyInfo(ctx context.Context, q queryRower) (CopyInfo, error) {
	var c CopyInfo
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&c.Version); err != nil {
		return c, err
	}
	var id, seq sql.NullString
	err := q.QueryRowContext(ctx, `SELECT (SELECT value FROM drive_state WHERE key = 'storage_id'),
		(SELECT value FROM drive_state WHERE key = ?)`, copySeq).Scan(&id, &seq)
	if err != nil {
		return c, err
	}
	c.StorageID = id.String
	if seq.Valid {
		if c.Seq, err = strconv.ParseInt(seq.String, 10, 64); err != nil || c.Seq < 0 || c.Seq > MaxCopySeq {
			return c, fmt.Errorf("the copy's number %q is not one a copy has", seq.String)
		}
	}
	if c.Version >= 11 { // access_changes came with migration 0011
		err = q.QueryRowContext(ctx, "SELECT n FROM access_changes").Scan(&c.Changes)
	}
	return c, err
}

// A Clearing says what to clear in a copy of the index (ClearCopy).
type Clearing struct {
	// Outsiders clears what lets guests in now and what is under way on one server: guests' sessions (their tokens'
	// hashes), the codes mailed to them (HMACs), the limits on trying codes, and uploads in progress.
	Outsiders bool
	// State lists the keys of drive_state to clear; Set the keys to keep a value under.
	State []string
	Set   map[string]string
	// SeqAtLeast, when it is more than the number of the copy's last copy, becomes that number: the next copy of an index
	// made from it is numbered after SeqAtLeast. At most MaxCopySeq.
	SeqAtLeast int64
}

// ClearCopy clears from the copy in file what c says, and sets what it says, in place. What is deleted is overwritten
// with zeros.
func ClearCopy(ctx context.Context, file string, c Clearing) error {
	if c.SeqAtLeast > MaxCopySeq {
		return fmt.Errorf("the number %d is past any a copy may have", c.SeqAtLeast)
	}
	db, err := openCopy(file, false)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if c.Outsiders {
		for _, table := range []string{"guest_sessions", "guest_codes", "guest_limits", "uploads"} {
			// A copy of an older schema may not have the table yet.
			var there bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = ?)", table).Scan(&there); err != nil {
				return err
			}
			if !there {
				continue
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return fmt.Errorf("clearing the copy of the index: %w", err)
			}
		}
	}
	for _, key := range c.State {
		if _, err := tx.ExecContext(ctx, "DELETE FROM drive_state WHERE key = ?", key); err != nil {
			return err
		}
	}
	for key, value := range c.Set {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drive_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	if c.SeqAtLeast > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO drive_state (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = CAST(max(CAST(value AS INTEGER), CAST(excluded.value AS INTEGER)) AS TEXT)`,
			copySeq, strconv.FormatInt(c.SeqAtLeast, 10)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return db.Close()
}

// CopyState reads the value a copy of the index that passed CheckCopy keeps under key in drive_state, without changing
// it; "" when it keeps none.
func CopyState(ctx context.Context, file, key string) (string, error) {
	db, err := openCopy(file, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var value sql.NullString
	err = db.QueryRowContext(ctx, "SELECT (SELECT value FROM drive_state WHERE key = ?)", key).Scan(&value)
	return value.String, err
}

// SharingIn is the sharing a file of the index holds: its public links, by the hash of each one's token (in hex), and
// its grants and invitations, each as one line of what was given, on what, to whom.
type SharingIn struct {
	Links               map[string]LinkIn
	Grants, Invitations map[string]bool
}

// LinkIn is a public link as SharingOf reads it: the item it is on, with its place and name.
type LinkIn struct {
	Place, Item int64
	Name        string
}

// SharingOf reads the sharing a file of the index holds, without checking or changing the file. It is also for a copy
// made by a newer Drive, whose schema this Drive does not know: it reads only columns the tables have had from the
// start, and is an error when they are no longer there.
func SharingOf(ctx context.Context, file string) (SharingIn, error) {
	db, err := openCopy(file, true)
	if err != nil {
		return SharingIn{}, err
	}
	defer db.Close()
	in := SharingIn{Links: map[string]LinkIn{}, Grants: map[string]bool{}, Invitations: map[string]bool{}}
	rows, err := db.QueryContext(ctx, `
		SELECT hex(links.token_hash), items.place_id, links.item_id, items.name FROM links JOIN items ON items.id = links.item_id`)
	if err != nil {
		return SharingIn{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		var l LinkIn
		if err := rows.Scan(&hash, &l.Place, &l.Item, &l.Name); err != nil {
			return SharingIn{}, err
		}
		in.Links[hash] = l
	}
	if err := rows.Err(); err != nil {
		return SharingIn{}, err
	}
	given, err := db.QueryContext(ctx, `
		SELECT place_id || '/' || IFNULL(item_id, 0) || ' ' || grantee || ' ' || IFNULL(member, '') || ' ' || IFNULL(guest_id, 0) || ' ' || role,
			guest_id IS NOT NULL FROM grants`)
	if err != nil {
		return SharingIn{}, err
	}
	defer given.Close()
	for given.Next() {
		var line string
		var invitation bool
		if err := given.Scan(&line, &invitation); err != nil {
			return SharingIn{}, err
		}
		if invitation {
			in.Invitations[line] = true
		} else {
			in.Grants[line] = true
		}
	}
	return in, given.Err()
}

// A CopySummary counts what a copy holds, for the log line that says a copy became the index.
type CopySummary struct {
	Places, SharedFolders, Items, Grants, Managers, Links, Guests, Acts int64
}

// CheckCopy reads the copy in file, without changing it, and says what it is: ErrCopyNewer when its schema is newer
// than this Drive's, ErrCopyDamaged (with what is wrong) when it is not a whole SQLite file that passes SQLite's
// integrity and foreign key checks, when its schema (tables, indexes, triggers, views) is not exactly what this Drive's
// migrations make for its version, or when a place's top folder, or the folder of a held My files' act, is not one
// topFolder allows. An older copy is fine: Open brings it up to date.
//
// Only a copy that passes becomes an index (internal/drive/adopt.go), so that a damaged one is never used, and one that
// was planted holds nothing but rows of the Drive's own tables: no trigger or view of its own, and no place whose top
// folder is somewhere a place's never is (the Drive's hidden folder, say).
func CheckCopy(ctx context.Context, file string, topFolder func(kind PlaceKind, folder string) bool) (CopyInfo, CopySummary, error) {
	var sum CopySummary
	header := make([]byte, 16)
	f, err := os.Open(file)
	if err != nil {
		return CopyInfo{}, sum, err
	}
	_, err = f.ReadAt(header, 0)
	f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return CopyInfo{}, sum, fmt.Errorf("%w: it is not an SQLite file", ErrCopyDamaged)
	}
	db, err := openCopy(file, true)
	if err != nil {
		return CopyInfo{}, sum, fmt.Errorf("%w: %v", ErrCopyDamaged, err)
	}
	defer db.Close()
	damaged := func(format string, args ...any) (CopyInfo, CopySummary, error) {
		return CopyInfo{}, sum, fmt.Errorf("%w: "+format, append([]any{ErrCopyDamaged}, args...)...)
	}

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return damaged("%v", err)
	}
	switch {
	case version > Version():
		return CopyInfo{Version: version}, sum, fmt.Errorf("%w (its schema is version %d, this Drive's %d)", ErrCopyNewer, version, Version())
	case version < 7: // drive_state, which says whose storage it belongs to, came with migration 0007
		return damaged("its schema is version %d, older than any copy", version)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return damaged("%v", err)
	}
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			return damaged("%v", err)
		}
		if line != "ok" && len(problems) < 3 {
			problems = append(problems, line)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return damaged("%v", err)
	}
	if len(problems) > 0 {
		return damaged("SQLite's integrity check: %s", strings.Join(problems, "; "))
	}
	var broken bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pragma_foreign_key_check)").Scan(&broken); err != nil {
		return damaged("%v", err)
	}
	if broken {
		return damaged("a row refers to one that is not there (foreign_key_check)")
	}

	// Its schema must be exactly what the migrations make for its version.
	theirs, err := schemaOf(ctx, db)
	if err != nil {
		return damaged("%v", err)
	}
	ours, err := freshSchema(ctx, version)
	if err != nil {
		return CopyInfo{}, sum, err
	}
	for i := 0; i < max(len(theirs), len(ours)); i++ {
		switch {
		case i >= len(theirs):
			return damaged("it lacks %s", ours[i])
		case i >= len(ours):
			return damaged("it holds %s, which the Drive never makes", theirs[i])
		case theirs[i] != ours[i]:
			return damaged("its %s differs from what the Drive makes", theirs[i])
		}
	}

	// Every place's top folder, and every act's, is where the Drive puts them.
	folders := "SELECT kind, folder FROM places"
	if version >= 10 { // held_acts came with migration 0010
		folders += " UNION ALL SELECT 'my-files', folder FROM held_acts"
	}
	places, err := db.QueryContext(ctx, folders)
	if err != nil {
		return damaged("%v", err)
	}
	for places.Next() {
		var kind PlaceKind
		var folder string
		if err := places.Scan(&kind, &folder); err != nil {
			places.Close()
			return damaged("%v", err)
		}
		if !topFolder(kind, folder) {
			places.Close()
			return damaged("a place's top folder is %q, where the Drive never puts one", folder)
		}
	}
	err = places.Err()
	places.Close()
	if err != nil {
		return damaged("%v", err)
	}

	info, err := copyInfo(ctx, db)
	if err != nil {
		return damaged("%v", err)
	}
	counts := []struct {
		n *int64
		q string
	}{
		{&sum.Places, "SELECT COUNT(*) FROM places"},
		{&sum.SharedFolders, "SELECT COUNT(*) FROM places WHERE kind = 'shared-folder'"},
		{&sum.Items, "SELECT COUNT(*) FROM items"},
		{&sum.Grants, "SELECT COUNT(*) FROM grants WHERE role != 'manager'"},
		{&sum.Managers, "SELECT COUNT(*) FROM grants WHERE role = 'manager'"},
		{&sum.Links, "SELECT COUNT(*) FROM links"},
	}
	if version >= 8 {
		counts = append(counts, struct {
			n *int64
			q string
		}{&sum.Guests, "SELECT COUNT(*) FROM guests"})
	}
	if version >= 10 {
		counts = append(counts, struct {
			n *int64
			q string
		}{&sum.Acts, "SELECT COUNT(*) FROM held_acts"})
	}
	for _, c := range counts {
		if err := db.QueryRowContext(ctx, c.q).Scan(c.n); err != nil {
			return damaged("%v", err)
		}
	}
	return info, sum, nil
}

// schemaOf lists the schema of a database, one line per table, index, trigger and view, in a fixed order, leaving out
// what SQLite makes by itself: the indexes of a table's own constraints (decided by its CREATE TABLE, which is
// compared), and its tables of AUTOINCREMENT numbers and statistics.
func schemaOf(ctx context.Context, db interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, name, tbl_name, IFNULL(sql, '') FROM sqlite_schema
		WHERE NOT (type = 'table' AND name IN ('sqlite_sequence', 'sqlite_stat1', 'sqlite_stat4'))
		  AND NOT (type = 'index' AND sql IS NULL AND name LIKE 'sqlite\_autoindex\_%' ESCAPE '\')
		ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, name, table, text string
		if err := rows.Scan(&kind, &name, &table, &text); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s %s on %s: %s", kind, name, table, text))
	}
	return out, rows.Err()
}

// freshSchema is the schema the migrations make, up to and including version, in an empty database in memory.
func freshSchema(ctx context.Context, version int) ([]string, error) {
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := registerSearchFunctions(); err != nil {
		return nil, err
	}
	if err := migrateConn(ctx, conn, version); err != nil {
		return nil, fmt.Errorf("making the schema of version %d to compare a copy with: %w", version, err)
	}
	return schemaOf(ctx, conn)
}
