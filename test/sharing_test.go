package test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's calls for sharing, as the interface makes them. itemID 0 is a place as a whole (only a shared folder
// can be given as a whole); a nil member is everyone in the project.

func itemAccessPath(placeID, itemID int64) string {
	if itemID == 0 {
		return fmt.Sprintf("/api/places/%d/access", placeID)
	}
	return fmt.Sprintf("/api/places/%d/items/%d/access", placeID, itemID)
}

func sharePath(placeID, itemID int64, m *harness.Member) string {
	if m == nil {
		return itemAccessPath(placeID, itemID) + "/everyone"
	}
	return itemAccessPath(placeID, itemID) + "/members/" + url.PathEscape(m.UserID)
}

func share(t *testing.T, c *harness.Client, placeID, itemID int64, m harness.Member, role string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, sharePath(placeID, itemID, &m), map[string]string{"role": role})
}

func shareWithEveryone(t *testing.T, c *harness.Client, placeID, itemID int64, role string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, sharePath(placeID, itemID, nil), map[string]string{"role": role})
}

func unshare(t *testing.T, c *harness.Client, placeID, itemID int64, m harness.Member) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, sharePath(placeID, itemID, &m), nil)
}

func unshareEveryone(t *testing.T, c *harness.Client, placeID, itemID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, sharePath(placeID, itemID, nil), nil)
}

// accessLines is who can see something, one line each, as the details panel shows it: who, the role, and why.
func accessLines(list []accessJSON) []string {
	var out []string
	for _, a := range list {
		who := a.UserID
		if a.Everyone {
			who = "everyone"
		}
		if a.Link {
			who = "link"
			if a.LinkKind == "upload-only" {
				who = "upload-only link" // no role: its visitors send files in, and see nothing
			}
		}
		line := who
		if a.Role != "" {
			line += " " + a.Role
		}
		switch {
		case a.Owner:
			line += " (owner)"
		case a.Admin:
			line += " (admin)"
		case a.Given:
			line += " (here)"
		case a.Inherited && a.From != nil:
			line += fmt.Sprintf(" (from %s)", a.From.Name)
		case a.Inherited:
			line += " (from a folder above)"
		}
		if a.Former {
			line += " (former)"
		}
		out = append(out, line)
	}
	return out
}

// sameAs fails the test unless res answers exactly as want, a request about something that does not exist, does.
func sameAs(t *testing.T, what string, res, want harness.Response) {
	t.Helper()
	if res.Status != http.StatusNotFound || res.Status != want.Status || !bytes.Equal(res.Body, want.Body) {
		t.Errorf("%s: got %d %s; something that does not exist answers %d %s", what, res.Status, res.Body, want.Status, want.Body)
	}
}

// bences is what Bence keeps in his My files in these tests. Offer.pdf is three levels below Clients.
//
//	Clients/Bakery/2026/Offer.pdf
//	Clients/Bakery/2026/Ledger.xlsx
//	Clients/Price list.pdf
//	Private/Secret.txt
//	Notes.txt
type bences struct {
	place                                                                 int64
	clients, bakery, y2026, offer, ledger, prices, private, secret, notes itemJSON
}

func bencesFiles(t *testing.T, d *harness.Drive) bences {
	t.Helper()
	b := d.As(bence)
	var x bences
	x.place, _ = myFiles(t, d, bence)
	x.clients = madeFolder(t, b, x.place, 0, "Clients")
	x.bakery = madeFolder(t, b, x.place, x.clients.ID, "Bakery")
	x.y2026 = madeFolder(t, b, x.place, x.bakery.ID, "2026")
	x.offer = uploadedInto(t, b, x.place, x.y2026.ID, "Offer.pdf", []byte("offer"))
	x.ledger = uploadedInto(t, b, x.place, x.y2026.ID, "Ledger.xlsx", []byte("ledger"))
	x.prices = uploadedInto(t, b, x.place, x.clients.ID, "Price list.pdf", []byte("prices"))
	x.private = madeFolder(t, b, x.place, 0, "Private")
	x.secret = uploadedInto(t, b, x.place, x.private.ID, "Secret.txt", []byte("secret"))
	x.notes = uploadedInto(t, b, x.place, 0, "Notes.txt", []byte("notes"))
	return x
}

func pathNames(path []folderJSON) []string {
	out := []string{}
	for _, f := range path {
		out = append(out, f.Name)
	}
	return out
}

// Access given on a folder applies to everything in it, at any depth, and to nothing above it or beside it. A viewer
// opens and downloads, sees who has access, and nothing more. The names of the folders above what they were given are
// never sent to them.
func TestAViewerOfAFolderOpensEverythingInsideItAndNothingElse(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))

	// Clients, and three levels down: the folder's address and the file's.
	l := list(t, o, x.place, x.clients.ID)
	if !slices.Equal(names(l.Items), []string{"Bakery", "Price list.pdf"}) || l.FromTop || l.Role != "viewer" ||
		!slices.Equal(l.Can, []string{"list", "download", "see-access"}) || !slices.Equal(pathNames(l.Path), []string{"Clients"}) {
		t.Fatalf("Dóra's Clients: %+v", l)
	}
	l = list(t, o, x.place, x.y2026.ID)
	if !slices.Equal(names(l.Items), []string{"Ledger.xlsx", "Offer.pdf"}) || !slices.Equal(pathNames(l.Path), []string{"Clients", "Bakery", "2026"}) || l.FromTop {
		t.Fatalf("Dóra's 2026: %+v", l)
	}
	if got := named(t, l.Items, "Offer.pdf").Can; !slices.Equal(got, []string{"list", "download", "see-access"}) {
		t.Fatalf("what Dóra may do with Offer.pdf: %q", got)
	}
	if res := o.Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusOK || string(res.Body) != "offer" {
		t.Fatalf("Dóra downloading Offer.pdf, three levels below Clients: %d %s", res.Status, res.Body)
	}
	// Bence sees the whole path, from his top folder.
	if l := list(t, b, x.place, x.y2026.ID); !l.FromTop || !slices.Equal(pathNames(l.Path), []string{"Clients", "Bakery", "2026"}) || l.Role != "owner" {
		t.Fatalf("Bence's 2026: %+v", l)
	}

	// Who can see Offer.pdf: Bence owns it; Dóra has it from Clients, which she can open.
	if got, want := accessLines(accessOf(t, o, itemAccessPath(x.place, x.offer.ID))), []string{bence.UserID + " owner (owner)", dora.UserID + " viewer (from Clients)"}; !slices.Equal(got, want) {
		t.Fatalf("who can see Offer.pdf, for Dóra: %q, want %q", got, want)
	}

	// Nothing else of Bence's: not his top folder, not the folder beside Clients, not what is in it, not his trash.
	missingPlace := o.Get(t, listPath(x.place+100, 0))
	missingItem := o.Get(t, downloadPath(x.place, x.notes.ID+100))
	sameAs(t, "Dóra listing Bence's top folder", o.Get(t, listPath(x.place, 0)), missingPlace)
	sameAs(t, "Dóra seeing Bence's trash", o.Get(t, fmt.Sprintf("/api/places/%d/trash", x.place)), missingPlace)
	sameAs(t, "Dóra downloading Notes.txt", o.Get(t, downloadPath(x.place, x.notes.ID)), missingItem)
	sameAs(t, "Dóra listing Private", o.Get(t, listPath(x.place, x.private.ID)), missingItem)
	sameAs(t, "Dóra downloading Secret.txt", o.Get(t, downloadPath(x.place, x.secret.ID)), missingItem)
	if got := placesOf(t, d, dora); len(got) != 1 || got[0].Kind != "my-files" || got[0].ID == x.place {
		t.Fatalf("Dóra's places: %+v; want only her own My files", got)
	}

	// A viewer changes nothing and shares nothing: every change answers as for something that does not exist.
	for what, res := range map[string]harness.Response{
		"upload into Clients":        uploadInto(t, o, x.place, x.clients.ID, "Planted.txt", []byte("x"), ""),
		"replace Offer.pdf":          uploadInto(t, o, x.place, x.y2026.ID, "Offer.pdf", []byte("x"), "replace"),
		"make a folder in Clients":   newFolder(t, o, x.place, x.clients.ID, "Planted"),
		"rename Offer.pdf":           rename(t, o, x.place, x.offer.ID, "Renamed.pdf"),
		"move Offer.pdf":             move(t, o, x.place, x.offer.ID, x.bakery.ID),
		"delete Offer.pdf":           deleteItem(t, o, x.place, x.offer.ID),
		"delete Clients":             deleteItem(t, o, x.place, x.clients.ID),
		"share Offer.pdf further":    share(t, o, x.place, x.offer.ID, reka, "viewer"),
		"share Clients further":      shareWithEveryone(t, o, x.place, x.clients.ID, "viewer"),
		"take her own viewer away":   unshare(t, o, x.place, x.clients.ID, dora),
		"give herself editor":        share(t, o, x.place, x.clients.ID, dora, "editor"),
		"see who can see My files":   o.Get(t, itemAccessPath(x.place, 0)),
		"share Bence's My files too": share(t, o, x.place, 0, dora, "editor"),
	} {
		if res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missingItem.Body) {
			t.Errorf("Dóra, a viewer, trying to %s: got %d %s, want 404 as for something that does not exist", what, res.Status, res.Body)
		}
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients"); !slices.Equal(got, []string{"Bakery/", "Price list.pdf"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery/2026"); !slices.Equal(got, []string{"Ledger.xlsx", "Offer.pdf"}) {
		t.Fatalf("2026 on the storage: %q", got)
	}

	// My files stays closed to the project admin: nothing was shared with her.
	sameAs(t, "Réka listing Clients", d.As(reka).Get(t, listPath(x.place, x.clients.ID)), d.As(reka).Get(t, listPath(x.place, x.notes.ID+100)))
}

// An editor can upload, make folders, rename, move and delete inside what they were given, restore what they deleted,
// and share it further. They cannot take anything out of it into a folder they were not given, nor rename, move or
// delete the folder they were given itself: that changes the folder it is in, which they cannot open.
func TestAnEditorWorksInsideWhatTheyWereGivenAndSharesItFurther(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	top := "My files/bence@example.com"

	l := list(t, o, x.place, x.clients.ID)
	if l.Role != "editor" || !slices.Contains(l.Can, "upload") || !slices.Contains(l.Can, "make-folder") || slices.Contains(l.Can, "see-trash") {
		t.Fatalf("Dóra's Clients: role %q, can %q", l.Role, l.Can)
	}
	if got := named(t, l.Items, "Price list.pdf").Can; !slices.Equal(got, []string{"list", "download", "upload", "make-folder", "rename", "move", "delete", "see-access", "share", "make-link", "manage-link", "invite-guest"}) {
		t.Fatalf("what Dóra may do with Price list.pdf: %q", got)
	}
	uploadedInto(t, o, x.place, x.bakery.ID, "Invoice.pdf", []byte("from Dóra"))
	drafts := madeFolder(t, o, x.place, x.clients.ID, "Drafts")
	ok(t, "Dóra renaming Price list.pdf", rename(t, o, x.place, x.prices.ID, "Prices 2026.pdf"))
	ok(t, "Dóra moving Offer.pdf into Drafts", move(t, o, x.place, x.offer.ID, drafts.ID))
	if _, ok := d.Storage.Get(t, top+"/Clients/Drafts/Offer.pdf"); !ok {
		t.Fatal("Offer.pdf did not move into Drafts on the storage")
	}
	if got, _ := d.Storage.Get(t, top+"/Clients/Bakery/Invoice.pdf"); string(got) != "from Dóra" {
		t.Fatalf("Dóra's upload on the storage: %q", got)
	}

	// Deleted into Bence's trash, and back with "Undo": she may restore what she could have deleted. The answer names
	// only the folders she can open.
	e := deleted(t, o, x.place, x.ledger.ID)
	if !slices.Equal(pathNames(e.From), []string{"Clients", "Bakery", "2026"}) {
		t.Fatalf("Ledger.xlsx goes back to %q", pathNames(e.From))
	}
	if trash := trashOf(t, b, x.place); len(trash) != 1 || trash[0].ID != x.ledger.ID {
		t.Fatalf("Bence's trash: %+v", trash)
	}
	if r := restored(t, o, x.place, x.ledger.ID); r.Item.ID != x.ledger.ID || !slices.Equal(pathNames(r.Path), []string{"Clients", "Bakery", "2026"}) {
		t.Fatalf("Dóra restoring Ledger.xlsx: %+v", r)
	}
	// Bence's trash stays his: she sees only what she may restore, by its address.
	sameAs(t, "Dóra seeing Bence's trash", o.Get(t, fmt.Sprintf("/api/places/%d/trash", x.place)), o.Get(t, fmt.Sprintf("/api/places/%d/trash", x.place+100)))

	// Out of Clients, into a folder she was not given or the top folder: the same answer as a folder that is not there.
	noFolder := move(t, o, x.place, x.prices.ID, x.notes.ID+100)
	if noFolder.Status != http.StatusNotFound {
		t.Fatalf("moving into a folder that does not exist: %d %s", noFolder.Status, noFolder.Body)
	}
	sameAs(t, "Dóra moving Price list.pdf into Private", move(t, o, x.place, x.prices.ID, x.private.ID), noFolder)
	sameAs(t, "Dóra moving Price list.pdf to Bence's top folder", move(t, o, x.place, x.prices.ID, 0), noFolder)
	missingItem := o.Get(t, downloadPath(x.place, x.notes.ID+100))
	sameAs(t, "Dóra moving Clients itself into Private", move(t, o, x.place, x.clients.ID, x.private.ID), missingItem)
	if got := onStorage(t, d, top); !slices.Equal(got, []string{"Clients/", "Notes.txt", "Private/"}) {
		t.Fatalf("Bence's top folder on the storage: %q", got)
	}

	// She shares Bakery further, with Réka as viewer: Réka sees Bakery and what is in it, and nothing above.
	ok(t, "Dóra sharing Bakery with Réka", share(t, o, x.place, x.bakery.ID, reka, "viewer"))
	r := d.As(reka)
	if l := list(t, r, x.place, x.bakery.ID); !slices.Equal(names(l.Items), []string{"2026", "Invoice.pdf"}) || !slices.Equal(pathNames(l.Path), []string{"Bakery"}) {
		t.Fatalf("Réka's Bakery: %+v", l)
	}
	sameAs(t, "Réka listing Clients", r.Get(t, listPath(x.place, x.clients.ID)), r.Get(t, listPath(x.place, x.notes.ID+100)))
	// For Réka, Dóra's editor role comes from a folder she cannot open: she sees who, not where.
	want := []string{bence.UserID + " owner (owner)", dora.UserID + " editor (from a folder above)", reka.UserID + " viewer (here)"}
	if got := accessLines(accessOf(t, r, itemAccessPath(x.place, x.bakery.ID))); !slices.Equal(got, want) {
		t.Fatalf("who can see Bakery, for Réka: %q, want %q", got, want)
	}
	for _, a := range accessOf(t, r, itemAccessPath(x.place, x.bakery.ID)) {
		if a.From != nil && a.From.Name == "Clients" {
			t.Fatal("Réka was told the name of a folder she cannot open")
		}
	}

	// The very folder her role is on is in Bence's top folder, which she cannot open: renaming it there could tell her
	// what else is there, and deleting it she could not undo. Both answer as for something that does not exist.
	sameAs(t, "Dóra renaming Clients to a name Bence has there", rename(t, o, x.place, x.clients.ID, "Notes.txt"), missingItem)
	sameAs(t, "Dóra renaming Clients to a free name", rename(t, o, x.place, x.clients.ID, "Clients 2026"), missingItem)
	sameAs(t, "Dóra deleting Clients", deleteItem(t, o, x.place, x.clients.ID), missingItem)
	if got := onStorage(t, d, top); !slices.Equal(got, []string{"Clients/", "Notes.txt", "Private/"}) {
		t.Fatalf("Bence's top folder on the storage: %q", got)
	}
	if got := onStorage(t, d, top+"/Clients"); !slices.Equal(got, []string{"Bakery/", "Drafts/", "Prices 2026.pdf"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	l = list(t, o, x.place, x.clients.ID)
	if l.Role != "editor" || !slices.Contains(l.Can, "share") || slices.Contains(l.Can, "rename") || slices.Contains(l.Can, "delete") {
		t.Fatalf("what Dóra may do in Clients itself: role %q, can %q", l.Role, l.Can)
	}
}

// Deeper levels can only add access: a higher role lower down adds to a lower one above, and a lower one lower down
// takes nothing away. A role on one item reaches neither its sibling nor its parent.
func TestDeeperLevelsOnlyAddAccess(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	missingItem := o.Get(t, downloadPath(x.place, x.notes.ID+100))

	// Editor on Offer.pdf alone: she opens and shares that file; renaming it would change 2026, which she cannot open.
	// Not its sibling, not its folder.
	ok(t, "sharing Offer.pdf with Dóra as editor", share(t, b, x.place, x.offer.ID, dora, "editor"))
	if res := o.Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusOK {
		t.Fatalf("Dóra downloading Offer.pdf: %d %s", res.Status, res.Body)
	}
	ok(t, "Dóra sharing Offer.pdf further with Réka", share(t, o, x.place, x.offer.ID, reka, "viewer"))
	sameAs(t, "Dóra renaming Offer.pdf", rename(t, o, x.place, x.offer.ID, "Offer v2.pdf"), missingItem)
	sameAs(t, "Dóra downloading Ledger.xlsx, its sibling", o.Get(t, downloadPath(x.place, x.ledger.ID)), missingItem)
	sameAs(t, "Dóra listing 2026, its folder", o.Get(t, listPath(x.place, x.y2026.ID)), missingItem)
	sameAs(t, "Dóra renaming Ledger.xlsx", rename(t, o, x.place, x.ledger.ID, "x"), missingItem)

	// Viewer on Clients, editor on Bakery: she opens everything in Clients, and works in Bakery only.
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Bakery with Dóra as editor", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	if l := list(t, o, x.place, x.clients.ID); l.Role != "viewer" || slices.Contains(l.Can, "upload") ||
		!slices.Equal(named(t, l.Items, "Price list.pdf").Can, []string{"list", "download", "see-access"}) {
		t.Fatalf("Dóra's Clients: %+v", l)
	}
	l := list(t, o, x.place, x.y2026.ID)
	if l.Role != "editor" || !slices.Contains(named(t, l.Items, "Offer.pdf").Can, "rename") || !slices.Contains(named(t, l.Items, "Ledger.xlsx").Can, "delete") {
		t.Fatalf("Dóra's 2026, in Bakery: %+v", l)
	}
	ok(t, "Dóra renaming Offer.pdf in Bakery", rename(t, o, x.place, x.offer.ID, "Offer v2.pdf"))
	uploadedInto(t, o, x.place, x.y2026.ID, "From Dóra.txt", []byte("dóra"))
	sameAs(t, "Dóra uploading into Clients", uploadInto(t, o, x.place, x.clients.ID, "x.txt", []byte("x"), ""), missingItem)
	sameAs(t, "Dóra renaming Price list.pdf", rename(t, o, x.place, x.prices.ID, "x"), missingItem)
	want := []string{bence.UserID + " owner (owner)", dora.UserID + " viewer (from Clients)", dora.UserID + " editor (from Bakery)",
		dora.UserID + " editor (here)", reka.UserID + " viewer (here)"}
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.offer.ID))); !slices.Equal(got, want) {
		t.Fatalf("who can see Offer.pdf: %q, want %q", got, want)
	}

	// Editor on Clients and viewer on Bakery: still an editor in Bakery.
	ok(t, "making Dóra an editor of Clients", share(t, b, x.place, x.clients.ID, dora, "editor"))
	ok(t, "making Dóra a viewer of Bakery", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	if l := list(t, o, x.place, x.bakery.ID); l.Role != "editor" {
		t.Fatalf("Dóra's role in Bakery: %q", l.Role)
	}
	uploadedInto(t, o, x.place, x.bakery.ID, "Still editing.txt", []byte("yes"))
	ok(t, "Dóra renaming Ledger.xlsx now", rename(t, o, x.place, x.ledger.ID, "Ledger 2026.xlsx"))
}

// Everyone in the project is every member, as long as they are one: new members are included, and leaving ends it.
func TestSharingWithEveryoneInTheProject(t *testing.T) {
	t.Parallel()
	mate := harness.Member{UserID: "e7aa01b2-mate", Email: "mate@example.com", DisplayName: "Máté Szabó", Role: "member"}
	d := harness.Start(t, reka, bence, dora)
	b := d.As(bence)
	x := bencesFiles(t, d)
	ok(t, "sharing Bakery with everyone as viewer", shareWithEveryone(t, b, x.place, x.bakery.ID, "viewer"))

	for _, m := range []harness.Member{reka, dora} {
		if res := d.As(m).Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusOK {
			t.Fatalf("%s downloading Offer.pdf: %d %s", m.Email, res.Status, res.Body)
		}
	}
	// Someone who joins later is included without anyone sharing again.
	d.SetMembers(t, reka, bence, dora, mate)
	harness.Eventually(t, "Máté is a member", func() bool { return d.As(mate).Get(t, "/api/me").Status == http.StatusOK })
	if l := list(t, d.As(mate), x.place, x.bakery.ID); l.Role != "viewer" || !slices.Equal(names(l.Items), []string{"2026"}) {
		t.Fatalf("Máté's Bakery: %+v", l)
	}
	want := []string{bence.UserID + " owner (owner)", "everyone viewer (from Bakery)"}
	if got := accessLines(accessOf(t, d.As(mate), itemAccessPath(x.place, x.offer.ID))); !slices.Equal(got, want) {
		t.Fatalf("who can see Offer.pdf: %q, want %q", got, want)
	}
	// Someone who leaves is refused before anything is decided.
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Máté is no longer a member", func() bool { return d.As(mate).Get(t, "/api/me").Status == http.StatusForbidden })
	if res := d.As(mate).Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusForbidden {
		t.Fatalf("Máté after leaving: %d %s", res.Status, res.Body)
	}

	// Everyone as editor on 2026, then taken away: the rest of Bakery stays as it was.
	ok(t, "everyone editor on 2026", shareWithEveryone(t, b, x.place, x.y2026.ID, "editor"))
	ok(t, "Dóra renaming Offer.pdf", rename(t, d.As(dora), x.place, x.offer.ID, "Offer (everyone).pdf"))
	ok(t, "taking everyone's editor away", unshareEveryone(t, b, x.place, x.y2026.ID))
	missing := d.As(dora).Get(t, downloadPath(x.place, x.notes.ID+100))
	sameAs(t, "Dóra renaming it again", rename(t, d.As(dora), x.place, x.offer.ID, "x"), missing)
	ok(t, "taking everyone's viewer away", unshareEveryone(t, b, x.place, x.bakery.ID))
	sameAs(t, "Dóra downloading it after", d.As(dora).Get(t, downloadPath(x.place, x.offer.ID)), missing)
}

// Taking access away, or giving less, takes effect on the very next request, for the item and for everything in it.
func TestTakingAccessAwayTakesEffectOnTheNextRequest(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	missing := o.Get(t, downloadPath(x.place, x.notes.ID+100))
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	uploadedInto(t, o, x.place, x.y2026.ID, "One.txt", []byte("1"))

	ok(t, "making Dóra a viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	sameAs(t, "Dóra uploading as a viewer, three levels down", uploadInto(t, o, x.place, x.y2026.ID, "Two.txt", []byte("2"), ""), missing)
	if res := o.Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusOK {
		t.Fatalf("Dóra downloading as a viewer: %d %s", res.Status, res.Body)
	}

	ok(t, "taking Dóra's access away", unshare(t, b, x.place, x.clients.ID, dora))
	for what, res := range map[string]harness.Response{
		"list Clients":       o.Get(t, listPath(x.place, x.clients.ID)),
		"list 2026":          o.Get(t, listPath(x.place, x.y2026.ID)),
		"download Offer.pdf": o.Get(t, downloadPath(x.place, x.offer.ID)),
		"see who can see it": o.Get(t, itemAccessPath(x.place, x.offer.ID)),
	} {
		sameAs(t, "Dóra after her access was taken away: "+what, res, missing)
	}
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.clients.ID))); !slices.Equal(got, []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("who can see Clients: %q", got)
	}
	// Taking away what nobody has is no error.
	ok(t, "taking it away again", unshare(t, b, x.place, x.clients.ID, dora))
}

// Every item in a listing says who can see it: its owner alone, the people it was shared with on it or above it, or
// everyone in the project. In a shared folder, its managers and the project admins can see everything.
func TestEveryItemSaysWhoCanSeeIt(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b := d.As(bence)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	ok(t, "sharing Offer.pdf with Réka", share(t, b, x.place, x.offer.ID, reka, "viewer"))
	ok(t, "sharing Private with everyone", shareWithEveryone(t, b, x.place, x.private.ID, "viewer"))

	seen := func(items []itemJSON, name string) string {
		t.Helper()
		s := named(t, items, name).SeenBy
		if s == nil {
			t.Fatalf("%s says nothing about who can see it", name)
		}
		if s.Everyone {
			return "everyone"
		}
		return strings.Join(s.Members, " ")
	}
	top := list(t, b, x.place, 0).Items
	for name, want := range map[string]string{
		"Notes.txt": bence.UserID,                     // own
		"Clients":   bence.UserID + " " + dora.UserID, // given on it
		"Private":   "everyone",                       // everyone
	} {
		if got := seen(top, name); got != want {
			t.Errorf("who can see %s: %q, want %q", name, got, want)
		}
	}
	in2026 := list(t, b, x.place, x.y2026.ID).Items
	for name, want := range map[string]string{
		"Ledger.xlsx": bence.UserID + " " + dora.UserID,                     // from Clients, above
		"Offer.pdf":   bence.UserID + " " + dora.UserID + " " + reka.UserID, // from above, and given on it
	} {
		if got := seen(in2026, name); got != want {
			t.Errorf("who can see %s: %q, want %q", name, got, want)
		}
	}
	if got := seen(list(t, b, x.place, x.private.ID).Items, "Secret.txt"); got != "everyone" {
		t.Errorf("who can see Secret.txt, in Private: %q", got)
	}
	// Dóra sees the same as Bence.
	if got := seen(list(t, d.As(dora), x.place, x.y2026.ID).Items, "Offer.pdf"); got != bence.UserID+" "+dora.UserID+" "+reka.UserID {
		t.Errorf("who can see Offer.pdf, for Dóra: %q", got)
	}

	// In a shared folder: its manager and the project admin, then whoever it is shared with.
	team := madeSharedFolder(t, b, "Team")
	plan := uploadedInto(t, b, team.ID, 0, "Plan.txt", []byte("plan"))
	if got := seen(list(t, b, team.ID, 0).Items, "Plan.txt"); got != bence.UserID+" "+reka.UserID {
		t.Errorf("who can see Plan.txt: %q", got)
	}
	ok(t, "sharing Plan.txt with Dóra", share(t, b, team.ID, plan.ID, dora, "viewer"))
	if got := seen(list(t, b, team.ID, 0).Items, "Plan.txt"); got != bence.UserID+" "+dora.UserID+" "+reka.UserID {
		t.Errorf("who can see Plan.txt, shared with Dóra: %q", got)
	}
	ok(t, "sharing Team with everyone", shareWithEveryone(t, b, team.ID, 0, "viewer"))
	if got := seen(list(t, b, team.ID, 0).Items, "Plan.txt"); got != "everyone" {
		t.Errorf("who can see Plan.txt, Team shared with everyone: %q", got)
	}
}

// In a shared folder, its managers decide who has access to it as a whole: viewer, editor or manager, to a member or
// (as viewer or editor) to everyone. Anyone who may share an item inside it can share that item further; only managers
// change who has access to the whole.
func TestSharingInASharedFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	clients := madeSharedFolder(t, b, "Clients")
	bakery := madeFolder(t, b, clients.ID, 0, "Bakery")
	offer := uploadedInto(t, b, clients.ID, bakery.ID, "Offer.pdf", []byte("offer"))
	missing := o.Get(t, downloadPath(clients.ID, offer.ID+100))

	// Viewer on it as a whole: it is in her places, she opens everything, and changes nothing.
	ok(t, "Dóra a viewer of Clients", share(t, b, clients.ID, 0, dora, "viewer"))
	p, sees := seesSharedFolder(t, d, dora, clients.ID)
	if !sees || p.Role != "viewer" || !slices.Equal(p.Can, []string{"list", "download", "see-access"}) {
		t.Fatalf("Dóra's view of Clients: %+v, %v", p, sees)
	}
	if res := o.Get(t, downloadPath(clients.ID, offer.ID)); res.Status != http.StatusOK {
		t.Fatalf("Dóra downloading: %d %s", res.Status, res.Body)
	}
	sameAs(t, "Dóra uploading as a viewer", upload(t, o, clients.ID, "x.txt", []byte("x")), missing)
	want := []string{reka.UserID + " manager (admin)", bence.UserID + " manager (here)", dora.UserID + " viewer (here)"}
	if got := accessLines(whoHasAccess(t, o, clients.ID)); !slices.Equal(got, want) {
		t.Fatalf("who has access to Clients: %q, want %q", got, want)
	}
	want = []string{reka.UserID + " manager (admin)", bence.UserID + " manager (from Clients)", dora.UserID + " viewer (from Clients)"}
	if got := accessLines(accessOf(t, o, itemAccessPath(clients.ID, offer.ID))); !slices.Equal(got, want) {
		t.Fatalf("who can see Offer.pdf: %q, want %q", got, want)
	}

	// Editor on Bakery: she works in it and shares it further, but cannot change who has Clients as a whole.
	ok(t, "Dóra an editor of Bakery", share(t, b, clients.ID, bakery.ID, dora, "editor"))
	uploadedInto(t, o, clients.ID, bakery.ID, "Invoice.pdf", []byte("dóra"))
	ok(t, "Dóra sharing Bakery with everyone", shareWithEveryone(t, o, clients.ID, bakery.ID, "viewer"))
	sameAs(t, "Dóra giving herself editor on Clients", share(t, o, clients.ID, 0, dora, "editor"), missing)
	sameAs(t, "Dóra giving everyone Clients", shareWithEveryone(t, o, clients.ID, 0, "viewer"), missing)

	// Editor on it as a whole: still not a manager. A manager is one.
	ok(t, "Dóra an editor of Clients", share(t, b, clients.ID, 0, dora, "editor"))
	if p, _ := seesSharedFolder(t, d, dora, clients.ID); p.Role != "editor" || slices.Contains(p.Can, "manage-access") || !slices.Contains(p.Can, "see-trash") {
		t.Fatalf("Dóra, an editor of Clients: %+v", p)
	}
	sameAs(t, "Dóra, an editor, making herself a manager", share(t, o, clients.ID, 0, dora, "manager"), missing)
	sameAs(t, "Dóra, an editor, deleting Clients", deleteSharedFolder(t, o, clients.ID), missing)
	ok(t, "Dóra a manager of Clients", share(t, b, clients.ID, 0, dora, "manager"))
	ok(t, "Dóra, a manager, giving everyone viewer", shareWithEveryone(t, o, clients.ID, 0, "viewer"))
	if _, sees := seesSharedFolder(t, d, reka, clients.ID); !sees {
		t.Fatal("Réka does not see Clients")
	}

	// A manager given less is no longer one; the last manager is kept when there is no project admin.
	ok(t, "Bence leaving Dóra to manage it", share(t, o, clients.ID, 0, bence, "viewer"))
	if p, _ := seesSharedFolder(t, d, bence, clients.ID); p.Role != "viewer" {
		t.Fatalf("Bence's role after being given viewer: %+v", p)
	}

	solo := harness.Start(t, bence, dora)
	team := madeSharedFolder(t, solo.As(bence), "Team")
	res := share(t, solo.As(bence), team.ID, 0, bence, "editor")
	if res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "manager") {
		t.Fatalf("the last manager making himself an editor: got %d %s, want 409", res.Status, res.Body)
	}
}

// Only members can be given access, only the roles there are, and only on an item or a shared folder as a whole: never
// on someone's My files as a whole, and never to its own owner.
func TestOnlyMembersAndTheRolesThereAreCanBeGiven(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b := d.As(bence)
	x := bencesFiles(t, d)
	stranger := harness.Member{UserID: "u-stranger", Email: "stranger@example.com"}
	for what, res := range map[string]harness.Response{
		"someone outside the member list": share(t, b, x.place, x.clients.ID, stranger, "viewer"),
		"Bence, its owner":                share(t, b, x.place, x.clients.ID, bence, "viewer"),
		"manager on a folder":             share(t, b, x.place, x.clients.ID, dora, "manager"),
		"manager to everyone":             shareWithEveryone(t, b, x.place, x.clients.ID, "manager"),
		"a role that does not exist":      share(t, b, x.place, x.clients.ID, dora, "owner"),
		"no role":                         share(t, b, x.place, x.clients.ID, dora, ""),
		"a request that is not JSON":      b.Do(t, http.MethodPut, sharePath(x.place, x.clients.ID, &dora), strings.NewReader("viewer")),
	} {
		if res.Status != http.StatusBadRequest || errorOf(t, res) == "" {
			t.Errorf("giving %s: got %d %s, want 400 with a reason", what, res.Status, res.Body)
		}
	}
	// My files as a whole is never shared, and has no list of who can see it: it is its owner's alone.
	missing := b.Get(t, listPath(x.place+100, 0))
	sameAs(t, "Bence sharing his My files as a whole", share(t, b, x.place, 0, dora, "viewer"), missing)
	sameAs(t, "Bence sharing his My files with everyone", shareWithEveryone(t, b, x.place, 0, "viewer"), missing)
	sameAs(t, "Bence asking who can see his My files", b.Get(t, itemAccessPath(x.place, 0)), missing)
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.clients.ID))); !slices.Equal(got, []string{bence.UserID + " owner (owner)"}) {
		t.Fatalf("who can see Clients: %q", got)
	}
	// A member who has never opened the Drive can be given access.
	ok(t, "sharing with Réka, who has never opened the Drive", share(t, b, x.place, x.clients.ID, reka, "viewer"))
	list(t, d.As(reka), x.place, x.clients.ID)

	// Nobody gives access on an item to themselves: it would outlast what they were given above, once that is taken
	// away. Taking away what was given to them is theirs to do.
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	o := d.As(dora)
	for what, res := range map[string]harness.Response{
		"Dóra giving herself editor on Bakery":  share(t, o, x.place, x.bakery.ID, dora, "editor"),
		"Dóra giving herself viewer on Clients": share(t, o, x.place, x.clients.ID, dora, "viewer"),
	} {
		if res.Status != http.StatusBadRequest || !strings.Contains(errorOf(t, res), "yourself") {
			t.Errorf("%s: got %d %s, want 400", what, res.Status, res.Body)
		}
	}
	ok(t, "Dóra sharing Bakery with everyone", shareWithEveryone(t, o, x.place, x.bakery.ID, "viewer"))
	ok(t, "Dóra stepping back from Clients", unshare(t, o, x.place, x.clients.ID, dora))
	if l := list(t, o, x.place, x.bakery.ID); l.Role != "viewer" {
		t.Fatalf("Dóra in Bakery after stepping back: %+v", l)
	}
}

// A share hangs on the item, not on its name or place in the folder tree: it stays through rename, move, replace and
// the trash, and goes when the item does.
func TestAShareStaysWithItsItem(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Offer.pdf with Dóra", share(t, b, x.place, x.offer.ID, dora, "viewer"))
	download := func() harness.Response { return o.Get(t, downloadPath(x.place, x.offer.ID)) }

	ok(t, "renaming it", rename(t, b, x.place, x.offer.ID, "Offer 2026.pdf"))
	ok(t, "moving it", move(t, b, x.place, x.offer.ID, x.private.ID))
	if res := uploadInto(t, b, x.place, x.private.ID, "Offer 2026.pdf", []byte("new offer"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing it: %d %s", res.Status, res.Body)
	}
	if res := download(); res.Status != http.StatusOK || string(res.Body) != "new offer" {
		t.Fatalf("Dóra downloading it after a rename, a move and a replace: %d %s", res.Status, res.Body)
	}
	missing := o.Get(t, downloadPath(x.place, x.notes.ID+100))
	deleted(t, b, x.place, x.offer.ID)
	sameAs(t, "Dóra downloading it in the trash", download(), missing)
	restored(t, b, x.place, x.offer.ID)
	if res := download(); res.Status != http.StatusOK {
		t.Fatalf("Dóra downloading it after the restore: %d %s", res.Status, res.Body)
	}
	// The replaced content went to the trash as a new item, which nobody shared.
	trash := trashOf(t, b, x.place)
	if len(trash) != 1 || !trash[0].Replaced {
		t.Fatalf("Bence's trash: %+v", trash)
	}
	sameAs(t, "Dóra restoring the replaced file", restore(t, o, x.place, trash[0].ID), o.Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/trash/%d/restore", x.place, x.notes.ID+100), nil))
	// Removed from the storage behind the Drive's back: once a listing sees it gone, so is the share.
	d.Storage.Delete(t, "My files/bence@example.com/Private/Offer 2026.pdf", false)
	list(t, b, x.place, x.private.ID)
	sameAs(t, "Dóra downloading it once it is gone", download(), missing)
}

// When Bence leaves the project, his My files is held: what he shared from it opens nothing, until he is back.
func TestWhatWasSharedFromTheMyFilesOfSomeoneWhoLeftOpensNothing(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	missing := o.Get(t, downloadPath(x.place, x.notes.ID+100))

	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Bence is refused", func() bool { return b.Get(t, "/api/me").Status == http.StatusForbidden })
	sameAs(t, "Dóra downloading from Bence's held My files", o.Get(t, downloadPath(x.place, x.offer.ID)), missing)
	sameAs(t, "Dóra uploading into it", uploadInto(t, o, x.place, x.clients.ID, "x.txt", []byte("x"), ""), missing)

	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Bence is back", func() bool { return b.Get(t, "/api/me").Status == http.StatusOK })
	if res := o.Get(t, downloadPath(x.place, x.offer.ID)); res.Status != http.StatusOK {
		t.Fatalf("Dóra downloading after Bence came back: %d %s", res.Status, res.Body)
	}
}

// Someone given one folder deep inside someone else's place gets, from every route that takes an item, the same answer
// for each item they were not given (the folder above, the file beside it, an item of another place, something in the
// trash they could not have deleted) as for an item that does not exist; and from every route about the place as a
// whole, the same answer as for a place that does not exist. The item routes are those of internal/server/routes.go;
// a test there keeps the two lists equal.
func TestSomeoneGivenPartOfAPlaceCannotTellWhatTheyWereNotGivenFromNothing(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Bakery with Dóra as editor", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	ok(t, "sharing Clients with Réka", share(t, b, x.place, x.clients.ID, reka, "viewer"))
	// In the trash: something deleted from outside Bakery, which she could not have deleted.
	gone := uploadedInto(t, b, x.place, x.clients.ID, "Old prices.pdf", []byte("old"))
	deleted(t, b, x.place, x.notes.ID)
	deleted(t, b, x.place, gone.ID)
	// Items of other places: Réka's My files, and a shared folder Dóra has no role on.
	rekas, _ := myFiles(t, d, reka)
	rekasFile := uploaded(t, d.As(reka), rekas, "Réka's.txt", []byte("hers"))
	payroll := madeSharedFolder(t, b, "Payroll")
	slip := uploadedInto(t, b, payroll.ID, 0, "Slip.pdf", []byte("slip"))
	nowhere := slip.ID + x.notes.ID + 1000
	// Uploads in progress that are not hers: Bence's into Clients and into Bakery (which she was given), and Réka's.
	notHers := map[string]string{
		"Bence's upload into Clients":  startedUpload(t, b, x.place, x.clients.ID, "Contract.pdf", 10).ID,
		"Bence's upload into Bakery":   startedUpload(t, b, x.place, x.bakery.ID, "Invoice.pdf", 10).ID,
		"Réka's upload into her files": startedUpload(t, d.As(reka), rekas, 0, "Mine.pdf", 10).ID,
	}

	type try struct{ method, path, body string }
	at := func(f string, a ...any) string { return fmt.Sprintf(f, a...) }
	tries := func(item int64) map[string]try {
		return map[string]try{
			"GET /api/places/{place}/items/{item}/access": {"GET", at("/api/places/%d/items/%d/access", x.place, item), ""},
			"PUT /api/places/{place}/items/{item}/access/members/{member}": {"PUT",
				at("/api/places/%d/items/%d/access/members/%s", x.place, item, url.PathEscape(dora.UserID)), `{"role":"editor"}`},
			"DELETE /api/places/{place}/items/{item}/access/members/{member}": {"DELETE",
				at("/api/places/%d/items/%d/access/members/%s", x.place, item, url.PathEscape(reka.UserID)), ""},
			"PUT /api/places/{place}/items/{item}/access/everyone":    {"PUT", at("/api/places/%d/items/%d/access/everyone", x.place, item), `{"role":"viewer"}`},
			"DELETE /api/places/{place}/items/{item}/access/everyone": {"DELETE", at("/api/places/%d/items/%d/access/everyone", x.place, item), ""},
			"POST /api/places/{place}/items/{item}/access/guests": {"POST", invitePath(x.place, item),
				`{"address":"anna@client.example","role":"viewer"}`},
			"PUT /api/places/{place}/items/{item}/access/guests/{guest}":    {"PUT", invitationPath(x.place, item, 1), `{"role":"editor"}`},
			"DELETE /api/places/{place}/items/{item}/access/guests/{guest}": {"DELETE", invitationPath(x.place, item, 1), ""},
			"GET /api/places/{place}/items/{item}/link":                     {"GET", linkPath(x.place, item), ""},
			"POST /api/places/{place}/items/{item}/link":                    {"POST", linkPath(x.place, item), `{"replace":true}`},
			"PUT /api/places/{place}/items/{item}/link":                     {"PUT", linkPath(x.place, item), `{"kind":"upload-only"}`},
			"POST /api/places/{place}/items/{item}/link/reset":              {"POST", linkPath(x.place, item) + "/reset", ""},
			"DELETE /api/places/{place}/items/{item}/link":                  {"DELETE", linkPath(x.place, item), ""},
			"GET /api/places/{place}/items/{item}/items":                    {"GET", listPath(x.place, item), ""},
			"POST /api/places/{place}/items/{item}/files":                   {"POST", at("/api/places/%d/items/%d/files?name=Planted.txt&existing=replace", x.place, item), "x"},
			"POST /api/places/{place}/items/{item}/folders":                 {"POST", at("/api/places/%d/items/%d/folders", x.place, item), `{"name":"Planted"}`},
			"GET /api/places/{place}/items/{item}/download":                 {"GET", downloadPath(x.place, item), ""},
			"GET /api/places/{place}/items/{item}/open":                     {"GET", openPath(x.place, item), ""},
			"POST /api/places/{place}/items/{item}/rename":                  {"POST", at("/api/places/%d/items/%d/rename", x.place, item), `{"name":"Renamed"}`},
			"POST /api/places/{place}/items/{item}/move":                    {"POST", at("/api/places/%d/items/%d/move", x.place, item), fmt.Sprintf(`{"to":%d}`, x.bakery.ID)},
			"GET /api/places/{place}/items/{item}/move":                     {"GET", at("/api/places/%d/items/%d/move?to=%d", x.place, item, x.bakery.ID), ""},
			"DELETE /api/places/{place}/items/{item}":                       {"DELETE", at("/api/places/%d/items/%d", x.place, item), ""},
			"POST /api/places/{place}/trash/{item}/restore":                 {"POST", at("/api/places/%d/trash/%d/restore", x.place, item), ""},
			"GET /api/places/{place}/items/{item}/zip":                      {"GET", zipPath(x.place, item), ""},
			"POST /api/places/{place}/items/{item}/uploads":                 {"POST", uploadsPath(x.place, item) + "?name=Planted.txt&size=1&existing=replace", ""},
		}
	}
	uploadTries := func(id string) map[string]try {
		return map[string]try{
			"GET /api/places/{place}/uploads/{upload}":         {"GET", sessionPath(x.place, id), ""},
			"PUT /api/places/{place}/uploads/{upload}":         {"PUT", sessionPath(x.place, id) + "?offset=0", "x"},
			"POST /api/places/{place}/uploads/{upload}/finish": {"POST", sessionPath(x.place, id) + "/finish?existing=replace", ""},
			"DELETE /api/places/{place}/uploads/{upload}":      {"DELETE", sessionPath(x.place, id), ""},
		}
	}
	send := func(c *harness.Client, t2 try) harness.Response {
		var body io.Reader
		if t2.body != "" {
			body = strings.NewReader(t2.body)
		}
		return c.Do(t, t2.method, t2.path, body)
	}
	if n := len(tries(0)) + len(uploadTries("")); n != 29 {
		t.Fatalf("the test tries %d routes that take an item or an upload; internal/server/server_test.go lists 29", n)
	}
	none := tries(nowhere)
	notGiven := map[string]int64{
		"Clients, the folder above Bakery":   x.clients.ID,
		"Price list.pdf, beside Bakery":      x.prices.ID,
		"Secret.txt, in another folder":      x.secret.ID,
		"Notes.txt, in the trash":            x.notes.ID,
		"Old prices.pdf, in the trash":       gone.ID,
		"a file of Réka's My files":          rekasFile.ID,
		"a file of a shared folder of Bence": slip.ID,
	}
	for route := range none {
		missing := send(o, none[route])
		for what, id := range notGiven {
			sameAs(t, fmt.Sprintf("Dóra: %s with %s", route, what), send(o, tries(id)[route]), missing)
		}
	}
	noUpload := uploadTries("0123456789abcdef0123456789abcdef")
	for route := range noUpload {
		missing := send(o, noUpload[route])
		for what, id := range notHers {
			sameAs(t, fmt.Sprintf("Dóra: %s with %s", route, what), send(o, uploadTries(id)[route]), missing)
		}
	}

	// The place as a whole answers as a place that does not exist.
	top := func(place int64) map[string]try {
		return map[string]try{
			"GET items":            {"GET", listPath(place, 0), ""},
			"POST files":           {"POST", at("/api/places/%d/files?name=Planted.txt", place), "x"},
			"POST folders":         {"POST", at("/api/places/%d/folders", place), `{"name":"Planted"}`},
			"GET trash":            {"GET", at("/api/places/%d/trash", place), ""},
			"GET access":           {"GET", at("/api/places/%d/access", place), ""},
			"PUT access member":    {"PUT", accessPath(place, dora), `{"role":"editor"}`},
			"DELETE access member": {"DELETE", accessPath(place, dora), ""},
			"PUT everyone":         {"PUT", at("/api/places/%d/access/everyone", place), `{"role":"viewer"}`},
			"DELETE everyone":      {"DELETE", at("/api/places/%d/access/everyone", place), ""},
			"DELETE place":         {"DELETE", at("/api/places/%d", place), ""},
			"POST restore":         {"POST", at("/api/places/%d/restore", place), ""},
			"PUT sharing outside":  {"PUT", folderSwitchPath(place), `{"on":false}`},
			"GET zip":              {"GET", zipPath(place, 0), ""},
			"POST uploads":         {"POST", uploadsPath(place, 0) + "?name=Planted.txt&size=1", ""},
		}
	}
	for route, x2 := range top(x.place) {
		sameAs(t, "Dóra: "+route+" of Bence's My files", send(o, x2), send(o, top(nowhere)[route]))
	}

	// Renaming Bakery, the folder she was given, would put a name into Clients, which she cannot open: whether a name
	// is taken there or not, the answer is the same, and nothing changes.
	for _, name := range []string{"Price list.pdf", "Bakery 2026", "Bakery"} {
		sameAs(t, "Dóra renaming Bakery to "+name, rename(t, o, x.place, x.bakery.ID, name), send(o, none["POST /api/places/{place}/items/{item}/rename"]))
	}

	// Moving what she was given into what she was not: the same answer as a folder that does not exist.
	noFolder := move(t, o, x.place, x.y2026.ID, nowhere)
	for what, id := range map[string]int64{"Clients": x.clients.ID, "Private": x.private.ID, "Réka's file": rekasFile.ID, "a folder of Payroll": slip.ID, "the top folder": 0} {
		sameAs(t, "Dóra moving 2026 into "+what, move(t, o, x.place, x.y2026.ID, id), noFolder)
	}

	// Nor into another place, naming it: what she was given stays in Bence's My files. Where she cannot add, the same
	// answer as a place that does not exist; where she can (her own places), the moves between places there are.
	noPlace := moveInto(t, o, x.place, x.y2026.ID, nowhere, 0)
	for what, p := range map[string][2]int64{"Réka's My files": {rekas, 0}, "Payroll": {payroll.ID, 0}, "Bence's own top folder": {x.place, 0},
		"Clients, naming the place": {x.place, x.clients.ID}} {
		sameAs(t, "Dóra moving 2026 into "+what, moveInto(t, o, x.place, x.y2026.ID, p[0], p[1]), noPlace)
		sameAs(t, "Dóra asking who would see 2026 in "+what, o.Get(t, previewPath(x.place, x.y2026.ID, p[0], p[1])), o.Get(t, previewPath(x.place, x.y2026.ID, nowhere, 0)))
	}
	dorasOwn, _ := myFiles(t, d, dora)
	dorasShared := madeSharedFolder(t, o, "Dóra's")
	for what, p := range map[string]int64{"her own My files": dorasOwn, "her own shared folder": dorasShared.ID} {
		if res := moveInto(t, o, x.place, x.y2026.ID, p, 0); res.Status != http.StatusBadRequest {
			t.Errorf("Dóra moving 2026 out of Bence's My files into %s: got %d %s, want 400", what, res.Status, res.Body)
		}
	}

	// Nothing changed on the storage, and nobody was given anything.
	if got := onStorage(t, d, "My files/bence@example.com"); !slices.Equal(got, []string{"Clients/", "Private/"}) {
		t.Errorf("Bence's top folder on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients"); !slices.Equal(got, []string{"Bakery/", "Price list.pdf"}) {
		t.Errorf("Clients on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Bakery on the storage: %q", got)
	}
	if got := inUploads(t, d.Storage); len(got) != 3 {
		t.Errorf("the uploads folder: %v, want the three uploads in progress, untouched", got)
	}
	want := []string{bence.UserID + " owner (owner)", reka.UserID + " viewer (here)"}
	if got := accessLines(accessOf(t, b, itemAccessPath(x.place, x.clients.ID))); !slices.Equal(got, want) {
		t.Errorf("who can see Clients: %q, want %q", got, want)
	}
	if got := trashOf(t, b, x.place); len(got) != 2 {
		t.Errorf("Bence's trash: %+v", got)
	}
}

// Restoring is for what someone could have deleted from where it goes back to, and, for someone given only part of a
// place, only for what they put in the trash themselves: never what the owner deleted or replaced before sharing it, and
// never into a folder they may not add to, even when the folder it was deleted from is gone.
func TestSomeoneGivenPartOfAPlaceRestoresOnlyWhatTheyDeletedAndOnlyWhereTheyMayAdd(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	top := "My files/bence@example.com"
	missingTrashed := func() harness.Response { return restore(t, o, x.place, x.notes.ID+1000) }

	// Before sharing Clients, Bence deletes a file from it and replaces another: the old content goes to his trash.
	salaries := uploadedInto(t, b, x.place, x.clients.ID, "Salaries.xlsx", []byte("salaries"))
	deleted(t, b, x.place, salaries.ID)
	if res := uploadInto(t, b, x.place, x.clients.ID, "Price list.pdf", []byte("new prices"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("replacing Price list.pdf: %d %s", res.Status, res.Body)
	}
	trash := trashOf(t, b, x.place)
	if len(trash) != 2 {
		t.Fatalf("Bence's trash: %+v", trash)
	}
	ok(t, "sharing Clients with Dóra as editor", share(t, b, x.place, x.clients.ID, dora, "editor"))
	for _, e := range trash {
		sameAs(t, "Dóra restoring "+e.Name+", which Bence put in the trash", restore(t, o, x.place, e.ID), missingTrashed())
	}
	// Nor what Bence deletes from Clients now: it is his to restore.
	deleted(t, b, x.place, x.ledger.ID)
	sameAs(t, "Dóra restoring Ledger.xlsx, deleted by Bence", restore(t, o, x.place, x.ledger.ID), missingTrashed())
	if got := onStorage(t, d, top+"/Clients"); !slices.Equal(got, []string{"Bakery/", "Price list.pdf"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	if got, _ := d.Storage.Get(t, top+"/Clients/Price list.pdf"); string(got) != "new prices" {
		t.Fatalf("Price list.pdf holds %q", got)
	}

	// What she deletes herself she may restore, back where it was.
	deleted(t, o, x.place, x.offer.ID)
	if r := restored(t, o, x.place, x.offer.ID); r.Item.ID != x.offer.ID || !slices.Equal(pathNames(r.Path), []string{"Clients", "Bakery", "2026"}) {
		t.Fatalf("Dóra restoring Offer.pdf: %+v", r)
	}

	// Its folder deleted after it: it would go back to Bence's top folder, where she may not add, so it stays in the
	// trash, even when it has a share of its own.
	ok(t, "Bence giving Dóra editor on Offer.pdf too", share(t, b, x.place, x.offer.ID, dora, "editor"))
	deleted(t, o, x.place, x.offer.ID)
	deleted(t, o, x.place, x.y2026.ID)
	sameAs(t, "Dóra restoring Offer.pdf while 2026 is in the trash", restore(t, o, x.place, x.offer.ID), missingTrashed())
	restored(t, o, x.place, x.y2026.ID) // 2026 itself goes back into Bakery, which she may add to
	restored(t, o, x.place, x.offer.ID) // and with it there, Offer.pdf goes back into it

	// Its folder gone from the storage behind the Drive's back: the guard finds it in the index, and the restore finds it
	// gone, decides again on the top folder instead, and stops.
	deleted(t, o, x.place, x.offer.ID)
	d.Storage.Delete(t, top+"/Clients/Bakery/2026", true)
	res := restore(t, o, x.place, x.offer.ID)
	if res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "cannot go back") {
		t.Fatalf("Dóra restoring Offer.pdf after 2026 went from the storage: got %d %s, want 409", res.Status, res.Body)
	}
	if got := onStorage(t, d, top); !slices.Equal(got, []string{"Clients/", "Notes.txt", "Private/"}) {
		t.Fatalf("Bence's top folder on the storage: %q", got)
	}
	// Bence may: it goes to his top folder.
	if r := restored(t, b, x.place, x.offer.ID); r.Item.ID != x.offer.ID || len(r.Path) != 0 {
		t.Fatalf("Bence restoring Offer.pdf: %+v", r)
	}
	if got, _ := d.Storage.Get(t, top+"/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("Offer.pdf in Bence's top folder holds %q", got)
	}
}
