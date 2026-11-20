package access

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Everyone the table tries. "member" is Réka, a member without the admin role; "admin" holds it. The next two are not
// members: someone the member list does not hold (as a former member, or a stranger with a forged id, would be) and a
// request without a user id. The rest are visitors, who came with a public link: on Clients or on Offer.pdf, made by
// Bence, by Réka, or by someone who has left the project since; a link of a kind nobody knows, one on no item; and Réka
// herself, signed in, opening her own link, who is a visitor there like anyone else.
var people = map[string]Person{
	"member":                         {UserID: "u-reka", Role: members.RoleMember},
	"admin":                          {UserID: "u-admin", Role: members.RoleAdmin},
	"someone not in the member list": {UserID: "u-stranger"},
	"no user id":                     {},

	"a visitor, link on Clients by Bence":                   {Link: link(onClients, ViewLink, bence)},
	"a visitor, link on Clients by Réka":                    {Link: link(onClients, ViewLink, reka)},
	"a visitor, link on Offer.pdf by Bence":                 {Link: link(onOffer, ViewLink, bence)},
	"a visitor, link on Clients by someone who left":        {Link: link(onClients, ViewLink, Person{UserID: "u-gone"})},
	"a visitor, link of an unknown kind on Clients":         {Link: link(onClients, "edit", bence)},
	"a visitor, link on no item":                            {Link: link(onTop, ViewLink, bence)},
	"Réka, signed in, with her own link on Clients":         {UserID: "u-reka", Role: members.RoleMember, Link: link(onClients, ViewLink, reka)},
	"the admin, signed in, with a link on Clients by Bence": {UserID: "u-admin", Role: members.RoleAdmin, Link: link(onClients, ViewLink, bence)},

	// Guests: Anna (guest 7), whom the places below invite, and another guest nobody invited there. A guest is a guest
	// whatever else the request says: with Réka's user id and role too, she is still only Anna; with a link, she is a
	// visitor like any other.
	"a guest":                     {Guest: anna},
	"another guest":               {Guest: &Guest{ID: 8}},
	"a guest with Réka's user id": {UserID: "u-reka", Role: members.RoleMember, Guest: anna},
	"a guest with a link on Clients by Bence": {Guest: anna, Link: link(onClients, ViewLink, bence)},
}

// anna is the guest the places invite.
var anna = &Guest{ID: 7}

// now is the time of the decisions about grants with an end date.
var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// toGuest is an invitation to Anna, given by by; until gives it an end date.
func toGuest(on int64, role Role, by Person) Grant {
	return Grant{On: on, Guest: anna.ID, Role: role, By: by}
}
func until(g Grant, ends time.Time) Grant { g.Ends = ends; return g }

var gone = Person{UserID: "u-gone"} // a member who has left the project

// The makers of the links the visitors came with, as the member list has them.
var (
	reka  = Person{UserID: "u-reka", Role: members.RoleMember}
	bence = Person{UserID: "u-bence", Role: members.RoleMember}
)

func link(on int64, kind LinkKind, by Person) *Link { return &Link{Item: on, Kind: kind, By: by} }

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
	// One file shared on its own.
	"Bence's My files › Offer.pdf, Réka given viewer on Offer.pdf": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(to("u-reka", onOffer, Viewer))},
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
	// A held My files as a whole: a project admin hands it over or deletes it, at its top folder, and opens nothing in it.
	"Bence's My files, Bence gone":                     {Kind: MyFiles, Owner: "u-bence", OwnerLeft: true},
	"Bence's My files, Bence gone, no sharing outside": {Kind: MyFiles, Owner: "u-bence", OwnerLeft: true, NoSharingOutside: true},
	"Réka's My files, her account gone":                {Kind: MyFiles, Owner: "u-reka", OwnerLeft: true},
	"Bence's My files, Bence gone, in the trash":       {Kind: MyFiles, Owner: "u-bence", OwnerLeft: true, Deleted: true},
	"My files without an owner, gone":                  {Kind: MyFiles, OwnerLeft: true},
	"Bence's My files, Bence gone, the admin given editor on its top folder and everyone viewer": {Kind: MyFiles, Owner: "u-bence",
		OwnerLeft: true, Grants: grants(to("u-admin", onTop, Editor), toEveryone(onTop, Viewer))},
	"Bence's My files › Clients, Bence gone, the admin given editor on Clients": {Kind: MyFiles, Owner: "u-bence", Path: clients,
		OwnerLeft: true, Grants: grants(to("u-admin", onClients, Editor))},
	"Bence's My files › Offer.pdf in the trash, deleted by the admin, Bence gone": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		OwnerLeft: true, DeletedBy: "u-admin"},
	// Who made a shared folder, or any Owner and OwnerLeft, counts for nothing there: nothing of it is held.
	"a shared folder owned by Bence, Bence gone": {Kind: SharedFolder, Owner: "u-bence", OwnerLeft: true},

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
	"a shared folder › Offer.pdf, given viewer on Offer.pdf": {Kind: SharedFolder, Path: offer,
		Grants: grants(to("u-reka", onOffer, Viewer))},
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

	// Where sharing outside the project is switched off (for the project, or for the shared folder): members work as
	// before, and may still see and remove a link, but not make one; and no link opens.
	"Réka's My files › Offer.pdf, no sharing outside": {Kind: MyFiles, Owner: "u-reka", Path: offer, NoSharingOutside: true},
	"Bence's My files › Offer.pdf, Réka given editor on Clients, no sharing outside": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		NoSharingOutside: true, Grants: grants(to("u-reka", onClients, Editor))},
	"a shared folder › Offer.pdf, given editor on it as a whole, no sharing outside": {Kind: SharedFolder, Path: offer,
		NoSharingOutside: true, Grants: grants(to("u-reka", onTop, Editor))},
	"a shared folder › Clients, no role, no sharing outside": {Kind: SharedFolder, Path: clients, NoSharingOutside: true},
	// The switch itself: its managers turn it back on where it is off; a My files has none of its own.
	"a shared folder, given manager, no sharing outside": {Kind: SharedFolder, NoSharingOutside: true, Grants: grants(to("u-reka", onTop, Manager))},
	"a shared folder, given editor, no sharing outside":  {Kind: SharedFolder, NoSharingOutside: true, Grants: grants(to("u-reka", onTop, Editor))},
	"Réka's My files, no sharing outside":                {Kind: MyFiles, Owner: "u-reka", NoSharingOutside: true},
	"Bence's My files, no sharing outside, the admin given editor on Clients": {Kind: MyFiles, Owner: "u-bence", NoSharingOutside: true,
		Grants: grants(to("u-admin", onClients, Editor))},
	"a deleted shared folder, given manager, no sharing outside": {Kind: SharedFolder, Deleted: true, NoSharingOutside: true,
		Grants: grants(to("u-reka", onTop, Manager))},

	// Guests. An invitation reaches everything inside what it is on, and nothing above it or beside it; an editor
	// renames, moves and deletes only what is inside it.
	"Bence's My files › Offer.pdf, the guest given viewer on Clients by Bence": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toGuest(onClients, Viewer, bence))},
	"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toGuest(onClients, Editor, bence))},
	"Bence's My files › 2026, the guest given editor on Clients by Bence": {Kind: MyFiles, Owner: "u-bence", Path: y2026,
		Grants: grants(toGuest(onClients, Editor, bence))},
	"Bence's My files › Clients, the guest given editor on Clients by Bence": {Kind: MyFiles, Owner: "u-bence", Path: clients,
		Grants: grants(toGuest(onClients, Editor, bence))},
	"Bence's My files › Offer.pdf, the guest given viewer on Offer.pdf by Bence": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toGuest(onOffer, Viewer, bence))},
	"Bence's My files › Offer.pdf, the guest given viewer on Clients and editor on Offer.pdf by Bence": {Kind: MyFiles, Owner: "u-bence",
		Path: offer, Grants: grants(toGuest(onClients, Viewer, bence), toGuest(onOffer, Editor, bence))},
	"Bence's My files › Ledger.xlsx, the guest given editor on Offer.pdf by Bence": {Kind: MyFiles, Owner: "u-bence", Path: ledger,
		Grants: grants(toGuest(onOffer, Editor, bence))},
	"Bence's My files › 2026, the guest given editor on Offer.pdf by Bence": {Kind: MyFiles, Owner: "u-bence", Path: y2026,
		Grants: grants(toGuest(onOffer, Editor, bence))},
	"Bence's My files, the guest given editor on Clients by Bence": {Kind: MyFiles, Owner: "u-bence",
		Grants: grants(toGuest(onClients, Editor, bence))},
	// In a My files an invitation is its giver's share: it counts while they may share its item, as a link.
	"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given editor on Clients": {Kind: MyFiles, Owner: "u-bence",
		Path: offer, Grants: grants(to("u-reka", onClients, Editor), toGuest(onBakery, Editor, reka))},
	"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given viewer on Clients": {Kind: MyFiles, Owner: "u-bence",
		Path: offer, Grants: grants(to("u-reka", onClients, Viewer), toGuest(onBakery, Editor, reka))},
	"Bence's My files › Offer.pdf, the guest given editor on Clients by someone who left": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Grants: grants(toGuest(onClients, Editor, gone))},
	"Bence's My files › Offer.pdf, the guest given viewer on Clients by Bence, Bence gone": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		OwnerLeft: true, Grants: grants(toGuest(onClients, Viewer, bence))},
	// An end date: before it, it counts; from it on, or when the time is not known, it does not.
	"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence until tomorrow": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Now: now, Grants: grants(until(toGuest(onClients, Editor, bence), now.Add(24*time.Hour)))},
	"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence until now": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Now: now, Grants: grants(until(toGuest(onClients, Editor, bence), now))},
	"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence until tomorrow, no time": {Kind: MyFiles, Owner: "u-bence",
		Path: offer, Grants: grants(until(toGuest(onClients, Editor, bence), now.Add(24*time.Hour)))},
	"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence, no sharing outside": {Kind: MyFiles, Owner: "u-bence",
		Path: offer, NoSharingOutside: true, Grants: grants(toGuest(onClients, Editor, bence))},
	"Réka's My files › Offer.pdf, the guest given editor on Clients by Réka": {Kind: MyFiles, Owner: "u-reka", Path: offer,
		Grants: grants(toGuest(onClients, Editor, reka))},
	// In a shared folder an invitation is the project's: it counts whoever gave it.
	"a shared folder › Offer.pdf, the guest given editor on Bakery by someone who left": {Kind: SharedFolder, Path: offer,
		Grants: grants(toGuest(onBakery, Editor, gone))},
	"a shared folder › Offer.pdf, the guest given viewer on Clients by Bence": {Kind: SharedFolder, Path: offer,
		Grants: grants(toGuest(onClients, Viewer, bence))},
	"a shared folder › Bakery, the guest given editor on Bakery by Bence": {Kind: SharedFolder, Path: bakery,
		Grants: grants(toGuest(onBakery, Editor, bence))},
	"a deleted shared folder › Offer.pdf, the guest given editor on Clients by Bence": {Kind: SharedFolder, Deleted: true, Path: offer,
		Grants: grants(toGuest(onClients, Editor, bence))},
	// Invitations that count for nothing: on a place as a whole, as manager, to a guest and a member at once; and a guest
	// is never "everyone in the project".
	"a shared folder, the guest given editor on it as a whole": {Kind: SharedFolder,
		Grants: grants(Grant{On: onTop, Guest: anna.ID, Role: Editor, By: bence})},
	"a shared folder › Offer.pdf, the guest given editor on it as a whole": {Kind: SharedFolder, Path: offer,
		Grants: grants(Grant{On: onTop, Guest: anna.ID, Role: Editor, By: bence})},
	"a shared folder › Offer.pdf, the guest given manager on Clients": {Kind: SharedFolder, Path: offer,
		Grants: grants(toGuest(onClients, Manager, bence))},
	"a shared folder › Offer.pdf, editor on Clients to the guest and Réka at once": {Kind: SharedFolder, Path: offer,
		Grants: grants(Grant{On: onClients, Guest: anna.ID, Member: "u-reka", Role: Editor, By: bence})},
	"a shared folder › Offer.pdf, editor on Clients to the guest and everyone at once": {Kind: SharedFolder, Path: offer,
		Grants: grants(Grant{On: onClients, Guest: anna.ID, Everyone: true, Role: Editor, By: bence})},
	"a place of a kind access does not know › Offer.pdf, the guest given editor on Clients": {Kind: "team-folder", Owner: "u-reka",
		Path: offer, Grants: grants(toGuest(onClients, Editor, reka))},

	// A guest who joined the project keeps their invitation as a member, with its end date.
	"Bence's My files › Offer.pdf, Réka given editor on Clients until tomorrow": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Now: now, Grants: grants(until(to("u-reka", onClients, Editor), now.Add(24*time.Hour)))},
	"Bence's My files › Offer.pdf, Réka given editor on Clients until an hour ago": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Now: now, Grants: grants(until(to("u-reka", onClients, Editor), now.Add(-time.Hour)))},
	// Nothing to everyone ends: such a grant counts for nothing.
	"Bence's My files › Offer.pdf, everyone given viewer on Clients until tomorrow": {Kind: MyFiles, Owner: "u-bence", Path: offer,
		Now: now, Grants: grants(until(toEveryone(onClients, Viewer), now.Add(24*time.Hour)))},
}

// Every action there is. A test below fails when one is added to the package without being added here.
var everyAction = []Action{Enter, SeeUsage, MakeSharedFolder, Oversee, List, Download, Upload, MakeFolder, Rename, Move,
	MoveOut, MoveIn, Delete, SeeTrash, Restore, SeeAccess, Share, MakeLink, ManageLink, InviteGuest, ManageAccess,
	SwitchSharingOutside, DeleteSharedFolder, RestoreSharedFolder, HandOverHeld, DeleteHeld, SeeThroughLink,
	DownloadThroughLink, SignInAsGuest, EnterAsGuest, ListAsGuest, DownloadAsGuest, UploadAsGuest, MakeFolderAsGuest,
	RenameAsGuest, MoveAsGuest, DeleteAsGuest}

// What the spec gives each role, written out here rather than taken from the package.
var (
	// In the top folder of their own My files: everything but showing or managing access, which is about items.
	// Downloading the top folder is the whole place as one zip.
	ownerAtTop = []Action{List, Download, Upload, MakeFolder, SeeTrash}
	// In a shared folder's top folder. Whoever may upload there may also move something in from their own My files.
	viewerAtTop = []Action{List, Download, SeeAccess}
	editorAtTop = append(slices.Clone(viewerAtTop), Upload, MakeFolder, SeeTrash, MoveIn)
	// Its managers also switch sharing outside the project off for it, or back on.
	managerAtTop = append(slices.Clone(editorAtTop), ManageAccess, SwitchSharingOutside, DeleteSharedFolder)
	// At an item, in any place. Owners of a My files and managers of a shared folder are editors of every item in it.
	viewerAtItem = []Action{List, Download, SeeAccess}
	// An editor of the item who cannot add to the folder it is in: inside it, and sharing it (with members, with guests
	// and with anyone through a public link, which they also see and remove), but not changing that folder by renaming
	// it, taking it out, or putting it back.
	editorAtItem = append(slices.Clone(viewerAtItem), Upload, MakeFolder, Share, MakeLink, ManageLink, InviteGuest)
	// An editor of the folder it is in, too, who cannot see the place's whole trash and did not delete it.
	editorInFolder = append(slices.Clone(editorAtItem), Rename, Move, Delete)
	// And who may restore it: they see the place's whole trash, or deleted it themselves.
	allAtItem = append(slices.Clone(editorInFolder), Restore)
	// The owner of a My files may also move what is in it out, into a shared folder, where it becomes the project's.
	ownerAtItem = append(slices.Clone(allAtItem), MoveOut)
	// In a shared folder, whoever may upload into a folder may also move something into it from their own My files.
	sharedEditorAtItem   = append(slices.Clone(editorAtItem), MoveIn)
	sharedEditorInFolder = append(slices.Clone(editorInFolder), MoveIn)
	sharedAllAtItem      = append(slices.Clone(allAtItem), MoveIn)
	// What a visitor may do through a link that counts where they are: see and download, nothing else.
	throughALink = []Action{SeeThroughLink, DownloadThroughLink}
	// What a guest may do where an invitation to them counts: a viewer sees and downloads; an editor also uploads and
	// makes folders; and inside what they were given, also renames, moves and deletes.
	guestViewer         = []Action{ListAsGuest, DownloadAsGuest}
	guestEditorAtItem   = append(slices.Clone(guestViewer), UploadAsGuest, MakeFolderAsGuest)
	guestEditorInFolder = append(slices.Clone(guestEditorAtItem), RenameAsGuest, MoveAsGuest, DeleteAsGuest)
	// Sharing outside the project: what is not done where it is switched off.
	outside = []Action{MakeLink, InviteGuest}
)

// without is actions without the ones given.
func without(actions []Action, not ...Action) []Action {
	return slices.DeleteFunc(slices.Clone(actions), func(a Action) bool { return slices.Contains(not, a) })
}

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
	// A project admin oversees what leaves the project and the space used, and switches sharing outside the project off
	// for the whole project, or back on.
	allow("admin", "no place", Oversee, SwitchSharingOutside)
	// Anyone who is not a member asks for a code to sign in as a guest; a guest uses their own pages.
	for _, p := range []string{"someone not in the member list", "no user id", "a guest", "another guest", "a guest with Réka's user id"} {
		allow(p, "no place", SignInAsGuest)
	}
	for _, p := range []string{"a guest", "another guest", "a guest with Réka's user id"} {
		allow(p, "no place", EnterAsGuest)
	}

	// In her own My files Réka may do everything, whatever was given to whom. Nobody else may do anything there,
	// admins included, unless an item was shared with them.
	allow("member", "Réka's My files", ownerAtTop...)
	allow("member", "Réka's My files › Offer.pdf", ownerAtItem...)
	allow("member", "Réka's My files › Offer.pdf, shared with the admin", ownerAtItem...)
	allow("admin", "Réka's My files › Offer.pdf, shared with the admin", viewerAtItem...)
	allow("member", "Réka's My files › Offer.pdf in the trash, deleted by the admin", ownerAtItem...)
	allow("admin", "Réka's My files › Offer.pdf in the trash, deleted by the admin", allAtItem...)

	// What is shared with Réka from Bence's My files, through a folder above it or on the item itself. An editor of
	// the item alone works inside it and shares it; renaming, moving and deleting it also need its folder.
	allow("member", "Bence's My files › Offer.pdf, Réka given viewer on Clients", viewerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given editor on Clients", editorInFolder...)
	allow("member", "Bence's My files › Clients, Réka given editor on Clients", editorAtItem...)
	allow("member", "Bence's My files › 2026, Réka given viewer on Clients", viewerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given viewer on Offer.pdf", viewerAtItem...)
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
	allow("member", "a shared folder › Offer.pdf, given editor on it as a whole", sharedAllAtItem...)
	allow("member", "a shared folder › Offer.pdf, given manager on it as a whole", sharedAllAtItem...)
	allow("member", "a shared folder › Offer.pdf, given viewer on it as a whole and editor on Bakery", sharedEditorInFolder...)
	allow("member", "a shared folder › Offer.pdf, given editor on it as a whole and viewer on Offer.pdf", sharedAllAtItem...)
	allow("member", "a shared folder › Offer.pdf, given editor on Bakery", sharedEditorInFolder...)
	allow("member", "a shared folder › Bakery, given editor on Bakery", sharedEditorAtItem...)
	allow("member", "a shared folder › Offer.pdf, given viewer on Offer.pdf", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf, everyone given viewer on Clients", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf, everyone given viewer on it as a whole", viewerAtItem...)
	allow("member", "a shared folder › Offer.pdf in the trash, deleted by Bence, given editor on it as a whole", sharedAllAtItem...)
	allow("member", "a shared folder › Offer.pdf in the trash, deleted by Réka, given editor on Bakery", sharedAllAtItem...)
	// A project admin is a manager of every shared folder, whatever they were given, and so an editor of everything in
	// it, its trash included.
	for name, place := range places {
		if place != nil && place.Kind == SharedFolder && !place.Deleted {
			if len(place.Path) == 0 {
				allow("admin", name, managerAtTop...)
			} else if place.NoSharingOutside {
				allow("admin", name, without(sharedAllAtItem, outside...)...)
			} else {
				allow("admin", name, sharedAllAtItem...)
			}
		}
	}
	// Where sharing outside the project is off, nothing changes for members but that no link can be made and no guest
	// invited: whoever may share an item still sees its link and removes it.
	allow("member", "Réka's My files › Offer.pdf, no sharing outside", without(ownerAtItem, outside...)...)
	allow("member", "Bence's My files › Offer.pdf, Réka given editor on Clients, no sharing outside", without(editorInFolder, outside...)...)
	allow("member", "a shared folder › Offer.pdf, given editor on it as a whole, no sharing outside", without(sharedAllAtItem, outside...)...)
	// A top folder has no links and no guests, so nothing changes there: a manager still switches it back on.
	allow("member", "a shared folder, given manager, no sharing outside", managerAtTop...)
	allow("member", "a shared folder, given editor, no sharing outside", editorAtTop...)
	allow("member", "Réka's My files, no sharing outside", ownerAtTop...)

	// Invitations change nothing for members, but that the member may share what Anna was invited to: Réka in her own
	// My files, and where she was given editor on Clients (and until her own end date).
	allow("member", "Réka's My files › Offer.pdf, the guest given editor on Clients by Réka", ownerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given editor on Clients", editorInFolder...)
	allow("member", "Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given viewer on Clients", viewerAtItem...)
	allow("member", "Bence's My files › Offer.pdf, Réka given editor on Clients until tomorrow", editorInFolder...)

	// Anna, the guest, sees and downloads where an invitation to her counts, and as editor works inside it; nowhere
	// else, whatever her request says she is. "another guest" was invited nowhere.
	for _, g := range []string{"a guest", "a guest with Réka's user id"} {
		for place, actions := range map[string][]Action{
			"Bence's My files › Offer.pdf, the guest given viewer on Clients by Bence":                             guestViewer,
			"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence":                             guestEditorInFolder,
			"Bence's My files › 2026, the guest given editor on Clients by Bence":                                  guestEditorInFolder,
			"Bence's My files › Clients, the guest given editor on Clients by Bence":                               guestEditorAtItem,
			"Bence's My files › Offer.pdf, the guest given viewer on Offer.pdf by Bence":                           guestViewer,
			"Bence's My files › Offer.pdf, the guest given viewer on Clients and editor on Offer.pdf by Bence":     guestEditorAtItem,
			"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given editor on Clients": guestEditorInFolder,
			"Bence's My files › Offer.pdf, the guest given editor on Clients by Bence until tomorrow":              guestEditorInFolder,
			"Réka's My files › Offer.pdf, the guest given editor on Clients by Réka":                               guestEditorInFolder,
			"a shared folder › Offer.pdf, the guest given editor on Bakery by someone who left":                    guestEditorInFolder,
			"a shared folder › Offer.pdf, the guest given viewer on Clients by Bence":                              guestViewer,
			"a shared folder › Bakery, the guest given editor on Bakery by Bence":                                  guestEditorAtItem,
		} {
			allow(g, place, actions...)
		}
	}

	// Visitors see and download, through a link that counts there, its item and everything inside it, and nothing else:
	// nothing beside it or above it, and none of what members do, whoever they are. A link on Clients covers Clients,
	// Bakery, 2026, Offer.pdf and Ledger.xlsx; one on Offer.pdf covers Offer.pdf alone. A link counts in a live place
	// where sharing outside the project is on: in a shared folder, whoever made it (it is the project's); in a My files
	// whose owner is in the member list, while its maker may share its item there (Bence in his own My files, Réka in
	// hers). A link of a kind nobody knows, or on no item, counts nowhere.
	byBence := []string{"a visitor, link on Clients by Bence", "the admin, signed in, with a link on Clients by Bence",
		"a guest with a link on Clients by Bence"}
	byReka := []string{"a visitor, link on Clients by Réka", "Réka, signed in, with her own link on Clients"}
	for name, place := range places {
		if place == nil || place.Deleted || place.NoSharingOutside {
			continue
		}
		var onClientsBy []string
		switch {
		case place.Kind == SharedFolder:
			onClientsBy = append(append(slices.Clone(byBence), byReka...), "a visitor, link on Clients by someone who left")
			if slices.Contains(place.Path, onOffer) {
				allow("a visitor, link on Offer.pdf by Bence", name, throughALink...)
			}
		case place.Kind == MyFiles && place.Owner == "u-bence" && !place.OwnerLeft:
			onClientsBy = byBence
			if slices.Contains(place.Path, onOffer) {
				allow("a visitor, link on Offer.pdf by Bence", name, throughALink...)
			}
		case place.Kind == MyFiles && place.Owner == "u-reka" && !place.OwnerLeft:
			onClientsBy = byReka
		}
		if slices.Contains(place.Path, onClients) {
			for _, v := range onClientsBy {
				allow(v, name, throughALink...)
			}
		}
	}
	// Réka's links in Bence's My files count where she may share Clients: where she was given editor on it. Not where she
	// was given less there, or only something below it, or a grant that counts for nothing, or once his My files is held.
	for _, place := range []string{
		"Bence's My files › Offer.pdf, Réka given editor on Clients",
		"Bence's My files › Clients, Réka given editor on Clients",
		"Bence's My files › Offer.pdf, Réka given editor on Clients and viewer on Offer.pdf",
		// (Items in the trash are never asked about for visitors: no route through a link finds them.)
		"Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Clients",
		"Bence's My files › Offer.pdf in the trash, deleted by Bence, Réka given editor on Clients",
		"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given editor on Clients",
		"Bence's My files › Offer.pdf, Réka given editor on Clients until tomorrow",
	} {
		for _, v := range byReka {
			allow(v, place, throughALink...)
		}
	}
	// A held My files, whose owner has left the member list: a project admin hands it over or deletes it, as a whole, at
	// its top folder, whatever was given on it and with sharing outside the project on or off. Nobody does anything in it,
	// and nobody does anything at all to one in the trash, or to one without an owner.
	for _, place := range []string{"Bence's My files, Bence gone", "Bence's My files, Bence gone, no sharing outside",
		"Réka's My files, her account gone", "Bence's My files, Bence gone, the admin given editor on its top folder and everyone viewer"} {
		allow("admin", place, AsAWhole...)
	}
	// A shared folder in the trash can only be restored, as a whole, by its managers and the project admins.
	// Not even switching sharing outside the project back on.
	allow("member", "a deleted shared folder, given manager", RestoreSharedFolder)
	allow("member", "a deleted shared folder, given manager, no sharing outside", RestoreSharedFolder)
	for _, place := range []string{"a deleted shared folder, no role", "a deleted shared folder, given viewer",
		"a deleted shared folder, given editor", "a deleted shared folder, given manager", "a deleted shared folder owned by Réka",
		"a deleted shared folder, given manager, no sharing outside"} {
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
		{"a visitor has no role, even with a link on the item", people["a visitor, link on Clients by Bence"], bences(offer), NoRole},
		{"nor a member who came with a link", people["Réka, signed in, with her own link on Clients"], &Place{Kind: MyFiles, Owner: "u-reka", Path: offer}, NoRole},
		{"an invitation reaches a file three levels down", people["a guest"], bences(offer, toGuest(onClients, Editor, bence)), Editor},
		{"the higher of two invitations on the way", people["a guest"], bences(offer, toGuest(onClients, Viewer, bence), toGuest(onOffer, Editor, bence)), Editor},
		{"an invitation does not reach its sibling", people["a guest"], bences(ledger, toGuest(onOffer, Editor, bence)), NoRole},
		{"nor the top folder", people["a guest"], bences(nil, toGuest(onClients, Editor, bence)), NoRole},
		{"another guest's invitation is theirs", people["another guest"], bences(offer, toGuest(onClients, Editor, bence)), NoRole},
		{"a guest is not everyone", people["a guest"], bences(offer, toEveryone(onClients, Editor)), NoRole},
		{"a guest with a member's id is still only the guest", people["a guest with Réka's user id"], bences(offer, to("u-reka", onClients, Editor)), NoRole},
		{"an invitation is no member's", member, bences(offer, Grant{On: onClients, Guest: anna.ID, Role: Editor, By: reka}), NoRole},
		{"an invitation from someone who may no longer share it", people["a guest"], bences(offer, toGuest(onClients, Editor, reka)), NoRole},
		{"an invitation that ended", people["a guest"], &Place{Kind: MyFiles, Owner: "u-bence", Path: offer, Now: now,
			Grants: grants(until(toGuest(onClients, Editor, bence), now.Add(-time.Second)))}, NoRole},
		{"an invitation in a shared folder is the project's", people["a guest"], shared(offer, toGuest(onBakery, Viewer, gone)), Viewer},
		{"a member's grant that ends, before it does", member, &Place{Kind: MyFiles, Owner: "u-bence", Path: offer, Now: now,
			Grants: grants(until(to("u-reka", onClients, Viewer), now.Add(time.Second)))}, Viewer},
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
			out = append(out, to("u-reka", on, role), to("u-admin", on, role), toEveryone(on, role), toGuest(on, role, bence),
				toGuest(on, role, reka), until(toGuest(on, role, bence), now.Add(time.Hour)))
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
			for _, a := range []Action{Download, Upload, MakeFolder, Rename, Move, MoveOut, MoveIn, Delete, SeeTrash, Restore, SeeAccess, Share, InviteGuest, ManageAccess, SwitchSharingOutside, DeleteSharedFolder} {
				if l != nil && may(a) && !may(List) {
					t.Errorf("%s may %s but not list", where, a)
				}
			}
			// Sharing outside the project switched off there takes away making links and inviting guests from members,
			// what a link opens from visitors, and what an invitation lets a guest do, and nothing else; and it gives
			// nobody anything. SwitchedOff says exactly when it is the only thing in the way.
			if l != nil {
				on, off := *l, *l
				on.NoSharingOutside, off.NoSharingOutside = false, true
				for _, a := range everyAction {
					withOn, withOff := Decide(p, a, &on), Decide(p, a, &off)
					if withOff && !withOn {
						t.Errorf("%s: switching sharing outside off lets them %s", where, a)
					}
					stopped := withOn && !withOff
					var stops bool
					switch {
					case p.Link != nil:
						stops = slices.Contains(ThroughALink, a)
					case p.Guest != nil:
						stops = slices.Contains(AsAGuestAtAnItem, a)
					default:
						stops = a == MakeLink || a == InviteGuest
					}
					if stopped && !stops {
						t.Errorf("%s: switching sharing outside off stops them doing %s", where, a)
					}
					if stops && withOn && withOff {
						t.Errorf("%s may still %s with sharing outside switched off", where, a)
					}
					if SwitchedOff(p, a, &off) != stopped || SwitchedOff(p, a, &on) {
						t.Errorf("%s: SwitchedOff for %s is %v off and %v on; switching it off stops it %v",
							where, a, SwitchedOff(p, a, &off), SwitchedOff(p, a, &on), stopped)
					}
				}
			}
			// The project's own switch, and what an admin oversees, are a project admin's alone, whatever was given.
			for _, a := range []Action{Oversee, SwitchSharingOutside} {
				if l == nil && may(a) != (p.isMember() && p.Role == members.RoleAdmin) {
					t.Errorf("%s: may %s for the whole project %v", where, a, may(a))
				}
			}
			if l != nil && may(Oversee) {
				t.Errorf("%s may oversee, which concerns no place, in a place", where)
			}
			for _, a := range AsAGuestAtAnItem {
				if may(a) && !may(ListAsGuest) {
					t.Errorf("%s may %s but not see it", where, a)
				}
			}
			// A guest does only what a guest may, and only at or inside an item given to them, and nobody else does it
			// (but ask for a code to sign in, which anyone outside the project may).
			guest := p.Link == nil && p.Guest != nil
			for _, a := range everyAction {
				if !may(a) {
					continue
				}
				if forGuests := slices.Contains(AsAGuest, a); guest != forGuests && !(a == SignInAsGuest && !p.isMember() && p.Link == nil) {
					t.Errorf("%s may %s: a guest %v", where, a, guest)
				}
				if guest && l != nil {
					invited := false
					for _, g := range l.Grants {
						invited = invited || (g.Guest == p.Guest.ID && g.On > 0 && slices.Contains(l.Path, g.On))
					}
					if !invited {
						t.Errorf("%s may %s, which is not at or inside an item given to them", where, a)
					}
				}
			}
			// A held My files is handed over or deleted as a whole: by a project admin, at the top folder of a My files whose
			// owner has left (and is not them), both or neither; and whoever may do that may do nothing else there.
			held := l != nil && l.Kind == MyFiles && l.Owner != "" && l.OwnerLeft && !l.Deleted && len(l.Path) == 0
			for _, a := range AsAWhole {
				if want := held && p.isMember() && p.Role == members.RoleAdmin && p.UserID != l.Owner; may(a) != want {
					t.Errorf("%s: may %s %v, want %v", where, a, may(a), want)
				}
			}
			if may(HandOverHeld) {
				for _, a := range everyAction {
					if may(a) && !slices.Contains(AsAWhole, a) {
						t.Errorf("%s may hand it over as a whole, and also %s", where, a)
					}
				}
			}
			if l == nil || l.Deleted {
				continue
			}
			// Moving between places: only out of one's own My files, by whoever may move it there, and only into a
			// folder of a shared folder where one may upload.
			if may(MoveOut) != (l.Kind == MyFiles && l.Owner == p.UserID && len(l.Path) > 0 && may(Move)) {
				t.Errorf("%s: may move it out %v, may move it %v", where, may(MoveOut), may(Move))
			}
			if may(MoveIn) != (l.Kind == SharedFolder && may(Upload)) {
				t.Errorf("%s: may move something in %v, may upload %v", where, may(MoveIn), may(Upload))
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
				for _, a := range []Action{SeeTrash, ManageAccess, SwitchSharingOutside, DeleteSharedFolder, RestoreSharedFolder} {
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
				for _, a := range []Action{Rename, Move, MoveOut, Delete, Restore, Share, MakeLink, ManageLink, SeeThroughLink, DownloadThroughLink} {
					if may(a) {
						t.Errorf("%s may %s, which concerns an item, in a top folder", where, a)
					}
				}
				// Whoever may open the top folder may download it whole, as one zip.
				if may(Download) != may(List) {
					t.Errorf("%s: may list %v, may download %v: want both or neither", where, may(List), may(Download))
				}
				// Whoever switches sharing outside the project off for a shared folder decides who has access to it, and the
				// other way round; a My files has no switch of its own.
				if may(SwitchSharingOutside) != may(ManageAccess) || (may(SwitchSharingOutside) && l.Kind != SharedFolder) {
					t.Errorf("%s: may switch sharing outside %v, may decide who has access %v", where, may(SwitchSharingOutside), may(ManageAccess))
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
			// A public link is sharing: whoever may make one may share the item, and whoever may share it sees its link and
			// removes it, also where sharing outside the project is off.
			if may(MakeLink) && (!may(Share) || l.NoSharingOutside) {
				t.Errorf("%s may make a link, but may share it %v, sharing outside off %v", where, may(Share), l.NoSharingOutside)
			}
			if may(ManageLink) != (may(Share) && len(l.Path) > 0) {
				t.Errorf("%s: may see and remove its link %v, may share it %v", where, may(ManageLink), may(Share))
			}
			// A visitor does only what a link lets them, and only at its item or inside it; a member never does.
			for _, a := range everyAction {
				if !may(a) {
					continue
				}
				if visitor := p.Link != nil; visitor != slices.Contains(ThroughALink, a) {
					t.Errorf("%s may %s: a visitor %v", where, a, visitor)
				}
				if p.Link != nil && !slices.Contains(l.Path, p.Link.Item) {
					t.Errorf("%s may %s, which is not at or inside the item of their link", where, a)
				}
			}
			// Access on a folder applies to everything inside it: what the person may do at the top folder or at a folder
			// they may also do at anything in it. (Restoring depends on who deleted each item.) So does a link, and so does
			// an invitation.
			for _, a := range append([]Action{List, Download, Upload, MakeFolder, Rename, Move, MoveOut, MoveIn, Delete, SeeAccess, Share,
				MakeLink, ManageLink, InviteGuest, SeeThroughLink, DownloadThroughLink}, AsAGuestAtAnItem...) {
				if len(l.Path) == 0 && (a == Rename || a == Move || a == MoveOut || a == Delete || a == Share || a == MakeLink || a == ManageLink || a == InviteGuest) {
					continue
				}
				if may(a) && !Decide(p, a, child) {
					t.Errorf("%s may %s, but not at something inside it", where, a)
				}
			}
			// A guest renames, moves and deletes only where they may put things into the folder it is in, so what they
			// were given stays where its giver put it.
			if len(l.Path) > 0 {
				for _, a := range []Action{RenameAsGuest, MoveAsGuest, DeleteAsGuest} {
					if may(a) != Decide(p, UploadAsGuest, l.Up(len(l.Path)-1)) && (may(a) || may(UploadAsGuest)) {
						t.Errorf("%s: may %s %v, may upload into its folder %v", where, a, may(a), Decide(p, UploadAsGuest, l.Up(len(l.Path)-1)))
					}
				}
			}
			// An invitation changes nothing for anyone but its guest; and nothing given to members, or to everyone,
			// gives a guest anything.
			for _, g := range []Grant{toGuest(onClients, Editor, bence), toGuest(onOffer, Editor, reka), toGuest(onTop, Viewer, bence),
				{On: onClients, Guest: 8, Role: Editor, By: bence}} {
				invited := *l
				invited.Grants = append(slices.Clone(l.Grants), g)
				for _, a := range everyAction {
					if (p.Guest == nil || p.Link != nil || p.Guest.ID != g.Guest) && may(a) != Decide(p, a, &invited) {
						t.Errorf("%s: inviting guest %d on item %d changes whether they may %s", where, g.Guest, g.On, a)
					}
				}
			}
			if guest {
				// (What is given to whoever invited them can: in a My files, the invitation stands on their right to share
				// the item.)
				givers := map[string]bool{}
				for _, g := range l.Grants {
					if g.Guest == p.Guest.ID {
						givers[g.By.UserID] = true
					}
				}
				for _, g := range []Grant{toEveryone(onTop, Editor), toEveryone(onClients, Editor), to("u-reka", onClients, Editor),
					to("u-admin", onClients, Editor), to("", onClients, Editor)} {
					if (g.Everyone && len(givers) > 0) || givers[g.Member] {
						continue
					}
					more := *l
					more.Grants = append(slices.Clone(l.Grants), g)
					for _, a := range everyAction {
						if may(a) != Decide(p, a, &more) {
							t.Errorf("%s: giving %+v changes whether the guest may %s", where, g, a)
						}
					}
				}
			}
			// Giving more never takes anything away, wherever it is given; giving it on an item off the path changes
			// nothing at all.
			for _, g := range extraGrants() {
				more := *l
				more.Grants = append(slices.Clone(l.Grants), g)
				off := *l
				offPath := g
				offPath.On = 98
				off.Grants = append(slices.Clone(l.Grants), offPath)
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
			// with them or with everyone, or they came with a link on the way to it.
			if l.Kind == MyFiles && (l.Owner != p.UserID || p.Link != nil || p.Guest != nil) {
				shared := p.Link != nil && slices.Contains(l.Path, p.Link.Item)
				for _, g := range l.Grants {
					toThem := (!guest && (g.Everyone || (g.Member != "" && g.Member == p.UserID))) || (guest && g.Guest == p.Guest.ID)
					shared = shared || (p.Link == nil && g.On != 0 && toThem && slices.Contains(l.Path, g.On))
				}
				for _, a := range everyAction {
					// (A project admin hands a held My files over or deletes it as a whole, at its top folder: nothing in it.)
					whole := slices.Contains(AsAWhole, a) && len(l.Path) == 0 && l.OwnerLeft
					if may(a) && !shared && !whole {
						t.Errorf("%s may %s in someone else's My files, where nothing was shared with them", where, a)
					}
				}
			}
		}
	}
}

// "Shared with me" lists, for every person and place the table knows, only the items in listed: what they may open
// without being able to open the folder it is in. Everything else is left out, written out here rather than worked out
// from the package: what is inside a folder shared with them (they open the folder), their own My files, a shared
// folder they have a role on as a whole, a held My files, a grant that counts for nothing or is someone else's.
func TestSharedWithMe(t *testing.T) {
	listed := map[string]bool{
		// Shared on the item itself, by name: a folder of Bence's, a single file of his, a folder or a file in a shared
		// folder Réka has no role on.
		"member Bence's My files › Clients, Réka given editor on Clients":     true,
		"member Bence's My files › Offer.pdf, Réka given viewer on Offer.pdf": true,
		"member a shared folder › Bakery, given editor on Bakery":             true,
		"member a shared folder › Offer.pdf, given viewer on Offer.pdf":       true,
		"admin Réka's My files › Offer.pdf, shared with the admin":            true,
		// Shared with everyone in the project: every member, the admin included.
		"member Bence's My files › Offer.pdf, everyone given editor on Offer.pdf": true,
		"admin Bence's My files › Offer.pdf, everyone given editor on Offer.pdf":  true,
		// SharedWithMe is about live items: the server never asks it about one in the trash, which no route finds.
		"member Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Offer.pdf": true,
	}
	// A guest's "Shared with me": where an invitation to them starts, whoever asks as that guest.
	for _, g := range []string{"a guest", "a guest with Réka's user id"} {
		for _, place := range []string{
			"Bence's My files › Clients, the guest given editor on Clients by Bence",
			"Bence's My files › Offer.pdf, the guest given viewer on Offer.pdf by Bence",
			"a shared folder › Bakery, the guest given editor on Bakery by Bence",
		} {
			listed[g+" "+place] = true
		}
	}
	tried := 0
	for pn, p := range people {
		for ln, l := range places {
			key := pn + " " + ln
			if got := SharedWithMe(p, l); got != listed[key] {
				t.Errorf("%s: listed %v, want %v", key, got, listed[key])
			}
			// Whatever is listed, they may open, and they cannot open its place's top folder: it is not among theirs.
			list := List
			if p.Guest != nil && p.Link == nil {
				list = ListAsGuest
			}
			if SharedWithMe(p, l) && (!Decide(p, list, l) || Decide(p, list, l.Up(0))) {
				t.Errorf("%s: listed, but may open it %v and its place %v", key, Decide(p, list, l), Decide(p, list, l.Up(0)))
			}
			delete(listed, key)
			tried++
		}
	}
	for key := range listed {
		t.Errorf("the table lists %q, which is not a combination the test tried", key)
	}
	if want := len(people) * len(places); tried != want {
		t.Fatalf("tried %d combinations, want %d", tried, want)
	}
}

// A search finds, for every person and place the table knows, only the items in found: whatever they may open, written
// out here rather than worked out from the package. Never a top folder, nothing in a held My files or in a shared folder
// in the trash, nothing beside, above or outside what was given, nothing given to someone else or that counts for
// nothing, and nothing at all for someone who is not a member.
func TestFinds(t *testing.T) {
	found := map[string]bool{}
	for _, place := range []string{
		// Her own My files, whatever was given to whom.
		"Réka's My files › Offer.pdf",
		"Réka's My files › Offer.pdf, shared with the admin",
		// What was shared with her in Bence's My files, through a folder above it or on the item itself, by name or to
		// everyone. (Items in the trash are never asked about: no route finds them, and a search walks around the trash.)
		"Bence's My files › Offer.pdf, Réka given viewer on Clients",
		"Bence's My files › Offer.pdf, Réka given editor on Clients",
		"Bence's My files › Clients, Réka given editor on Clients",
		"Bence's My files › 2026, Réka given viewer on Clients",
		"Bence's My files › Offer.pdf, Réka given viewer on Offer.pdf",
		"Bence's My files › Offer.pdf, Réka given viewer on Clients and editor on Offer.pdf",
		"Bence's My files › Offer.pdf, Réka given editor on Clients and viewer on Offer.pdf",
		"Bence's My files › Offer.pdf, everyone given viewer on Bakery",
		"Bence's My files › Offer.pdf, everyone given editor on Offer.pdf",
		"Bence's My files › Offer.pdf, everyone given viewer on Clients and Réka editor on Bakery",
		"Réka's My files › Offer.pdf in the trash, deleted by the admin",
		"Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Clients",
		"Bence's My files › Offer.pdf in the trash, deleted by Bence, Réka given editor on Clients",
		"Bence's My files › Offer.pdf in the trash, deleted by Réka, Réka given editor on Offer.pdf",
		// In a shared folder: by a role on it as a whole, or on the way down to the item.
		"a shared folder › Offer.pdf, given viewer on it as a whole",
		"a shared folder › Offer.pdf, given editor on it as a whole",
		"a shared folder › Offer.pdf, given manager on it as a whole",
		"a shared folder › Offer.pdf, given viewer on it as a whole and editor on Bakery",
		"a shared folder › Offer.pdf, given editor on it as a whole and viewer on Offer.pdf",
		"a shared folder › Offer.pdf, given editor on Bakery",
		"a shared folder › Bakery, given editor on Bakery",
		"a shared folder › Offer.pdf, given viewer on Offer.pdf",
		"a shared folder › Offer.pdf, everyone given viewer on Clients",
		"a shared folder › Offer.pdf, everyone given viewer on it as a whole",
		"a shared folder › Offer.pdf in the trash, deleted by Bence, given editor on it as a whole",
		"a shared folder › Offer.pdf in the trash, deleted by Réka, given editor on Bakery",
		// Sharing outside the project switched off changes nothing for members.
		"Réka's My files › Offer.pdf, no sharing outside",
		"Bence's My files › Offer.pdf, Réka given editor on Clients, no sharing outside",
		"a shared folder › Offer.pdf, given editor on it as a whole, no sharing outside",
		// Nor do invitations; an end date counts for a member too.
		"Réka's My files › Offer.pdf, the guest given editor on Clients by Réka",
		"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given editor on Clients",
		"Bence's My files › Offer.pdf, the guest given editor on Bakery by Réka, Réka given viewer on Clients",
		"Bence's My files › Offer.pdf, Réka given editor on Clients until tomorrow",
	} {
		found["member "+place] = true
	}
	for _, place := range []string{
		"Réka's My files › Offer.pdf, shared with the admin",
		"Réka's My files › Offer.pdf in the trash, deleted by the admin",
		"Bence's My files › Offer.pdf, everyone given viewer on Bakery",
		"Bence's My files › Offer.pdf, everyone given editor on Offer.pdf",
		"Bence's My files › Offer.pdf, everyone given viewer on Clients and Réka editor on Bakery",
		"Bence's My files › Offer.pdf, the admin given editor on Clients",
	} {
		found["admin "+place] = true
	}
	// A project admin manages every shared folder, and so finds everything in it.
	for name, place := range places {
		if place != nil && place.Kind == SharedFolder && !place.Deleted && len(place.Path) > 0 {
			found["admin "+name] = true
		}
	}
	tried := 0
	for pn, p := range people {
		for ln, l := range places {
			key := pn + " " + ln
			if got := Finds(p, l); got != found[key] {
				t.Errorf("%s: found %v, want %v", key, got, found[key])
			}
			// Whatever is found, they may open; and what they may open below a top folder, they find.
			if l != nil && !l.Deleted && len(l.Path) > 0 && Finds(p, l) != Decide(p, List, l) {
				t.Errorf("%s: found %v, may open %v", key, Finds(p, l), Decide(p, List, l))
			}
			delete(found, key)
			tried++
		}
	}
	for key := range found {
		t.Errorf("the table finds %q, which is not a combination the test tried", key)
	}
	if want := len(people) * len(places); tried != want {
		t.Fatalf("tried %d combinations, want %d", tried, want)
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
		// Invitations: viewer or editor on an item, in either kind of place; nothing on a place as a whole.
		{MyFiles, toGuest(onOffer, Viewer, bence), true},
		{SharedFolder, toGuest(onOffer, Editor, bence), true},
		{SharedFolder, until(toGuest(onOffer, Editor, bence), now), true},
		{SharedFolder, toGuest(onTop, Viewer, bence), false},
		{MyFiles, toGuest(onTop, Viewer, bence), false},
		{SharedFolder, toGuest(onOffer, Manager, bence), false},
		{SharedFolder, toGuest(onOffer, Owner, bence), false},
		{SharedFolder, Grant{On: onOffer, Guest: 7, Member: "u", Role: Viewer}, false},
		{SharedFolder, Grant{On: onOffer, Guest: 7, Everyone: true, Role: Viewer}, false},
		{SharedFolder, Grant{On: onOffer, Guest: -7, Role: Viewer}, false},
		{"team-folder", toGuest(onOffer, Viewer, bence), false},
		// An end date: on a member's grant (a guest who joined the project keeps theirs), never on everyone's.
		{MyFiles, until(to("u", onOffer, Editor), now), true},
		{SharedFolder, until(toEveryone(onOffer, Viewer), now), false},
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
	for _, a := range append(append(append(slices.Clone(InAPlace), Nowhere...), ThroughALink...), AsAGuest...) {
		if !slices.Contains(everyAction, a) {
			t.Errorf("%q is an action that the table does not try", a)
		}
		kinds := 0
		for _, list := range [][]Action{InAPlace, Nowhere, ThroughALink, AsAGuest} {
			if slices.Contains(list, a) {
				kinds++
			}
		}
		if kinds != 1 {
			t.Errorf("%q is an action of %d kinds (a member's in a place or in none, a visitor's, a guest's)", a, kinds)
		}
	}
	for _, a := range AsAGuestAtAnItem {
		if !slices.Contains(AsAGuest, a) {
			t.Errorf("%q is a guest's action at an item, but not among a guest's actions", a)
		}
	}
	for _, a := range AsAWhole {
		if !slices.Contains(InAPlace, a) {
			t.Errorf("%q is an action on a held My files as a whole, but not a member's in a place", a)
		}
	}
	if len(everyAction) != len(InAPlace)+len(Nowhere)+len(ThroughALink)+len(AsAGuest) {
		t.Errorf("the table tries %d actions; the package has %d in a place, %d in none, %d through a link and %d as a guest",
			len(everyAction), len(InAPlace), len(Nowhere), len(ThroughALink), len(AsAGuest))
	}
}

// SwitchedOff is true, for every person and place the table knows, only in switchedOff: where sharing outside the
// project is off and is the only thing in the way. Written out here rather than worked out from the package: whoever
// may share the item, for making a link and inviting a guest; a visitor whose link would count there; the guest an
// invitation there would let in. Nobody else is ever told that sharing outside is off: not someone who could not do it
// anyway, nor at a place's top folder, nor in a shared folder in the trash.
func TestSwitchedOff(t *testing.T) {
	switchedOff := map[string]bool{}
	off := func(person, place string, actions ...Action) {
		if l, ok := places[place]; !ok || !l.NoSharingOutside {
			t.Fatalf("the table switches off something in %q, which is not a place it tries with sharing outside off", place)
		}
		for _, a := range actions {
			switchedOff[fmt.Sprintf("%s %s %s", person, a, place)] = true
		}
	}
	inShared := []string{"a visitor, link on Clients by Bence", "the admin, signed in, with a link on Clients by Bence",
		"a guest with a link on Clients by Bence", "a visitor, link on Clients by Réka", "Réka, signed in, with her own link on Clients",
		"a visitor, link on Clients by someone who left"}
	byBence := []string{"a visitor, link on Clients by Bence", "the admin, signed in, with a link on Clients by Bence",
		"a guest with a link on Clients by Bence"}
	byReka := []string{"a visitor, link on Clients by Réka", "Réka, signed in, with her own link on Clients"}

	off("member", "Réka's My files › Offer.pdf, no sharing outside", outside...)
	for _, v := range byReka {
		off(v, "Réka's My files › Offer.pdf, no sharing outside", throughALink...)
	}
	off("member", "Bence's My files › Offer.pdf, Réka given editor on Clients, no sharing outside", outside...)
	for _, v := range append(append(slices.Clone(byBence), byReka...), "a visitor, link on Offer.pdf by Bence") {
		off(v, "Bence's My files › Offer.pdf, Réka given editor on Clients, no sharing outside", throughALink...)
	}
	for _, p := range []string{"member", "admin"} {
		off(p, "a shared folder › Offer.pdf, given editor on it as a whole, no sharing outside", outside...)
	}
	for _, v := range append(slices.Clone(inShared), "a visitor, link on Offer.pdf by Bence") {
		off(v, "a shared folder › Offer.pdf, given editor on it as a whole, no sharing outside", throughALink...)
	}
	off("admin", "a shared folder › Clients, no role, no sharing outside", outside...)
	for _, v := range inShared {
		off(v, "a shared folder › Clients, no role, no sharing outside", throughALink...)
	}
	for _, g := range []string{"a guest", "a guest with Réka's user id"} {
		off(g, "Bence's My files › Offer.pdf, the guest given editor on Clients by Bence, no sharing outside", guestEditorInFolder...)
	}
	for _, v := range append(slices.Clone(byBence), "a visitor, link on Offer.pdf by Bence") {
		off(v, "Bence's My files › Offer.pdf, the guest given editor on Clients by Bence, no sharing outside", throughALink...)
	}

	tried := 0
	for pn, p := range people {
		for ln, l := range places {
			for _, a := range everyAction {
				key := fmt.Sprintf("%s %s %s", pn, a, ln)
				if got := SwitchedOff(p, a, l); got != switchedOff[key] {
					t.Errorf("%s: switched off %v, want %v", key, got, switchedOff[key])
				}
				delete(switchedOff, key)
				tried++
			}
		}
	}
	for key := range switchedOff {
		t.Errorf("the table switches off %q, which is not a combination the test tried", key)
	}
	if want := len(people) * len(places) * len(everyAction); tried != want {
		t.Fatalf("tried %d combinations, want %d", tried, want)
	}
}

// A project admin manages every shared folder and oversees the project, but may do nothing at all in anyone else's My
// files, through any action, with sharing outside the project on or off, but what was shared with them (by name or with
// everyone) on the way down to the item. Overseeing concerns no place.
func TestAProjectAdminDoesNothingInAnotherPersonsMyFiles(t *testing.T) {
	admin := people["admin"]
	checked := 0
	for ln, l := range places {
		if l == nil || l.Kind != MyFiles || l.Owner == admin.UserID {
			continue
		}
		shared := false
		for _, g := range l.Grants {
			shared = shared || (g.On != 0 && slices.Contains(l.Path, g.On) && (g.Everyone || g.Member == admin.UserID))
		}
		for _, sharing := range []bool{false, true} {
			q := *l
			q.NoSharingOutside = sharing
			for _, a := range everyAction {
				// The one thing a project admin does to another person's My files without anything shared with them: hand a
				// held one over, or delete it, as a whole, at its top folder. Nothing in it is opened or named.
				whole := slices.Contains(AsAWhole, a) && len(q.Path) == 0 && q.OwnerLeft
				inside := shared && slices.Contains(InAPlace, a) && !slices.Contains(AsAWhole, a)
				if Decide(admin, a, &q) && !inside && !whole {
					t.Errorf("the admin may %s in %s (sharing outside off %v), where nothing was shared with them", a, ln, sharing)
				}
				if q.OwnerLeft && Decide(admin, a, &q) && !slices.Contains(AsAWhole, a) {
					t.Errorf("the admin may %s in %s, a held My files", a, ln)
				}
				if !shared && (a == List || a == Download) && Decide(admin, a, &q) {
					t.Errorf("the admin may %s in %s", a, ln)
				}
			}
			checked++
		}
	}
	if checked < 40 {
		t.Fatalf("only %d places in someone else's My files were tried", checked/2)
	}
	if Decide(admin, Oversee, &Place{Kind: MyFiles, Owner: "u-bence"}) || Decide(admin, Oversee, &Place{Kind: SharedFolder}) {
		t.Fatal("overseeing was allowed in a place")
	}
}

// Which guests can see something, for "who can see this" and the list's sentence: those an invitation that counts there
// reaches, each once.
func TestGuests(t *testing.T) {
	other := Grant{On: onBakery, Guest: 8, Role: Viewer, By: bence}
	cases := []struct {
		name  string
		place *Place
		want  []int64
	}{
		{"invited on a folder above, and on the item", &Place{Kind: MyFiles, Owner: "u-bence", Path: offer,
			Grants: grants(toGuest(onClients, Viewer, bence), toGuest(onOffer, Editor, bence), other)}, []int64{7, 8}},
		{"invited on its sibling", &Place{Kind: MyFiles, Owner: "u-bence", Path: ledger, Grants: grants(toGuest(onOffer, Viewer, bence))}, nil},
		{"an invitation whose giver may no longer share it", &Place{Kind: MyFiles, Owner: "u-bence", Path: offer,
			Grants: grants(toGuest(onClients, Viewer, reka), other)}, []int64{8}},
		{"an invitation that ended", &Place{Kind: SharedFolder, Path: offer, Now: now,
			Grants: grants(until(toGuest(onClients, Viewer, bence), now))}, nil},
		{"sharing outside switched off", &Place{Kind: SharedFolder, Path: offer, NoSharingOutside: true, Grants: grants(other)}, nil},
		{"members and everyone", &Place{Kind: SharedFolder, Path: offer, Grants: grants(toEveryone(onTop, Viewer), to("u-reka", onClients, Editor))}, nil},
		{"no place", nil, nil},
	}
	for _, c := range cases {
		if got := Guests(c.place); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Whether anyone with a link can see an item, for "who can see this": a link on it or on a folder above it that lets
// its visitors in there now, decided as a visitor with it would be.
func TestByLink(t *testing.T) {
	onClients := *link(onClients, ViewLink, bence)
	cases := []struct {
		name  string
		place *Place
		want  bool
	}{
		{"a link on a folder above, in Bence's My files", &Place{Kind: MyFiles, Owner: "u-bence", Path: offer, Links: []Link{onClients}}, true},
		{"a link on the item itself", &Place{Kind: SharedFolder, Path: offer, Links: []Link{*link(onOffer, ViewLink, bence)}}, true},
		{"a link on its sibling", &Place{Kind: SharedFolder, Path: ledger, Links: []Link{*link(onOffer, ViewLink, bence)}}, false},
		{"a link on something inside it", &Place{Kind: SharedFolder, Path: clients, Links: []Link{*link(onOffer, ViewLink, bence)}}, false},
		{"a link whose maker may no longer share it", &Place{Kind: MyFiles, Owner: "u-bence", Path: offer, Links: []Link{*link(onClients.Item, ViewLink, reka)}}, false},
		{"a link in a held My files", &Place{Kind: MyFiles, Owner: "u-bence", OwnerLeft: true, Path: offer, Links: []Link{onClients}}, false},
		{"a link where sharing outside is off", &Place{Kind: SharedFolder, Path: offer, NoSharingOutside: true, Links: []Link{onClients}}, false},
		{"no link", &Place{Kind: SharedFolder, Path: offer}, false},
		{"no place", nil, false},
	}
	for _, c := range cases {
		if got := ByLink(c.place); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// A listing adds the links on each item to the folder's: the folder's own stay as they were.
	folder := &Place{Kind: SharedFolder, Path: bakery, Links: []Link{onClients}}
	child := folder.Below(12, nil).WithLinks(*link(12, ViewLink, bence))
	if len(child.Links) != 2 || len(folder.Links) != 1 {
		t.Fatalf("WithLinks: %d links below, %d in the folder", len(child.Links), len(folder.Links))
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	admin := Person{UserID: "u1", Role: members.RoleAdmin}
	for _, place := range []*Place{nil, {Kind: MyFiles, Owner: "u1"}, {Kind: MyFiles, Owner: "u1", Path: offer}, {Kind: SharedFolder, Grants: grants(to("u1", onTop, Manager))}} {
		if Decide(admin, Action("own"), place) {
			t.Fatalf("an action without a rule must be refused (in %+v)", place)
		}
	}
	guest := Person{Guest: anna}
	for _, place := range []*Place{nil, {Kind: SharedFolder, Path: offer, Grants: grants(toGuest(onClients, Editor, bence))}} {
		if Decide(guest, Action("own"), place) {
			t.Fatalf("an action without a rule must be refused for a guest (in %+v)", place)
		}
	}
}
