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

// Sharing outside the project switched off, and what a project admin oversees: the interface's calls, as the interface
// makes them; a visitor's and a guest's browser, as in links_test.go and guests_test.go.

type (
	// switchJSON is a switch of sharing outside the project: on, or off since when and by whom.
	switchJSON struct {
		On     bool        `json:"on"`
		By     *personJSON `json:"by"`
		Former bool        `json:"former"`
		At     *time.Time  `json:"at"`
	}
	// meOutsideJSON is what /api/me says about sharing outside the project, and what the person may do that concerns no
	// place.
	meOutsideJSON struct {
		SharingOutside switchJSON `json:"sharingOutside"`
		Can            []string   `json:"can"`
	}
	// placeOutsideJSON is a place as the places list gives it, with its own switch (a shared folder's) and whether
	// sharing outside the project is on in it now.
	placeOutsideJSON struct {
		placeJSON
		SharingOutside *switchJSON `json:"sharingOutside"`
		Outside        string      `json:"outside"`
	}
	// outsideShareJSON is one thing shared outside the project, as a project admin oversees it.
	outsideShareJSON struct {
		Kind string `json:"kind"`
		Item struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"item"`
		Place struct {
			ID        int64       `json:"id"`
			Kind      string      `json:"kind"`
			Name      string      `json:"name"`
			Owner     *personJSON `json:"owner"`
			OwnerLeft bool        `json:"ownerLeft"`
			Outside   string      `json:"outside"`
		} `json:"place"`
		SharedBy *personJSON `json:"sharedBy"`
		Former   bool        `json:"former"`
		Guest    *guestJSON  `json:"guest"`
		Role     string      `json:"role"`
		Since    time.Time   `json:"since"`
		Until    *time.Time  `json:"until"`
		Uses     int64       `json:"uses"`
		Used     *time.Time  `json:"used"`
		Works    bool        `json:"works"`
	}
	spaceEntryJSON struct {
		Person  *personJSON `json:"person"`
		Former  bool        `json:"former"`
		Folder  string      `json:"folder"`
		ID      int64       `json:"id"`
		Name    string      `json:"name"`
		Deleted bool        `json:"deleted"`
		Used    int64       `json:"used"`
	}
	spaceJSON struct {
		Used          int64            `json:"used"`
		Capacity      *int64           `json:"capacity"`
		People        []spaceEntryJSON `json:"people"`
		SharedFolders []spaceEntryJSON `json:"sharedFolders"`
	}
)

// switchOutside switches sharing outside the project on or off for the whole project.
func switchOutside(t *testing.T, c *harness.Client, on bool) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, "/api/sharing-outside", map[string]bool{"on": on})
}

func switchedOutside(t *testing.T, c *harness.Client, on bool) switchJSON {
	t.Helper()
	res := switchOutside(t, c, on)
	if res.Status != http.StatusOK {
		t.Fatalf("switching sharing outside the project %v: got %d %s", on, res.Status, res.Body)
	}
	var got struct {
		SharingOutside switchJSON `json:"sharingOutside"`
	}
	res.JSON(t, &got)
	return got.SharingOutside
}

func folderSwitchPath(placeID int64) string {
	return fmt.Sprintf("/api/places/%d/sharing-outside", placeID)
}

// switchFolderOutside switches sharing outside the project on or off for one shared folder.
func switchFolderOutside(t *testing.T, c *harness.Client, placeID int64, on bool) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, folderSwitchPath(placeID), map[string]bool{"on": on})
}

func switchedFolderOutside(t *testing.T, c *harness.Client, placeID int64, on bool) placeOutsideJSON {
	t.Helper()
	res := switchFolderOutside(t, c, placeID, on)
	if res.Status != http.StatusOK {
		t.Fatalf("switching sharing outside %v for shared folder %d: got %d %s", on, placeID, res.Status, res.Body)
	}
	var p placeOutsideJSON
	res.JSON(t, &p)
	return p
}

func meOutside(t *testing.T, c *harness.Client) meOutsideJSON {
	t.Helper()
	res := c.Get(t, "/api/me")
	if res.Status != http.StatusOK {
		t.Fatalf("/api/me: got %d %s", res.Status, res.Body)
	}
	var me meOutsideJSON
	res.JSON(t, &me)
	return me
}

// placeOutside is the place with this id as the member's places list gives it.
func placeOutside(t *testing.T, c *harness.Client, id int64) placeOutsideJSON {
	t.Helper()
	res := c.Get(t, "/api/places")
	if res.Status != http.StatusOK {
		t.Fatalf("places: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Places []placeOutsideJSON `json:"places"`
	}
	res.JSON(t, &l)
	for _, p := range l.Places {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("place %d is not among the places: %s", id, res.Body)
	return placeOutsideJSON{}
}

// outsideOf says where sharing outside the project is switched off for what an answer is about ("outside").
func outsideOf(t *testing.T, res harness.Response) string {
	t.Helper()
	var got struct {
		Outside any `json:"outside"`
	}
	res.JSON(t, &got)
	s, _ := got.Outside.(string)
	return s
}

// refusedOutside fails the test unless res is the member's answer for something only sharing outside the project
// switched off stops: 409, saying where it was switched off.
func refusedOutside(t *testing.T, what string, res harness.Response, where string) {
	t.Helper()
	if res.Status != http.StatusConflict || outsideOf(t, res) != where || !strings.Contains(errorOf(t, res), "Sharing outside the project is switched off") {
		t.Errorf("%s: got %d %s; want 409 with outside %q", what, res.Status, res.Body, where)
	}
}

// notAvailable fails the test unless res is a visitor's page for a link that would open, but not now.
func notAvailable(t *testing.T, what string, res harness.Response) {
	t.Helper()
	if res.Status != http.StatusForbidden || !strings.Contains(string(res.Body), "<h1>This link is not available</h1>") ||
		!strings.Contains(string(res.Body), "switched off for now") || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("%s: got %d %.300s; want the page of a link that is not available now", what, res.Status, res.Body)
	}
	visitorHeaders(t, what, res)
}

// unavailableToGuest fails the test unless res is a guest's answer for something given to them that is not available
// now: 403, saying so.
func unavailableToGuest(t *testing.T, what string, res harness.Response) {
	t.Helper()
	var got struct {
		Unavailable bool   `json:"unavailable"`
		Error       string `json:"error"`
	}
	if res.Status == http.StatusForbidden {
		res.JSON(t, &got)
	}
	if res.Status != http.StatusForbidden || !got.Unavailable || !strings.Contains(got.Error, "Kovács Accounting has switched off sharing outside") {
		t.Errorf("%s: got %d %s; want 403, not available now", what, res.Status, res.Body)
	}
}

// guestUnavailable is whether the guest's "Shared with me" says that something given to them is not available now.
func guestUnavailable(t *testing.T, c *harness.Client) bool {
	t.Helper()
	res := c.Get(t, guestAPI+"/shared-with-me")
	if res.Status != http.StatusOK {
		t.Fatalf("a guest's Shared with me: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Unavailable bool `json:"unavailable"`
	}
	res.JSON(t, &l)
	return l.Unavailable
}

// A project admin switches sharing outside the project off for the whole project. From then on no public link opens and
// no guest gets in: a visitor whose link would open is told it is not available (anything else answers as a link that
// does not exist), a guest stays signed in and is told so, and members can make no link and invite no guest. What was
// made stays: listed, with whether it works, and removable; sharing with members goes on as before. Switched back on,
// every link and invitation works again as it was, but what was removed meanwhile.
func TestAProjectAdminSwitchesSharingOutsideOffForTheWholeProject(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	r, b, v := d.As(reka), d.As(bence), visitor(d)
	x := bencesFiles(t, d)
	_, clientsLink := madeLink(t, d, b, x.place, x.clients.ID, false)
	_, notesLink := madeLink(t, d, b, x.place, x.notes.ID, false)
	annaGiven := invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	bobGiven := invited(t, b, x.place, x.secret.ID, bob, "viewer", nil)
	smtp.WaitFor(t, 2)
	annaCookie := signedIn(t, d, smtp, anna)
	annas := withCookie(d, annaCookie)
	if l := guestList(t, annas, x.place, x.bakery.ID); !slices.Equal(guestNames(l.Items), []string{"2026"}) {
		t.Fatalf("Anna's Bakery: %+v", l)
	}
	missing := v.Get(t, noSuchLink(t))
	noGuestItem := annas.Get(t, guestItemPath(x.place, x.notes.ID+1000, ""))

	// On, for everyone to see; only a project admin may switch it.
	for _, m := range []harness.Member{reka, bence, dora} {
		if me := meOutside(t, d.As(m)); !me.SharingOutside.On || me.SharingOutside.By != nil {
			t.Fatalf("%s: sharing outside the project at first: %+v", m.Email, me.SharingOutside)
		}
	}
	if got := meOutside(t, r).Can; !slices.Equal(got, []string{"enter", "see-usage", "make-shared-folder", "oversee", "switch-sharing-outside"}) {
		t.Errorf("what Réka, a project admin, may do in no place: %q", got)
	}
	if got := meOutside(t, b).Can; !slices.Equal(got, []string{"enter", "see-usage", "make-shared-folder"}) {
		t.Errorf("what Bence may do in no place: %q", got)
	}
	for _, m := range []harness.Member{bence, dora} {
		if res := switchOutside(t, d.As(m), false); res.Status != http.StatusNotFound || errorOf(t, res) != notHere {
			t.Errorf("%s, not an admin, switching sharing outside off: got %d %s", m.Email, res.Status, res.Body)
		}
	}
	if res := sendJSON(t, r, http.MethodPut, "/api/sharing-outside", map[string]string{"off": "yes"}); res.Status != http.StatusBadRequest {
		t.Errorf("switching it without saying on or off: got %d %s", res.Status, res.Body)
	}
	if res := switchOutside(t, r.From("https://evil.example"), false); res.Status != http.StatusForbidden {
		t.Errorf("switching it from another site's page: got %d %s", res.Status, res.Body)
	}
	if !meOutside(t, b).SharingOutside.On {
		t.Fatal("sharing outside the project went off without a project admin")
	}

	// Réka switches it off.
	sw := switchedOutside(t, r, false)
	if sw.On || sw.By == nil || sw.By.UserID != reka.UserID || sw.At == nil || time.Since(*sw.At) > time.Minute {
		t.Fatalf("switched off: %+v", sw)
	}
	if again := switchedOutside(t, r, false); again.At == nil || !again.At.Equal(*sw.At) {
		t.Errorf("switching it off again: %+v, want it as it was", again)
	}
	harness.Eventually(t, "the log says it was switched off", func() bool {
		return strings.Contains(d.Logs(), "sharing outside the project was switched off for the whole project")
	})
	if me := meOutside(t, d.As(dora)); me.SharingOutside.On || me.SharingOutside.By == nil || me.SharingOutside.By.DisplayName != reka.DisplayName {
		t.Errorf("Dóra sees sharing outside the project as %+v", me.SharingOutside)
	}

	// Visitors: the link would open this, so it says it is not available, on every route; anything it does not cover is
	// still a link that does not exist, as is a link that never was.
	notAvailable(t, "the link on Clients", v.Get(t, clientsLink))
	notAvailable(t, "Bakery through the link", v.Get(t, inLink(clientsLink, x.bakery.ID, "")))
	notAvailable(t, "Offer.pdf downloaded through the link", v.Get(t, inLink(clientsLink, x.offer.ID, "/download")))
	notAvailable(t, "Offer.pdf opened through the link", v.Get(t, inLink(clientsLink, x.offer.ID, "/open")))
	notAvailable(t, "Bakery as a zip through the link", v.Get(t, inLink(clientsLink, x.bakery.ID, "/zip")))
	notAvailable(t, "the link on Notes.txt, by a signed-in member", b.Get(t, notesLink))
	for what, path := range map[string]string{
		"Secret.txt through the link on Clients": inLink(clientsLink, x.secret.ID, "/download"),
		"Notes.txt through the link on Clients":  inLink(clientsLink, x.notes.ID, ""),
		"Clients through the link on Notes.txt":  inLink(notesLink, x.clients.ID, ""),
		"an item that does not exist":            inLink(clientsLink, x.notes.ID+1000, "/download"),
		"a link that never was":                  noSuchLink(t),
	} {
		sameAsNoLink(t, what+", while off", v.Get(t, path), missing)
	}
	if res := v.Do(t, http.MethodPost, clientsLink, nil); res.Status != http.StatusMethodNotAllowed {
		t.Errorf("a change through the link while off: got %d", res.Status)
	}

	// Anna stays signed in, and is told what was given to her is not available now: it is not even named.
	if who := signedInAs(t, d, annaCookie); who != anna {
		t.Fatalf("Anna's session while off: %q", who)
	}
	items, body := guestShared(t, annas)
	if len(items) != 0 || !guestUnavailable(t, annas) || strings.Contains(body, "Bakery") {
		t.Errorf("Anna's Shared with me while off: %s", body)
	}
	unavailableToGuest(t, "Anna opening Bakery", annas.Get(t, guestItemPath(x.place, x.bakery.ID, "")))
	unavailableToGuest(t, "Anna listing Bakery", annas.Get(t, guestItemPath(x.place, x.bakery.ID, "/items")))
	unavailableToGuest(t, "Anna downloading Offer.pdf", annas.Get(t, guestItemPath(x.place, x.offer.ID, "/download")))
	unavailableToGuest(t, "Anna uploading into Bakery", guestUpload(t, annas, x.place, x.bakery.ID, "Planted.txt", []byte("x"), ""))
	unavailableToGuest(t, "Anna making a folder in Bakery",
		sendJSON(t, annas, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/folders"), map[string]string{"name": "Planted"}))
	unavailableToGuest(t, "Anna deleting 2026", annas.Do(t, http.MethodDelete, guestItemPath(x.place, x.y2026.ID, ""), nil))
	for what, id := range map[string]int64{"Clients": x.clients.ID, "Secret.txt": x.secret.ID, "nothing": x.notes.ID + 1000} {
		res := annas.Get(t, guestItemPath(x.place, id, ""))
		if res.Status != http.StatusNotFound || string(res.Body) != string(noGuestItem.Body) {
			t.Errorf("Anna asking for %s while off: got %d %s, want as for nothing", what, res.Status, res.Body)
		}
	}
	// Signed in afresh on another browser while it is off, she is told the same.
	again := withCookie(d, signedIn(t, d, smtp, anna))
	if !guestUnavailable(t, again) {
		t.Error("Anna, signed in again while off, is not told what was given to her is not available")
	}

	// Members: no new link, no new guest, no invitation changed; a member's address is shared with as before.
	refusedOutside(t, "Bence making a link on Price list.pdf", makeLink(t, b, x.place, x.prices.ID, false), "project")
	refusedOutside(t, "Bence making a new link on Clients", makeLink(t, b, x.place, x.clients.ID, true), "project")
	refusedOutside(t, "Bence inviting Cleo", invite(t, b, x.place, x.prices.ID, cleo, "viewer", nil), "project")
	refusedOutside(t, "Bence making Anna a viewer", changeInvitation(t, b, x.place, x.bakery.ID, annaGiven.Guest.ID, "viewer", nil), "project")
	if res := invite(t, b, x.place, x.prices.ID, dora.Email, "viewer", nil); res.Status != http.StatusOK {
		t.Errorf("Bence sharing Price list.pdf with Dóra by her address while off: got %d %s", res.Status, res.Body)
	}
	if res := sendJSON(t, d.As(dora), http.MethodPost, linkPath(x.place, x.prices.ID), nil); res.Status != http.StatusNotFound {
		t.Errorf("Dóra, a viewer, making a link while off: got %d %s, want as for nothing", res.Status, res.Body)
	}
	// What was made is listed, saying it does not work now, and removable.
	if l := linkOn(t, b, x.place, x.clients.ID); l == nil || l.Works {
		t.Errorf("the link on Clients as Bence sees it while off: %+v", l)
	}
	if got := outsideOf(t, b.Get(t, linkPath(x.place, x.clients.ID))); got != "project" {
		t.Errorf("the link's answer says sharing outside is %q", got)
	}
	lines := accessOf(t, b, itemAccessPath(x.place, x.bakery.ID))
	if g := guestLine(lines, anna); g == nil || g.Works == nil || *g.Works {
		t.Errorf("Anna's line on Bakery while off: %+v", g)
	}
	if slices.Contains(accessLines(lines), "link viewer (from Clients)") {
		t.Errorf("who can see Bakery while off says anyone with the link can: %q", accessLines(lines))
	}
	if l := list(t, b, x.place, x.clients.ID); named(t, l.Items, "Bakery").SeenBy.Link || named(t, l.Items, "Bakery").SeenBy.Guests != 0 {
		t.Errorf("Bakery listed while off: %+v", named(t, l.Items, "Bakery").SeenBy)
	}
	if got := outsideOf(t, b.Get(t, listPath(x.place, x.clients.ID))); got != "project" {
		t.Errorf("Clients' listing says sharing outside is %q", got)
	}
	if res := removeLink(t, b, x.place, x.notes.ID); res.Status != http.StatusNoContent {
		t.Fatalf("Bence removing the link on Notes.txt while off: got %d %s", res.Status, res.Body)
	}
	ok(t, "Bence taking Bob's invitation away while off", uninvite(t, b, x.place, x.secret.ID, bobGiven.Guest.ID))

	// Back on: what was left works again as it was, the same sessions included; what was removed stays removed.
	if sw := switchedOutside(t, r, true); !sw.On || sw.By != nil || sw.At != nil {
		t.Fatalf("switched back on: %+v", sw)
	}
	if p := readPage(t, v, clientsLink); p.name != "Clients" {
		t.Errorf("the link on Clients, back on: %+v", p)
	}
	if res := v.Get(t, inLink(clientsLink, x.offer.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "offer" {
		t.Errorf("Offer.pdf through the link, back on: %d %s", res.Status, res.Body)
	}
	sameAsNoLink(t, "the link on Notes.txt, removed while off", v.Get(t, notesLink), missing)
	if items, _ := guestShared(t, annas); len(items) != 1 || items[0].ID != x.bakery.ID || guestUnavailable(t, annas) {
		t.Errorf("Anna's Shared with me, back on: %+v", items)
	}
	if l := guestList(t, annas, x.place, x.bakery.ID); !slices.Equal(guestNames(l.Items), []string{"2026"}) {
		t.Errorf("Anna's Bakery, back on, in the same session: %+v", l)
	}
	if l := linkOn(t, b, x.place, x.clients.ID); l == nil || !l.Works {
		t.Errorf("the link on Clients, back on: %+v", l)
	}
	if res := makeLink(t, b, x.place, x.prices.ID, false); res.Status != http.StatusCreated {
		t.Errorf("Bence making a link, back on: %d %s", res.Status, res.Body)
	}
}

// A manager switches sharing outside the project off for one shared folder: its links open nothing and its guests get
// nothing, while everything else works as before. Only its managers (and the project admins, who manage every shared
// folder) switch it, and only for a shared folder: a My files has no switch of its own, and anyone else is answered as
// for a place that does not exist. Switched on for the folder, it stays off there while it is off for the whole project.
// Something with a link moved into it from a My files keeps its link, which opens nothing there.
func TestAManagerSwitchesSharingOutsideOffForOneSharedFolder(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	r, b, o, v := d.As(reka), d.As(bence), d.As(dora), visitor(d)
	payroll := madeSharedFolder(t, b, "Payroll")
	clients := madeSharedFolder(t, b, "Clients")
	ok(t, "making Dóra an editor of Payroll", sendJSON(t, b, http.MethodPut, accessPath(payroll.ID, dora), map[string]string{"role": "editor"}))
	slips := madeFolder(t, b, payroll.ID, 0, "Slips")
	slip := uploadedInto(t, b, payroll.ID, slips.ID, "November.pdf", []byte("salaries"))
	offer := uploadedInto(t, b, clients.ID, 0, "Offer.pdf", []byte("offer"))
	_, payrollLink := madeLink(t, d, b, payroll.ID, slip.ID, false)
	_, clientsLink := madeLink(t, d, b, clients.ID, offer.ID, false)
	invited(t, b, payroll.ID, slips.ID, anna, "viewer", nil)
	invited(t, b, clients.ID, offer.ID, anna, "viewer", nil)
	smtp.WaitFor(t, 2)
	annas := withCookie(d, signedIn(t, d, smtp, anna))
	bences, _ := myFiles(t, d, bence)
	missing := switchFolderOutside(t, r, payroll.ID+clients.ID+bences+100, false)

	// Its own switch, on, is in the places list for whoever has a role on it.
	for _, m := range []harness.Member{reka, bence, dora} {
		if p := placeOutside(t, d.As(m), payroll.ID); p.SharingOutside == nil || !p.SharingOutside.On || p.Outside != "on" {
			t.Fatalf("%s: Payroll's switch at first: %+v", m.Email, p)
		}
	}
	if p := placeOutside(t, b, bences); p.SharingOutside != nil || p.Outside != "on" {
		t.Errorf("Bence's My files has a switch of its own: %+v", p)
	}
	if !slices.Contains(placeOutside(t, b, payroll.ID).Can, "switch-sharing-outside") || slices.Contains(placeOutside(t, o, payroll.ID).Can, "switch-sharing-outside") {
		t.Errorf("who may switch Payroll: Bence %q, Dóra %q", placeOutside(t, b, payroll.ID).Can, placeOutside(t, o, payroll.ID).Can)
	}

	// Not an editor's to switch, and nobody's for a My files, nor for a shared folder in the trash: as for no place.
	gone := madeSharedFolder(t, b, "Old")
	ok(t, "deleting Old", deleteSharedFolder(t, b, gone.ID))
	for what, res := range map[string]harness.Response{
		"Dóra, an editor, switching Payroll":       switchFolderOutside(t, o, payroll.ID, false),
		"Bence switching his own My files":         switchFolderOutside(t, b, bences, false),
		"Réka, a project admin, switching Bence's": switchFolderOutside(t, r, bences, false),
		"Bence switching Old, in the trash":        switchFolderOutside(t, b, gone.ID, false),
	} {
		if res.Status != http.StatusNotFound || string(res.Body) != string(missing.Body) {
			t.Errorf("%s: got %d %s, want as for no place", what, res.Status, res.Body)
		}
	}
	if res := sendJSON(t, b, http.MethodPut, folderSwitchPath(payroll.ID), map[string]any{}); res.Status != http.StatusBadRequest {
		t.Errorf("switching Payroll without saying on or off: got %d %s", res.Status, res.Body)
	}

	// Bence, its manager, switches it off for Payroll alone.
	p := switchedFolderOutside(t, b, payroll.ID, false)
	if p.SharingOutside == nil || p.SharingOutside.On || p.SharingOutside.By == nil || p.SharingOutside.By.UserID != bence.UserID ||
		p.Outside != "folder" || p.Name != "Payroll" {
		t.Fatalf("Payroll switched off: %+v", p)
	}
	if p := placeOutside(t, o, payroll.ID); p.SharingOutside.On || p.Outside != "folder" {
		t.Errorf("Payroll as Dóra sees it: %+v", p)
	}
	if !meOutside(t, o).SharingOutside.On {
		t.Error("switching it off for Payroll switched it off for the whole project")
	}
	notAvailable(t, "the link on November.pdf in Payroll", v.Get(t, payrollLink))
	if res := v.Get(t, inLink(clientsLink, offer.ID, "/download")); res.Status != http.StatusOK {
		t.Errorf("the link on Offer.pdf in Clients, with Payroll's off: %d", res.Status)
	}
	unavailableToGuest(t, "Anna opening Slips", annas.Get(t, guestItemPath(payroll.ID, slips.ID, "")))
	if items, _ := guestShared(t, annas); len(items) != 1 || items[0].ID != offer.ID || !guestUnavailable(t, annas) {
		t.Errorf("Anna's Shared with me with Payroll's off: %+v", items)
	}
	refusedOutside(t, "Dóra making a link in Payroll", makeLink(t, o, payroll.ID, slips.ID, false), "folder")
	refusedOutside(t, "Dóra inviting Bob into Payroll", invite(t, o, payroll.ID, slips.ID, bob, "viewer", nil), "folder")
	if res := makeLink(t, o, clients.ID, offer.ID, true); res.Status != http.StatusNotFound {
		t.Errorf("Dóra, with no role on Clients, making a link there: %d %s", res.Status, res.Body)
	}
	if res := makeLink(t, b, clients.ID, offer.ID, true); res.Status != http.StatusCreated {
		t.Errorf("Bence making a new link in Clients, with Payroll's off: %d %s", res.Status, res.Body)
	}
	if got := outsideOf(t, o.Get(t, listPath(payroll.ID, slips.ID))); got != "folder" {
		t.Errorf("Slips' listing says sharing outside is %q", got)
	}

	// Moving something with a link from Bence's My files into Payroll: the warning says it is off there, and once there
	// its link opens nothing, and is still listed, to be removed.
	letter := uploaded(t, b, bences, "Letter.pdf", []byte("letter"))
	_, letterLink := madeLink(t, d, b, bences, letter.ID, false)
	res := b.Get(t, previewPath(bences, letter.ID, payroll.ID, 0))
	var preview previewJSON
	res.JSON(t, &preview)
	if res.Status != http.StatusOK || outsideOf(t, res) != "folder" || preview.SeenBy.Link {
		t.Fatalf("who would see Letter.pdf in Payroll: %d %s", res.Status, res.Body)
	}
	ok(t, "moving Letter.pdf into Payroll", moveInto(t, b, bences, letter.ID, payroll.ID, 0))
	notAvailable(t, "Letter.pdf's link, in Payroll", v.Get(t, letterLink))
	if l := linkOn(t, b, payroll.ID, letter.ID); l == nil || l.Works {
		t.Errorf("Letter.pdf's link in Payroll: %+v", l)
	}

	// Réka, a project admin, manages Payroll too: she switches it back on, and its links and guests work again.
	if p := switchedFolderOutside(t, r, payroll.ID, true); !p.SharingOutside.On || p.Outside != "on" {
		t.Fatalf("Payroll switched back on by Réka: %+v", p)
	}
	if res := v.Get(t, payrollLink); res.Status != http.StatusOK {
		t.Errorf("Payroll's link, back on: %d", res.Status)
	}
	if res := annas.Get(t, guestItemPath(payroll.ID, slips.ID, "")); res.Status != http.StatusOK {
		t.Errorf("Anna opening Slips, back on: %d %s", res.Status, res.Body)
	}

	// Off for the whole project, and off for Payroll: on for Payroll is still off there, until the project's is on too.
	switchedOutside(t, r, false)
	switchedFolderOutside(t, b, payroll.ID, false)
	if p := placeOutside(t, b, payroll.ID); p.Outside != "project" || p.SharingOutside.On {
		t.Errorf("Payroll, off for both: %+v", p)
	}
	if p := switchedFolderOutside(t, b, payroll.ID, true); !p.SharingOutside.On || p.Outside != "project" {
		t.Errorf("Payroll switched on with the project's off: %+v", p)
	}
	notAvailable(t, "Payroll's link, on for Payroll, off for the project", v.Get(t, payrollLink))
	refusedOutside(t, "Bence making a link in Payroll, off for the project", makeLink(t, b, payroll.ID, slips.ID, false), "project")
	switchedOutside(t, r, true)
	if res := v.Get(t, payrollLink); res.Status != http.StatusOK {
		t.Errorf("Payroll's link, on for both: %d", res.Status)
	}

	// A shared folder's own switch stays with it through the trash.
	switchedFolderOutside(t, b, clients.ID, false)
	ok(t, "deleting Clients", deleteSharedFolder(t, b, clients.ID))
	ok(t, "restoring Clients", restoreSharedFolder(t, b, clients.ID))
	if p := placeOutside(t, b, clients.ID); p.SharingOutside.On || p.Outside != "folder" {
		t.Errorf("Clients, restored: %+v", p)
	}
}

// sharedOutside is everything shared outside the project, as a project admin oversees it, and the raw answer.
func sharedOutside(t *testing.T, c *harness.Client) ([]outsideShareJSON, string) {
	t.Helper()
	res := c.Get(t, "/api/oversight/shared-outside")
	if res.Status != http.StatusOK {
		t.Fatalf("what is shared outside the project: got %d %s", res.Status, res.Body)
	}
	var l struct {
		SharingOutside switchJSON         `json:"sharingOutside"`
		Items          []outsideShareJSON `json:"items"`
	}
	res.JSON(t, &l)
	return l.Items, string(res.Body)
}

// outsideLine is one thing shared outside, as a line: what, on what, where, by whom, with whom, and whether it works.
func outsideLine(e outsideShareJSON) string {
	where := e.Place.Name
	if e.Place.Owner != nil {
		where = "My files of " + e.Place.Owner.DisplayName
	}
	if e.Place.OwnerLeft {
		where = "held My files"
	}
	by := "someone who left"
	if e.SharedBy != nil {
		by = e.SharedBy.DisplayName
	}
	with := "anyone with the link"
	if e.Guest != nil {
		with = e.Guest.Address
	}
	works := "works"
	if !e.Works {
		works = "does not work"
	}
	return fmt.Sprintf("%s %s %q in %s, by %s, with %s as %s: %s", e.Kind, e.Item.Kind, e.Item.Name, where, by, with, e.Role, works)
}

// A project admin sees everything shared outside the project, links and guests, and nothing else: the item's name
// (never the folders above it, nor what is in it), whose My files or which shared folder it is in, who shared it, with
// whom, how often it was used and when last, until when, and whether it works now. What is in the trash, or gone from
// the storage, is not shared outside, and is not listed. Nobody but a project admin sees the list, and it opens
// nothing: the admin still cannot list, open or download what is in anyone's My files.
func TestAProjectAdminSeesEverythingSharedOutsideTheProject(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	r, b, o, v := d.As(reka), d.As(bence), d.As(dora), visitor(d)
	x := bencesFiles(t, d)
	// Bence's My files: a link on Clients, Anna invited to Bakery as editor until a day ahead, Bob to Offer.pdf (three
	// levels below Clients) as viewer. Dóra, given Private as editor, makes a link on it, and loses it again.
	_, clientsLink := madeLink(t, d, b, x.place, x.clients.ID, false)
	until := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Millisecond)
	annaGiven := invited(t, b, x.place, x.bakery.ID, anna, "editor", &until)
	invited(t, b, x.place, x.offer.ID, bob, "viewer", nil)
	ok(t, "giving Dóra Private as editor", share(t, b, x.place, x.private.ID, dora, "editor"))
	madeLink(t, d, o, x.place, x.private.ID, false)
	ok(t, "taking Private from Dóra", unshare(t, b, x.place, x.private.ID, dora))
	// Dóra's shared folder Payroll: a link on a slip; and a link on a file in the trash, and on one gone from the storage.
	payroll := madeSharedFolder(t, o, "Payroll")
	slip := uploadedInto(t, o, payroll.ID, 0, "Slip.pdf", []byte("slip"))
	madeLink(t, d, o, payroll.ID, slip.ID, false)
	old := uploadedInto(t, o, payroll.ID, 0, "Old slip.pdf", []byte("old"))
	madeLink(t, d, o, payroll.ID, old.ID, false)
	deleted(t, o, payroll.ID, old.ID)
	vanished := uploadedInto(t, o, payroll.ID, 0, "Vanished.pdf", []byte("vanished"))
	madeLink(t, d, o, payroll.ID, vanished.ID, false)
	d.Storage.Delete(t, "Shared folders/Payroll/Vanished.pdf", false)
	if l := list(t, o, payroll.ID, 0); slices.Contains(names(l.Items), "Vanished.pdf") {
		l = listedAfterLook(t, o, payroll.ID)
		if slices.Contains(names(l.Items), "Vanished.pdf") {
			t.Fatalf("Vanished.pdf is still listed after it went from the storage")
		}
	}
	smtp.WaitFor(t, 2)
	// Used: the link on Clients twice by visitors, Anna's invitation once.
	readPage(t, v, clientsLink)
	readPage(t, v, inLink(clientsLink, x.bakery.ID, ""))
	annas := withCookie(d, signedIn(t, d, smtp, anna))
	guestList(t, annas, x.place, x.bakery.ID)

	items, body := sharedOutside(t, r)
	got := []string{}
	byName := map[string]outsideShareJSON{}
	for _, e := range items {
		got = append(got, outsideLine(e))
		byName[e.Kind+" "+e.Item.Name] = e
	}
	slices.Sort(got)
	want := []string{
		`guest file "Offer.pdf" in My files of Bence Tóth, by Bence Tóth, with bob@other.example as viewer: works`,
		`guest folder "Bakery" in My files of Bence Tóth, by Bence Tóth, with anna@client.example as editor: works`,
		`link file "Slip.pdf" in Payroll, by Dóra Nagy, with anyone with the link as viewer: works`,
		`link folder "Clients" in My files of Bence Tóth, by Bence Tóth, with anyone with the link as viewer: works`,
		`link folder "Private" in My files of Bence Tóth, by Dóra Nagy, with anyone with the link as viewer: does not work`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("what is shared outside the project:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if e := byName["link Clients"]; e.Uses != 2 || e.Used == nil || time.Since(*e.Used) > time.Minute || time.Since(e.Since) > time.Minute ||
		e.Place.Kind != "my-files" || e.Place.Outside != "on" {
		t.Errorf("the link on Clients: %+v", e)
	}
	if e := byName["guest Bakery"]; e.Uses < 1 || e.Used == nil || e.Until == nil || !e.Until.Equal(until) || e.Guest.ID != annaGiven.Guest.ID {
		t.Errorf("Anna's invitation to Bakery: %+v", e)
	}
	if e := byName["guest Offer.pdf"]; e.Uses != 0 || e.Used != nil || e.Until != nil {
		t.Errorf("Bob's invitation to Offer.pdf: %+v", e)
	}
	if e := byName["link Slip.pdf"]; e.Place.Kind != "shared-folder" || e.Place.ID != payroll.ID || e.Place.Owner != nil {
		t.Errorf("the link on Slip.pdf: %+v", e)
	}
	// Names only of what is shared outside: nothing above it, beside it or inside it, nor anything else.
	for _, never := range []string{`"name":"2026"`, "Ledger.xlsx", "Price list.pdf", "Secret.txt", "Notes.txt", "Old slip.pdf", "Vanished.pdf", "salaries"} {
		if strings.Contains(body, never) {
			t.Errorf("the list names %q", never)
		}
	}

	// Nobody but a project admin sees it, nor the space used.
	for _, m := range []harness.Member{bence, dora} {
		for _, path := range []string{"/api/oversight/shared-outside", "/api/oversight/space"} {
			if res := d.As(m).Get(t, path); res.Status != http.StatusNotFound || errorOf(t, res) != notHere {
				t.Errorf("%s asking for %s: got %d %s", m.Email, path, res.Status, res.Body)
			}
		}
	}
	// It opens nothing: Réka still cannot list, open or download what is in Bence's My files, by the ids it names.
	nothing := r.Get(t, downloadPath(x.place, x.notes.ID+1000))
	for _, e := range items {
		if e.Place.Kind != "my-files" {
			continue
		}
		for _, path := range []string{downloadPath(x.place, e.Item.ID), listPath(x.place, e.Item.ID), zipPath(x.place, e.Item.ID),
			itemAccessPath(x.place, e.Item.ID), linkPath(x.place, e.Item.ID)} {
			sameAs(t, "Réka asking for "+path, r.Get(t, path), nothing)
		}
	}

	// Switched off for the whole project, nothing works, and all of it is still listed.
	switchedOutside(t, r, false)
	items, _ = sharedOutside(t, r)
	if len(items) != len(want) {
		t.Fatalf("while off, the list holds %d", len(items))
	}
	for _, e := range items {
		if e.Works || e.Place.Outside != "project" {
			t.Errorf("while off: %s (outside %q)", outsideLine(e), e.Place.Outside)
		}
	}
}

// listedAfterLook is the top folder's list once the Drive has looked at it on the storage.
func listedAfterLook(t *testing.T, c *harness.Client, placeID int64) itemsJSON {
	t.Helper()
	res := c.Get(t, listPath(placeID, 0)+"?wait")
	if res.Status != http.StatusOK {
		t.Fatalf("listing place %d: got %d %s", placeID, res.Status, res.Body)
	}
	var l itemsJSON
	res.JSON(t, &l)
	return l
}

// A project admin sees how much space each person's My files and each shared folder uses, the trash included, as the
// sidebar's meter counts it: every member, someone who left with their files held, and every shared folder, the one in
// the trash too, the largest first. Sums only: nothing in anything is named.
func TestAProjectAdminSeesTheSpaceEachPersonAndSharedFolderUses(t *testing.T) {
	t.Parallel()
	emil := harness.Member{UserID: "e1f2-emil", Email: "emil@example.com", DisplayName: "Emil Szabó", Role: "member"}
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora, emil}, Env: map[string]string{"DRIVE_CAPACITY": "1TB"}})
	r, b, o := d.As(reka), d.As(bence), d.As(dora)
	bences, _ := myFiles(t, d, bence)
	uploaded(t, b, bences, "Tax notes.pdf", []byte(strings.Repeat("t", 3000)))
	gone := uploaded(t, b, bences, "Old.pdf", []byte(strings.Repeat("o", 500)))
	deleted(t, b, bences, gone.ID) // the trash counts
	doras, _ := myFiles(t, d, dora)
	uploaded(t, o, doras, "Dóra's.txt", []byte(strings.Repeat("d", 700)))
	payroll := madeSharedFolder(t, b, "Payroll")
	uploadedInto(t, b, payroll.ID, 0, "Slip.pdf", []byte(strings.Repeat("s", 2000)))
	archive := madeSharedFolder(t, b, "Archive")
	uploadedInto(t, b, archive.ID, 0, "2025.zip", []byte(strings.Repeat("a", 100)))
	ok(t, "deleting Archive", deleteSharedFolder(t, b, archive.ID))
	myFiles(t, d, reka)
	// Dóra leaves the project: her files are held, and still take their space.
	d.SetMembers(t, reka, bence, emil)
	harness.Eventually(t, "Dóra is no longer a member", func() bool { return d.As(dora).Get(t, "/api/me").Status == http.StatusForbidden })

	res := r.Get(t, "/api/oversight/space")
	if res.Status != http.StatusOK {
		t.Fatalf("the space used: got %d %s", res.Status, res.Body)
	}
	var s spaceJSON
	res.JSON(t, &s)
	people := []string{}
	for _, e := range s.People {
		who := e.Folder + " (former)"
		if e.Person != nil {
			who = e.Person.DisplayName
		}
		people = append(people, fmt.Sprintf("%s %d", who, e.Used))
	}
	if want := []string{"Bence Tóth 3500", "dora@example.com (former) 700", "Emil Szabó 0", "Réka Kovács 0"}; !slices.Equal(people, want) {
		t.Errorf("the space each person uses: %q, want %q", people, want)
	}
	folders := []string{}
	for _, e := range s.SharedFolders {
		folders = append(folders, fmt.Sprintf("%s %d deleted %v", e.Name, e.Used, e.Deleted))
	}
	if want := []string{"Payroll 2000 deleted false", "Archive 100 deleted true"}; !slices.Equal(folders, want) {
		t.Errorf("the space each shared folder uses: %q, want %q", folders, want)
	}
	if u := usage(t, d); s.Used != 6300 || *u.Used != s.Used || s.Capacity == nil || *s.Capacity != *u.Capacity {
		t.Errorf("the whole: %d of %v; the meter says %d of %v", s.Used, s.Capacity, *u.Used, u.Capacity)
	}
	for _, never := range []string{"Tax notes", "Old.pdf", "Dóra's.txt", "Slip.pdf", "2025.zip"} {
		if strings.Contains(string(res.Body), never) {
			t.Errorf("the space used names %q", never)
		}
	}
}
