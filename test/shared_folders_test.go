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
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The interface's calls for shared folders, as the interface makes them.

// placesOf is every place the member can open, as /api/places lists them.
func placesOf(t *testing.T, d *harness.Drive, m harness.Member) []placeJSON {
	t.Helper()
	res := d.As(m).Get(t, "/api/places")
	if res.Status != http.StatusOK {
		t.Fatalf("places for %s: %d %s", m.Email, res.Status, res.Body)
	}
	var l placesJSON
	res.JSON(t, &l)
	return l.Places
}

// sharedFolders is the shared folders the member can open, by name, with their ids.
func sharedFolders(t *testing.T, d *harness.Drive, m harness.Member) map[string]placeJSON {
	t.Helper()
	out := map[string]placeJSON{}
	for _, p := range placesOf(t, d, m) {
		if p.Kind == "shared-folder" {
			out[p.Name+fmt.Sprintf(" #%d", p.ID)] = p
		}
	}
	return out
}

// seesSharedFolder says whether the member's places list the shared folder with this id.
func seesSharedFolder(t *testing.T, d *harness.Drive, m harness.Member, id int64) (placeJSON, bool) {
	t.Helper()
	for _, p := range placesOf(t, d, m) {
		if p.ID == id {
			if p.Kind != "shared-folder" {
				t.Fatalf("place %d is listed as %q", id, p.Kind)
			}
			return p, true
		}
	}
	return placeJSON{}, false
}

func newSharedFolder(t *testing.T, c *harness.Client, name string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, "/api/places", map[string]string{"name": name})
}

// madeSharedFolder makes a shared folder and fails the test unless the Drive made it.
func madeSharedFolder(t *testing.T, c *harness.Client, name string) placeJSON {
	t.Helper()
	res := newSharedFolder(t, c, name)
	if res.Status != http.StatusCreated {
		t.Fatalf("making the shared folder %q: got %d %s", name, res.Status, res.Body)
	}
	var p placeJSON
	res.JSON(t, &p)
	return p
}

func accessPath(placeID int64, member harness.Member) string {
	return fmt.Sprintf("/api/places/%d/access/members/%s", placeID, url.PathEscape(member.UserID))
}

// accessOf is who has access, as GET answers it at path.
func accessOf(t *testing.T, c *harness.Client, path string) []accessJSON {
	t.Helper()
	res := c.Get(t, path)
	if res.Status != http.StatusOK {
		t.Fatalf("who has access at %s: got %d %s", path, res.Status, res.Body)
	}
	var l struct {
		Access []accessJSON `json:"access"`
	}
	res.JSON(t, &l)
	return l.Access
}

func whoHasAccess(t *testing.T, c *harness.Client, placeID int64) []accessJSON {
	t.Helper()
	return accessOf(t, c, fmt.Sprintf("/api/places/%d/access", placeID))
}

// managers is who has access, as "user id role (given|admin|former)" lines, in the order the Drive lists them.
func managers(people []accessJSON) []string {
	var out []string
	for _, p := range people {
		var why []string
		if p.Given {
			why = append(why, "given")
		}
		if p.Admin {
			why = append(why, "admin")
		}
		if p.Former {
			why = append(why, "former")
		}
		who := p.UserID
		if p.Everyone {
			who = "everyone"
		}
		out = append(out, fmt.Sprintf("%s %s (%s)", who, p.Role, strings.Join(why, ", ")))
	}
	return out
}

func makeManager(t *testing.T, c *harness.Client, placeID int64, m harness.Member) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPut, accessPath(placeID, m), map[string]string{"role": "manager"})
}

func takeAccess(t *testing.T, c *harness.Client, placeID int64, m harness.Member) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, accessPath(placeID, m), nil)
}

func deleteSharedFolder(t *testing.T, c *harness.Client, placeID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, fmt.Sprintf("/api/places/%d", placeID), nil)
}

func restoreSharedFolder(t *testing.T, c *harness.Client, placeID int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, fmt.Sprintf("/api/places/%d/restore", placeID), nil)
}

func deletedSharedFolders(t *testing.T, c *harness.Client) []deletedPlaceJSON {
	t.Helper()
	res := c.Get(t, "/api/places/deleted")
	if res.Status != http.StatusOK {
		t.Fatalf("shared folders in the trash: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Places []deletedPlaceJSON `json:"places"`
	}
	res.JSON(t, &l)
	return l.Places
}

func ok(t *testing.T, what string, res harness.Response) {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s: got %d %s, want 200", what, res.Status, res.Body)
	}
}

// "New shared folder" makes one top folder on the storage per shared folder, named after it, beside "My files". Its
// maker manages it, and what is put into it lands in its folder.
func TestNewSharedFolderMakesOneTopFolderOnTheStoragePerSharedFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b := d.As(bence)

	clients := madeSharedFolder(t, b, "Clients")
	payroll := madeSharedFolder(t, b, "Payroll 2026 – Árvíztűrő")
	for _, p := range []placeJSON{clients, payroll} {
		if p.Kind != "shared-folder" || p.Role != "manager" || p.ID == 0 ||
			!slices.Contains(p.Can, "manage-access") || !slices.Contains(p.Can, "delete-shared-folder") || !slices.Contains(p.Can, "upload") {
			t.Fatalf("a new shared folder: %+v; want Bence to manage it", p)
		}
	}
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Clients/", "Payroll 2026 – Árvíztűrő/"}) {
		t.Fatalf("Shared folders on the storage: %q", got)
	}
	if got := onStorage(t, d, "Shared folders/Clients"); len(got) != 0 {
		t.Fatalf("a new shared folder is not empty: %q", got)
	}
	// Nothing on the storage is named after the person who made it: beside the shared folders is only the Drive's own
	// hidden folder, with its mark.
	if got := onStorage(t, d, ""); !slices.Equal(got, []string{".superit-drive/", "Shared folders/"}) {
		t.Fatalf("the Drive's folder holds %q", got)
	}

	// Its list follows its folder on the storage, like any place.
	bakery := madeFolder(t, b, clients.ID, 0, "Bakery Ltd")
	offer := uploadedInto(t, b, clients.ID, bakery.ID, "Offer.pdf", []byte("offer"))
	if got, _ := d.Storage.Get(t, "Shared folders/Clients/Bakery Ltd/Offer.pdf"); string(got) != "offer" {
		t.Fatalf("the upload is not in the shared folder's folder: %q", got)
	}
	d.Storage.Put(t, "Shared folders/Clients/Put there directly.txt", "direct")
	if l := list(t, b, clients.ID, 0); !slices.Equal(names(l.Items), []string{"Bakery Ltd", "Put there directly.txt"}) {
		t.Fatalf("Clients lists %q", names(l.Items))
	}
	if res := b.Get(t, downloadPath(clients.ID, offer.ID)); res.Status != http.StatusOK || string(res.Body) != "offer" {
		t.Fatalf("downloading from the shared folder: %d %s", res.Status, res.Body)
	}

	// Bence's places: My files first, then his shared folders by name.
	var listed []string
	for _, p := range placesOf(t, d, bence) {
		listed = append(listed, p.Kind+" "+p.Name)
	}
	if want := []string{"my-files ", "shared-folder Clients", "shared-folder Payroll 2026 – Árvíztűrő"}; !slices.Equal(listed, want) {
		t.Fatalf("Bence's places: %q, want %q", listed, want)
	}

	// A name that cannot be stored is refused, and so is one he can already see.
	for _, name := range []string{"", ".", "..", "a/b", "../My files", strings.Repeat("a", 256), "tab\there"} {
		if res := newSharedFolder(t, b, name); res.Status != http.StatusBadRequest || errorOf(t, res) == "" {
			t.Errorf("a shared folder called %q: got %d %s, want 400 with a reason", name, res.Status, res.Body)
		}
	}
	res := newSharedFolder(t, b, "Clients")
	var conflict conflictJSON
	res.JSON(t, &conflict)
	if res.Status != http.StatusConflict || conflict.Exists != "folder" || !strings.Contains(conflict.Error, "Clients") {
		t.Errorf("a second Clients for Bence: got %d %s, want 409", res.Status, res.Body)
	}

	// Dóra cannot see Bence's Clients, so she is not told it exists: hers is made, called the same, in a folder of its
	// own. Each sees only their own; the project admin sees both.
	doras := madeSharedFolder(t, d.As(dora), "Clients")
	if doras.ID == clients.ID || doras.Name != "Clients" {
		t.Fatalf("Dóra's Clients: %+v", doras)
	}
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Clients (2)/", "Clients/", "Payroll 2026 – Árvíztűrő/"}) {
		t.Fatalf("Shared folders on the storage: %q", got)
	}
	uploadedInto(t, d.As(dora), doras.ID, 0, "Dóra's.txt", []byte("hers"))
	if _, ok := d.Storage.Get(t, "Shared folders/Clients (2)/Dóra's.txt"); !ok {
		t.Fatal("Dóra's upload is not in her shared folder's folder")
	}
	if _, sees := seesSharedFolder(t, d, bence, doras.ID); sees {
		t.Error("Bence sees Dóra's shared folder")
	}
	if _, sees := seesSharedFolder(t, d, dora, clients.ID); sees {
		t.Error("Dóra sees Bence's shared folder")
	}
	if got := sharedFolders(t, d, reka); len(got) != 3 {
		t.Errorf("the project admin sees %v, want all three", got)
	}
}

// The creator is the shared folder's first manager. A manager can make other members managers, take that away again,
// and delete the shared folder, which goes to the trash whole until a manager restores it.
func TestTheCreatorManagesItAndCanMakeOthersManagersAndDeleteIt(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	clients := madeSharedFolder(t, b, "Clients")
	ledger := uploadedInto(t, b, clients.ID, madeFolder(t, b, clients.ID, 0, "Bakery Ltd").ID, "Ledger.xlsx", []byte("ledger"))

	// Bence was given it; Réka manages every shared folder as a project admin. Dóra has no role, so she cannot see it.
	want := []string{reka.UserID + " manager (admin)", bence.UserID + " manager (given)"}
	if got := managers(whoHasAccess(t, b, clients.ID)); !slices.Equal(got, want) {
		t.Fatalf("who manages Clients: %q, want %q", got, want)
	}
	if _, sees := seesSharedFolder(t, d, dora, clients.ID); sees {
		t.Fatal("Dóra sees Clients without a role")
	}
	if res := o.Get(t, listPath(clients.ID, 0)); res.Status != http.StatusNotFound {
		t.Fatalf("Dóra listing Clients: got %d %s", res.Status, res.Body)
	}

	// Bence makes Dóra a manager: she sees it, may do everything in it, and may make others managers in turn.
	ok(t, "making Dóra a manager", makeManager(t, b, clients.ID, dora))
	ok(t, "making Dóra a manager again", makeManager(t, b, clients.ID, dora))
	p, sees := seesSharedFolder(t, d, dora, clients.ID)
	if !sees || p.Role != "manager" || !slices.Contains(p.Can, "manage-access") || !slices.Contains(p.Can, "delete-shared-folder") {
		t.Fatalf("Dóra's view of Clients: %+v, %v", p, sees)
	}
	if res := o.Get(t, downloadPath(clients.ID, ledger.ID)); res.Status != http.StatusOK || string(res.Body) != "ledger" {
		t.Fatalf("Dóra downloading Bence's file: %d %s", res.Status, res.Body)
	}
	uploadedInto(t, o, clients.ID, 0, "From Dóra.txt", []byte("dóra"))
	want = []string{reka.UserID + " manager (admin)", bence.UserID + " manager (given)", dora.UserID + " manager (given)"}
	if got := managers(whoHasAccess(t, o, clients.ID)); !slices.Equal(got, want) {
		t.Fatalf("who manages Clients: %q, want %q", got, want)
	}
	people := whoHasAccess(t, o, clients.ID)
	if people[2].Email != dora.Email || people[2].DisplayName != dora.DisplayName {
		t.Fatalf("Dóra is listed as %+v", people[2])
	}

	// Only members can be given a role, and only the roles there are; everyone in the project cannot be a manager.
	stranger := harness.Member{UserID: "u-stranger"}
	if res := makeManager(t, b, clients.ID, stranger); res.Status != http.StatusBadRequest {
		t.Errorf("making someone outside the member list a manager: got %d %s, want 400", res.Status, res.Body)
	}
	if res := sendJSON(t, b, http.MethodPut, fmt.Sprintf("/api/places/%d/access/everyone", clients.ID), map[string]string{"role": "manager"}); res.Status != http.StatusBadRequest {
		t.Errorf("making everyone a manager: got %d %s, want 400", res.Status, res.Body)
	}
	for _, role := range []string{"owner", "admin", "Manager", ""} {
		res := sendJSON(t, b, http.MethodPut, accessPath(clients.ID, reka), map[string]string{"role": role})
		if res.Status != http.StatusBadRequest {
			t.Errorf("giving the role %q: got %d %s, want 400", role, res.Status, res.Body)
		}
	}
	if res := b.Do(t, http.MethodPut, accessPath(clients.ID, reka), strings.NewReader("not json")); res.Status != http.StatusBadRequest {
		t.Errorf("a role that is not JSON: got %d", res.Status)
	}

	// Dóra takes Bence's role away. He is not a project admin, so the shared folder is gone from his view.
	ok(t, "taking Bence's role away", takeAccess(t, o, clients.ID, bence))
	ok(t, "taking away a role nobody has", takeAccess(t, o, clients.ID, bence))
	if _, sees := seesSharedFolder(t, d, bence, clients.ID); sees {
		t.Fatal("Bence still sees Clients after his role was taken away")
	}
	if res := b.Get(t, downloadPath(clients.ID, ledger.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("Bence downloading from Clients without a role: got %d %s", res.Status, res.Body)
	}
	if res := makeManager(t, b, clients.ID, bence); res.Status != http.StatusNotFound {
		t.Fatalf("Bence giving himself the role back: got %d %s, want 404", res.Status, res.Body)
	}

	// Dóra deletes the shared folder: its whole top folder goes into its own trash folder on the storage, and nobody
	// can open it any more.
	res := deleteSharedFolder(t, o, clients.ID)
	var e deletedPlaceJSON
	res.JSON(t, &e)
	if res.Status != http.StatusOK || e.ID != clients.ID || e.Name != "Clients" || e.Size != int64(len("ledger")+len("dóra")) ||
		!e.Removed.Equal(e.Deleted.Add(30*24*time.Hour)) {
		t.Fatalf("deleting Clients: got %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "Shared folders"); len(got) != 0 {
		t.Fatalf("Shared folders on the storage still holds %q", got)
	}
	trash := ".superit-drive/trash/Shared folders/Clients"
	entries := onStorage(t, d, trash)
	if len(entries) != 1 || !trashEntry.MatchString(entries[0]) {
		t.Fatalf("the shared folder's trash on the storage holds %q", entries)
	}
	if got, _ := d.Storage.Get(t, trash+"/"+entries[0]+"Clients/Bakery Ltd/Ledger.xlsx"); string(got) != "ledger" {
		t.Fatalf("Ledger.xlsx is not in the trash with its shared folder: %q", got)
	}
	for _, m := range []harness.Member{reka, bence, dora} {
		if _, sees := seesSharedFolder(t, d, m, clients.ID); sees {
			t.Errorf("%s still sees Clients in the trash", m.Email)
		}
	}
	for what, res := range map[string]harness.Response{
		"list it":            d.As(reka).Get(t, listPath(clients.ID, 0)),
		"download from it":   o.Get(t, downloadPath(clients.ID, ledger.ID)),
		"upload into it":     uploadInto(t, o, clients.ID, 0, "Late.txt", []byte("late"), ""),
		"see who manages it": o.Get(t, fmt.Sprintf("/api/places/%d/access", clients.ID)),
		"delete it again":    deleteSharedFolder(t, o, clients.ID),
	} {
		if res.Status != http.StatusNotFound {
			t.Errorf("%s in the trash: got %d %s, want 404", what, res.Status, res.Body)
		}
	}
	if _, ok := d.Storage.List(t, "Shared folders/Clients"); ok {
		t.Fatal("something made the shared folder's top folder again while it is in the trash")
	}

	// Its managers and the project admins find it in the trash; Bence, who no longer has a role, does not.
	for m, want := range map[harness.Member]bool{dora: true, reka: true, bence: false} {
		got := deletedSharedFolders(t, d.As(m))
		if found := len(got) == 1 && got[0].ID == clients.ID; found != want {
			t.Errorf("%s's shared folders in the trash: %+v", m.Email, got)
		}
	}
	if res := restoreSharedFolder(t, b, clients.ID); res.Status != http.StatusNotFound {
		t.Errorf("Bence restoring it without a role: got %d %s, want 404", res.Status, res.Body)
	}

	// Réka restores it, as a project admin: everything comes back where it was, with its ids and its managers.
	res = restoreSharedFolder(t, d.As(reka), clients.ID)
	var back placeJSON
	res.JSON(t, &back)
	if res.Status != http.StatusOK || back.ID != clients.ID || back.Name != "Clients" || back.Role != "manager" {
		t.Fatalf("restoring Clients: got %d %s", res.Status, res.Body)
	}
	if got := onStorage(t, d, "Shared folders/Clients"); !slices.Equal(got, []string{"Bakery Ltd/", "From Dóra.txt"}) {
		t.Fatalf("Clients on the storage after the restore: %q", got)
	}
	if got := onStorage(t, d, trash); len(got) != 0 {
		t.Fatalf("the shared folder's trash still holds %q", got)
	}
	if res := o.Get(t, downloadPath(clients.ID, ledger.ID)); res.Status != http.StatusOK || string(res.Body) != "ledger" {
		t.Fatalf("Ledger.xlsx after the restore: %d %s", res.Status, res.Body)
	}
	want = []string{reka.UserID + " manager (admin)", dora.UserID + " manager (given)"}
	if got := managers(whoHasAccess(t, o, clients.ID)); !slices.Equal(got, want) {
		t.Fatalf("who manages Clients after the restore: %q, want %q", got, want)
	}
	if res := restoreSharedFolder(t, o, clients.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring it again: got %d %s, want 404", res.Status, res.Body)
	}
	if got := deletedSharedFolders(t, o); len(got) != 0 {
		t.Fatalf("the trash still lists %+v", got)
	}
}

// A shared folder always has someone in charge: a manager who is still in the project, or a project admin. Taking
// the last one away is refused.
func TestASharedFolderAlwaysKeepsAManager(t *testing.T) {
	t.Parallel()
	// No project admin here, as a Drive run outside Superit may have.
	d := harness.Start(t, bence, dora)
	b, o := d.As(bence), d.As(dora)
	team := madeSharedFolder(t, b, "Team")
	res := takeAccess(t, b, team.ID, bence)
	if res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "manager") {
		t.Fatalf("taking away the only manager: got %d %s, want 409", res.Status, res.Body)
	}
	ok(t, "making Dóra a manager", makeManager(t, b, team.ID, dora))
	ok(t, "Bence leaving it to Dóra", takeAccess(t, b, team.ID, bence))
	if res := takeAccess(t, o, team.ID, dora); res.Status != http.StatusConflict {
		t.Fatalf("Dóra taking her own role away, as the last manager: got %d %s, want 409", res.Status, res.Body)
	}

	// With a project admin, the admin is always in charge, so the last manager given the role may step back.
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Réka is in the member list", func() bool { return d.As(reka).Get(t, "/api/me").Status == http.StatusOK })
	ok(t, "Dóra stepping back while a project admin is there", takeAccess(t, o, team.ID, dora))
	if got := managers(whoHasAccess(t, d.As(reka), team.ID)); !slices.Equal(got, []string{reka.UserID + " manager (admin)"}) {
		t.Fatalf("who manages Team: %q", got)
	}
}

// A project admin sees and manages every shared folder, as long as they hold the admin role. Their own role on the
// project decides it, not anything given to them.
func TestAProjectAdminSeesAndManagesEverySharedFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, r := d.As(bence), d.As(reka)
	payroll := madeSharedFolder(t, b, "Payroll")
	slip := uploadedInto(t, b, payroll.ID, 0, "November.xlsx", []byte("salaries"))

	p, sees := seesSharedFolder(t, d, reka, payroll.ID)
	if !sees || p.Role != "manager" || !slices.Contains(p.Can, "delete-shared-folder") {
		t.Fatalf("Réka's view of Payroll: %+v, %v", p, sees)
	}
	// Everything a manager may do.
	if res := r.Get(t, downloadPath(payroll.ID, slip.ID)); res.Status != http.StatusOK || string(res.Body) != "salaries" {
		t.Fatalf("Réka downloading: %d %s", res.Status, res.Body)
	}
	archive := madeFolder(t, r, payroll.ID, 0, "Archive")
	ok(t, "Réka renaming", rename(t, r, payroll.ID, slip.ID, "November 2026.xlsx"))
	ok(t, "Réka moving", move(t, r, payroll.ID, slip.ID, archive.ID))
	deleted(t, r, payroll.ID, slip.ID)
	if trash := trashOf(t, r, payroll.ID); len(trash) != 1 || trash[0].ID != slip.ID {
		t.Fatalf("Payroll's trash for Réka: %+v", trash)
	}
	restored(t, r, payroll.ID, slip.ID)
	ok(t, "Réka making Dóra a manager", makeManager(t, r, payroll.ID, dora))
	ok(t, "Réka taking it away", takeAccess(t, r, payroll.ID, dora))
	ok(t, "Réka taking away Bence's, the only one given", takeAccess(t, r, payroll.ID, bence))
	if _, sees := seesSharedFolder(t, d, bence, payroll.ID); sees {
		t.Fatal("Bence still sees Payroll")
	}
	ok(t, "Réka deleting Payroll", deleteSharedFolder(t, r, payroll.ID))
	ok(t, "Réka restoring Payroll", restoreSharedFolder(t, r, payroll.ID))
	if got, _ := d.Storage.Get(t, "Shared folders/Payroll/Archive/November 2026.xlsx"); string(got) != "salaries" {
		t.Fatalf("the file after all that: %q", got)
	}

	// Without the admin role she is a member with no role on it, and it is gone from her view; with it back, it is back.
	d.SetMembers(t, harness.Member{UserID: reka.UserID, Email: reka.Email, DisplayName: reka.DisplayName, Role: "member"}, bence, dora)
	harness.Eventually(t, "Réka is no longer a project admin", func() bool {
		_, sees := seesSharedFolder(t, d, reka, payroll.ID)
		return !sees
	})
	if res := r.Get(t, listPath(payroll.ID, 0)); res.Status != http.StatusNotFound {
		t.Fatalf("Réka listing Payroll as a member without a role: got %d %s", res.Status, res.Body)
	}
	d.SetMembers(t, reka, bence, dora)
	harness.Eventually(t, "Réka is a project admin again", func() bool {
		_, sees := seesSharedFolder(t, d, reka, payroll.ID)
		return sees
	})

	// Being a manager of every shared folder gives nothing in anyone's My files.
	bences, _ := myFiles(t, d, bence)
	if res := r.Get(t, listPath(bences, 0)); res.Status != http.StatusNotFound {
		t.Fatalf("Réka listing Bence's My files: got %d %s", res.Status, res.Body)
	}
}

// A member without a role on a shared folder gets, from every route that takes a place or an item, the same answer as
// for a place that does not exist: they learn nothing about it, and change nothing. A shared folder in the trash and
// another member's My files answer the same way. An item of the shared folder, through a place of their own, is not
// found either. The routes are those of internal/server/routes.go that take a place; a test there keeps the two lists
// equal.
func TestAMemberWithoutARoleCannotTellASharedFolderFromNone(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)

	payroll := madeSharedFolder(t, b, "Payroll")
	year := madeFolder(t, b, payroll.ID, 0, "2026")
	slip := uploadedInto(t, b, payroll.ID, year.ID, "November.xlsx", []byte("salaries"))
	old := uploadedInto(t, b, payroll.ID, 0, "Old.zip", []byte("old"))
	deleted(t, b, payroll.ID, old.ID)
	// Bence's uploads in progress: into Payroll, into Payroll 2025 before it went to the trash, into his My files.
	payrollUpload := startedUpload(t, b, payroll.ID, year.ID, "December.xlsx", 10)
	gone := madeSharedFolder(t, b, "Payroll 2025")
	goneUpload := startedUpload(t, b, gone.ID, 0, "Old.xlsx", 10)
	ok(t, "deleting Payroll 2025", deleteSharedFolder(t, b, gone.ID))
	bences, _ := myFiles(t, d, bence)
	bencesUpload := startedUpload(t, b, bences, 0, "Payslip.pdf", 10)

	// Dóra's own places, where she may do everything.
	doras, _ := myFiles(t, d, dora)
	hers := madeSharedFolder(t, o, "Dóra's")
	hersFolder := madeFolder(t, o, hers.ID, 0, "Inside")
	nowhere := payroll.ID + gone.ID + hers.ID + bences + doras + 100

	type try struct{ method, path, body string }
	tries := func(place, folder, file, trashed int64, upload string) map[string]try {
		at := func(f string, a ...any) string { return fmt.Sprintf(f, a...) }
		return map[string]try{
			"DELETE /api/places/{place}":                         {"DELETE", at("/api/places/%d", place), ""},
			"POST /api/places/{place}/restore":                   {"POST", at("/api/places/%d/restore", place), ""},
			"GET /api/places/{place}/access":                     {"GET", at("/api/places/%d/access", place), ""},
			"PUT /api/places/{place}/access/members/{member}":    {"PUT", accessPath(place, dora), `{"role":"manager"}`},
			"DELETE /api/places/{place}/access/members/{member}": {"DELETE", accessPath(place, bence), ""},
			"PUT /api/places/{place}/access/everyone":            {"PUT", at("/api/places/%d/access/everyone", place), `{"role":"editor"}`},
			"DELETE /api/places/{place}/access/everyone":         {"DELETE", at("/api/places/%d/access/everyone", place), ""},
			"GET /api/places/{place}/items/{item}/access":        {"GET", at("/api/places/%d/items/%d/access", place, file), ""},
			"PUT /api/places/{place}/items/{item}/access/members/{member}": {"PUT",
				at("/api/places/%d/items/%d/access/members/%s", place, file, url.PathEscape(dora.UserID)), `{"role":"editor"}`},
			"DELETE /api/places/{place}/items/{item}/access/members/{member}": {"DELETE",
				at("/api/places/%d/items/%d/access/members/%s", place, file, url.PathEscape(bence.UserID)), ""},
			"PUT /api/places/{place}/items/{item}/access/everyone":    {"PUT", at("/api/places/%d/items/%d/access/everyone", place, folder), `{"role":"editor"}`},
			"DELETE /api/places/{place}/items/{item}/access/everyone": {"DELETE", at("/api/places/%d/items/%d/access/everyone", place, folder), ""},
			"GET /api/places/{place}/items/{item}/link":               {"GET", linkPath(place, folder), ""},
			"POST /api/places/{place}/items/{item}/link":              {"POST", linkPath(place, folder), `{"replace":true}`},
			"DELETE /api/places/{place}/items/{item}/link":            {"DELETE", linkPath(place, file), ""},
			"GET /api/places/{place}/items":                           {"GET", listPath(place, 0), ""},
			"GET /api/places/{place}/items/{item}/items":              {"GET", listPath(place, folder), ""},
			"POST /api/places/{place}/files":                          {"POST", at("/api/places/%d/files?name=Planted.txt&existing=replace", place), "x"},
			"POST /api/places/{place}/items/{item}/files":             {"POST", at("/api/places/%d/items/%d/files?name=November.xlsx&existing=replace", place, folder), "x"},
			"POST /api/places/{place}/folders":                        {"POST", at("/api/places/%d/folders", place), `{"name":"Planted"}`},
			"POST /api/places/{place}/items/{item}/folders":           {"POST", at("/api/places/%d/items/%d/folders", place, folder), `{"name":"Planted"}`},
			"GET /api/places/{place}/items/{item}/download":           {"GET", downloadPath(place, file), ""},
			"GET /api/places/{place}/items/{item}/open":               {"GET", openPath(place, file), ""},
			"POST /api/places/{place}/items/{item}/rename":            {"POST", at("/api/places/%d/items/%d/rename", place, file), `{"name":"Renamed.xlsx"}`},
			"POST /api/places/{place}/items/{item}/move":              {"POST", at("/api/places/%d/items/%d/move", place, file), `{"to":null}`},
			"GET /api/places/{place}/items/{item}/move":               {"GET", at("/api/places/%d/items/%d/move?to=%d", place, file, folder), ""},
			"DELETE /api/places/{place}/items/{item}":                 {"DELETE", at("/api/places/%d/items/%d", place, file), ""},
			"GET /api/places/{place}/trash":                           {"GET", at("/api/places/%d/trash", place), ""},
			"POST /api/places/{place}/trash/{item}/restore":           {"POST", at("/api/places/%d/trash/%d/restore", place, trashed), ""},
			"GET /api/places/{place}/zip":                             {"GET", zipPath(place, 0), ""},
			"GET /api/places/{place}/items/{item}/zip":                {"GET", zipPath(place, folder), ""},
			"POST /api/places/{place}/uploads":                        {"POST", uploadsPath(place, 0) + "?name=Planted.txt&size=1&existing=replace", ""},
			"POST /api/places/{place}/items/{item}/uploads":           {"POST", uploadsPath(place, folder) + "?name=November.xlsx&size=1&existing=replace", ""},
			"GET /api/places/{place}/uploads/{upload}":                {"GET", sessionPath(place, upload), ""},
			"PUT /api/places/{place}/uploads/{upload}":                {"PUT", sessionPath(place, upload) + "?offset=0", "x"},
			"POST /api/places/{place}/uploads/{upload}/finish":        {"POST", sessionPath(place, upload) + "/finish?existing=replace", ""},
			"DELETE /api/places/{place}/uploads/{upload}":             {"DELETE", sessionPath(place, upload), ""},
		}
	}
	send := func(c *harness.Client, x try) harness.Response {
		var body io.Reader
		if x.body != "" {
			body = strings.NewReader(x.body)
		}
		return c.Do(t, x.method, x.path, body)
	}

	none := tries(nowhere, year.ID, slip.ID, old.ID, payrollUpload.ID)
	type placeAndUpload struct {
		place  int64
		upload string
	}
	for who, c := range map[string]struct {
		as     *harness.Client
		places map[string]placeAndUpload
	}{
		// Dóra has no role on Payroll, nor on Payroll 2025 in the trash, and Bence's My files is his alone.
		"Dóra": {o, map[string]placeAndUpload{"Payroll": {payroll.ID, payrollUpload.ID},
			"Payroll 2025 in the trash": {gone.ID, goneUpload.ID}, "Bence's My files": {bences, bencesUpload.ID}}},
		// A project admin manages every shared folder, and still cannot open anyone's My files; and an upload in
		// progress is its uploader's alone, even in a shared folder the admin manages.
		"Réka, a project admin": {d.As(reka), map[string]placeAndUpload{"Bence's My files": {bences, bencesUpload.ID}}},
	} {
		for what, p := range c.places {
			for route, x := range tries(p.place, year.ID, slip.ID, old.ID, p.upload) {
				res, missing := send(c.as, x), send(c.as, none[route])
				if res.Status != http.StatusNotFound || res.Status != missing.Status || !bytes.Equal(res.Body, missing.Body) {
					t.Errorf("%s, %s in %s: got %d %s; a place that does not exist answers %d %s",
						who, route, what, res.Status, res.Body, missing.Status, missing.Body)
				}
			}
		}
	}

	// Payroll's items through Dóra's own places, where she may do everything, are not found: an item id from one place
	// never works through another.
	for _, own := range []int64{hers.ID, doras} {
		missing := send(o, tries(own, nowhere, nowhere, nowhere, payrollUpload.ID)["GET /api/places/{place}/items/{item}/download"])
		for route, x := range tries(own, year.ID, slip.ID, old.ID, payrollUpload.ID) {
			if !strings.Contains(route, "{item}") && !strings.Contains(route, "{upload}") {
				continue
			}
			res := send(o, x)
			if res.Status != http.StatusNotFound || (route == "GET /api/places/{place}/items/{item}/download" && !bytes.Equal(res.Body, missing.Body)) {
				t.Errorf("%s with Payroll's item through Dóra's place %d: got %d %s, want 404", route, own, res.Status, res.Body)
			}
		}
		if res := move(t, o, hers.ID, hersFolder.ID, year.ID); res.Status != http.StatusNotFound {
			t.Errorf("moving into Payroll's folder from Dóra's shared folder: got %d %s, want 404", res.Status, res.Body)
		}
	}
	// Into Payroll, Payroll 2025 in the trash, or Bence's My files, from her own My files, naming the place: the same
	// answer as for a place that does not exist, whether she moves or only asks who would see it there.
	mine := uploaded(t, o, doras, "Mine.txt", []byte("mine"))
	for _, verb := range []string{"POST", "GET"} {
		attempt := func(place, folder int64) harness.Response {
			if verb == "GET" {
				return o.Get(t, previewPath(doras, mine.ID, place, folder))
			}
			return moveInto(t, o, doras, mine.ID, place, folder)
		}
		missing := attempt(nowhere, 0)
		for what, p := range map[string][2]int64{"Payroll": {payroll.ID, 0}, "Payroll's 2026": {payroll.ID, year.ID},
			"Payroll 2025 in the trash": {gone.ID, 0}, "Bence's My files": {bences, 0}} {
			if res := attempt(p[0], p[1]); res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missing.Body) {
				t.Errorf("Dóra moving (%s) her file into %s: got %d %s; a place that does not exist answers %d %s", verb, what, res.Status, res.Body, missing.Status, missing.Body)
			}
		}
	}

	// She sees neither in her places or in the trash, and nothing changed on the storage.
	if got := sharedFolders(t, d, dora); len(got) != 1 {
		t.Errorf("Dóra's shared folders: %v, want only her own", got)
	}
	if got := deletedSharedFolders(t, o); len(got) != 0 {
		t.Errorf("Dóra's shared folders in the trash: %+v", got)
	}
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Dóra's/", "Payroll/"}) {
		t.Errorf("Shared folders on the storage: %q", got)
	}
	if got := onStorage(t, d, "Shared folders/Payroll"); !slices.Equal(got, []string{"2026/"}) {
		t.Errorf("Payroll on the storage: %q", got)
	}
	if got := onStorage(t, d, "Shared folders/Payroll/2026"); !slices.Equal(got, []string{"November.xlsx"}) {
		t.Errorf("Payroll's 2026 on the storage: %q", got)
	}
	if got := inUploads(t, d.Storage); len(got) != 3 {
		t.Errorf("the uploads folder: %v, want Bence's three uploads in progress, untouched", got)
	}
	// Admins included: Bence's uploads are still his, and Réka's own into Payroll is hers.
	if s := stateOf(t, b, payroll.ID, payrollUpload.ID); s.Offset != 0 {
		t.Errorf("Bence's upload into Payroll: %+v", s)
	}
	sameAs(t, "Réka with Bence's upload into Payroll, which she manages", d.As(reka).Get(t, sessionPath(payroll.ID, payrollUpload.ID)),
		d.As(reka).Get(t, sessionPath(payroll.ID, "0123456789abcdef0123456789abcdef")))
	if got, _ := d.Storage.Get(t, "Shared folders/Payroll/2026/November.xlsx"); string(got) != "salaries" {
		t.Errorf("November.xlsx holds %q", got)
	}
	if got := managers(whoHasAccess(t, b, payroll.ID)); !slices.Equal(got, []string{reka.UserID + " manager (admin)", bence.UserID + " manager (given)"}) {
		t.Errorf("who manages Payroll: %q", got)
	}
	if got := deletedSharedFolders(t, b); len(got) != 1 || got[0].ID != gone.ID {
		t.Errorf("Bence's shared folders in the trash: %+v", got)
	}
}

// A shared folder is the project's: nothing about it depends on the account of the person who made it. When they
// leave the project, it stays where it is, with its files, for the managers left and the project admins.
func TestASharedFolderOutlivesTheAccountThatMadeIt(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	clients := madeSharedFolder(t, b, "Clients")
	offer := uploadedInto(t, b, clients.ID, 0, "Offer.pdf", []byte("offer"))
	ok(t, "making Dóra a manager", makeManager(t, b, clients.ID, dora))

	// Bence leaves the project.
	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Bence is refused", func() bool { return b.Get(t, "/api/places").Status == http.StatusForbidden })

	for _, m := range []harness.Member{dora, reka} {
		if p, sees := seesSharedFolder(t, d, m, clients.ID); !sees || p.Role != "manager" {
			t.Fatalf("%s's view of Clients after Bence left: %+v, %v", m.Email, p, sees)
		}
		if res := d.As(m).Get(t, downloadPath(clients.ID, offer.ID)); res.Status != http.StatusOK || string(res.Body) != "offer" {
			t.Fatalf("%s downloading Bence's file: %d %s", m.Email, res.Status, res.Body)
		}
	}
	uploadedInto(t, o, clients.ID, 0, "After Bence.txt", []byte("still here"))
	if got := onStorage(t, d, "Shared folders/Clients"); !slices.Equal(got, []string{"After Bence.txt", "Offer.pdf"}) {
		t.Fatalf("Clients on the storage: %q", got)
	}
	// Who manages it says Bence's role is that of someone no longer in the project, and a manager can take it away.
	want := []string{reka.UserID + " manager (admin)", bence.UserID + " manager (given, former)", dora.UserID + " manager (given)"}
	if got := managers(whoHasAccess(t, o, clients.ID)); !slices.Equal(got, want) {
		t.Fatalf("who manages Clients: %q, want %q", got, want)
	}
	ok(t, "taking away the former member's role", takeAccess(t, o, clients.ID, bence))
	// Dóra can leave it too: Réka, a project admin, is still in charge.
	ok(t, "Dóra stepping back", takeAccess(t, o, clients.ID, dora))
	if got := managers(whoHasAccess(t, d.As(reka), clients.ID)); !slices.Equal(got, []string{reka.UserID + " manager (admin)"}) {
		t.Fatalf("who manages Clients: %q", got)
	}
	ok(t, "Réka deleting it", deleteSharedFolder(t, d.As(reka), clients.ID))
	ok(t, "Réka restoring it", restoreSharedFolder(t, d.As(reka), clients.ID))
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Clients/"}) {
		t.Fatalf("Shared folders on the storage: %q", got)
	}
}

// Inside a shared folder several people work: replace, keep both, delete and restore act on the shared folder's own
// trash, beside its path, and every manager sees and restores what any of them deleted.
func TestTheTrashReplaceAndKeepBothInASharedFolder(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	clients := madeSharedFolder(t, b, "Clients")
	ok(t, "making Dóra a manager", makeManager(t, b, clients.ID, dora))
	drafts := madeFolder(t, b, clients.ID, 0, "Drafts")
	report := uploadedInto(t, b, clients.ID, drafts.ID, "Report.pdf", []byte("Bence's"))

	// Dóra uploads the same name: keep both, then replace. Bence's goes to the shared folder's trash.
	res := uploadInto(t, o, clients.ID, drafts.ID, "Report.pdf", []byte("Dóra's first"), "keep-both")
	var kept itemJSON
	res.JSON(t, &kept)
	if res.Status != http.StatusCreated || kept.Name != "Report (2).pdf" {
		t.Fatalf("keep both: got %d %s", res.Status, res.Body)
	}
	res = uploadInto(t, o, clients.ID, drafts.ID, "Report.pdf", []byte("Dóra's second"), "replace")
	var replaced itemJSON
	res.JSON(t, &replaced)
	if res.Status != http.StatusCreated || replaced.ID != report.ID || !replaced.Replaced {
		t.Fatalf("replace: got %d %s", res.Status, res.Body)
	}
	trash := trashOf(t, b, clients.ID)
	if len(trash) != 1 || !trash[0].Replaced || trash[0].Name != "Report.pdf" || len(trash[0].From) != 1 || trash[0].From[0].ID != drafts.ID {
		t.Fatalf("Clients' trash for Bence: %+v", trash)
	}
	sharedTrash := ".superit-drive/trash/Shared folders/Clients"
	entries := onStorage(t, d, sharedTrash)
	if len(entries) != 1 {
		t.Fatalf("the shared folder's trash on the storage: %q", entries)
	}
	if got, _ := d.Storage.Get(t, sharedTrash+"/"+entries[0]+"Report.pdf"); string(got) != "Bence's" {
		t.Fatalf("the replaced file in the trash holds %q", got)
	}
	// Bence restores his: beside Dóra's, under a free name.
	r := restored(t, b, clients.ID, trash[0].ID)
	if r.Item.Name != "Report (3).pdf" || len(r.Path) != 1 || r.Path[0].ID != drafts.ID {
		t.Fatalf("Bence's report came back as %+v in %+v", r.Item, r.Path)
	}

	// Dóra deletes the folder Bence made; Bence and Réka see it in the trash, and Réka restores it.
	deleted(t, o, clients.ID, drafts.ID)
	for _, m := range []harness.Member{bence, reka} {
		if trash := trashOf(t, d.As(m), clients.ID); len(trash) != 1 || trash[0].ID != drafts.ID {
			t.Fatalf("Clients' trash for %s: %+v", m.Email, trash)
		}
	}
	restored(t, d.As(reka), clients.ID, drafts.ID)
	if got := onStorage(t, d, "Shared folders/Clients/Drafts"); !slices.Equal(got, []string{"Report (2).pdf", "Report (3).pdf", "Report.pdf"}) {
		t.Fatalf("Drafts on the storage: %q", got)
	}
	// Nothing of it reached anyone's My files trash.
	bences, _ := myFiles(t, d, bence)
	if trash := trashOf(t, b, bences); len(trash) != 0 {
		t.Fatalf("Bence's own trash: %+v", trash)
	}

	// Moving stays inside the shared folder: a folder of someone's My files is not found from it.
	mine, _ := myFiles(t, d, dora)
	inMine := madeFolder(t, o, mine, 0, "Mine")
	if res := move(t, o, clients.ID, report.ID, inMine.ID); res.Status != http.StatusNotFound {
		t.Fatalf("moving into Dóra's My files from Clients: got %d %s, want 404", res.Status, res.Body)
	}
	archive := madeFolder(t, o, clients.ID, 0, "Archive")
	ok(t, "moving inside Clients", move(t, o, clients.ID, report.ID, archive.ID))
	if _, ok := d.Storage.Get(t, "Shared folders/Clients/Archive/Report.pdf"); !ok {
		t.Fatal("Report.pdf did not move inside Clients")
	}
}

// A shared folder in the trash is kept 30 days with everything in it, then removed for good with its trash, by the
// Drive on its own. Its folder's name is held meanwhile: a new shared folder of that name gets a folder of its own.
func TestASharedFolderInTheTrashIsRemovedForGoodAfter30Days(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b := d.As(bence)
	old := madeSharedFolder(t, b, "Old")
	file := uploadedInto(t, b, old.ID, 0, "Old.bin", randomBytes(t, 4000))
	early := uploadedInto(t, b, old.ID, 0, "Early.bin", randomBytes(t, 50))
	deleted(t, b, old.ID, early.ID)
	ok(t, "deleting Old", deleteSharedFolder(t, b, old.ID))
	newer := madeSharedFolder(t, b, "Old")
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Old (2)/"}) {
		t.Fatalf("Shared folders on the storage: %q", got)
	}
	if used := usage(t, d).Used; *used != 4050 {
		t.Fatalf("used %d, want 4050: a shared folder in the trash counts", *used)
	}

	// A restart changes the Drive's address: each request below makes a new client.
	d.Restart(t, map[string]string{harness.TestClockShift: (29 * 24 * time.Hour).String()})
	if got := deletedSharedFolders(t, d.As(bence)); len(got) != 1 || got[0].ID != old.ID || got[0].Size != 4050 {
		t.Fatalf("after 29 days the trash lists %+v", got)
	}

	d.Restart(t, map[string]string{harness.TestClockShift: (31 * 24 * time.Hour).String()})
	harness.Eventually(t, "Old is removed from the trash for good", func() bool {
		_, ok := d.Storage.List(t, ".superit-drive/trash/Shared folders/Old")
		return !ok
	})
	if got := deletedSharedFolders(t, d.As(bence)); len(got) != 0 {
		t.Fatalf("after 31 days the trash lists %+v", got)
	}
	if res := restoreSharedFolder(t, d.As(bence), old.ID); res.Status != http.StatusNotFound {
		t.Fatalf("restoring Old after 31 days: got %d %s, want 404", res.Status, res.Body)
	}
	if res := d.As(bence).Get(t, downloadPath(old.ID, file.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("a file of Old after 31 days: got %d %s, want 404", res.Status, res.Body)
	}
	if used := usage(t, d).Used; *used != 0 {
		t.Fatalf("used %d, want 0 once Old is gone for good", *used)
	}
	if _, sees := seesSharedFolder(t, d, bence, newer.ID); !sees {
		t.Fatal("the newer Old went with the old one")
	}
	if got := onStorage(t, d, "Shared folders"); !slices.Equal(got, []string{"Old (2)/"}) {
		t.Fatalf("Shared folders on the storage: %q", got)
	}
}

// Deleting a shared folder waits for an upload into it to end, and the upload lands in the trash with it: nothing
// makes the shared folder's top folder again afterwards.
func TestDeletingASharedFolderWaitsForAnUploadIntoIt(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b := d.As(bence)
	clients := madeSharedFolder(t, b, "Clients")

	body, write := io.Pipe()
	uploadDone := slowUpload(d, bence, clients.ID, "Big.bin", body, 6)
	if _, err := write.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // the upload is on its way to the storage
	deleteDone := make(chan harness.Response, 1)
	go func() { deleteDone <- deleteSharedFolder(t, b, clients.ID) }()
	select {
	case res := <-deleteDone:
		t.Fatalf("the shared folder was deleted while an upload into it was under way: %d %s", res.Status, res.Body)
	case <-time.After(time.Second):
	}
	_, _ = write.Write([]byte("def"))
	_ = write.Close()
	if status := <-uploadDone; status != http.StatusCreated {
		t.Fatalf("the upload: got %d", status)
	}
	if res := <-deleteDone; res.Status != http.StatusOK {
		t.Fatalf("deleting after the upload: got %d %s", res.Status, res.Body)
	}
	if _, ok := d.Storage.List(t, "Shared folders/Clients"); ok {
		t.Fatal("the shared folder's top folder is on the storage after it was deleted")
	}
	entries := onStorage(t, d, ".superit-drive/trash/Shared folders/Clients")
	if len(entries) != 1 {
		t.Fatalf("the shared folder's trash holds %q", entries)
	}
	if got, _ := d.Storage.Get(t, ".superit-drive/trash/Shared folders/Clients/"+entries[0]+"Clients/Big.bin"); string(got) != "abcdef" {
		t.Fatalf("the upload in the trash holds %q", got)
	}
	// An upload that starts now finds no such place, and makes nothing.
	if res := upload(t, b, clients.ID, "Late.txt", []byte("late")); res.Status != http.StatusNotFound {
		t.Fatalf("an upload into the deleted shared folder: got %d %s", res.Status, res.Body)
	}
	if _, ok := d.Storage.List(t, "Shared folders/Clients"); ok {
		t.Fatal("a late upload made the shared folder's top folder again")
	}
}

// Every member may see who the members are, to choose whom to give access.
func TestTheMemberList(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	res := d.As(dora).Get(t, "/api/members")
	var l struct {
		Members []struct {
			UserID      string `json:"userId"`
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
			Role        string `json:"role"`
		} `json:"members"`
	}
	res.JSON(t, &l)
	var got []string
	for _, m := range l.Members {
		got = append(got, m.UserID+" "+m.Email+" "+m.DisplayName+" "+m.Role)
	}
	want := []string{
		bence.UserID + " bence@example.com Bence Tóth member",
		dora.UserID + " dora@example.com Dóra Nagy member",
		reka.UserID + " reka@example.com Réka Kovács admin",
	}
	if res.Status != http.StatusOK || !slices.Equal(got, want) {
		t.Fatalf("the member list: %d %q, want %q", res.Status, got, want)
	}
}
