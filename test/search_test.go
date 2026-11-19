package test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's call for a search by name, as the interface makes it.

type foundJSON struct {
	itemJSON
	Place     int64        `json:"place"`
	PlaceKind string       `json:"placeKind"`
	PlaceName string       `json:"placeName"`
	Owner     *personJSON  `json:"owner"`
	Role      string       `json:"role"`
	Path      []folderJSON `json:"path"`
	FromTop   bool         `json:"fromTop"`
}

func searchPath(q string) string { return "/api/search?q=" + url.QueryEscape(q) }

// search is what a search finds for the client, whether there is more, and the answer's raw body.
func search(t *testing.T, c *harness.Client, q string) ([]foundJSON, bool, string) {
	t.Helper()
	res := c.Get(t, searchPath(q))
	if res.Status != http.StatusOK {
		t.Fatalf("searching for %q: got %d %s", q, res.Status, res.Body)
	}
	var l struct {
		Items []foundJSON `json:"items"`
		More  bool        `json:"more"`
	}
	res.JSON(t, &l)
	return l.Items, l.More, string(res.Body)
}

// foundNames is what a search finds, by name, in the order it answers.
func foundNames(t *testing.T, c *harness.Client, q string) []string {
	t.Helper()
	items, _, _ := search(t, c, q)
	out := []string{}
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// A search finds, by name, everything the person can see, in every kind of place: their own My files, shared folders
// they have a role on, and what was shared with them in part of a place, with everything inside it. It finds nothing
// else: not in someone else's My files beside or above what was shared, not in a shared folder they have no role on,
// not in a held My files, not in the trash or inside a folder in the trash, not in a shared folder in the trash. A
// result says where it is only as far as the person can open the folders above it.
func TestASearchFindsEverythingThePersonCanSeeAndNothingElse(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora, mate)
	b, o, r := d.As(bence), d.As(dora), d.As(reka)
	x := bencesFiles(t, d)

	// Bence's My files: some of it shared with Dóra, with everyone, or not at all.
	uploadedInto(t, b, x.place, 0, "Report top.txt", []byte("x"))
	uploadedInto(t, b, x.place, x.clients.ID, "Report clients.txt", []byte("x"))
	uploadedInto(t, b, x.place, x.y2026.ID, "Report deep.txt", []byte("x"))
	uploadedInto(t, b, x.place, x.private.ID, "Report private.txt", []byte("x"))
	alone := uploadedInto(t, b, x.place, x.private.ID, "Report alone.txt", []byte("x"))
	everyone := uploadedInto(t, b, x.place, 0, "Report everyone.txt", []byte("x"))
	binned := uploadedInto(t, b, x.place, x.clients.ID, "Report binned.txt", []byte("x"))
	oldReports := madeFolder(t, b, x.place, x.clients.ID, "Old reports")
	uploadedInto(t, b, x.place, oldReports.ID, "Report inside the binned folder.txt", []byte("x"))
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Report alone.txt with Dóra", share(t, b, x.place, alone.ID, dora, "viewer"))
	ok(t, "sharing Report everyone.txt with everyone", shareWithEveryone(t, b, x.place, everyone.ID, "viewer"))
	deleted(t, b, x.place, binned.ID)
	deleted(t, b, x.place, oldReports.ID)

	// Shared folders: Payroll (Dóra has no role, but one folder of it was shared with her), Team (she is a viewer),
	// Archive (she was a viewer, and it is in the trash).
	payroll := madeSharedFolder(t, b, "Payroll")
	uploadedInto(t, b, payroll.ID, 0, "Report payroll.txt", []byte("x"))
	slips := madeFolder(t, b, payroll.ID, 0, "2026 slips")
	uploadedInto(t, b, payroll.ID, slips.ID, "Report slips.txt", []byte("x"))
	ok(t, "sharing 2026 slips with Dóra", share(t, b, payroll.ID, slips.ID, dora, "editor"))
	team := madeSharedFolder(t, b, "Team")
	uploadedInto(t, b, team.ID, 0, "Report team.txt", []byte("x"))
	ok(t, "Dóra a viewer of Team", share(t, b, team.ID, 0, dora, "viewer"))
	archive := madeSharedFolder(t, b, "Archive")
	uploadedInto(t, b, archive.ID, 0, "Report archive.txt", []byte("x"))
	ok(t, "Dóra a viewer of Archive", share(t, b, archive.ID, 0, dora, "viewer"))
	ok(t, "deleting Archive", deleteSharedFolder(t, b, archive.ID))

	// Dóra's own, and Máté's, shared with her before he left: his My files is held.
	doras, _ := myFiles(t, d, dora)
	uploadedInto(t, o, doras, 0, "Report mine.txt", []byte("x"))
	mates, _ := myFiles(t, d, mate)
	mates2 := uploadedInto(t, d.As(mate), mates, 0, "Report mate.txt", []byte("x"))
	ok(t, "sharing Report mate.txt with Dóra", share(t, d.As(mate), mates, mates2.ID, dora, "viewer"))
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Máté is refused", func() bool { return d.As(mate).Get(t, "/api/me").Status == http.StatusForbidden })

	items, more, body := search(t, o, "report")
	got := []string{}
	byName := map[string]foundJSON{}
	for _, it := range items {
		got = append(got, it.Name)
		byName[it.Name] = it
	}
	want := []string{"Report alone.txt", "Report clients.txt", "Report deep.txt", "Report everyone.txt", "Report mine.txt",
		"Report slips.txt", "Report team.txt"}
	if !slices.Equal(sorted(got), want) || more {
		t.Fatalf("Dóra searching for report: %q (more %v), want %q", got, more, want)
	}
	for _, name := range []string{"Payroll", "Private", "Archive", "Report top", "Report private", "Report binned",
		"Report inside", "Old reports", "Report mate", "Report payroll", "Report archive", "Notes.txt", "Secret.txt"} {
		if strings.Contains(body, name) {
			t.Errorf("Dóra's search tells her %q: %s", name, body)
		}
	}
	// Where each is, as far as she can open the folders above it.
	for name, w := range map[string]struct {
		path      []string
		fromTop   bool
		placeName string
		kind      string
		owner     string
		role      string
	}{
		"Report clients.txt":  {[]string{"Clients"}, false, "", "my-files", bence.UserID, "viewer"},
		"Report deep.txt":     {[]string{"Clients", "Bakery", "2026"}, false, "", "my-files", bence.UserID, "viewer"},
		"Report alone.txt":    {[]string{}, false, "", "my-files", bence.UserID, "viewer"},
		"Report everyone.txt": {[]string{}, false, "", "my-files", bence.UserID, "viewer"},
		"Report slips.txt":    {[]string{"2026 slips"}, false, "", "shared-folder", "", "editor"},
		"Report team.txt":     {[]string{}, true, "Team", "shared-folder", "", "viewer"},
		"Report mine.txt":     {[]string{}, true, "", "my-files", "", "owner"},
	} {
		it := byName[name]
		owner := ""
		if it.Owner != nil {
			owner = it.Owner.UserID
		}
		if !slices.Equal(pathNames(it.Path), w.path) || it.FromTop != w.fromTop || it.PlaceName != w.placeName ||
			it.PlaceKind != w.kind || owner != w.owner || it.Role != w.role || it.Kind != "file" || it.Can == nil || it.SeenBy == nil {
			t.Errorf("%s in Dóra's search: %+v, want %+v", name, it, w)
		}
	}
	// What is found opens, at its own address.
	if res := o.Get(t, openPath(byName["Report deep.txt"].Place, byName["Report deep.txt"].ID)); res.Status != http.StatusOK {
		t.Errorf("Dóra opening Report deep.txt from her search: %d %s", res.Status, res.Body)
	}

	// Bence finds his own, and his shared folders', and nothing in the trash or in Dóra's My files.
	want = []string{"Report clients.txt", "Report deep.txt", "Report alone.txt", "Report everyone.txt", "Report payroll.txt",
		"Report private.txt", "Report slips.txt", "Report team.txt", "Report top.txt"}
	if got := foundNames(t, b, "report"); !slices.Equal(sorted(got), sorted(want)) {
		t.Errorf("Bence searching for report: %q, want %q", got, sorted(want))
	}
	// Réka, a project admin, manages every shared folder, and sees in Bence's My files only what everyone was given.
	want = []string{"Report everyone.txt", "Report payroll.txt", "Report slips.txt", "Report team.txt"}
	if got := foundNames(t, r, "report"); !slices.Equal(sorted(got), want) {
		t.Errorf("Réka searching for report: %q, want %q", got, want)
	}
	// Folders are found too, as far as the person can see them.
	if got := foundNames(t, o, "2026"); !slices.Equal(sorted(got), []string{"2026", "2026 slips"}) {
		t.Errorf("Dóra searching for 2026: %q", got)
	}
	if got := foundNames(t, o, "payroll"); len(got) != 0 {
		t.Errorf("Dóra searching for payroll: %q", got)
	}

	// Taken away, gone from her search on the next request; given back, found again.
	ok(t, "taking Clients away from Dóra", unshare(t, b, x.place, x.clients.ID, dora))
	if got := foundNames(t, o, "report clients"); len(got) != 0 {
		t.Errorf("Dóra searching after Clients was taken away: %q", got)
	}
	ok(t, "sharing Clients with Dóra again", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	if got := foundNames(t, o, "report clients"); !slices.Equal(got, []string{"Report clients.txt"}) {
		t.Errorf("Dóra searching after Clients was shared again: %q", got)
	}
	// Renamed, found by its new name only; moved into the trash, not found.
	ok(t, "renaming Report clients.txt", rename(t, b, x.place, named(t, list(t, b, x.place, x.clients.ID).Items, "Report clients.txt").ID, "Summary.txt"))
	if got := foundNames(t, o, "report clients"); len(got) != 0 {
		t.Errorf("searching for the old name: %q", got)
	}
	if got := foundNames(t, o, "summary"); !slices.Equal(got, []string{"Summary.txt"}) {
		t.Errorf("searching for the new name: %q", got)
	}
	deleted(t, b, x.place, x.bakery.ID)
	if got := foundNames(t, o, "report deep"); len(got) != 0 {
		t.Errorf("searching for what is inside a folder in the trash: %q", got)
	}

	// Only members search, through the sign-in.
	if res := d.WithHeaders(http.Header{"Remote-User-Id": {dora.UserID}}).Get(t, searchPath("report")); res.Status != http.StatusForbidden {
		t.Errorf("a search without the secret: %d %s", res.Status, res.Body)
	}
	if res := d.As(mate).Get(t, searchPath("report")); res.Status != http.StatusForbidden {
		t.Errorf("a search by someone who left: %d %s", res.Status, res.Body)
	}
}

// A search ignores case and accents, in either Unicode form of a name: typed without Hungarian letters, it finds the
// names with them, and the other way round. It looks at names only, never at what is in a file.
func TestASearchIgnoresCaseAndAccents(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	for _, name := range []string{"Számla őszi.pdf", "SZÁMLA TAVASZI.PDF", "Számla téli.pdf", "szamla nyari.pdf", "Árvíztűrő tükörfúrógép.txt"} {
		uploaded(t, c, placeID, name, []byte("the word invoice is in here, not in the name"))
	}
	all := []string{"SZÁMLA TAVASZI.PDF", "Számla téli.pdf", "Számla őszi.pdf", "szamla nyari.pdf"}
	for _, q := range []string{"szamla", "SZAMLA", "számla", "SZÁMLA", "Számla", "  számla  "} {
		if got := foundNames(t, c, q); !slices.Equal(sorted(got), sorted(all)) {
			t.Errorf("searching for %q: %q, want %q", q, got, sorted(all))
		}
	}
	for q, want := range map[string]string{"őszi": "Számla őszi.pdf", "OSZI": "Számla őszi.pdf", "teli": "Számla téli.pdf",
		"TÜKÖRFÚRÓ": "Árvíztűrő tükörfúrógép.txt", "arvizturo tukor": "Árvíztűrő tükörfúrógép.txt", "tükör árvíz": "Árvíztűrő tükörfúrógép.txt"} {
		if got := foundNames(t, c, q); !slices.Equal(got, []string{want}) {
			t.Errorf("searching for %q: %q, want %q", q, got, want)
		}
	}
	if got := foundNames(t, c, "invoice"); len(got) != 0 {
		t.Errorf("searching for a word inside the files: %q", got)
	}
}

// The best matches come first: the whole name, then its start, then the start of a word, then anywhere, then each
// word somewhere. A search answers with at most 50, and says when there are more; that, and the order, come only from
// what the person can see: matching names they cannot see take no place and leave no trace.
func TestASearchAnswersTheBestFiftyAndSaysWhenThereAreMore(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	doras, _ := myFiles(t, d, dora)
	for _, name := range []string{"Old plan notes.txt", "Planet.txt", "Plan.txt", "My plan.txt", "Airplane.txt", "Plan B.txt"} {
		uploaded(t, o, doras, name, []byte("x"))
	}
	if got, want := foundNames(t, o, "plan"), []string{"Plan.txt", "Plan B.txt", "Planet.txt", "My plan.txt", "Old plan notes.txt", "Airplane.txt"}; !slices.Equal(got, want) {
		t.Errorf("searching for plan: %q, want %q", got, want)
	}
	if got, want := foundNames(t, o, "notes plan"), []string{"Old plan notes.txt"}; !slices.Equal(got, want) {
		t.Errorf("searching for notes plan: %q, want %q", got, want)
	}

	// 60 files Dóra cannot see, all called Budget: her search finds only her own one, and says nothing more.
	bences, _ := myFiles(t, d, bence)
	secret := madeFolder(t, b, bences, 0, "Budget folder")
	for i := range 60 {
		uploadedInto(t, b, bences, secret.ID, fmt.Sprintf("Budget %02d.xlsx", i), []byte("x"))
	}
	uploaded(t, o, doras, "Budget.xlsx", []byte("x"))
	items, more, _ := search(t, o, "budget")
	if len(items) != 1 || items[0].Name != "Budget.xlsx" || more {
		t.Fatalf("Dóra searching for budget: %d items, more %v", len(items), more)
	}
	// Bence sees all of them: 50 answered, best first (all start with it: then by name), and more.
	items, more, _ = search(t, b, "budget")
	if len(items) != 50 || !more || items[0].Name != "Budget 00.xlsx" || items[49].Name != "Budget 49.xlsx" {
		t.Fatalf("Bence searching for budget: %d items, more %v, first %q", len(items), more, items[0].Name)
	}
	// Shared with Dóra, the folder's files are hers to find too.
	ok(t, "sharing Budget folder with Dóra", share(t, b, bences, secret.ID, dora, "viewer"))
	if items, more, _ := search(t, o, "budget"); len(items) != 50 || !more {
		t.Fatalf("Dóra searching for budget with the folder shared: %d items, more %v", len(items), more)
	}
}

// A search needs something to look for, and nothing longer than a name can be. It makes nothing: a member who has never
// opened the Drive finds nothing, and gets no My files from searching.
func TestASearchNeedsAPartOfAName(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	uploaded(t, c, placeID, strings.Repeat("a", 255), []byte("x"))
	for what, q := range map[string]string{"nothing": "", "spaces": "   ", "a mark alone": "́", "256 bytes": strings.Repeat("a", 256)} {
		res := c.Get(t, searchPath(q))
		if message := errorOf(t, res); res.Status != http.StatusBadRequest || message == "" {
			t.Errorf("searching for %s: got %d %s, want 400 with a reason", what, res.Status, res.Body)
		}
	}
	if got := foundNames(t, c, strings.Repeat("a", 255)); len(got) != 1 {
		t.Errorf("searching for a whole 255-byte name: %q", got)
	}
	if res := c.Get(t, "/api/search"); res.Status != http.StatusBadRequest {
		t.Errorf("a search without q: %d %s", res.Status, res.Body)
	}
	// Bence has never opened the Drive.
	if got := foundNames(t, d.As(bence), "a"); len(got) != 0 {
		t.Errorf("Bence's first search: %q", got)
	}
	if entries, _ := d.Storage.List(t, "My files"); len(entries) != 1 {
		t.Errorf("My files on the storage after Bence's search: %+v, want only Réka's", entries)
	}
}
