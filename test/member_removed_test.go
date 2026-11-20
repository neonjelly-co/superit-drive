package test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// When a member is removed from the member list: what they shared outside the project from their My files stops at
// once; their My files is kept and opens for nobody; a project admin hands it to another member or deletes it, as a
// whole and without opening it, and the act is recorded; what is in shared folders is unaffected; and someone who comes
// back with the same user id has their files back, unless they were handed over or deleted.

type (
	// heldJSON is a held My files, as a project admin sees it.
	heldJSON struct {
		Place  int64          `json:"place"`
		UserID string         `json:"userId"`
		Folder string         `json:"folder"`
		Files  int64          `json:"files"`
		Size   int64          `json:"size"`
		Used   int64          `json:"used"`
		Act    *actRecordJSON `json:"act"`
		Back   bool           `json:"back"`
	}
	// actRecordJSON is a hand-over or a deletion of a held My files: who did it, when, and what.
	actRecordJSON struct {
		ID       int64       `json:"id"`
		Kind     string      `json:"kind"`
		UserID   string      `json:"userId"`
		Folder   string      `json:"folder"`
		Files    int64       `json:"files"`
		Size     int64       `json:"size"`
		By       *personJSON `json:"by"`
		ByFormer bool        `json:"byFormer"`
		At       time.Time   `json:"at"`
		To       *personJSON `json:"to"`
		ToFormer bool        `json:"toFormer"`
		Into     string      `json:"into"`
		Done     *time.Time  `json:"done"`
	}
)

// heldList is what a project admin sees of the held My files, and the record of hand-overs and deletions.
func heldList(t *testing.T, c *harness.Client) ([]heldJSON, []actRecordJSON) {
	t.Helper()
	res := c.Get(t, "/api/held")
	if res.Status != http.StatusOK {
		t.Fatalf("the held My files: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Held []heldJSON      `json:"held"`
		Acts []actRecordJSON `json:"acts"`
	}
	res.JSON(t, &l)
	return l.Held, l.Acts
}

func handOverPath(placeID int64) string { return fmt.Sprintf("/api/held/%d/hand-over", placeID) }

func handOver(t *testing.T, c *harness.Client, placeID int64, to harness.Member) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, handOverPath(placeID), map[string]string{"to": to.UserID})
}

func deleteHeld(t *testing.T, c *harness.Client, placeID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, fmt.Sprintf("/api/held/%d", placeID), nil)
}

// actDone is the act a hand-over or deletion answered with, which must be done.
func actDone(t *testing.T, what string, res harness.Response) actRecordJSON {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s: got %d %s", what, res.Status, res.Body)
	}
	var got struct {
		Act actRecordJSON `json:"act"`
	}
	res.JSON(t, &got)
	if got.Act.Done == nil {
		t.Fatalf("%s: the act is not done: %s", what, res.Body)
	}
	return got.Act
}

// removed waits until the Drive has read a member list without m: they are refused.
func removed(t *testing.T, d *harness.Drive, m harness.Member) {
	t.Helper()
	harness.Eventually(t, m.Email+" is refused", func() bool { return d.As(m).Get(t, "/api/me").Status == http.StatusForbidden })
}

// welcomed waits until the Drive has read a member list with m: they are let in.
func welcomed(t *testing.T, d *harness.Drive, m harness.Member) {
	t.Helper()
	harness.Eventually(t, m.Email+" is let in", func() bool { return d.As(m).Get(t, "/api/me").Status == http.StatusOK })
}

// pathIn finds the item at the end of names, from the top folder of a place, through the folders as a member lists them.
func pathIn(t *testing.T, c *harness.Client, placeID int64, names ...string) itemJSON {
	t.Helper()
	var it itemJSON
	for _, name := range names {
		it = named(t, list(t, c, placeID, it.ID).Items, name)
	}
	return it
}

// The guest invitations and public links someone made from their My files stop the moment they are no longer in the
// member list, and a guest's session that depended on nothing else ends; what they made in a shared folder is the
// project's and goes on. They themselves, in a browser left open, are refused everything.
func TestWhatSomeoneWhoLeftSharedOutsideFromTheirMyFilesStops(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, o, r, v := d.As(bence), d.As(dora), d.As(reka), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))

	// From his My files: a public link, a guest (Anna) given nothing else, and Dóra.
	_, fromMine := madeLink(t, d, b, x.place, x.clients.ID, false)
	invited(t, b, x.place, x.private.ID, anna, "viewer", nil)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	// In Team, a shared folder he made: a public link, a guest (Bob, also given Notes.txt in his My files) and Dóra.
	team := madeSharedFolder(t, b, "Team")
	plan := uploadedInto(t, b, team.ID, 0, "Plan.pdf", []byte("plan"))
	_, inTeam := madeLink(t, d, b, team.ID, plan.ID, false)
	invited(t, b, team.ID, plan.ID, bob, "viewer", nil)
	invited(t, b, x.place, x.notes.ID, bob, "viewer", nil)
	ok(t, "sharing Plan.pdf with Dóra", share(t, b, team.ID, plan.ID, dora, "viewer"))
	annas, bobs := signedIn(t, d, smtp, anna), signedIn(t, d, smtp, bob)
	nothing := o.Get(t, downloadPath(x.place, x.notes.ID+1000))

	d.SetMembers(t, reka, dora)
	removed(t, d, bence)

	// Bence, in a browser left open: refused, whatever he asks.
	if res := b.Get(t, listPath(x.place, 0)); res.Status != http.StatusForbidden || !strings.Contains(errorOf(t, res), "not a member of this project") {
		t.Fatalf("Bence opening his My files once he left: got %d %s", res.Status, res.Body)
	}
	if res := uploadInto(t, b, x.place, x.clients.ID, "Late.txt", []byte("late"), ""); res.Status != http.StatusForbidden {
		t.Fatalf("Bence uploading once he left: got %d %s", res.Status, res.Body)
	}

	// From his My files: the link opens nothing, as one that never existed; Anna, given nothing else, is signed out and
	// is sent no code; what he shared with Dóra opens nothing either.
	sameAsNoLink(t, "Bence's link from his My files once he left", v.Get(t, fromMine), missing)
	harness.Eventually(t, "Anna is signed out", func() bool { return signedInAs(t, d, annas) == "" })
	n := len(codesTo(t, smtp, anna))
	askForCode(t, browser(d), anna)
	quietly(t, smtp, len(smtp.Messages()))
	if len(codesTo(t, smtp, anna)) != n {
		t.Fatal("Anna was sent a code with nothing that lets her in")
	}
	sameAs(t, "Dóra opening Clients", o.Get(t, listPath(x.place, x.clients.ID)), nothing)
	sameAs(t, "Dóra downloading Offer.pdf", o.Get(t, downloadPath(x.place, x.offer.ID)), nothing)
	if got := sharedNames(t, o); !slices.Equal(got, []string{entry("Plan.pdf", team.ID)}) {
		t.Fatalf("Dóra's Shared with me once Bence left: %q", got)
	}

	// Bob stays signed in: Plan.pdf, in Team, is the project's. Notes.txt is not his any more.
	if signedInAs(t, d, bobs) != bob {
		t.Fatal("Bob was signed out, with Plan.pdf still given to him")
	}
	if res := withCookie(d, bobs).Get(t, guestItemPath(team.ID, plan.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "plan" {
		t.Fatalf("Bob downloading Plan.pdf: got %d %s", res.Status, res.Body)
	}
	if res := withCookie(d, bobs).Get(t, guestItemPath(x.place, x.notes.ID, "/download")); res.Status != http.StatusNotFound {
		t.Fatalf("Bob downloading Notes.txt from Bence's held My files: got %d %s", res.Status, res.Body)
	}

	// In Team nothing changed: the file, its link, what was given on it to Dóra; Réka sees the link made by someone who
	// has left, working.
	if p := readPage(t, v, inTeam); p.name != "Plan.pdf" {
		t.Fatalf("the link in Team once Bence left: %+v", p)
	}
	if got := contentOf(t, o, team.ID, plan.ID); got != "plan" {
		t.Fatalf("Dóra downloading Plan.pdf: %s", got)
	}
	if l := linkOn(t, r, team.ID, plan.ID); l == nil || !l.Works || !l.Former {
		t.Fatalf("the link on Plan.pdf, as Réka sees it: %+v", l)
	}
	if lines := itemAccess(t, r, team.ID, plan.ID); !slices.Contains(lines, dora.UserID+" viewer (here)") {
		t.Fatalf("who can see Plan.pdf: %q", lines)
	}
}

// The My files of someone who left is kept as it is, on the storage and in the Drive, and opens for nobody: not for a
// project admin, not for whoever it was shared with, through no route. A project admin sees that it is held, whose it
// was and how much it holds, and nothing in it.
func TestTheMyFilesOfSomeoneWhoLeftIsKeptAndOpensForNobody(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o, r := d.As(bence), d.As(dora), d.As(reka)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	ok(t, "sharing Private with Réka", share(t, b, x.place, x.private.ID, reka, "editor"))
	ok(t, "sharing Notes.txt with everyone", shareWithEveryone(t, b, x.place, x.notes.ID, "viewer"))
	deleted(t, b, x.place, x.secret.ID)
	rekas, _ := myFiles(t, d, reka)
	nothing := r.Get(t, downloadPath(x.place+rekas+1000, x.notes.ID))

	d.SetMembers(t, reka, dora)
	removed(t, d, bence)

	at := func(f string, a ...any) string { return fmt.Sprintf(f, a...) }
	for who, c := range map[string]*harness.Client{"Réka, a project admin": r, "Dóra": o} {
		for what, res := range map[string]harness.Response{
			"listing it":             c.Get(t, listPath(x.place, 0)),
			"listing Clients":        c.Get(t, listPath(x.place, x.clients.ID)),
			"listing Private":        c.Get(t, listPath(x.place, x.private.ID)),
			"downloading Offer.pdf":  c.Get(t, downloadPath(x.place, x.offer.ID)),
			"opening Offer.pdf":      c.Get(t, openPath(x.place, x.offer.ID)),
			"downloading Notes.txt":  c.Get(t, downloadPath(x.place, x.notes.ID)),
			"downloading it as zip":  c.Get(t, zipPath(x.place, 0)),
			"downloading Clients":    c.Get(t, zipPath(x.place, x.clients.ID)),
			"seeing its trash":       c.Get(t, at("/api/places/%d/trash", x.place)),
			"restoring Secret.txt":   c.Do(t, http.MethodPost, at("/api/places/%d/trash/%d/restore", x.place, x.secret.ID), nil),
			"who can see Clients":    c.Get(t, itemAccessPath(x.place, x.clients.ID)),
			"the link on Clients":    c.Get(t, linkPath(x.place, x.clients.ID)),
			"uploading into Clients": uploadInto(t, c, x.place, x.clients.ID, "Planted.txt", []byte("x"), ""),
			"making a folder":        newFolder(t, c, x.place, 0, "Planted"),
			"renaming Notes.txt":     rename(t, c, x.place, x.notes.ID, "Renamed.txt"),
			"deleting Notes.txt":     deleteItem(t, c, x.place, x.notes.ID),
			"sharing Clients":        share(t, c, x.place, x.clients.ID, dora, "editor"),
		} {
			sameAs(t, who+" "+what, res, nothing)
		}
		if found, _, _ := search(t, c, "Offer"); len(found) != 0 {
			t.Errorf("%s searching for Offer: %+v", who, found)
		}
		if got := sharedNames(t, c); len(got) != 0 {
			t.Errorf("%s's Shared with me: %q", who, got)
		}
	}

	// Kept on the storage as it was, its trash included.
	if got, _ := d.Storage.Get(t, "My files/bence@example.com/Clients/Bakery/2026/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("Offer.pdf on the storage: %q", got)
	}
	if entries, ok := d.Storage.List(t, ".superit-drive/trash/My files/bence@example.com"); !ok || len(entries) != 1 {
		t.Fatalf("Bence's trash on the storage: %+v", entries)
	}

	// A project admin sees whose it was and how much it holds: four files, and Secret.txt in the trash. Nothing in it is
	// named. Dóra does not see it.
	held, acts := heldList(t, r)
	if len(held) != 1 || len(acts) != 0 {
		t.Fatalf("the held My files: %+v, acts %+v", held, acts)
	}
	if h := held[0]; h.Place != x.place || h.UserID != bence.UserID || h.Folder != "bence@example.com" || h.Files != 4 ||
		h.Size != 22 || h.Used != 28 || h.Act != nil {
		t.Fatalf("Bence's held My files: %+v", h)
	}
	if body := string(r.Get(t, "/api/held").Body); strings.Contains(body, "Offer") || strings.Contains(body, "Clients") {
		t.Fatalf("the held My files name what is in them: %s", body)
	}
	sameAs(t, "Dóra asking for the held My files", o.Get(t, "/api/held"), nothing)

	// A walk through the storage changes none of it.
	d.Restart(t, fastWalk("200ms"))
	walked(t, d, 1)
	if held, _ := heldList(t, d.As(reka)); len(held) != 1 || held[0].Files != 4 {
		t.Fatalf("the held My files after a walk: %+v", held)
	}
}

// A project admin hands the My files of someone who left, as a whole, to another member: it becomes a folder of its own
// in their My files, never merged into one that is there, with nothing given on it or in it (no link, no guest, no share
// comes along), and without what was in its trash. The act is recorded with who did it and when, and kept. Only a
// project admin may, only to a member, and only a held My files.
func TestAProjectAdminHandsTheFilesOfSomeoneWhoLeftToAnotherMember(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, o, r, v := d.As(bence), d.As(dora), d.As(reka), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))
	_, link := madeLink(t, d, b, x.place, x.clients.ID, false)
	invited(t, b, x.place, x.private.ID, anna, "editor", nil)
	ok(t, "sharing Clients with Réka", share(t, b, x.place, x.clients.ID, reka, "editor"))
	ok(t, "sharing Notes.txt with everyone", shareWithEveryone(t, b, x.place, x.notes.ID, "viewer"))
	deleted(t, b, x.place, x.secret.ID)
	contract := startedUpload(t, b, x.place, x.clients.ID, "Contract.pdf", 10)
	annas := signedIn(t, d, smtp, anna)
	// Dóra already has a folder called after Bence's.
	doras, _ := myFiles(t, d, dora)
	hers := madeFolder(t, o, doras, 0, "bence@example.com")
	uploadedInto(t, o, doras, hers.ID, "Hers.txt", []byte("hers"))
	rekas, _ := myFiles(t, d, reka)
	nothing := r.Get(t, downloadPath(x.place+doras+rekas+1000, x.notes.ID))

	d.SetMembers(t, reka, dora)
	removed(t, d, bence)

	// Only a project admin, only a held My files, only to a member.
	sameAs(t, "Dóra handing it to herself", handOver(t, o, x.place, dora), nothing)
	sameAs(t, "Dóra deleting it", deleteHeld(t, o, x.place), nothing)
	sameAs(t, "Dóra listing what is held", o.Get(t, "/api/held"), nothing)
	sameAs(t, "Réka handing over a place that does not exist", handOver(t, r, x.place+doras+rekas+1000, dora), nothing)
	sameAs(t, "Réka handing over Dóra's My files", handOver(t, r, doras, reka), nothing)
	sameAs(t, "Réka handing over a shared folder", handOver(t, r, madeSharedFolder(t, o, "Team").ID, reka), nothing)
	sameAs(t, "Réka handing over her own My files", handOver(t, r, rekas, dora), nothing)
	for _, to := range []harness.Member{bence, {UserID: "u-nobody"}} {
		if res := handOver(t, r, x.place, to); res.Status != http.StatusBadRequest {
			t.Fatalf("handing it to %q, who is not a member: got %d %s", to.UserID, res.Status, res.Body)
		}
	}

	before := time.Now().Add(-time.Second)
	a := actDone(t, "Réka handing Bence's files to Dóra", handOver(t, r, x.place, dora))
	if a.Kind != "hand-over" || a.UserID != bence.UserID || a.Folder != "bence@example.com" || a.By == nil || a.By.UserID != reka.UserID ||
		a.To == nil || a.To.UserID != dora.UserID || a.Into != "bence@example.com (2)" || a.Files != 4 || a.Size != 22 ||
		a.At.Before(before) || a.At.After(time.Now().Add(time.Second)) || a.Done.Before(a.At) {
		t.Fatalf("the hand-over: %+v", a)
	}

	// Dóra has them, beside her own folder of that name, which is as it was.
	_, mine := myFiles(t, d, dora)
	if got := names(mine); !slices.Equal(got, []string{"bence@example.com", "bence@example.com (2)"}) {
		t.Fatalf("Dóra's My files: %q", got)
	}
	if got := names(list(t, o, doras, hers.ID).Items); !slices.Equal(got, []string{"Hers.txt"}) {
		t.Fatalf("Dóra's own folder: %q", got)
	}
	handed := named(t, mine, "bence@example.com (2)")
	inside := list(t, o, doras, handed.ID).Items
	if got := names(inside); !slices.Equal(got, []string{"Clients", "Private", "Notes.txt"}) {
		t.Fatalf("what Dóra got: %q", got)
	}
	offer := pathIn(t, o, doras, "bence@example.com (2)", "Clients", "Bakery", "2026", "Offer.pdf")
	if got := contentOf(t, o, doras, offer.ID); got != "offer" {
		t.Fatalf("Offer.pdf, in Dóra's My files: %s", got)
	}
	if got, _ := d.Storage.Get(t, "My files/dora@example.com/bence@example.com (2)/Clients/Bakery/2026/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("Offer.pdf on the storage: %q", got)
	}

	// Nothing given on them came along: only Dóra sees them; the link is dead, Anna is signed out, Réka and everyone
	// else see nothing of them.
	for _, it := range append(inside, handed) {
		if it.SeenBy == nil || it.SeenBy.Everyone || it.SeenBy.Link || it.SeenBy.Guests != 0 || !slices.Equal(it.SeenBy.Members, []string{dora.UserID}) {
			t.Fatalf("who can see %s: %+v", it.Name, it.SeenBy)
		}
	}
	clients := named(t, inside, "Clients")
	if l := linkOn(t, o, doras, clients.ID); l != nil {
		t.Fatalf("the link on Clients came along: %+v", l)
	}
	if lines := itemAccess(t, o, doras, clients.ID); !slices.Equal(lines, []string{dora.UserID + " owner (owner)"}) {
		t.Fatalf("who can see Clients: %q", lines)
	}
	sameAsNoLink(t, "Bence's link once his files were handed over", v.Get(t, link), missing)
	harness.Eventually(t, "Anna is signed out", func() bool { return signedInAs(t, d, annas) == "" })
	n := len(codesTo(t, smtp, anna))
	askForCode(t, browser(d), anna)
	quietly(t, smtp, len(smtp.Messages()))
	if len(codesTo(t, smtp, anna)) != n {
		t.Fatal("Anna was sent a code once Bence's files were handed over")
	}
	sameAs(t, "Réka downloading Notes.txt, shared with everyone by Bence", r.Get(t, downloadPath(doras, named(t, inside, "Notes.txt").ID)), nothing)
	if got := sharedNames(t, r); len(got) != 0 {
		t.Fatalf("Réka's Shared with me: %q", got)
	}

	// His trash was not handed over, and nothing of his is left on the storage.
	if got := trashOf(t, o, doras); len(got) != 0 {
		t.Fatalf("Dóra's trash: %+v", got)
	}
	if got := onStorage(t, d, "My files"); !slices.Equal(got, []string{"dora@example.com/", "reka@example.com/"}) {
		t.Fatalf("My files on the storage: %q", got)
	}
	if got := onStorage(t, d, ".superit-drive/trash/My files"); slices.Contains(got, "bence@example.com/") {
		t.Fatalf("Bence's trash is still on the storage: %q", got)
	}

	// The record: who did it, when, what; nothing is held any more. It is kept.
	held, acts := heldList(t, r)
	if len(held) != 0 || len(acts) != 1 || acts[0].ID != a.ID || acts[0].Done == nil || !acts[0].At.Equal(a.At) {
		t.Fatalf("the held My files after the hand-over: %+v, acts %+v", held, acts)
	}
	d.Restart(t, nil) // a restart gives the Drive a new address
	b, r, v = d.As(bence), d.As(reka), visitor(d)
	if _, kept := heldList(t, r); len(kept) != 1 || kept[0].ID != a.ID || kept[0].Into != a.Into || !kept[0].Done.Equal(*a.Done) {
		t.Fatalf("the record after a restart: %+v", kept)
	}
	sameAs(t, "handing it over again", handOver(t, r, x.place, reka), nothing)
	sameAs(t, "deleting it after the hand-over", deleteHeld(t, r, x.place), nothing)

	// Bence back with the same user id: his files went to Dóra, so his My files is a new, empty one, and his upload in
	// progress went with the old one. The link stays dead.
	d.SetMembers(t, reka, bence, dora)
	welcomed(t, d, bence)
	if place, items := myFiles(t, d, bence); place == x.place || len(items) != 0 {
		t.Fatalf("Bence's My files once he is back: %d %q", place, names(items))
	}
	if res := b.Get(t, sessionPath(x.place, contract.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("Bence's upload in progress into his old My files: got %d %s", res.Status, res.Body)
	}
	sameAsNoLink(t, "Bence's link once he is back", v.Get(t, link), missing)
	if got := onStorage(t, d, "My files"); !slices.Equal(got, []string{"bence@example.com (2)/", "dora@example.com/", "reka@example.com/"}) {
		t.Fatalf("My files on the storage once Bence is back: %q", got)
	}
}

// A project admin deletes the My files of someone who left for good, as a whole, its trash included: the act is
// recorded, and everything that was shared from it stays dead.
func TestAProjectAdminDeletesTheFilesOfSomeoneWhoLeft(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, o, r, v := d.As(bence), d.As(dora), d.As(reka), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))
	_, link := madeLink(t, d, b, x.place, x.clients.ID, false)
	invited(t, b, x.place, x.private.ID, anna, "viewer", nil)
	deleted(t, b, x.place, x.secret.ID)
	annas := signedIn(t, d, smtp, anna)
	nothing := r.Get(t, downloadPath(x.place+1000, x.notes.ID))

	d.SetMembers(t, reka, dora)
	removed(t, d, bence)
	sameAs(t, "Dóra deleting it", deleteHeld(t, o, x.place), nothing)

	before := time.Now().Add(-time.Second)
	a := actDone(t, "Réka deleting Bence's files", deleteHeld(t, r, x.place))
	if a.Kind != "delete" || a.UserID != bence.UserID || a.Folder != "bence@example.com" || a.By == nil || a.By.UserID != reka.UserID ||
		a.To != nil || a.Into != "" || a.Files != 4 || a.Size != 22 || a.At.Before(before) {
		t.Fatalf("the deletion: %+v", a)
	}
	if _, ok := d.Storage.List(t, "My files/bence@example.com"); ok {
		t.Fatal("Bence's My files is still on the storage")
	}
	if _, ok := d.Storage.List(t, ".superit-drive/trash/My files/bence@example.com"); ok {
		t.Fatal("Bence's trash is still on the storage")
	}
	held, acts := heldList(t, r)
	if len(held) != 0 || len(acts) != 1 || acts[0].ID != a.ID || acts[0].Kind != "delete" {
		t.Fatalf("the held My files after the deletion: %+v, acts %+v", held, acts)
	}
	sameAsNoLink(t, "Bence's link once his files were deleted", v.Get(t, link), missing)
	harness.Eventually(t, "Anna is signed out", func() bool { return signedInAs(t, d, annas) == "" })
	var space struct {
		People []struct {
			Former bool   `json:"former"`
			Folder string `json:"folder"`
		} `json:"people"`
	}
	r.Get(t, "/api/oversight/space").JSON(t, &space)
	for _, p := range space.People {
		if p.Former {
			t.Fatalf("someone who left still takes space: %+v", space)
		}
	}

	// Back, Bence starts with a new, empty My files.
	d.SetMembers(t, reka, bence, dora)
	welcomed(t, d, bence)
	if place, items := myFiles(t, d, bence); place == x.place || len(items) != 0 {
		t.Fatalf("Bence's My files once he is back: %d %q", place, names(items))
	}
}

// Someone who leaves the member list and comes back with the same user id (a list briefly wrong, a mistake undone, a new
// address) has their My files back as it was: their files, their trash, and what they shared from it, which works
// again. People are told apart by user id, never by address: two members swapping addresses keep their own files.
func TestSomeoneWhoLeftAndCameBackHasTheirFilesBack(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b, o, r, v := d.As(bence), d.As(dora), d.As(reka), visitor(d)
	x := bencesFiles(t, d)
	missing := v.Get(t, noSuchLink(t))
	_, link := madeLink(t, d, b, x.place, x.clients.ID, false)
	invited(t, b, x.place, x.private.ID, anna, "viewer", nil)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	deleted(t, b, x.place, x.notes.ID)
	annas := signedIn(t, d, smtp, anna)
	doras, _ := myFiles(t, d, dora)

	// The list briefly holds only Réka: Bence and Dóra are refused, what Bence shared stops, and both their My files are
	// held.
	d.SetMembers(t, reka)
	removed(t, d, bence)
	removed(t, d, dora)
	sameAsNoLink(t, "Bence's link while he is out", v.Get(t, link), missing)
	if signedInAs(t, d, annas) != "" {
		t.Fatal("Anna still signed in while Bence is out")
	}
	if held, _ := heldList(t, r); len(held) != 2 {
		t.Fatalf("held while the list holds only Réka: %+v", held)
	}

	// Back, Bence with a new address: everything is as it was, the trash included, and what he shared works again
	// (Anna signs in again with a new code).
	moved := bence
	moved.Email = "bence.toth@example.com"
	d.SetMembers(t, reka, moved, dora)
	welcomed(t, d, moved)
	welcomed(t, d, dora)
	place, items := myFiles(t, d, moved)
	if place != x.place || !slices.Equal(names(items), []string{"Clients", "Private"}) {
		t.Fatalf("Bence's My files once he is back: %d %q", place, names(items))
	}
	if got := contentOf(t, b, x.place, x.offer.ID); got != "offer" {
		t.Fatalf("Offer.pdf once he is back: %s", got)
	}
	if trash := trashOf(t, b, x.place); len(trash) != 1 || trash[0].Name != "Notes.txt" {
		t.Fatalf("Bence's trash once he is back: %+v", trash)
	}
	restored(t, b, x.place, x.notes.ID)
	if got := names(list(t, o, x.place, x.clients.ID).Items); !slices.Equal(got, []string{"Bakery", "Price list.pdf"}) {
		t.Fatalf("Dóra opening Clients once Bence is back: %q", got)
	}
	if p := readPage(t, v, link); p.name != "Clients" {
		t.Fatalf("Bence's link once he is back: %+v", p)
	}
	if res := withCookie(d, signedIn(t, d, smtp, anna)).Get(t, guestItemPath(x.place, x.secret.ID, "/download")); res.Status != http.StatusOK {
		t.Fatalf("Anna downloading Secret.txt once Bence is back: got %d %s", res.Status, res.Body)
	}
	if held, acts := heldList(t, r); len(held) != 0 || len(acts) != 0 {
		t.Fatalf("held once both are back: %+v, %+v", held, acts)
	}

	// Bence and Dóra swap addresses: each keeps their own My files, by user id.
	swappedB, swappedD := bence, dora
	swappedB.Email, swappedD.Email = dora.Email, bence.Email
	d.SetMembers(t, reka, swappedB, swappedD)
	harness.Eventually(t, "the swap is read", func() bool { return meOf(t, d, bence).Email == dora.Email })
	if place, _ := myFiles(t, d, swappedB); place != x.place {
		t.Fatalf("Bence's My files after the swap: %d, want %d", place, x.place)
	}
	if place, _ := myFiles(t, d, swappedD); place != doras {
		t.Fatalf("Dóra's My files after the swap: %d, want %d", place, doras)
	}
	if got := names(list(t, o, x.place, x.clients.ID).Items); !slices.Equal(got, []string{"Bakery", "Price list.pdf"}) {
		t.Fatalf("Dóra opening Clients after the swap: %q", got)
	}
}

// A hand-over is recorded before anything changes, and done in steps: when the Drive stops after its files moved on the
// storage, and before it knew, it finishes the hand-over when it starts again, before anything looks at the storage.
// The files are then the receiver's, with their ids, even if their owner came back meanwhile: the act was decided. No
// walk through the storage undoes it.
func TestAHandOverCutShortIsFinishedWhenTheDriveStartsAgain(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: map[string]string{harness.TestStopAt: "hand-over moved"}})
	b, o, r := d.As(bence), d.As(dora), d.As(reka)
	x := bencesFiles(t, d)
	deleted(t, b, x.place, x.secret.ID)
	doras, _ := myFiles(t, d, dora)
	d.SetMembers(t, reka, dora)
	removed(t, d, bence)

	if res, err := r.Try(t, http.MethodPost, handOverPath(x.place), strings.NewReader(`{"to":"`+dora.UserID+`"}`)); err == nil {
		t.Fatalf("the Drive answered the hand-over it was to stop in: %d %s", res.Status, res.Body)
	}
	if !d.Exited() {
		t.Fatal("the Drive did not stop")
	}
	// It stopped after the move on the storage: the files are in Dóra's folder there, Bence's trash is still there.
	if got, _ := d.Storage.Get(t, "My files/dora@example.com/bence@example.com/Clients/Bakery/2026/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("Offer.pdf on the storage when the Drive stopped: %q", got)
	}
	if _, ok := d.Storage.List(t, ".superit-drive/trash/My files/bence@example.com"); !ok {
		t.Fatal("Bence's trash went before the Drive stopped")
	}

	// Bence is back meanwhile; the Drive starts again, and walks the storage at once.
	d.SetMembers(t, reka, bence, dora)
	env := fastWalk("300ms")
	env[harness.TestStopAt] = ""
	d.Restart(t, env)
	b, o, r = d.As(bence), d.As(dora), d.As(reka)
	var a actRecordJSON
	harness.Eventually(t, "the hand-over is finished", func() bool {
		_, acts := heldList(t, r)
		if len(acts) == 1 && acts[0].Done != nil {
			a = acts[0]
		}
		return a.Done != nil
	})
	if a.Kind != "hand-over" || a.To == nil || a.To.UserID != dora.UserID || a.Into != "bence@example.com" || a.Files != 4 {
		t.Fatalf("the hand-over, finished: %+v", a)
	}
	walked(t, d, 2)
	if got := contentOf(t, o, doras, pathIn(t, o, doras, "bence@example.com", "Clients", "Bakery", "2026", "Offer.pdf").ID); got != "offer" {
		t.Fatalf("Offer.pdf in Dóra's My files: %s", got)
	}
	if got := names(list(t, o, doras, pathIn(t, o, doras, "bence@example.com").ID).Items); !slices.Equal(got, []string{"Clients", "Private", "Notes.txt"}) {
		t.Fatalf("what Dóra got: %q", got)
	}
	if _, ok := d.Storage.List(t, ".superit-drive/trash/My files/bence@example.com"); ok {
		t.Fatal("Bence's trash is still on the storage")
	}
	welcomed(t, d, bence)
	if place, items := myFiles(t, d, bence); place == x.place || len(items) != 0 {
		t.Fatalf("Bence's My files once he is back: %d %q", place, names(items))
	}
	if strings.Contains(d.Logs(), "could not be finished") {
		t.Fatalf("the hand-over failed on its way:\n%s", d.Logs())
	}
}

// A deletion cut short after the files went from the storage, before the index knew, is finished when the Drive starts
// again.
func TestADeletionCutShortIsFinishedWhenTheDriveStartsAgain(t *testing.T) {
	t.Parallel()
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: map[string]string{harness.TestStopAt: "delete removed"}})
	r := d.As(reka)
	x := bencesFiles(t, d)
	d.SetMembers(t, reka, dora)
	removed(t, d, bence)

	if res, err := r.Try(t, http.MethodDelete, fmt.Sprintf("/api/held/%d", x.place), nil); err == nil {
		t.Fatalf("the Drive answered the deletion it was to stop in: %d %s", res.Status, res.Body)
	}
	if !d.Exited() {
		t.Fatal("the Drive did not stop")
	}
	if _, ok := d.Storage.List(t, "My files/bence@example.com"); ok {
		t.Fatal("Bence's My files is still on the storage when the Drive stopped")
	}
	d.Restart(t, map[string]string{harness.TestStopAt: ""})
	r = d.As(reka)
	harness.Eventually(t, "the deletion is finished", func() bool {
		held, acts := heldList(t, r)
		return len(held) == 0 && len(acts) == 1 && acts[0].Done != nil && acts[0].Kind == "delete"
	})
}

// Two acts asked for at once on the same held My files (two project admins, or a double click): one is done, the other
// is refused, and the record holds one.
func TestTwoActsAtOnceOnTheSameHeldMyFilesDoOne(t *testing.T) {
	t.Parallel()
	emil := harness.Member{UserID: "e81f02aa-emil", Email: "emil@example.com", DisplayName: "Emil Szabó", Role: "admin"}
	d := harness.Start(t, reka, bence, dora, emil)
	x := bencesFiles(t, d)
	doras, _ := myFiles(t, d, dora)
	d.SetMembers(t, reka, dora, emil)
	removed(t, d, bence)

	answers := make(chan harness.Response, 3)
	for _, ask := range []func() (harness.Response, error){
		func() (harness.Response, error) {
			return d.As(reka).Try(t, http.MethodPost, handOverPath(x.place), strings.NewReader(`{"to":"`+dora.UserID+`"}`))
		},
		func() (harness.Response, error) {
			return d.As(emil).Try(t, http.MethodPost, handOverPath(x.place), strings.NewReader(`{"to":"`+emil.UserID+`"}`))
		},
		func() (harness.Response, error) {
			return d.As(emil).Try(t, http.MethodDelete, fmt.Sprintf("/api/held/%d", x.place), nil)
		},
	} {
		go func() {
			res, err := ask()
			if err != nil {
				res = harness.Response{Status: -1, Body: []byte(err.Error())}
			}
			answers <- res
		}()
	}
	done := 0
	for range 3 {
		res := <-answers
		switch res.Status {
		case http.StatusOK:
			done++
		case http.StatusNotFound, http.StatusConflict:
		default:
			t.Errorf("an act asked for at the same time as others: got %d %s", res.Status, res.Body)
		}
	}
	held, acts := heldList(t, d.As(reka))
	if done != 1 || len(held) != 0 || len(acts) != 1 || acts[0].Done == nil {
		t.Fatalf("%d acts done; held %+v, acts %+v", done, held, acts)
	}
	_, mine := myFiles(t, d, dora)
	emils, his := myFiles(t, d, emil)
	switch a := acts[0]; {
	case a.Kind == "delete" && len(mine) == 0 && len(his) == 0:
	case a.Kind == "hand-over" && a.To.UserID == dora.UserID && slices.Equal(names(mine), []string{"bence@example.com"}) && len(his) == 0:
	case a.Kind == "hand-over" && a.To.UserID == emil.UserID && len(mine) == 0 && slices.Equal(names(his), []string{"bence@example.com"}):
	default:
		t.Fatalf("the act %+v, and Dóra has %q (%d), Emil %q (%d)", a, names(mine), doras, names(his), emils)
	}
}
