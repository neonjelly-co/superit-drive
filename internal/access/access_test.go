package access

import (
	"fmt"
	"slices"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Everyone the table tries. "member" is Réka, a member without the admin role; "admin" holds it. The other two are not
// members: someone the member list does not hold (as a former member, or a stranger with a forged id, would be) and a
// request without a user id.
var people = map[string]Person{
	"member":                         {UserID: "u-reka", Role: members.RoleMember},
	"admin":                          {UserID: "u-admin", Role: members.RoleAdmin},
	"someone not in the member list": {UserID: "u-stranger"},
	"no user id":                     {},
}

// The same tree is in every place the table tries:
//
//	top folder
//	├── Clients (10)
//	│   └── Bakery (11)
//	│       └── 2026 (12)
//	│           ├── Offer.pdf (13)
//	│           └── Ledger.xlsx (14)
//	└── Notes.txt (15)
//
// Offer.pdf is three levels below Clients. Ledger.xlsx is its sibling, 2026 its parent, Notes.txt outside Clients.
var (
	clients = []int64{10}
	bakery  = []int64{10, 11}
	y2026   = []int64{10, 11, 12}
	offer   = []int64{10, 11, 12, 13}
	ledger  = []int64{10, 11, 12, 14}
	notes   = []int64{15}
)

const (
	onTop     = 0
	onClients = 10
	onBakery  = 11
	onOffer   = 13
)

// to is a grant to a member; toEveryone one to everyone in the project.
func to(member string, on int64, role Role) Grant { return Grant{On: on, Member: member, Role: role} }
func toEveryone(on int64, role Role) Grant        { return Grant{On: on, Everyone: true, Role: role} }

func grants(g ...Grant) []Grant { return g }

// Every place the table tries, with the item in it the request is about (Path, empty for its top folder) and the
// access given there. Each one is tried by every person with every action.
var places = map[string]*Place{
	"no place": nil,

	// Réka's own My files: she owns everything in it, whatever was given to whom.
	"Réka's My files":                                    {Kind: MyFiles, Owner: "u-reka"},
	"Réka's My files › Offer.pdf":                        {Kind: MyFiles, Owner: "u-reka", Path: offer},
	"Réka's My files › Offer.pdf, shared with the admin": {Kind: MyFiles, Owner: "u-reka", Path: offer, Grants: grants(to("u-admin", onOffer, Viewer))},
	"Réka's My files, deleted":                           {Kind: MyFiles, Owner: "u-reka", Deleted: true},
	"Réka's My files › Offer.pdf, deleted":               {Kind: MyFiles, Owner: "u-reka", Path: offer, Deleted: true},
	"Réka's My files › Offer.pdf, her account gone":      {Kind: MyFiles, Owner: "u-reka", Path: offer, OwnerLeft: true},
	// In the trash: she restores what anyone deleted from her My files.
	"Réka's My files › Offer.pdf in the trash, deleted by the admin": {Kind: MyFiles, Owner: "u-reka", Path: offer, DeletedBy: "u-admin",
		Grants: grants(to("u-admin", onClients, Editor))},

	// Bence's My files: closed to everyone else, project admins included, except what is shared with them.
	"Bence's My files":             {Kind: MyFiles, Owner: "u-bence"},
	"Bence's My files › Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer},
	// Nothing given on My files as a whole counts: only items in it are shared.
	"Bence's My files, Réka and the admin given editor on its top folder": {Kind: MyFiles, Owner: "u-bence",
		Grants: grants(to("u-reka", onTop, Editor), to("u-admin", onTop, Editor))},
	"Bence's My files › Offer.pdf, Réka and the admin given manager on its top folder": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onTop, Manager), to("u-admin", onTop, Manager))},
	"Bence's My files › Offer.pdf, everyone given viewer on its top folder": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toEveryone(onTop, Viewer))},
	// A role on a folder reaches everything in it: Offer.pdf is three levels below Clients.
	"Bence's My files › Offer.pdf, Réka given viewer on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, Viewer))},
	"Bence's My files › Offer.pdf, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, Editor))},
	"Bence's My files › Clients, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: clients,
		Grants: grants(to("u-reka", onClients, Editor))},
	// In the trash, given part of the place: only what she deleted herself, and only back into a folder she can add to.
	"Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		DeletedBy: "u-reka", Grants: grants(to("u-reka", onClients, Editor))},
	"Bence's My files › Offer.pdf in the trash, deleted by Bence, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		DeletedBy: "u-bence", Grants: grants(to("u-reka", onClients, Editor))},
	"Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		DeletedBy: "u-reka", Grants: grants(to("u-reka", onOffer, Editor))},
	"Bence's My files › 2026, Réka given viewer on Clients": {Kind: MyFiles, Owner: "u-bence", Path: y2026,
		Grants: grants(to("u-reka", onClients, Viewer))},
	// Deeper levels only add: a higher role lower down adds to a lower one above; a lower one takes nothing away.
	"Bence's My files › Offer.pdf, Réka given viewer on Clients and editor on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, Viewer), to("u-reka", onOffer, Editor))},
	"Bence's My files › Offer.pdf, Réka given editor on Clients and viewer on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, Editor), to("u-reka", onOffer, Viewer))},
	// A role on one item reaches neither its sibling, nor its parent, nor the top folder, nor anything outside it.
	"Bence's My files › Ledger.xlsx, Réka given editor on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: ledger,
		Grants: grants(to("u-reka", onOffer, Editor))},
	"Bence's My files › 2026, Réka given editor on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: y2026,
		Grants: grants(to("u-reka", onOffer, Editor))},
	"Bence's My files, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence",
		Grants: grants(to("u-reka", onClients, Editor))},
	"Bence's My files › Notes.txt, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: notes,
		Grants: grants(to("u-reka", onClients, Editor))},
	// Everyone in the project is every member, admins included, and nobody else.
	"Bence's My files › Offer.pdf, everyone given viewer on Bakery": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toEveryone(onBakery, Viewer))},
	"Bence's My files › Offer.pdf, everyone given editor on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toEveryone(onOffer, Editor))},
	"Bence's My files › Offer.pdf, everyone given viewer on Clients and Réka editor on Bakery": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toEveryone(onClients, Viewer), to("u-reka", onBakery, Editor))},
	// What was given to someone else is theirs; what was given to someone not in the member list helps nobody.
	"Bence's My files › Offer.pdf, Bence given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-bence", onClients, Editor))},
	"Bence's My files › Offer.pdf, the admin given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-admin", onClients, Editor))},
	"Bence's My files › Offer.pdf, the stranger given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-stranger", onClients, Editor))},
	// Grants that count for nothing: a manager on an item, a role nobody knows, one to a member and everyone at once.
	"Bence's My files › Offer.pdf, Réka given manager on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, Manager))},
	"Bence's My files › Offer.pdf, Réka given an unknown role on Clients": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onClients, "owner"))},
	"Bence's My files › Offer.pdf, editor on Clients to Réka and everyone at once": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(Grant{On: onClients, Member: "u-reka", Everyone: true, Role: Editor})},
	// Bence has left the project: his My files is held, and what he shared from it opens nothing.
	"Bence's My files › Offer.pdf, Réka given editor on Clients, Bence gone": {Kind: MyFiles, Owner: "u-bence", Path: offer, OwnerLeft: true,
		Grants: grants(to("u-reka", onClients, Editor), toEveryone(onClients, Viewer))},
	"My files without an owner › Offer.pdf, everyone given editor on Clients": {Kind: MyFiles, Path: offer,
		Grants: grants(toEveryone(onClients, Editor))},

	// A shared folder as a whole: a role on it, or none.
	"a shared folder, no role":                {Kind: SharedFolder},
	"a shared folder, given viewer":           {Kind: SharedFolder, Grants: grants(to("u-reka", onTop, Viewer))},
	"a shared folder, given editor":           {Kind: SharedFolder, Grants: grants(to("u-reka", onTop, Editor))},
	"a shared folder, given manager":          {Kind: SharedFolder, Grants: grants(to("u-reka", onTop, Manager))},
	"a shared folder, an unknown role":        {Kind: SharedFolder, Grants: grants(to("u-reka", onTop, "owner"))},
	"a shared folder, Bence given manager":    {Kind: SharedFolder, Grants: grants(to("u-bence", onTop, Manager))},
	"a shared folder, everyone given viewer":  {Kind: SharedFolder, Grants: grants(toEveryone(onTop, Viewer))},
	"a shared folder, everyone given editor":  {Kind: SharedFolder, Grants: grants(toEveryone(onTop, Editor))},
	"a shared folder, everyone given manager": {Kind: SharedFolder, Grants: grants(toEveryone(onTop, Manager))},
	"a shared folder, given editor on Bakery": {Kind: SharedFolder, Grants: grants(to("u-reka", onBakery, Editor))},
	// A shared folder belongs to the project: who made it, or any Owner, counts for nothing.
	"a shared folder owned by Réka, no role": {Kind: SharedFolder, Owner: "u-reka"},
	"a shared folder owned by the admin":     {Kind: SharedFolder, Owner: "u-admin"},
	// Items in a shared folder.
	"a shared folder › Offer.pdf, no role": {Kind: SharedFolder, Path: offer},
	"a shared folder › Offer.pdf, given viewer on it as a whole": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onTop, Viewer))},
	"a shared folder › Offer.pdf, given editor on it as a whole": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onTop, Editor))},
	"a shared folder › Offer.pdf, given manager on it as a whole": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onTop, Manager))},
	"a shared folder › Offer.pdf, given viewer on it as a whole and editor on Bakery": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onTop, Viewer), to("u-reka", onBakery, Editor))},
	"a shared folder › Offer.pdf, given editor on it as a whole and viewer on Offer.pdf": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onTop, Editor), to("u-reka", onOffer, Viewer))},
	"a shared folder › Offer.pdf, given editor on Bakery": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onBakery, Editor))},
	"a shared folder › Bakery, given editor on Bakery": {Kind: SharedFolder, Path: bakery,
		Grants: grants(to("u-reka", onBakery, Editor))},
	"a shared folder › Clients, given editor on Bakery": {Kind: SharedFolder, Path: clients,
		Grants: grants(to("u-reka", onBakery, Editor))},
	"a shared folder › Ledger.xlsx, given editor on Offer.pdf": {Kind: SharedFolder, Path: ledger,
		Grants: grants(to("u-reka", onOffer, Editor))},
	"a shared folder › Offer.pdf, given manager on Bakery": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onBakery, Manager))},
	"a shared folder › Offer.pdf, everyone given viewer on Clients": {Kind: SharedFolder, Path: offer,
		Grants: grants(toEveryone(onClients, Viewer))},
	"a shared folder › Offer.pdf, everyone given viewer on it as a whole": {Kind: SharedFolder, Path: offer,
		Grants: grants(toEveryone(onTop, Viewer))},
	"a shared folder › Offer.pdf in the trash, deleted by Bence, given editor on it as a whole": {Kind: SharedFolder, Path: offer,
		DeletedBy: "u-bence", Grants: grants(to("u-reka", onTop, Editor))},
	"a shared folder › Offer.pdf in the trash, deleted by Réka, given editor on Bakery": {Kind: SharedFolder, Path: offer,
		DeletedBy: "u-reka", Grants: grants(to("u-reka", onBakery, Editor))},

	// A shared folder in the trash: only restoring it, as a whole.
	"a deleted shared folder, no role":       {Kind: SharedFolder, Deleted: true},
	"a deleted shared folder, given viewer":  {Kind: SharedFolder, Deleted: true, Grants: grants(to("u-reka", onTop, Viewer))},
	"a deleted shared folder, given editor":  {Kind: SharedFolder, Deleted: true, Grants: grants(to("u-reka", onTop, Editor))},
	"a deleted shared folder, given manager": {Kind: SharedFolder, Deleted: true, Grants: grants(to("u-reka", onTop, Manager))},
	"a deleted shared folder owned by Réka":  {Kind: SharedFolder, Deleted: true, Owner: "u-reka"},
	"a deleted shared folder › Offer.pdf, given manager": {Kind: SharedFolder, Deleted: true, Path: offer,
		Grants: grants(to("u-reka", onTop, Manager))},

	"a place of a kind access does not know": {Kind: "team-folder", Owner: "u-reka", Grants: grants(to("u-reka", onTop, Manager))},
	"a place of a kind access does not know › Offer.pdf": {Kind: "team-folder", Owner: "u-reka", Path: offer,
		Grants: grants(to("u-reka", onClients, Editor), toEveryone(onClients, Editor))},
	"a place of no kind, owned and given all": {Owner: "u-reka", Grants: grants(to("u-reka", onTop, Manager))},
}

// Every action there is. A test below fails when one is added to the package without being added here.
var everyAction = []Action{Enter, SeeUsage, MakeSharedFolder, List, Download, Upload, MakeFolder, Rename, Move, Delete,
	SeeTrash, Restore, SeeAccess, Share, ManageAccess, DeleteSharedFolder, RestoreSharedFolder}

// What the spec gives each role, written out here rather than taken from the package.
var (
	// In the top folder of their own My files: everything but showing or managing access, which is about items.
	ownerAtTop = []Action{List, Upload, MakeFolder, SeeTrash}
	// In a shared folder's top folder.
	viewerAtTop  = []Action{List, SeeAccess}
	editorAtTop  = append(slices.Clone(viewerAtTop), Upload, MakeFolder, SeeTrash)
	managerAtTop = append(slices.Clone(editorAtTop), ManageAccess, DeleteSharedFolder)
	// At an item, in any place. Owners of a My files and managers of a shared folder are editors of every item in it.
	viewerAtItem = []Action{List, Download, SeeAccess}
	// An editor of the item who cannot add to the folder it is in: inside it, and sharing it, but not changing that
	// folder by renaming it, taking it out, or putting it back.
	editorAtItem = append(slices.Clone(viewerAtItem), Upload, MakeFolder, Share)
	// An editor of the folder it is in, too, who cannot see the place's whole trash and did not delete it.
	editorInFolder = append(slices.Clone(editorAtItem), Rename, Move, Delete)
	// And who may restore it: they see the place's whole trash, or deleted it themselves.
	allAtItem = append(slices.Clone(editorInFolder), Restore)
)

// The table grows with every kind of person, place, role and action: add each new one to the lists above, and every
// combination is tried. Only the combinations in allowed may pass; everything else must be refused.
func TestDecide(t *testing.T) {
	allowed := map[string]bool{}
	allow := func(person, place string, actions ...Action) {
		if _, ok := places[place]; !ok {
			t.Fatalf("the table allows something in %q, which is not a place it tries", place)
		}
		for _, a := range actions {
			allowed[fmt.Sprintf("%s %s %s", person, a, place)] = true
		}
	}
	for _, p := range []string{"member", "admin"} {
		allow(p, "no place", Enter, SeeUsage, MakeSharedFolder)
	}

	// In her own My files Réka may do everything, whatever was given to whom. Nobody else may do anything there,
	// admins included, unless an item was shared with them.
	allow("member", "Réka's My files", ownerAtTop...)
	allow("member", "Réka's My files › Offer.pdf", allAtItem...)
	allow("member", "Réka's My files › Offer.pdf, shared with the admin", allAtItem...)
	allow("admin", "Réka's My files › Offer.pdf, shared with the admin", viewerAtItem...)
	allow("member", "Réka's My files › Offer.pdf in the trash, deleted by the admin", allAtItem...)
	allow("admin", "Réka's My files › Offer.pdf in the trash, deleted by the admin", allAtItem...)

	// What is shared with Réka from Bence's My files, through a folder above it or on the item itself. An editor of
	// the item alone works inside it and shares it; renaming, moving and deleting it also need its folder.
	allow("member", "Bence's My files › Offer.pdf, Réka given viewer on Clients", viewerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given editor on Clients", editorInFolder...)
	allow("member", "Bence's My files › Clients, Réka given editor on Clients", editorAtItem...)
	allow("member", "Bence's My files › 2026, Réka given viewer on Clients", viewerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given viewer on Clients and editor on Offer.pdf", editorAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given editor on Clients and viewer on Offer.pdf", editorInFolder...)
	allow("member", "Bence's My files › Offer.pdf, everyone given viewer on Clients and Réka editor on Bakery", editorInFolder...)
	allow("admin", "Bence's My files › Offer.pdf, everyone given viewer on Clients and Réka editor on Bakery", viewerAtItem...)
	allow("admin", "Bence's My files › Offer.pdf, the admin given editor on Clients", editorInFolder...)
	// Only what she deleted herself, and only back into a folder she may add to.
	allow("member", "Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Clients", allAtItem...)
	allow("member", "Bence's My files › Offer.pdf in the trash, deleted by Bence, Réka given editor on Clients", editorInFolder...)
	allow("member", "Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Offer.pdf", editorAtItem...)
	// Everyone in the project: both members, the admin included; not the stranger, who is not a member.
	for _, p := range []string{"member", "admin"} {
		allow(p, "Bence's My files › Offer.pdf, everyone given viewer on Bakery", viewerAtItem...)
		allow(p, "Bence's My files › Offer.pdf, everyone given editor on Offer.pdf", editorAtItem...)
	}

	// A shared folder: what the role on it allows, at its top folder and at every item in it.
	allow("member", "a shared folder, given viewer", viewerAtTop...)
	allow("member", "a shared folder, given editor", editorAtTop...)
	allow("member", "a shared folder, given manager", managerAtTop...)
	allow("member", "a shared folder, everyone given viewer", viewerAtTop...)
	allow("member", "a shared folder, everyone given editor", editorAtTop...)
	allow("member", "a shared folder › Offer.pdf, given viewer on it as a whole", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf, given editor on it as a whole", allAtItem...)
	allow("member", "a shared folder › Offer.pdf, given manager on it as a whole", allAtItem...)
	allow("member", "a shared folder › Offer.pdf, given viewer on it as a whole and editor on Bakery", editorInFolder...)
	allow("member", "a shared folder › Offer.pdf, given editor on it as a whole and viewer on Offer.pdf", allAtItem...)
	allow("member", "a shared folder › Offer.pdf, given editor on Bakery", editorInFolder...)
	allow("member", "a shared folder › Bakery, given editor on Bakery", editorAtItem...)
	allow("member", "a shared folder › Offer.pdf, everyone given viewer on Clients", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf, everyone given viewer on it as a whole", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf in the trash, deleted by Bence, given editor on it as a whole", allAtItem...)
	allow("member", "a shared folder › Offer.pdf in the trash, deleted by Réka, given editor on Bakery", allAtItem...)
	// A project admin is a manager of every shared folder, whatever they were given, and so an editor of everything in
	// it, its trash included.
	for name, place := range places {
		if place != nil && place.Kind == SharedFolder && !place.Deleted {
			if len(place.Path) == 0 {
				allow("admin", name, managerAtTop...)
			} else {
				allow("admin", name, allAtItem...)
			}
		}
	}
	// A shared folder in the trash can only be restored, as a whole, by its managers and the project admins.
	allow("member", "a deleted shared folder, given manager", RestoreSharedFolder)
	for _, place := range []string{"a deleted shared folder, no role", "a deleted shared folder, given viewer",
		"a deleted shared folder, given editor", "a deleted shared folder, given manager", "a deleted shared folder owned by Réka"} {
		allow("admin", place, RestoreSharedFolder)
	}

	tried := 0
	for pn, p := range people {
		for ln, l := range places {
			for _, a := range everyAction {
				key := fmt.Sprintf("%s %s %s", pn, a, ln)
				if got := Decide(p, a, l); got != allowed[key] {
					t.Errorf("%s: got %v, want %v", key, got, allowed[key])
				}
				delete(allowed, key)
				tried++
			}
		}
	}
	for key := range allowed {
		t.Errorf("the table allows %q, which is not a combination the test tried", key)
	}
	if want := len(people) * len(places) * len(everyAction); tried != want {
		t.Fatalf("tried %d combinations, want %d", tried, want)
	}
}

// The role at an item is the one place the Drive works it out. These are the inheritance cases by name.
func TestRoleAt(t *testing.T) {
	member, admin, stranger := people["member"], people["admin"], people["someone not in the member list"]
	bences := func(path []int64, g ...Grant) *Place {
		return &Place{Kind: MyFiles, Owner: "u-bence", Path: path, Grants: g}
	}
	shared := func(path []int64, g ...Grant) *Place { return &Place{Kind: SharedFolder, Path: path, Grants: g} }
	cases := []struct {
		name   string
		person Person
		place  *Place
		want   Role
	}{
		{"a role on a folder reaches a file three levels down", member, bences(offer, to("u-reka", onClients, Viewer)), Viewer},
		{"and every folder on the way", member, bences(bakery, to("u-reka", onClients, Editor)), Editor},
		{"and the folder itself", member, bences(clients, to("u-reka", onClients, Editor)), Editor},
		{"a higher role lower down adds to a lower one above", member, bences(offer, to("u-reka", onClients, Viewer), to("u-reka", onOffer, Editor)), Editor},
		{"a lower role lower down takes nothing from a higher one above", member, bences(offer, to("u-reka", onClients, Editor), to("u-reka", onOffer, Viewer)), Editor},
		{"the order the grants come in does not matter", member, bences(offer, to("u-reka", onOffer, Viewer), to("u-reka", onClients, Editor)), Editor},
		{"a role on an item does not reach its sibling", member, bences(ledger, to("u-reka", onOffer, Editor)), NoRole},
		{"nor its parent", member, bences(y2026, to("u-reka", onOffer, Editor)), NoRole},
		{"nor the top folder", member, bences(nil, to("u-reka", onClients, Editor)), NoRole},
		{"nor anything outside the folder", member, bences(notes, to("u-reka", onClients, Editor)), NoRole},
		{"everyone reaches a member", member, bences(offer, toEveryone(onBakery, Viewer)), Viewer},
		{"everyone reaches a project admin", admin, bences(offer, toEveryone(onBakery, Viewer)), Viewer},
		{"everyone does not reach someone not in the member list", stranger, bences(offer, toEveryone(onBakery, Viewer)), NoRole},
		{"nor does a grant naming them", stranger, bences(offer, to("u-stranger", onBakery, Editor)), NoRole},
		{"everyone and a member's own role: the higher", member, bences(offer, toEveryone(onClients, Editor), to("u-reka", onOffer, Viewer)), Editor},
		{"someone else's role is theirs", member, bences(offer, to("u-bence", onClients, Editor)), NoRole},
		{"the owner owns everything in their My files", Person{UserID: "u-bence", Role: members.RoleMember}, bences(offer, to("u-bence", onOffer, Viewer)), Owner},
		{"a project admin has nothing in someone's My files", admin, bences(offer), NoRole},
		{"a My files as a whole is never shared", member, bences(offer, to("u-reka", onTop, Editor), toEveryone(onTop, Editor)), NoRole},
		{"nothing in a held My files", member, &Place{Kind: MyFiles, Owner: "u-bence", OwnerLeft: true, Path: offer, Grants: grants(to("u-reka", onOffer, Editor))}, NoRole},
		{"a manager on an item counts for nothing", member, bences(offer, to("u-reka", onClients, Manager)), NoRole},
		{"a shared folder as a whole reaches everything in it", member, shared(offer, to("u-reka", onTop, Viewer)), Viewer},
		{"a manager of a shared folder is one at every item in it", member, shared(offer, to("u-reka", onTop, Manager)), Manager},
		{"an editor lower down in a shared folder adds to a viewer above", member, shared(offer, to("u-reka", onTop, Viewer), to("u-reka", onBakery, Editor)), Editor},
		{"a project admin manages every shared folder", admin, shared(offer), Manager},
		{"everyone is never a manager", member, shared(nil, toEveryone(onTop, Manager)), NoRole},
		{"a role on an item of a shared folder does not reach its top", member, shared(nil, to("u-reka", onBakery, Editor)), NoRole},
		{"a place of a kind access does not know", member, &Place{Kind: "team-folder", Grants: grants(to("u-reka", onTop, Manager))}, NoRole},
		{"no place", member, nil, NoRole},
	}
	for _, c := range cases {
		if got := RoleAt(c.person, c.place); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToEveryone(t *testing.T) {
	cases := []struct {
		place *Place
		want  Role
	}{
		{&Place{Kind: MyFiles, Owner: "u-bence", Path: offer, Grants: grants(toEveryone(onClients, Viewer), to("u-reka", onOffer, Editor))}, Viewer},
		{&Place{Kind: MyFiles, Owner: "u-bence", Path: ledger, Grants: grants(toEveryone(onOffer, Editor))}, NoRole},
		{&Place{Kind: MyFiles, Owner: "u-bence", Path: offer, OwnerLeft: true, Grants: grants(toEveryone(onClients, Viewer))}, NoRole},
		{&Place{Kind: SharedFolder, Path: offer, Grants: grants(toEveryone(onTop, Editor), toEveryone(onOffer, Viewer))}, Editor},
		{&Place{Kind: SharedFolder, Grants: grants(toEveryone(onTop, Manager), to("u-reka", onTop, Manager))}, NoRole},
		{&Place{Kind: "team-folder", Grants: grants(toEveryone(onTop, Editor))}, NoRole},
		{nil, NoRole},
	}
	for i, c := range cases {
		if got := ToEveryone(c.place); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

// Grants that could be added to any place the table tries: every role that can be given, to Réka, the admin and
// everyone, on the top folder and on every item of every path.
func extraGrants() []Grant {
	var out []Grant
	for _, on := range []int64{onTop, 10, 11, 12, 13, 14, 15} {
		for _, role := range []Role{Viewer, Editor, Manager} {
			out = append(out, to("u-reka", on, role), to("u-admin", on, role), toEveryone(on, role))
		}
	}
	return out
}

// The rules have to hang together, for every person and place the table knows. These are the decisions about the
// trash, replacing, sharing and inheritance that the spec leaves to be read from "whoever could have deleted it may
// restore it", "an editor can share further" and "deeper levels can only add access".
func TestTheRulesHangTogether(t *testing.T) {
	for pn, p := range people {
		for ln, l := range places {
			may := func(a Action) bool { return Decide(p, a, l) }
			where := fmt.Sprintf("%s in %s", pn, ln)
			// Nobody acts on what they cannot see.
			for _, a := range []Action{Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore, SeeAccess, Share, ManageAccess, DeleteSharedFolder} {
				if may(a) && !may(List) {
					t.Errorf("%s may %s but not list", where, a)
				}
			}
			if l == nil || l.Deleted {
				continue
			}
			child := l.Below(99, nil) // something in it, with nothing given on it
			if len(l.Path) > 0 {
				folder := l.Up(len(l.Path) - 1)
				// An editor of the item works inside it and shares it further; a viewer does neither.
				for _, a := range []Action{MakeFolder, Share} {
					if may(a) != may(Upload) {
						t.Errorf("%s: may upload %v, but may %s %v: want both or neither", where, may(Upload), a, may(a))
					}
				}
				if may(Share) && !may(SeeAccess) {
					t.Errorf("%s may share but not see who has access", where)
				}
				// Renaming, moving and deleting change the folder it is in: whoever may put things there may, and nobody
				// else, so nobody learns through a clashing name what is in a folder they cannot open.
				for _, a := range []Action{Rename, Move, Delete} {
					if may(a) != Decide(p, Upload, folder) {
						t.Errorf("%s: may %s %v, may upload into its folder %v: want both or neither", where, a, may(a), Decide(p, Upload, folder))
					}
				}
				// Whoever may restore something could have deleted it from where it goes back to, and either sees the
				// place's whole trash or put it there themselves. (An upload can replace a file, which puts it in the
				// trash, so whoever may upload into a folder may delete what is in it.)
				if may(Restore) != (may(Delete) && (l.DeletedBy == p.UserID || Decide(p, SeeTrash, l.Up(0)))) {
					t.Errorf("%s: may restore %v, may delete %v, deleted it %v", where, may(Restore), may(Delete), l.DeletedBy == p.UserID)
				}
				if may(Upload) && !Decide(p, Delete, child) {
					t.Errorf("%s may upload into it but not delete what is in it", where)
				}
				for _, a := range []Action{SeeTrash, ManageAccess, DeleteSharedFolder, RestoreSharedFolder} {
					if may(a) {
						t.Errorf("%s may %s, which concerns a place as a whole, at an item", where, a)
					}
				}
			} else {
				// In a top folder: whoever may put something there may see the trash and restore what is deleted from it.
				if may(Upload) != may(SeeTrash) || may(Upload) != may(MakeFolder) {
					t.Errorf("%s: upload %v, make a folder %v, see the trash %v: want all or none", where, may(Upload), may(MakeFolder), may(SeeTrash))
				}
				if may(SeeTrash) && (!Decide(p, Delete, child) || !Decide(p, Restore, child)) {
					t.Errorf("%s may see the trash but not delete and restore what is in the top folder", where)
				}
				for _, a := range []Action{Download, Rename, Move, Delete, Restore, Share} {
					if may(a) {
						t.Errorf("%s may %s, which concerns an item, in a top folder", where, a)
					}
				}
				// Whoever decides who has access to a shared folder, or may delete it, may do everything else in it.
				if may(ManageAccess) || may(DeleteSharedFolder) {
					for _, a := range managerAtTop {
						if !may(a) {
							t.Errorf("%s manages it but may not %s", where, a)
						}
					}
				}
				// Whoever could delete a shared folder may restore it from the trash.
				if l.Kind == SharedFolder {
					deleted := *l
					deleted.Deleted = true
					if may(DeleteSharedFolder) != Decide(p, RestoreSharedFolder, &deleted) {
						t.Errorf("%s: may delete it %v, may restore it %v", where, may(DeleteSharedFolder), Decide(p, RestoreSharedFolder, &deleted))
					}
				}
			}
			// Access on a folder applies to everything inside it: what the person may do at the top folder or at a folder
			// they may also do at anything in it. (Restoring depends on who deleted each item.)
			for _, a := range []Action{List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeAccess, Share} {
				if len(l.Path) == 0 && (a == Download || a == Rename || a == Move || a == Delete || a == Share) {
					continue
				}
				if may(a) && !Decide(p, a, child) {
					t.Errorf("%s may %s, but not at something inside it", where, a)
				}
			}
			// Giving more never takes anything away, wherever it is given; giving it on an item off the path changes
			// nothing at all.
			for _, g := range extraGrants() {
				more := *l
				more.Grants = append(slices.Clone(l.Grants), g)
				off := *l
				off.Grants = append(slices.Clone(l.Grants), Grant{On: 98, Member: g.Member, Everyone: g.Everyone, Role: g.Role})
				for _, a := range everyAction {
					if may(a) && !Decide(p, a, &more) {
						t.Errorf("%s: giving %+v takes %s away", where, g, a)
					}
					if may(a) != Decide(p, a, &off) {
						t.Errorf("%s: giving %+v on an item off the path changes %s", where, g, a)
					}
				}
			}
			// Nobody but the owner may do anything in a My files, unless something on the way to the item was shared
			// with them or with everyone.
			if l.Kind == MyFiles && l.Owner != p.UserID {
				shared := false
				for _, g := range l.Grants {
					shared = shared || (g.On != 0 && (g.Everyone || g.Member == p.UserID) && slices.Contains(l.Path, g.On))
				}
				for _, a := range everyAction {
					if may(a) && !shared {
						t.Errorf("%s may %s in someone else's My files, where nothing was shared with them", where, a)
					}
				}
			}
		}
	}
}

func TestCounts(t *testing.T) {
	cases := []struct {
		kind PlaceKind
		g    Grant
		want bool
	}{
		{MyFiles, to("u", onOffer, Viewer), true},
		{MyFiles, to("u", onOffer, Editor), true},
		{MyFiles, toEveryone(onOffer, Editor), true},
		{MyFiles, to("u", onOffer, Manager), false},
		{MyFiles, to("u", onTop, Viewer), false},
		{MyFiles, toEveryone(onTop, Viewer), false},
		{SharedFolder, to("u", onTop, Viewer), true},
		{SharedFolder, to("u", onTop, Editor), true},
		{SharedFolder, to("u", onTop, Manager), true},
		{SharedFolder, toEveryone(onTop, Editor), true},
		{SharedFolder, toEveryone(onTop, Manager), false},
		{SharedFolder, to("u", onBakery, Manager), false},
		{SharedFolder, to("u", onBakery, "owner"), false},
		{SharedFolder, to("u", onBakery, Owner), false},
		{SharedFolder, to("u", onBakery, NoRole), false},
		{SharedFolder, Grant{On: onBakery, Role: Viewer}, false},
		{SharedFolder, Grant{On: onBakery, Member: "u", Everyone: true, Role: Viewer}, false},
		{SharedFolder, to("u", -1, Viewer), false},
		{"team-folder", to("u", onBakery, Viewer), false},
	}
	for _, c := range cases {
		if got := Counts(c.kind, c.g); got != c.want {
			t.Errorf("Counts(%s, %+v) = %v, want %v", c.kind, c.g, got, c.want)
		}
	}
}

func TestBelowAndUp(t *testing.T) {
	p := &Place{Kind: SharedFolder, Path: bakery, Grants: grants(to("u-reka", onClients, Viewer))}
	b := p.Below(12, grants(to("u-reka", 12, Editor)))
	if !slices.Equal(b.Path, y2026) || len(b.Grants) != 2 || !slices.Equal(p.Path, bakery) || len(p.Grants) != 1 {
		t.Fatalf("Below: %+v from %+v", b, p)
	}
	b.Path[0] = 99 // a copy, not the original's path
	if p.Path[0] != 10 {
		t.Fatal("Below shares its path with the place it came from")
	}
	if u := p.Up(0); len(u.Path) != 0 || len(u.Grants) != 1 {
		t.Fatalf("Up(0): %+v", u)
	}
	if u := p.Up(1); !slices.Equal(u.Path, clients) {
		t.Fatalf("Up(1): %+v", u)
	}
	if u := p.Up(5); !slices.Equal(u.Path, bakery) {
		t.Fatalf("Up(5): %+v", u)
	}
}

func TestEveryActionIsInTheTable(t *testing.T) {
	for _, a := range InAPlace {
		if !slices.Contains(everyAction, a) {
			t.Errorf("%q is an action in a place that the table does not try", a)
		}
	}
	if len(everyAction) != len(InAPlace)+3 {
		t.Errorf("the table tries %d actions; the package has %d in a place and 3 in none", len(everyAction), len(InAPlace))
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	admin := Person{UserID: "u1", Role: members.RoleAdmin}
	for _, place := range []*Place{nil, {Kind: MyFiles, Owner: "u1"}, {Kind: MyFiles, Owner: "u1", Path: offer}, {Kind: SharedFolder, Grants: grants(to("u1", onTop, Manager))}} {
		if Decide(admin, Action("own"), place) {
			t.Fatalf("an action without a rule must be refused (in %+v)", place)
		}
	}
}
