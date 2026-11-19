package test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's calls for moving into another place, as the interface makes them: toPlace is the place it goes to,
// to the folder in it (0: its top folder).

func moveInto(t *testing.T, c *harness.Client, placeID, itemID, toPlace, to int64) harness.Response {
	t.Helper()
	var dest any
	if to != 0 {
		dest = to
	}
	return sendJSON(t, c, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/move", placeID, itemID), map[string]any{"to": dest, "place": toPlace})
}

func previewPath(placeID, itemID, toPlace, to int64) string {
	p := fmt.Sprintf("/api/places/%d/items/%d/move?place=%d", placeID, itemID, toPlace)
	if to != 0 {
		p += fmt.Sprintf("&to=%d", to)
	}
	return p
}

type previewJSON struct {
	BecomesProjects bool         `json:"becomesProjects"`
	Access          []accessJSON `json:"access"`
	SeenBy          seenByJSON   `json:"seenBy"`
}

// preview is who would see the item where it would go, as the Drive says before the move; and the raw body.
func preview(t *testing.T, c *harness.Client, placeID, itemID, toPlace, to int64) (previewJSON, string) {
	t.Helper()
	res := c.Get(t, previewPath(placeID, itemID, toPlace, to))
	if res.Status != http.StatusOK {
		t.Fatalf("who would see item %d in place %d: got %d %s", itemID, toPlace, res.Status, res.Body)
	}
	var p previewJSON
	res.JSON(t, &p)
	return p, string(res.Body)
}

var mate = harness.Member{UserID: "e7aa01b2-mate", Email: "mate@example.com", DisplayName: "Máté Szabó", Role: "member"}

// Bence moves Bakery out of his My files into the shared folder Team. Before, the Drive says who will see it there: Team's
// managers and the project admins, and Máté, to whom Bakery itself was given. After, it is the project's: it is in
// Team's folder on the storage, keeps its id and what was given on it and on everything in it, and no longer has what
// it had from Clients, the folder above it in Bence's My files.
func TestMovingFromMyFilesIntoASharedFolderWarnsThenKeepsOnlyItsOwnAccess(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora, mate)
	b, o, m := d.As(bence), d.As(dora), d.As(mate)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Bakery with Máté", share(t, b, x.place, x.bakery.ID, mate, "editor"))
	ok(t, "sharing Ledger.xlsx, inside Bakery, with Dóra", share(t, b, x.place, x.ledger.ID, dora, "viewer"))
	team := madeSharedFolder(t, b, "Team")
	if l := list(t, b, x.place, x.bakery.ID); !slices.Contains(l.Can, "move-out") {
		t.Fatalf("what Bence may do in Bakery: %q", l.Can)
	}
	if p, _ := seesSharedFolder(t, d, bence, team.ID); !slices.Contains(p.Can, "move-in") {
		t.Fatalf("what Bence may do in Team: %q", p.Can)
	}

	// Before: who will see it there. Dóra, who has it now from Clients, is not among them.
	p, _ := preview(t, b, x.place, x.bakery.ID, team.ID, 0)
	want := []string{reka.UserID + " manager (admin)", bence.UserID + " manager (from Team)", mate.UserID + " editor (here)"}
	if got := accessLines(p.Access); !p.BecomesProjects || !slices.Equal(got, want) {
		t.Fatalf("who would see Bakery in Team: becomes the project's %v, %q, want %q", p.BecomesProjects, got, want)
	}
	if p.SeenBy.Everyone || !slices.Equal(p.SeenBy.Members, []string{bence.UserID, mate.UserID, reka.UserID}) {
		t.Fatalf("who would see Bakery in Team, in a sentence: %+v", p.SeenBy)
	}
	// Nothing moved yet.
	if got := onStorage(t, d, "Shared folders/Team"); len(got) != 0 {
		t.Fatalf("Team on the storage after the preview: %q", got)
	}

	res := moveInto(t, b, x.place, x.bakery.ID, team.ID, 0)
	var moved struct {
		itemJSON
		Place int64 `json:"place"`
	}
	res.JSON(t, &moved)
	if res.Status != http.StatusOK || moved.ID != x.bakery.ID || moved.Place != team.ID || moved.Name != "Bakery" {
		t.Fatalf("moving Bakery into Team: %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "Shared folders/Team/Bakery/2026"); !slices.Equal(got, []string{"Ledger.xlsx", "Offer.pdf"}) {
		t.Fatalf("Team's Bakery/2026 on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients"); !slices.Equal(got, []string{"Price list.pdf"}) {
		t.Fatalf("Bence's Clients on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, "Shared folders/Team/Bakery/2026/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("Offer.pdf in Team holds %q", got)
	}
	// Who can see it there is what the warning said.
	if got := accessLines(accessOf(t, b, itemAccessPath(team.ID, x.bakery.ID))); !slices.Equal(got, want) {
		t.Fatalf("who can see Bakery in Team: %q, want %q", got, want)
	}
	if got := names(list(t, b, team.ID, 0).Items); !slices.Equal(got, []string{"Bakery"}) {
		t.Fatalf("Team lists %q", got)
	}
	// It keeps its ids and what was given on it: Máté works in Bakery, and finds it in his Shared with me.
	uploadedInto(t, m, team.ID, x.y2026.ID, "From Máté.txt", []byte("máté"))
	if got := sharedNames(t, m); !slices.Equal(got, []string{entry("Bakery", team.ID)}) {
		t.Fatalf("Máté's Shared with me: %q", got)
	}
	// What was given on Ledger.xlsx inside it went with it; what Dóra had from Clients did not.
	if res := o.Get(t, downloadPath(team.ID, x.ledger.ID)); res.Status != http.StatusOK || string(res.Body) != "ledger" {
		t.Fatalf("Dóra downloading Ledger.xlsx in Team: %d %s", res.Status, res.Body)
	}
	missing := o.Get(t, downloadPath(team.ID, x.notes.ID+1000))
	sameAs(t, "Dóra downloading Offer.pdf in Team", o.Get(t, downloadPath(team.ID, x.offer.ID)), missing)
	sameAs(t, "Dóra listing Bakery in Team", o.Get(t, listPath(team.ID, x.bakery.ID)), missing)
	// Nothing of it is found through Bence's My files any more.
	for what, path := range map[string]string{"Bakery": listPath(x.place, x.bakery.ID), "Offer.pdf": downloadPath(x.place, x.offer.ID)} {
		sameAs(t, "Bence reaching "+what+" through his My files", b.Get(t, path), b.Get(t, downloadPath(x.place, x.notes.ID+1000)))
	}
	// Dóra still has Clients, and nothing else there.
	if got := names(list(t, o, x.place, x.clients.ID).Items); !slices.Equal(got, []string{"Price list.pdf"}) {
		t.Fatalf("Dóra's Clients: %q", got)
	}
	// Bence, a manager of Team, can do everything with it there; the project admin too.
	ok(t, "Bence renaming Bakery in Team", rename(t, b, team.ID, x.bakery.ID, "Bakery Ltd"))
	if l := list(t, d.As(reka), team.ID, x.y2026.ID); l.Role != "manager" || !slices.Equal(pathNames(l.Path), []string{"Bakery Ltd", "2026"}) ||
		!slices.Equal(names(l.Items), []string{"From Máté.txt", "Ledger.xlsx", "Offer.pdf"}) {
		t.Fatalf("Réka's Team › Bakery Ltd › 2026: %+v", l)
	}
}

// Only what is in one's own My files moves to another place, and only into a shared folder where one may add: the
// rest is refused, and nothing moves. A place or folder one cannot add to answers as one that does not exist.
func TestOnlyOwnMyFilesMoveIntoASharedFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	team := madeSharedFolder(t, b, "Team")
	plan := uploadedInto(t, b, team.ID, 0, "Plan.txt", []byte("plan"))
	archive := madeSharedFolder(t, b, "Archive")
	readOnly := madeSharedFolder(t, d.As(reka), "Read only")
	ok(t, "Bence a viewer of Read only", share(t, d.As(reka), readOnly.ID, 0, bence, "viewer"))
	doras := madeSharedFolder(t, o, "Dóra's")
	gone := madeSharedFolder(t, b, "Gone")
	ok(t, "deleting Gone", deleteSharedFolder(t, b, gone.ID))
	bencesOwn, _ := myFiles(t, d, bence)
	dorasOwn, _ := myFiles(t, d, dora)
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	nowhere := team.ID + archive.ID + readOnly.ID + doras.ID + gone.ID + bencesOwn + dorasOwn + 100

	isNotBetweenPlaces := func(what string, res harness.Response) {
		t.Helper()
		if res.Status != http.StatusBadRequest || !strings.Contains(errorOf(t, res), "Only what is in your own My files") {
			t.Errorf("%s: got %d %s, want 400 saying which moves there are", what, res.Status, res.Body)
		}
	}
	// Out of a shared folder: neither into another shared folder nor into one's own My files.
	isNotBetweenPlaces("Bence moving Plan.txt from Team into Archive", moveInto(t, b, team.ID, plan.ID, archive.ID, 0))
	isNotBetweenPlaces("Bence moving Plan.txt from Team into his My files", moveInto(t, b, team.ID, plan.ID, bencesOwn, 0))
	// Out of someone else's My files: what was shared with Dóra stays where Bence keeps it.
	isNotBetweenPlaces("Dóra moving Bakery out of Bence's My files into her shared folder", moveInto(t, o, x.place, x.bakery.ID, doras.ID, 0))
	isNotBetweenPlaces("Dóra moving Bakery into her own My files", moveInto(t, o, x.place, x.bakery.ID, dorasOwn, 0))
	// The same, asked before a move.
	if res := o.Get(t, previewPath(x.place, x.bakery.ID, doras.ID, 0)); res.Status != http.StatusBadRequest {
		t.Errorf("Dóra asking who would see Bakery in her shared folder: %d %s", res.Status, res.Body)
	}

	// Where Bence cannot add: as a place or folder that does not exist.
	noPlace := moveInto(t, b, x.place, x.notes.ID, nowhere, 0)
	if noPlace.Status != http.StatusNotFound {
		t.Fatalf("moving into a place that does not exist: %d %s", noPlace.Status, noPlace.Body)
	}
	for what, res := range map[string]harness.Response{
		"a shared folder he only views":        moveInto(t, b, x.place, x.notes.ID, readOnly.ID, 0),
		"a shared folder he has no role on":    moveInto(t, b, x.place, x.notes.ID, doras.ID, 0),
		"a shared folder in the trash":         moveInto(t, b, x.place, x.notes.ID, gone.ID, 0),
		"Dóra's My files":                      moveInto(t, b, x.place, x.notes.ID, dorasOwn, 0),
		"a folder of Team through Archive":     moveInto(t, b, x.place, x.notes.ID, archive.ID, plan.ID),
		"a folder of his My files, in Archive": moveInto(t, b, x.place, x.notes.ID, archive.ID, x.private.ID),
		"who would see it in Dóra's":           b.Get(t, previewPath(x.place, x.notes.ID, doras.ID, 0)),
		"who would see it in the trash":        b.Get(t, previewPath(x.place, x.notes.ID, gone.ID, 0)),
	} {
		if res.Status != http.StatusNotFound || string(res.Body) != string(noPlace.Body) {
			t.Errorf("Bence moving Notes.txt into %s: got %d %s; a place that does not exist answers %d %s", what, res.Status, res.Body, noPlace.Status, noPlace.Body)
		}
	}
	// A place given by an id that is not one is not read.
	if res := sendJSON(t, b, http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/move", x.place, x.notes.ID), map[string]any{"to": nil, "place": "Team"}); res.Status != http.StatusBadRequest {
		t.Errorf("a place named, not given by id: %d %s", res.Status, res.Body)
	}
	// Into his own My files, by its id: a move inside it, as before.
	ok(t, "Bence moving Notes.txt into Private, naming his My files", moveInto(t, b, x.place, x.notes.ID, x.place, x.private.ID))

	// A name taken there is not replaced.
	uploadedInto(t, b, team.ID, 0, "Notes.txt", []byte("team's notes"))
	res := moveInto(t, b, x.place, x.notes.ID, team.ID, 0)
	var conflict conflictJSON
	res.JSON(t, &conflict)
	if res.Status != http.StatusConflict || conflict.Exists != "file" || conflict.Name != "Notes.txt" {
		t.Fatalf("moving Notes.txt where one is: %d %s", res.Status, res.Body)
	}
	// An upload in progress into a folder inside it: it waits.
	startedUpload(t, b, x.place, x.y2026.ID, "Big.bin", 10)
	if res := moveInto(t, b, x.place, x.clients.ID, team.ID, 0); res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "being uploaded") {
		t.Fatalf("moving Clients while an upload goes into it: %d %s", res.Status, res.Body)
	}
	// Nothing moved.
	if got := onStorage(t, d, "Shared folders/Team"); !slices.Equal(got, []string{"Notes.txt", "Plan.txt"}) {
		t.Errorf("Team on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, "Shared folders/Team/Notes.txt"); string(got) != "team's notes" {
		t.Errorf("Team's Notes.txt holds %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com"); !slices.Equal(got, []string{"Clients/", "Private/"}) {
		t.Errorf("Bence's My files on the storage: %q", got)
	}
	for _, folder := range []string{"Shared folders/Archive", "Shared folders/Dóra's", "Shared folders/Read only"} {
		if got := onStorage(t, d, folder); len(got) != 0 {
			t.Errorf("%s on the storage: %q", folder, got)
		}
	}
}

// Moving into a folder of a shared folder that one was given on its own (no role on the shared folder as a whole): the
// warning names who will see it, but not the folders above, nor the shared folder, which one cannot open.
func TestTheMoveWarningNamesNothingTheMoverCannotOpen(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	secret := madeSharedFolder(t, o, "Secret project")
	hidden := madeFolder(t, o, secret.ID, 0, "Hidden level")
	inbox := madeFolder(t, o, secret.ID, hidden.ID, "Inbox")
	ok(t, "Dóra sharing Hidden level with everyone", shareWithEveryone(t, o, secret.ID, hidden.ID, "viewer"))
	ok(t, "Dóra giving Bence editor on Inbox", share(t, o, secret.ID, inbox.ID, bence, "editor"))
	ok(t, "Bence sharing Notes.txt with Réka", share(t, b, x.place, x.notes.ID, reka, "viewer"))

	p, body := preview(t, b, x.place, x.notes.ID, secret.ID, inbox.ID)
	if strings.Contains(body, "Secret project") {
		t.Fatalf("the warning names the shared folder: %s", body)
	}
	want := []string{reka.UserID + " manager (admin)", dora.UserID + " manager (from a folder above)", "everyone viewer (from Hidden level)",
		bence.UserID + " editor (from Inbox)", reka.UserID + " viewer (here)"}
	if got := accessLines(p.Access); !p.BecomesProjects || !slices.Equal(got, want) || !p.SeenBy.Everyone {
		t.Fatalf("who would see Notes.txt in Inbox: %q (everyone %v), want %q", got, p.SeenBy.Everyone, want)
	}
	ok(t, "Bence moving Notes.txt into Inbox", moveInto(t, b, x.place, x.notes.ID, secret.ID, inbox.ID))
	if got, _ := d.Storage.Get(t, "Shared folders/Secret project/Hidden level/Inbox/Notes.txt"); string(got) != "notes" {
		t.Fatalf("Notes.txt in Inbox holds %q", got)
	}
	// He works in Inbox, and the shared folder stays out of his places.
	if l := list(t, b, secret.ID, inbox.ID); !slices.Equal(names(l.Items), []string{"Notes.txt"}) || l.FromTop || !slices.Equal(pathNames(l.Path), []string{"Hidden level", "Inbox"}) {
		t.Fatalf("Bence's Inbox: %+v", l)
	}
	if _, sees := seesSharedFolder(t, d, bence, secret.ID); sees {
		t.Fatal("Bence sees Secret project")
	}
}

// Moves into a shared folder from several My files at once, while the shared folder is deleted: every request is
// answered (both places' locks are taken in one order, so none waits for another for ever), and each file is either
// still where it was or went into the trash with the shared folder.
func TestMovesIntoASharedFolderBeingDeletedAllEnd(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	team := madeSharedFolder(t, d.As(bence), "Team")
	ok(t, "Dóra an editor of Team", share(t, d.As(bence), team.ID, 0, dora, "editor"))
	type file struct {
		who   harness.Member
		place int64
		id    int64
		name  string
	}
	var files []file
	for _, m := range []harness.Member{bence, dora} {
		place, _ := myFiles(t, d, m)
		for i := range 6 {
			name := fmt.Sprintf("%s-%d.txt", m.Email, i)
			files = append(files, file{m, place, uploaded(t, d.As(m), place, name, []byte(name)).ID, name})
		}
	}
	var wg sync.WaitGroup
	statuses := make([]int, len(files))
	for i, f := range files {
		wg.Go(func() { statuses[i] = moveInto(t, d.As(f.who), f.place, f.id, team.ID, 0).Status })
	}
	var deleted harness.Response
	wg.Go(func() { deleted = deleteSharedFolder(t, d.As(bence), team.ID) })
	wg.Wait()
	if deleted.Status != http.StatusOK {
		t.Fatalf("deleting Team: %d %s", deleted.Status, deleted.Body)
	}
	entries := onStorage(t, d, ".superit-drive/trash/Shared folders/Team")
	if len(entries) != 1 {
		t.Fatalf("Team's trash: %q", entries)
	}
	inTrash := map[string]bool{}
	if got, ok := d.Storage.List(t, ".superit-drive/trash/Shared folders/Team/"+strings.TrimSuffix(entries[0], "/")+"/Team"); ok {
		for _, e := range got {
			inTrash[e.Name] = true
		}
	}
	for i, f := range files {
		_, stayed := d.Storage.Get(t, "My files/"+f.who.Email+"/"+f.name)
		switch {
		case statuses[i] == http.StatusOK && (stayed || !inTrash[f.name]):
			t.Errorf("%s: moved, but it stayed %v, in the trash with Team %v", f.name, stayed, inTrash[f.name])
		case statuses[i] == http.StatusNotFound && (!stayed || inTrash[f.name]):
			t.Errorf("%s: not moved, but it stayed %v, in the trash with Team %v", f.name, stayed, inTrash[f.name])
		case statuses[i] != http.StatusOK && statuses[i] != http.StatusNotFound:
			t.Errorf("%s: moving it answered %d", f.name, statuses[i])
		}
	}
}
