package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Keeping the index in step with the storage, which wins for which files exist and what is in them. The Drive looks at
// one folder of a place at a time (a listing of it), and Reconcile brings that folder of the index in step with what the
// look found. What the storage shows that the index does not know is added; what the index knows that the storage no
// longer shows is marked missing (hidden everywhere, as if it were not there) and dropped, with everything in it and
// everything given on it, only when a later look, made at least a while after, still finds it gone. Access is never read
// from the storage: an item added this way has nothing given on it, and so the access of the folder it is in.
//
// What makes an item "the same": its place, the folder it is in, its name and its kind. A live item the look finds again
// keeps its id, whatever is in it now (the storage wins for what is in a file, as when an upload through the Drive
// replaces it). An item the Drive has seen gone is back, with its id and everything given on it, only when the storage
// shows it again exactly as the Drive last knew it: the same name, kind, size and time of its last change, which a
// storage that answered wrong for a while gives back and a different file put there does not. Anything else that takes
// its name is a new item, with nothing given on it, and the missing one is dropped when its time comes. A file where a
// folder was (or the other way round) is a new item. A file renamed or moved outside the Drive is one item gone and
// another one new.

// ErrChanged says that a look at a folder found it changed by the Drive itself while it was being read: what it found is
// out of date, and nothing was changed.
var ErrChanged = errors.New("the folder changed while it was being read")

// A Reread is what one look at a folder on the storage found, for Reconcile.
type Reread struct {
	// Path is the names of the folders from the place's top folder down to the folder looked at (empty for the top
	// folder), as the look found it there. The look counts only while the folder is still at that path in the index.
	Path []string
	// Seen is what the storage showed directly in the folder.
	Seen []Seen
	// Leave says, by name, what to leave as it is: neither added, changed, marked missing nor dropped (a name a change
	// of the Drive's own holds right now, or one the Drive does not use).
	Leave func(name string) bool
	// Busy and Stale are asked once the index is held for writing. Busy says the Drive is changing the folder, or a
	// folder above it, on the storage right now; Stale that it changed what is in the folder since the look began. Either
	// way the look is out of date, and nothing is changed (ErrChanged).
	Busy, Stale func() bool
	// At is when the look was made.
	At time.Time
	// Hide says whether what the look did not find may be marked missing, and missing items dropped: only when the
	// storage was checked to be the Drive's own.
	Hide bool
	// Old is when an item must have been missing since for it to be dropped now.
	Old time.Time
	// Confirmed is, by id, missing items whose own address a second look found empty, with when each was missing since
	// then: each is dropped if it still is (and has been since before Old).
	Confirmed map[int64]time.Time
	// Shallow is true when the folder is as deep as folders go: nothing new is added in it.
	Shallow bool
}

// An Outcome counts what one look changed in the index.
type Outcome struct {
	Added, Changed, Back, Hidden, Dropped int
}

// Add adds o2's counts to o's.
func (o *Outcome) Add(o2 Outcome) {
	o.Added += o2.Added
	o.Changed += o2.Changed
	o.Back += o2.Back
	o.Hidden += o2.Hidden
	o.Dropped += o2.Dropped
}

// A Missing item is one the storage no longer showed when the Drive last looked.
type Missing struct {
	ID     int64
	Name   string
	Folder bool
	// Size and Changed are as the Drive last knew it: it is back only when the storage shows it so again.
	Size    int64
	Changed time.Time
	Since   time.Time
	// Entry is, for an item of the trash, the entry it is in.
	Entry string
}

// MissingIn returns the items directly in one folder of a place (folderID 0: its top folder) that have been missing
// since before the time given, at most limit of them, those missing longest first.
func (x *Index) MissingIn(ctx context.Context, placeID, folderID int64, before time.Time, limit int) ([]Missing, error) {
	return scanMissing(x.db.QueryContext(ctx, `
		SELECT id, name, is_folder, size, changed_at, missing_at, '' FROM items
		WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL AND missing_at IS NOT NULL AND missing_at <= ?
		ORDER BY missing_at, id LIMIT ?`, placeID, folderID, before.UnixMilli(), limit))
}

func scanMissing(rows *sql.Rows, err error) ([]Missing, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Missing
	for rows.Next() {
		var m Missing
		var changed, since int64
		if err := rows.Scan(&m.ID, &m.Name, &m.Folder, &m.Size, &changed, &since, &m.Entry); err != nil {
			return nil, err
		}
		m.Changed, m.Since = time.Unix(changed, 0).UTC(), time.UnixMilli(since).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// Same says whether the storage shows an item exactly as the Drive last knew it.
func Same(m Missing, s Seen) bool {
	return m.Folder == s.Folder && m.Size == s.Size && m.Changed.Unix() == s.Changed.Unix()
}

// A Known item is one the index holds directly in a folder, live or missing, as a look compares it with the storage.
type Known struct {
	Name    string
	Folder  bool
	Size    int64
	Changed time.Time
	// Missing is when it went missing; zero while the storage shows it.
	Missing time.Time
}

// KnownIn returns what the index holds directly in one folder of a place (folderID 0: its top folder), live and
// missing, not what is in the trash.
func (x *Index) KnownIn(ctx context.Context, placeID, folderID int64) ([]Known, error) {
	rows, err := x.db.QueryContext(ctx, `
		SELECT name, is_folder, size, changed_at, IFNULL(missing_at, 0) FROM items
		WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL`, placeID, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Known
	for rows.Next() {
		var k Known
		var changed, missing int64
		if err := rows.Scan(&k.Name, &k.Folder, &k.Size, &changed, &missing); err != nil {
			return nil, err
		}
		k.Changed = time.Unix(changed, 0).UTC()
		if missing != 0 {
			k.Missing = time.UnixMilli(missing).UTC()
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Differs says whether a look that found seen in a folder whose items the index holds as known would change anything:
// add, change, bring back, hide or drop an item (old and leave as for a Reread). A look that would not need not be
// applied.
func Differs(known []Known, seen []Seen, leave func(name string) bool, old time.Time) bool {
	live := make(map[string]Known, len(known))
	for _, k := range known {
		if leave(k.Name) {
			continue
		}
		if k.Missing.IsZero() {
			live[k.Name] = k
		} else if !k.Missing.After(old) {
			return true // missing long enough: it may be dropped
		}
	}
	found := 0
	for _, s := range seen {
		if leave(s.Name) {
			continue
		}
		k, ok := live[s.Name]
		if !ok || k.Folder != s.Folder || k.Size != s.Size || k.Changed.Unix() != s.Changed.Unix() {
			return true
		}
		found++
	}
	return found != len(live)
}

// known is an item of one folder as Reconcile reads it.
type known struct {
	id      int64
	name    string
	folder  bool
	size    int64
	changed int64
	missing int64 // unix milliseconds; 0 while the storage shows it
}

// Reconcile brings one folder of a place (folderID 0: its top folder) in step with what one look at it on the storage
// found (see the top of this file), in one change. A folder that is no longer a live folder of the place, or no longer
// at r.Path, is ErrNoFolder or ErrChanged, and nothing changes.
func (x *Index) Reconcile(ctx context.Context, placeID, folderID int64, r Reread) (Outcome, error) {
	var o Outcome
	err := x.write(ctx, func(tx *sql.Tx) error {
		if r.Stale() || r.Busy() {
			return ErrChanged
		}
		if folderID != 0 {
			it, err := item(ctx, tx, placeID, folderID)
			if errors.Is(err, ErrNoItem) {
				return fmt.Errorf("folder %d of place %d: %w", folderID, placeID, ErrNoFolder)
			}
			if err != nil {
				return err
			}
			if !it.Folder {
				return fmt.Errorf("item %d of place %d is not a folder: %w", folderID, placeID, ErrNoFolder)
			}
			if !slices.Equal(namesOf(pathOf(it)), r.Path) {
				return ErrChanged
			}
		} else if len(r.Path) != 0 {
			return ErrChanged
		}

		rows, err := tx.QueryContext(ctx, `
			SELECT id, name, is_folder, size, changed_at, IFNULL(missing_at, 0) FROM items
			WHERE place_id = ? AND IFNULL(parent_id, 0) = ? AND trashed_at IS NULL`, placeID, folderID)
		if err != nil {
			return err
		}
		live := map[string]*known{}
		missing := map[string][]*known{}
		for rows.Next() {
			k := &known{}
			if err := rows.Scan(&k.id, &k.name, &k.folder, &k.size, &k.changed, &k.missing); err != nil {
				rows.Close()
				return err
			}
			if k.missing == 0 {
				live[k.name] = k
			} else {
				missing[k.name] = append(missing[k.name], k)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}

		at := r.At.UnixMilli()
		seen := make(map[string]bool, len(r.Seen))
		for _, s := range r.Seen {
			if r.Leave(s.Name) || seen[s.Name] {
				continue
			}
			seen[s.Name] = true
			changed := s.Changed.Unix()
			if k := live[s.Name]; k != nil && k.folder == s.Folder {
				if k.size != s.Size || k.changed != changed {
					if _, err := tx.ExecContext(ctx, "UPDATE items SET size = ?, changed_at = ? WHERE id = ?", s.Size, changed, k.id); err != nil {
						return err
					}
					o.Changed++
				}
				continue
			} else if k != nil {
				// A file where a folder was, or the other way round: that one is gone, and this one is new.
				if _, err := tx.ExecContext(ctx, "UPDATE items SET missing_at = ? WHERE id = ?", at, k.id); err != nil {
					return err
				}
				k.missing = at
				missing[s.Name] = append(missing[s.Name], k)
				delete(live, s.Name)
				o.Hidden++
			}
			// Exactly as an item missing since an earlier look was last known: that item is back.
			var back *known
			for _, m := range missing[s.Name] {
				same := m.folder == s.Folder && m.size == s.Size && m.changed == changed
				if same && (back == nil || m.missing > back.missing || (m.missing == back.missing && m.id > back.id)) {
					back = m
				}
			}
			if back != nil {
				if _, err := tx.ExecContext(ctx, "UPDATE items SET missing_at = NULL, size = ?, changed_at = ? WHERE id = ?",
					s.Size, changed, back.id); err != nil {
					return err
				}
				back.missing = 0
				live[s.Name] = back
				missing[s.Name] = slices.DeleteFunc(missing[s.Name], func(m *known) bool { return m == back })
				o.Back++
				continue
			}
			if r.Shallow {
				continue
			}
			var id int64
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
				placeID, parent(folderID), s.Name, s.Folder, s.Size, changed).Scan(&id); err != nil {
				return err
			}
			live[s.Name] = &known{id: id, name: s.Name, folder: s.Folder}
			o.Added++
		}
		if !r.Hide {
			return nil
		}

		for name, k := range live {
			if seen[name] || r.Leave(name) {
				continue
			}
			if _, err := tx.ExecContext(ctx, "UPDATE items SET missing_at = ? WHERE id = ?", at, k.id); err != nil {
				return err
			}
			o.Hidden++
		}

		// Missing long enough: dropped when another item now has its name there (the storage shows something else at its
		// address), or when a second look at its own address found nothing.
		old := r.Old.UnixMilli()
		for name, ms := range missing {
			if r.Leave(name) {
				continue
			}
			for _, m := range ms {
				if m.missing == 0 || m.missing > old {
					continue
				}
				since, confirmed := r.Confirmed[m.id]
				if live[name] == nil && !(confirmed && since.UnixMilli() == m.missing) {
					continue
				}
				res, err := tx.ExecContext(ctx, "DELETE FROM items WHERE id = ? AND missing_at = ?", m.id, m.missing)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				o.Dropped += int(n)
			}
		}
		return nil
	})
	return o, err
}

// pathOf is an item after the folders above it, from the top of its place down.
func pathOf(it Item) []Item { return append(append(make([]Item, 0, len(it.Above)+1), it.Above...), it) }

func namesOf(items []Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

// A TrashReread is what one look at a place's trash on the storage found, for ReconcileTrash.
type TrashReread struct {
	// Present says whether the look found the entry (a folder of the trash) of that name.
	Present func(entry string) bool
	// Leave says, by entry, what to leave as it is: an entry a change of the Drive's own is making or emptying.
	Leave func(entry string) bool
	// Stale is asked once the index is held for writing: true when the Drive changed the trash since the look began.
	Stale func() bool
	// At, Hide, Old and Confirmed are as for a Reread, by item of the trash and the entry it is in.
	At        time.Time
	Hide      bool
	Old       time.Time
	Confirmed map[int64]time.Time
	// Adopt is what the look found in entries the index does not know, each holding one item: they are added to the
	// trash as items deleted on the day the entry names, by nobody the Drive knows, going back to the top folder.
	Adopt []Adopted
}

// An Adopted item is one found in an entry of a place's trash that the index did not know.
type Adopted struct {
	Entry   string
	Item    Seen
	Deleted time.Time
}

// TrashMissing returns the items of the place's trash that have been missing since before the time given, at most limit
// of them, those missing longest first.
func (x *Index) TrashMissing(ctx context.Context, placeID int64, before time.Time, limit int) ([]Missing, error) {
	return scanMissing(x.db.QueryContext(ctx, `
		SELECT id, name, is_folder, size, changed_at, missing_at, trash_folder FROM items
		WHERE place_id = ? AND trashed_at IS NOT NULL AND missing_at IS NOT NULL AND missing_at <= ?
		ORDER BY missing_at, id LIMIT ?`, placeID, before.UnixMilli(), limit))
}

// MarkTrashMissing marks an item of the place's trash as missing from the storage at the time given: its entry no
// longer holds it. It is hidden from the trash from then on, and dropped when a later look confirms it (ReconcileTrash).
func (x *Index) MarkTrashMissing(ctx context.Context, placeID, id int64, at time.Time) error {
	_, err := x.db.ExecContext(ctx, `
		UPDATE items SET missing_at = ? WHERE id = ? AND place_id = ? AND trashed_at IS NOT NULL AND missing_at IS NULL`,
		at.UnixMilli(), id, placeID)
	return err
}

// ReconcileTrash brings the index's record of a place's trash in step with one look at the trash on the storage, by
// entry: an item whose entry the look did not find is marked missing (hidden from the trash), and dropped when a later
// look still does not find it; one whose entry is found again is back (Present says so only when the entry holds it as
// the Drive last knew it); entries the index did not know are adopted.
func (x *Index) ReconcileTrash(ctx context.Context, placeID int64, r TrashReread) (Outcome, error) {
	var o Outcome
	err := x.write(ctx, func(tx *sql.Tx) error {
		if r.Stale() {
			return ErrChanged
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id, trash_folder, IFNULL(missing_at, 0) FROM items WHERE place_id = ? AND trashed_at IS NOT NULL`, placeID)
		if err != nil {
			return err
		}
		type trashed struct {
			id      int64
			entry   string
			missing int64
		}
		var all []trashed
		entries := map[string]bool{}
		for rows.Next() {
			var t trashed
			if err := rows.Scan(&t.id, &t.entry, &t.missing); err != nil {
				rows.Close()
				return err
			}
			all = append(all, t)
			entries[t.entry] = true
		}
		if err := rows.Close(); err != nil {
			return err
		}
		at, old := r.At.UnixMilli(), r.Old.UnixMilli()
		for _, t := range all {
			switch {
			case r.Leave(t.entry):
			case r.Present(t.entry) && t.missing != 0:
				if _, err := tx.ExecContext(ctx, "UPDATE items SET missing_at = NULL WHERE id = ?", t.id); err != nil {
					return err
				}
				o.Back++
			case r.Present(t.entry) || !r.Hide:
			case t.missing == 0:
				if _, err := tx.ExecContext(ctx, "UPDATE items SET missing_at = ? WHERE id = ?", at, t.id); err != nil {
					return err
				}
				o.Hidden++
			case t.missing <= old:
				if since, ok := r.Confirmed[t.id]; !ok || since.UnixMilli() != t.missing {
					continue
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM items WHERE id = ? AND missing_at = ?", t.id, t.missing); err != nil {
					return err
				}
				o.Dropped++
			}
		}
		var deletedEntry sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT deleted_entry FROM places WHERE id = ?", placeID).Scan(&deletedEntry); err != nil {
			return err
		}
		for _, a := range r.Adopt {
			if entries[a.Entry] || (deletedEntry.Valid && deletedEntry.String == a.Entry) || r.Leave(a.Entry) || !r.Present(a.Entry) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at, trashed_at, trash_folder, trashed_why)
				VALUES (?, NULL, ?, ?, ?, ?, ?, ?, 'deleted')`,
				placeID, a.Item.Name, a.Item.Folder, a.Item.Size, a.Item.Changed.Unix(), a.Deleted.UnixMilli(), a.Entry); err != nil {
				return err
			}
			entries[a.Entry] = true
			o.Added++
		}
		return nil
	})
	return o, err
}

// State returns what the Drive keeps under key between starts; ok is false when it keeps nothing there.
func (x *Index) State(ctx context.Context, key string) (value string, ok bool, err error) {
	err = x.db.QueryRowContext(ctx, "SELECT value FROM drive_state WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetState keeps value under key; an empty value keeps nothing there.
func (x *Index) SetState(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := x.db.ExecContext(ctx, "DELETE FROM drive_state WHERE key = ?", key)
		return err
	}
	_, err := x.db.ExecContext(ctx, `
		INSERT INTO drive_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AddStorageID keeps id as the id of the storage the index belongs to, unless it keeps one already, and returns the one
// it keeps.
func (x *Index) AddStorageID(ctx context.Context, id string) (string, error) {
	if _, err := x.db.ExecContext(ctx, "INSERT INTO drive_state (key, value) VALUES ('storage_id', ?) ON CONFLICT DO NOTHING", id); err != nil {
		return "", err
	}
	kept, _, err := x.State(ctx, "storage_id")
	return kept, err
}

// A WalkStep is one folder of the walk through the storage: a place's trash (Folder TrashStep), its top folder
// (Folder 0), or one of its folders, by id.
type WalkStep struct {
	Place, Folder int64
}

// TrashStep is the Folder of a WalkStep that is a place's trash.
const TrashStep = -1

func (s WalkStep) String() string {
	return strconv.FormatInt(s.Place, 10) + ":" + strconv.FormatInt(s.Folder, 10)
}

// ParseWalkStep reads a step as String writes it.
func ParseWalkStep(v string) (WalkStep, bool) {
	place, folder, ok := strings.Cut(v, ":")
	p, err1 := strconv.ParseInt(place, 10, 64)
	f, err2 := strconv.ParseInt(folder, 10, 64)
	if !ok || err1 != nil || err2 != nil || p < 0 || f < TrashStep {
		return WalkStep{}, false
	}
	return WalkStep{Place: p, Folder: f}, true
}

// NextWalkStep is the step after s in the walk's order: places that are not in the trash, by id; in each, its trash,
// its top folder, then its folders that are not in the trash or missing, by id. A folder made during a walk has a
// higher id than any before it, so the walk reaches it in a place it has not left. The zero step comes before every
// other; ok is false after the last.
func (x *Index) NextWalkStep(ctx context.Context, s WalkStep) (next WalkStep, ok bool, err error) {
	if s.Place > 0 {
		if s.Folder == TrashStep {
			if _, err := x.PlaceByID(ctx, s.Place); err == nil {
				return WalkStep{Place: s.Place, Folder: 0}, true, nil
			} else if !errors.Is(err, ErrNoPlace) {
				return WalkStep{}, false, err
			}
		} else {
			var id int64
			err := x.db.QueryRowContext(ctx, `
				SELECT id FROM items
				WHERE place_id = ? AND is_folder = 1 AND id > ? AND trashed_at IS NULL AND missing_at IS NULL
				ORDER BY id LIMIT 1`, s.Place, s.Folder).Scan(&id)
			if err == nil {
				return WalkStep{Place: s.Place, Folder: id}, true, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return WalkStep{}, false, err
			}
		}
	}
	var place int64
	err = x.db.QueryRowContext(ctx, "SELECT id FROM places WHERE id > ? AND deleted_at IS NULL ORDER BY id LIMIT 1", s.Place).Scan(&place)
	if errors.Is(err, sql.ErrNoRows) {
		return WalkStep{}, false, nil
	}
	if err != nil {
		return WalkStep{}, false, err
	}
	return WalkStep{Place: place, Folder: TrashStep}, true, nil
}
