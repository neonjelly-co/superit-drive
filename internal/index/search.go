package index

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// Search: finding live items by name. Who may find what is decided in internal/access; Search only looks where it is
// told (a Scope), so that the work it does, and the time it takes, depends on nothing the person cannot see.

// SearchKey is a name, or what someone searches for, as a search compares them:
//
//   - letters in one case, by Unicode case folding (Á and á, Ő and ő, ẞ and ss are the same);
//   - accents and other marks taken off (á, ä and a are all a; ő, ö and o are all o), so a search typed without
//     them, as on a keyboard without Hungarian letters, finds the name with them, and the other way round;
//   - compatibility forms made plain (the ligature ﬁ is fi; a full-width Ａ is A);
//   - every run of white space one space, and none at either end.
//
// A name gives the same key in either Unicode form (composed, as the interface sends names; decomposed, as some Mac
// programs write them).
func SearchKey(s string) string {
	if ascii(s) {
		return asciiKey(s)
	}
	return unicodeKey(s)
}

func unicodeKey(s string) string {
	s = norm.NFKD.String(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // a mark on the letter before it, such as an accent
		case unicode.IsSpace(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return cases.Fold().String(b.String())
}

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiKey is SearchKey for plain ASCII, which most names are: the same result, without the Unicode tables.
func asciiKey(s string) string {
	b := make([]byte, 0, len(s))
	space := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r':
			space = true
			continue
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		if space && len(b) > 0 {
			b = append(b, ' ')
		}
		space = false
		b = append(b, c)
	}
	return string(b)
}

// Match ranks how well a name matches what someone searches for, best first:
//
//	0  the whole name, or the name without its extension ("offer" finds "Offer.pdf")
//	1  the start of the name
//	2  the start of a word in it
//	3  anywhere in it
//	4  each word searched for, somewhere in it, in any order ("tax 2026" finds "Tax notes 2026.pdf")
//
// and -1 when it does not match. key is the search as SearchKey makes it.
func Match(name, key string) int {
	if key == "" {
		return -1
	}
	n := SearchKey(name)
	if n == key || (strings.LastIndexByte(n, '.') > 0 && n[:strings.LastIndexByte(n, '.')] == key) {
		return 0
	}
	if strings.HasPrefix(n, key) {
		return 1
	}
	if strings.Contains(n, key) {
		for from := 0; ; {
			i := strings.Index(n[from:], key)
			if i < 0 {
				return 3
			}
			at := from + i
			if before, _ := utf8.DecodeLastRuneInString(n[:at]); !unicode.IsLetter(before) && !unicode.IsDigit(before) {
				return 2
			}
			_, size := utf8.DecodeRuneInString(n[at:])
			from = at + size
		}
	}
	words := strings.Split(key, " ")
	if len(words) < 2 {
		return -1
	}
	for _, w := range words {
		if !strings.Contains(n, w) {
			return -1
		}
	}
	return 4
}

// The functions searchSQL calls, registered with the SQLite driver once, before the index is opened. They are only
// used in queries, never in the schema, so the file stays readable without them.
var (
	searchFunctions    sync.Once
	searchFunctionsErr error
)

func registerSearchFunctions() error {
	searchFunctions.Do(func() {
		text := func(v driver.Value) string {
			switch v := v.(type) {
			case string:
				return v
			case []byte:
				return string(v)
			}
			return ""
		}
		searchFunctionsErr = sqlite.RegisterDeterministicScalarFunction("drive_search_match", 2,
			func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				return int64(Match(text(args[0]), text(args[1]))), nil
			})
		if searchFunctionsErr == nil {
			searchFunctionsErr = sqlite.RegisterDeterministicScalarFunction("drive_search_key", 1,
				func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
					return SearchKey(text(args[0])), nil
				})
		}
	})
	return searchFunctionsErr
}

// A Scope is where a search looks: whole places (every live item in them, at any depth), and single live items with
// everything in them, each in its place. The caller decides what goes into it, through internal/access, and an item
// inside a place of Places, or inside another of Items, must not be in Items too.
type Scope struct {
	Places []int64
	Items  []Granted
}

// Search returns the live items in scope whose names match what is searched for (Match), the best matches first, then
// by name, at most limit of them. It walks the folder tree down from the scope and reads nothing outside it: nothing in
// the trash, nothing inside a folder in the trash, no other place. It reads only the index, holds no lock of the Drive,
// and the database stops it at limit.
func (x *Index) Search(ctx context.Context, scope Scope, search string, limit int) ([]Item, error) {
	key := SearchKey(search)
	if key == "" || limit <= 0 || (len(scope.Places) == 0 && len(scope.Items) == 0) {
		return nil, nil
	}
	places, err := json.Marshal(append([]int64{}, scope.Places...))
	if err != nil {
		return nil, err
	}
	pairs := make([][2]int64, 0, len(scope.Items))
	for _, g := range scope.Items {
		pairs = append(pairs, [2]int64{g.Item, g.Place})
	}
	items, err := json.Marshal(pairs)
	if err != nil {
		return nil, err
	}
	found, err := scanItems(x.db.QueryContext(ctx, searchSQL, string(places), string(items), MaxDepth, key, limit))
	if err != nil {
		return nil, fmt.Errorf("searching the index: %w", err)
	}
	return found, nil
}

// searchSQL walks down from the scope (whole places from their top folders, given items from themselves) through
// items that are not in the trash, and keeps those whose names match, best first.
const searchSQL = `
	WITH RECURSIVE
		whole (place_id) AS (SELECT value FROM json_each(?)),
		given (id, place_id) AS (SELECT json_extract(value, '$[0]'), json_extract(value, '$[1]') FROM json_each(?)),
		tree (id, depth) AS (
			-- Each start is found through an index (the top folder's items by place, a given item by its id), so that
			-- not even finding where to start reads anything outside the scope.
			SELECT items.id, 0 FROM whole JOIN items
				ON items.place_id = whole.place_id AND IFNULL(items.parent_id, 0) = 0 AND items.trashed_at IS NULL
			UNION ALL
			SELECT items.id, 0 FROM given JOIN items
				ON items.id = given.id AND items.place_id = given.place_id AND items.trashed_at IS NULL
			UNION ALL
			SELECT items.id, tree.depth + 1 FROM items JOIN tree ON items.parent_id = tree.id
			WHERE items.trashed_at IS NULL AND tree.depth < ?
		),
		found AS (
			-- CROSS JOIN keeps the walk the outer loop: SQLite would otherwise read every item and look each up in it.
			SELECT items.*, drive_search_match(items.name, ?) AS rank FROM tree CROSS JOIN items ON items.id = tree.id
		)
	SELECT ` + itemColumns + ` FROM found AS items
	WHERE rank >= 0
	ORDER BY rank, drive_search_key(name), name, id
	LIMIT ?`
