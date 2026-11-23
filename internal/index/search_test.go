package index

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSearchKey(t *testing.T) {
	cases := map[string]string{
		"Offer.PDF":                     "offer.pdf",
		"  Tax   notes\t2026  ":         "tax notes 2026",
		"Árvíztűrő tükörfúrógép":        "arvizturo tukorfurogep",
		"ÁRVÍZTŰRŐ TÜKÖRFÚRÓGÉP":        "arvizturo tukorfurogep",
		"Számla – Őszi kör":             "szamla – oszi kor",
		"Számla ősz":                  "szamla osz", // decomposed, as some Mac programs write names
		"Straße":                        "strasse",
		"ﬁle":                           "file",
		"ＡＢＣ":                           "abc", // full-width
		"Ἀθῆναι":                        "αθηναι",
		"İstanbul":                      "istanbul",
		"":                              "",
		" ́ ":                           "",
		"plain ascii, 100% (of it).txt": "plain ascii, 100% (of it).txt",
	}
	for in, want := range cases {
		if got := SearchKey(in); got != want {
			t.Errorf("SearchKey(%q) = %q, want %q", in, got, want)
		}
	}
	// The plain ASCII way gives what the Unicode way would.
	for _, s := range []string{"Offer.PDF", " A  b\tC ", "x_Y-z.TXT", "\x7f~"} {
		if a, u := asciiKey(s), unicodeKey(s); a != u {
			t.Errorf("%q: %q the ASCII way, %q the Unicode way", s, a, u)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		name, search string
		want         int
	}{
		{"Offer.pdf", "offer.pdf", 0},
		{"Offer.pdf", "OFFER", 0},
		{"Offer 2026.pdf", "offer", 1},
		{"Tax notes 2026.pdf", "notes", 2},
		{"Tax notes 2026.pdf", "2026", 2},
		{"Árvíztűrő tükörfúrógép.txt", "tukor", 2},
		{"Árvíztűrő tükörfúrógép.txt", "TÜKÖR", 2},
		{"Számla 2026.xlsx", "szamla", 1},
		{"szamla.xlsx", "Számla", 0}, // accents count for nothing, either way
		{"Bakery-ledger.xlsx", "ledger", 2},
		{"Workbook.xlsx", "book", 3},
		{"Tax notes 2026.pdf", "2026 tax", 4},
		{"Tax notes 2026.pdf", "tax 2027", -1},
		{"Offer.pdf", "invoice", -1},
		{"Offer.pdf", "", -1},
		{"Offer.pdf", "of fer", 4},
		{"Offer.pdf", "offer pdf2", -1},
		{"50% off.txt", "%", 3},
		{"a_b.txt", "_", 3},
	}
	for _, c := range cases {
		if got := Match(c.name, SearchKey(c.search)); got != c.want {
			t.Errorf("Match(%q, %q) = %d, want %d", c.name, c.search, got, c.want)
		}
	}
}

// The time a search takes must not depend on what is outside its scope, or it would tell how much there is that the
// person cannot see: no step of the database's plan for it reads the whole items table. Each start is found through an
// index, and each step down through the folder index.
func TestASearchNeverReadsTheWholeIndex(t *testing.T) {
	x := open(t)
	rows, err := x.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+searchSQL, "[1]", "[[5,2]]", MaxDepth, "x", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var step string
		if err := rows.Scan(&id, &parent, &unused, &step); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, step)
	}
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN items") {
			t.Errorf("the search reads every item: %q, in the plan\n%s", step, strings.Join(plan, "\n"))
		}
	}
	if !slices.ContainsFunc(plan, func(s string) bool { return strings.Contains(s, "items_by_parent") }) {
		t.Errorf("the search does not walk down through the folder index:\n%s", strings.Join(plan, "\n"))
	}
}

// A search looks only where it is told: whole places from their top folders down, given items with everything in
// them; never in the trash, inside a folder in the trash, beside or above a given item, or in another place.
func TestSearchLooksOnlyInItsScope(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	reka, bence, dora := place(t, x, "u-reka"), place(t, x, "u-bence"), place(t, x, "u-dora")
	put(t, x, reka, 0, "Invoice 1.pdf", false, 1)
	drafts := put(t, x, reka, 0, "Drafts", true, 0)
	put(t, x, reka, drafts.ID, "Invoice 2.pdf", false, 1)
	old := put(t, x, reka, 0, "Old", true, 0)
	put(t, x, reka, old.ID, "Invoice 3.pdf", false, 1)
	binned := put(t, x, reka, 0, "Invoice 4.pdf", false, 1)
	if err := x.Trash(ctx, reka.ID, old.ID, time.Now(), "e1", "u-reka"); err != nil {
		t.Fatal(err)
	}
	if err := x.Trash(ctx, reka.ID, binned.ID, time.Now(), "e2", "u-reka"); err != nil {
		t.Fatal(err)
	}
	clients := put(t, x, bence, 0, "Clients", true, 0)
	bakery := put(t, x, bence, clients.ID, "Bakery invoices", true, 0)
	put(t, x, bence, bakery.ID, "Invoice 5.pdf", false, 1)
	put(t, x, bence, clients.ID, "Invoice 6.pdf", false, 1)
	put(t, x, bence, 0, "Invoice 7.pdf", false, 1)
	put(t, x, dora, 0, "Invoice 8.pdf", false, 1)

	names := func(scope Scope, search string, limit int) []string {
		t.Helper()
		found, err := x.Search(ctx, scope, search, limit)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, it := range found {
			out = append(out, it.Name)
		}
		return out
	}
	scope := Scope{Places: []int64{reka.ID}, Items: []Granted{{Place: bence.ID, Item: bakery.ID}}}
	if got, want := names(scope, "invoice", 100), []string{"Invoice 1.pdf", "Invoice 2.pdf", "Invoice 5.pdf", "Bakery invoices"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	// The best matches first, at most limit.
	if got, want := names(scope, "invoice", 2), []string{"Invoice 1.pdf", "Invoice 2.pdf"}; !slices.Equal(got, want) {
		t.Fatalf("limited: got %q, want %q", got, want)
	}
	// A given item named with another place is not found there.
	if got := names(Scope{Items: []Granted{{Place: dora.ID, Item: bakery.ID}}}, "invoice", 100); len(got) != 0 {
		t.Fatalf("an item given with the wrong place: %q", got)
	}
	// Nothing to look in, or nothing to look for: nothing found.
	if got := names(Scope{}, "invoice", 100); len(got) != 0 {
		t.Fatalf("no scope: %q", got)
	}
	if got := names(scope, " \t ", 100); len(got) != 0 {
		t.Fatalf("an empty search: %q", got)
	}
	// Characters that mean something to SQL mean nothing here.
	if got := names(scope, "%", 100); len(got) != 0 {
		t.Fatalf("%%: %q", got)
	}
	if got := names(scope, strings.Repeat("x", 300), 100); len(got) != 0 {
		t.Fatalf("a long search: %q", got)
	}
}

// A page of a folder's list is read through the index in list order: never the whole folder, never sorted.
func TestAPageOfAListIsReadInOrder(t *testing.T) {
	x := open(t)
	for _, first := range []bool{true, false} {
		rows, err := x.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+childrenSQL, 1, 2, first, false, false, "a", 10)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var step string
			if err := rows.Scan(&id, &parent, &unused, &step); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, step)
		}
		rows.Close()
		joined := strings.Join(plan, "\n")
		if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "items_in_list_order") {
			t.Errorf("a page of a list (from the start: %v) is not read through its index in order:\n%s", first, joined)
		}
	}
}

// manyItems puts n files into the top folder of the place, in one write.
func manyItems(t *testing.T, x *Index, p Place, n int) {
	t.Helper()
	_, err := x.db.ExecContext(context.Background(), `
		WITH RECURSIVE n (i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO items (place_id, parent_id, name, is_folder, size, changed_at, added_by_guest, added_through_link)
		SELECT ?, NULL, 'Árvíztűrő report ' || i || '.pdf', 0, 1, 0, NULL, 0 FROM n`, n, p.ID)
	if err != nil {
		t.Fatal(err)
	}
}

// A search that takes too long is ended, in the middle of its reading, and says so: it does not go on holding a
// connection of the index until it has read everything.
func TestASearchThatTakesTooLongIsEnded(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	reka := place(t, x, "u-reka")
	manyItems(t, x, reka, 60000)
	scope := Scope{Places: []int64{reka.ID}}

	start := time.Now()
	found, err := x.Search(ctx, scope, "nothing called this", 10)
	whole := time.Since(start)
	if err != nil || len(found) != 0 {
		t.Fatalf("a search with all the time it needs: %d found, %v", len(found), err)
	}

	x.EndSearchesAfter(whole / 20)
	start = time.Now()
	_, err = x.Search(ctx, scope, "nothing called this", 10)
	if took := time.Since(start); !errors.Is(err, ErrSearchTooLong) || took > whole/2 {
		t.Fatalf("a search that takes %v, with %v to do it in: got %v after %v, want it ended as taking too long", whole, whole/20, err, took)
	}
	// Its turn and its connection are free again: the next one runs.
	x.EndSearchesAfter(SearchMost)
	if _, err := x.Search(ctx, scope, "report 7.pdf", 10); err != nil {
		t.Fatalf("a search after one that was ended: %v", err)
	}
	// A person who went away is not a search that took too long.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := x.Search(gone, scope, "report", 10); !errors.Is(err, context.Canceled) || errors.Is(err, ErrSearchTooLong) {
		t.Fatalf("a search by someone who has gone: %v, want only that they have gone", err)
	}
}

// Only a few searches run at once, so that the rest of the index's connections are left to everything else; the others
// wait their turn, and are refused when none comes free in time.
func TestOnlyAFewSearchesRunAtOnce(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	if SearchesAtOnce >= connections {
		t.Fatalf("%d searches at once would take all %d connections of the index", SearchesAtOnce, connections)
	}
	reka := place(t, x, "u-reka")
	put(t, x, reka, 0, "Report.pdf", false, 1)
	scope := Scope{Places: []int64{reka.ID}}

	// Every turn is taken, as by searches under way.
	var leave []func()
	for range SearchesAtOnce {
		l, err := x.searchTurn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		leave = append(leave, l)
	}
	// One more waits, and is refused when its wait is over; everything else in the index is answered meanwhile.
	x.searchWait = 100 * time.Millisecond
	start := time.Now()
	if _, err := x.Search(ctx, scope, "report", 10); !errors.Is(err, ErrSearchBusy) || time.Since(start) < x.searchWait {
		t.Fatalf("a search with every turn taken: %v after %v, want it refused as busy after waiting %v", err, time.Since(start), x.searchWait)
	}
	if _, err := x.PlaceByID(ctx, reka.ID); err != nil {
		t.Fatalf("a lookup while every search's turn is taken: %v", err)
	}
	// Those that wait run when a turn comes free.
	x.searchWait = 10 * time.Second
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			found, err := x.Search(ctx, scope, "report", 10)
			if err == nil && len(found) != 1 {
				err = errors.New("the search found nothing")
			}
			results <- err
		})
	}
	time.Sleep(50 * time.Millisecond)
	if len(results) != 0 {
		t.Fatal("a search ran with every turn taken")
	}
	leave[0]()
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("a search that waited its turn: %v", err)
		}
	}
	for _, l := range leave[1:] {
		l()
	}
}
