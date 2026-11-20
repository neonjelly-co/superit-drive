package index

import (
	"context"
	"slices"
	"strings"
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
