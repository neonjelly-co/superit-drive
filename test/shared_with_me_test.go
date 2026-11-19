package test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's call for "Shared with me", as the interface makes it.

type (
	personJSON struct {
		UserID      string `json:"userId"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
	}
	sharedItemJSON struct {
		itemJSON
		Place      int64       `json:"place"`
		PlaceKind  string      `json:"placeKind"`
		Owner      *personJSON `json:"owner"`
		Role       string      `json:"role"`
		SharedBy   *personJSON `json:"sharedBy"`
		ToEveryone bool        `json:"toEveryone"`
		Shared     time.Time   `json:"shared"`
	}
)

// sharedWithMe is "Shared with me" for the client, and the answer's raw body.
func sharedWithMe(t *testing.T, c *harness.Client) ([]sharedItemJSON, string) {
	t.Helper()
	res := c.Get(t, "/api/shared-with-me")
	if res.Status != http.StatusOK {
		t.Fatalf("Shared with me: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Items []sharedItemJSON `json:"items"`
	}
	res.JSON(t, &l)
	return l.Items, string(res.Body)
}

// sharedNames is "Shared with me" as "name (place)" lines, in the order it lists them.
func sharedNames(t *testing.T, c *harness.Client) []string {
	t.Helper()
	items, _ := sharedWithMe(t, c)
	out := []string{}
	for _, it := range items {
		out = append(out, fmt.Sprintf("%s (%d)", it.Name, it.Place))
	}
	return out
}

func entry(name string, place int64) string { return fmt.Sprintf("%s (%d)", name, place) }

// "Shared with me" lists what others shared with the member that they reach only through that share: from someone's
// My files, and from a shared folder they have no role on as a whole; a folder or a single file; by name or with
// everyone in the project. Each path counts once, at its highest shared item. Nothing they reach through their own
// places is listed, and the names above what was shared are never sent.
func TestSharedWithMeListsWhatOthersSharedWithTheMember(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	if got := sharedNames(t, o); len(got) != 0 {
		t.Fatalf("Dóra's Shared with me before anything was shared: %q", got)
	}

	// From Bence's My files: a folder, a file inside it (already reached through the folder), a file on its own, and a
	// folder shared with everyone.
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Offer.pdf, inside Clients, with Dóra", share(t, b, x.place, x.offer.ID, dora, "editor"))
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	ok(t, "sharing Private with everyone", shareWithEveryone(t, b, x.place, x.private.ID, "viewer"))
	// From shared folders: Payroll, which Dóra has no role on, and Team, which is in her places.
	payroll := madeSharedFolder(t, b, "Payroll")
	year := madeFolder(t, b, payroll.ID, 0, "2026 slips")
	slip := uploadedInto(t, b, payroll.ID, year.ID, "November.pdf", []byte("slip"))
	summary := uploadedInto(t, b, payroll.ID, 0, "Summary.pdf", []byte("summary"))
	ok(t, "sharing 2026 slips with Dóra", share(t, b, payroll.ID, year.ID, dora, "editor"))
	ok(t, "sharing Summary.pdf with Dóra", share(t, b, payroll.ID, summary.ID, dora, "viewer"))
	team := madeSharedFolder(t, b, "Team")
	plan := uploadedInto(t, b, team.ID, 0, "Plan.txt", []byte("plan"))
	ok(t, "Dóra a viewer of Team", share(t, b, team.ID, 0, dora, "viewer"))
	ok(t, "sharing Plan.txt with Dóra too", share(t, b, team.ID, plan.ID, dora, "editor"))
	// Her own My files: nothing of hers is "shared with her".
	doras, _ := myFiles(t, d, dora)
	uploaded(t, o, doras, "Mine.txt", []byte("mine"))

	items, body := sharedWithMe(t, o)
	want := []string{entry("Summary.pdf", payroll.ID), entry("2026 slips", payroll.ID), entry("Private", x.place),
		entry("Notes.txt", x.place), entry("Clients", x.place)}
	if got := sharedNames(t, o); !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me: %q, want %q, the most recently shared first", got, want)
	}
	// Nothing above what was shared, and not the shared folder's name: she cannot open them.
	for _, name := range []string{"Payroll", "Bakery", "2026\"", "Team", "Plan.txt", "Mine.txt", "Offer.pdf"} {
		if strings.Contains(body, name) {
			t.Errorf("Shared with me tells Dóra %q: %s", name, body)
		}
	}
	byName := map[string]sharedItemJSON{}
	for _, it := range items {
		byName[it.Name] = it
	}
	clients := byName["Clients"]
	if clients.Kind != "folder" || clients.PlaceKind != "my-files" || clients.Role != "viewer" || clients.Owner == nil ||
		clients.Owner.UserID != bence.UserID || clients.SharedBy == nil || clients.SharedBy.DisplayName != bence.DisplayName ||
		clients.ToEveryone || clients.Shared.IsZero() || !slices.Equal(clients.Can, []string{"list", "download", "see-access"}) {
		t.Errorf("Clients in Dóra's Shared with me: %+v", clients)
	}
	if n := byName["Notes.txt"]; n.Kind != "file" || n.Size == nil || *n.Size != int64(len("notes")) || n.Role != "viewer" {
		t.Errorf("Notes.txt in Dóra's Shared with me: %+v", n)
	}
	if p := byName["Private"]; !p.ToEveryone || p.SharedBy == nil || p.SharedBy.UserID != bence.UserID || p.SeenBy == nil || !p.SeenBy.Everyone {
		t.Errorf("Private, shared with everyone: %+v", p)
	}
	if y := byName["2026 slips"]; y.PlaceKind != "shared-folder" || y.Owner != nil || y.Role != "editor" ||
		!slices.Contains(y.Can, "upload") || slices.Contains(y.Can, "rename") || slices.Contains(y.Can, "move-in") == false {
		t.Errorf("2026 slips in Dóra's Shared with me: %+v", y)
	}
	// Everyone in the project includes the project admin; what was shared with Dóra by name is hers alone.
	if got, want := sharedNames(t, d.As(reka)), []string{entry("Private", x.place)}; !slices.Equal(got, want) {
		t.Errorf("Réka's Shared with me: %q, want %q", got, want)
	}
	if got := sharedNames(t, b); len(got) != 0 {
		t.Errorf("Bence's Shared with me: %q", got)
	}

	// What is listed opens: the folder from its own address, with the path starting at it; the file downloads, and its
	// details say who can see it.
	l := list(t, o, payroll.ID, year.ID)
	if l.FromTop || !slices.Equal(pathNames(l.Path), []string{"2026 slips"}) || !slices.Equal(names(l.Items), []string{"November.pdf"}) {
		t.Fatalf("Dóra opening 2026 slips: %+v", l)
	}
	if res := o.Get(t, downloadPath(payroll.ID, slip.ID)); res.Status != http.StatusOK || string(res.Body) != "slip" {
		t.Fatalf("Dóra downloading November.pdf: %d %s", res.Status, res.Body)
	}
	if res := o.Get(t, downloadPath(x.place, x.notes.ID)); res.Status != http.StatusOK || string(res.Body) != "notes" {
		t.Fatalf("Dóra downloading Notes.txt: %d %s", res.Status, res.Body)
	}
	if got := accessLines(accessOf(t, o, itemAccessPath(payroll.ID, summary.ID))); !slices.Equal(got,
		[]string{reka.UserID + " manager (admin)", bence.UserID + " manager (from a folder above)", dora.UserID + " viewer (here)"}) {
		t.Fatalf("who can see Summary.pdf, for Dóra: %q", got)
	}
	// The shared folder stays out of her places.
	if _, sees := seesSharedFolder(t, d, dora, payroll.ID); sees {
		t.Fatal("Payroll is in Dóra's places")
	}

	// Taking the folder away brings what was shared inside it on its own into the list.
	ok(t, "taking Clients away from Dóra", unshare(t, b, x.place, x.clients.ID, dora))
	if got := sharedNames(t, o); !slices.Contains(got, entry("Offer.pdf", x.place)) || slices.Contains(got, entry("Clients", x.place)) {
		t.Fatalf("Dóra's Shared with me without Clients: %q", got)
	}
	// Given Payroll as a whole, it is in her places, and nothing in it is listed any more.
	ok(t, "Dóra a viewer of Payroll", share(t, b, payroll.ID, 0, dora, "viewer"))
	if got := sharedNames(t, o); slices.Contains(got, entry("2026 slips", payroll.ID)) || slices.Contains(got, entry("Summary.pdf", payroll.ID)) {
		t.Fatalf("Dóra's Shared with me with Payroll in her places: %q", got)
	}
}

// "Shared with me" is decided again on every request, as everything else is: what no longer counts is not listed. A
// held My files, an item in the trash, a shared folder in the trash, a share taken away, or one given to someone else.
func TestSharedWithMeListsOnlyWhatStillCounts(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora, mate)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	ok(t, "sharing Private with Máté", share(t, b, x.place, x.private.ID, mate, "viewer"))
	payroll := madeSharedFolder(t, b, "Payroll")
	year := madeFolder(t, b, payroll.ID, 0, "2026")
	ok(t, "sharing Payroll's 2026 with Dóra", share(t, b, payroll.ID, year.ID, dora, "viewer"))
	all := []string{entry("2026", payroll.ID), entry("Notes.txt", x.place), entry("Clients", x.place)}
	if got := sharedNames(t, o); !slices.Equal(got, all) {
		t.Fatalf("Dóra's Shared with me: %q, want %q", got, all)
	}

	// Notes.txt in the trash: not listed; back from it: listed again, with its share.
	deleted(t, b, x.place, x.notes.ID)
	if got := sharedNames(t, o); slices.Contains(got, entry("Notes.txt", x.place)) {
		t.Fatalf("Notes.txt in the trash is listed: %q", got)
	}
	restored(t, b, x.place, x.notes.ID)
	// Payroll in the trash: nothing of it is listed; restored, it is back.
	ok(t, "deleting Payroll", deleteSharedFolder(t, b, payroll.ID))
	if got := sharedNames(t, o); slices.Contains(got, entry("2026", payroll.ID)) {
		t.Fatalf("2026 of Payroll in the trash is listed: %q", got)
	}
	ok(t, "restoring Payroll", restoreSharedFolder(t, b, payroll.ID))
	if got := sharedNames(t, o); !slices.Equal(got, all) {
		t.Fatalf("Dóra's Shared with me after the restores: %q, want %q", got, all)
	}
	// Bence leaves: his My files is held, and nothing shared from it is listed; Payroll is the project's and stays.
	d.SetMembers(t, reka, dora, mate)
	harness.Eventually(t, "Bence is refused", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	if got, want := sharedNames(t, o), []string{entry("2026", payroll.ID)}; !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me with Bence gone: %q, want %q", got, want)
	}
	if got := sharedNames(t, d.As(mate)); len(got) != 0 {
		t.Fatalf("Máté's Shared with me with Bence gone: %q", got)
	}
	d.SetMembers(t, reka, bence, dora, mate)
	harness.Eventually(t, "Bence is back", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	// Máté leaves: he is refused before anything is listed; his share helps nobody else.
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Máté is refused", func() bool { return d.As(mate).Get(t, "/api/me").Status == http.StatusForbidden })
	if res := d.As(mate).Get(t, "/api/shared-with-me"); res.Status != http.StatusForbidden {
		t.Fatalf("Máté's Shared with me after he left: %d %s", res.Status, res.Body)
	}
	if got := sharedNames(t, o); !slices.Equal(got, all) {
		t.Fatalf("Dóra's Shared with me: %q, want %q", got, all)
	}
	// A share taken away is not listed from the very next request.
	ok(t, "taking Clients away from Dóra", unshare(t, b, x.place, x.clients.ID, dora))
	if got := sharedNames(t, o); slices.Contains(got, entry("Clients", x.place)) {
		t.Fatalf("Clients is still listed: %q", got)
	}
	// Gone from the storage behind the Drive's back: once a listing of its folder sees it gone, so is the share.
	d.Storage.Delete(t, "My files/bence@example.com/Notes.txt", false)
	list(t, b, x.place, 0)
	if got, want := sharedNames(t, o), []string{entry("2026", payroll.ID)}; !slices.Equal(got, want) {
		t.Fatalf("Dóra's Shared with me: %q, want %q", got, want)
	}
	// Only members ask, through the sign-in.
	if res := d.WithHeaders(http.Header{"Remote-User-Id": {dora.UserID}}).Get(t, "/api/shared-with-me"); res.Status != http.StatusForbidden {
		t.Fatalf("Shared with me without the secret: %d %s", res.Status, res.Body)
	}
}
