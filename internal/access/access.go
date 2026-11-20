// Package access is the Drive's one authorisation function. Every route asks Decide before it does anything, and
// nothing else in the Drive decides who may do what. (The survey of other drives found the same "escape the shared
// folder" hole in six projects, each caused by permission checks scattered over the code.)
//
// The package knows nothing about HTTP, the index or the storage. Callers describe who is asking, what they want to do
// and where: the place, the item in it the request is about with the folders above it (Place.Path), and the access
// given there (Place.Grants). They get a yes or a no. That keeps the rules in one place where a table test can cover
// every case.
//
// Version 1 so far:
//
//   - Only members (people in the member list) may do anything. A member may use the Drive, see about how much space it
//     uses, and make a shared folder.
//   - In their own "My files" a member may do everything: list, download, upload, make folders, rename, move, delete
//     into the trash, see and restore from its trash, and share any item in it. Nobody else may do anything there,
//     project admins included, except with an item shared with them.
//   - A shared folder belongs to the project, not to a person. What someone may do in it follows from their role on it:
//     viewer, editor or manager, each able to do what the one before it can and more. Everyone holding the admin role
//     on the project is a manager of every shared folder. Without a role, a member may do nothing there, except with
//     an item shared with them.
//   - Access is given on a file or folder (in any place), or on a shared folder as a whole (its top folder), to a named
//     member or to everyone in the project, as viewer or editor; manager only on a shared folder as a whole, to a named
//     member. Access on a folder applies to everything in it, at any depth. Deeper levels can only add access, never
//     take it away: the role someone has at an item is the highest of what they were given on it, on every folder
//     above it and on the place, and what the place gives them by itself (owner, project admin). RoleAt computes it.
//   - A viewer may open, download (a file, or a folder with everything in it as one zip, the top folder included) and
//     see who has access; an editor may also upload and make folders in it, and share it further. Only managers decide
//     who has access to a shared folder as a whole, and only they may delete it.
//   - Renaming, moving, deleting and restoring an item change the folder it is in (or goes back to), so they need the
//     right to put things into that folder, as an upload does: someone given one folder or file works inside it, but
//     cannot rename it, take it out of the folder it is in, or put it back into one they cannot add to, and so never
//     learns what else is in a folder they were not given. Restoring also needs the right to see the place's whole
//     trash, or to be the one who put the item there.
//   - A shared folder in the trash can only be restored, by a manager.
//   - A "My files" whose owner has left the member list is held: nobody may do anything there.
//   - Moving between places, in version 1, is only out of one's own "My files" into a shared folder, where it becomes
//     the project's: its owner may move it out (MoveOut), and it may go into a folder of a shared folder where they may
//     upload (MoveIn). Every other move stays inside its place.
//   - "Shared with me" lists what someone may open without being able to open the folder it is in (SharedWithMe): the
//     highest point of a place their access reaches, given on it to them or to everyone in the project.
//   - A search by name finds every live item someone may open, and nothing else (Finds).
//   - Whoever may share a file or folder may make a public link to it (MakeLink), see it and remove it (ManageLink):
//     not a viewer. A link is on a file or folder, never on a place's top folder, one per item.
//   - A visitor is someone who opened a public link: a person of their own kind, never a member, whatever else is
//     known about them. They may see the link's item and everything inside it, and download them (SeeThroughLink,
//     DownloadThroughLink), and nothing else: nothing beside it or above it, and none of what members do. A link
//     counts while its item is live and its place open (not a held My files, not a shared folder in the trash), and
//     while sharing outside the project is on there. In a My files a link is its maker's share: it counts only while
//     they may still share its item. A shared folder's links are the project's, like everything in it, and do not
//     depend on their maker.
//   - A guest is someone outside the project whom a member invited by email address, and who proved the address with a
//     code: a person of their own kind, never a member and never "everyone in the project", whatever else is known about
//     them. Whoever may share a file or folder may invite a guest to it (InviteGuest), as viewer or editor, with an end
//     date if they like, while sharing outside the project is on there. A guest does only what the actions for guests
//     (AsAGuest) let them, and only at or inside an item given to them: a viewer opens and downloads; an editor also
//     uploads and makes folders, and renames, moves and deletes what is inside what they were given. Never anything at a
//     place's top folder, never sharing, never a link, never the trash, never who has access. An invitation counts while
//     its item is live and its place open, before its end date, and while sharing outside the project is on there; in a
//     My files it is its giver's share, as a link is: it counts only while they may still share its item.
//   - Sharing outside the project (public links and guests) can be switched off: for the whole project by a project
//     admin (SwitchSharingOutside, at no place), and for one shared folder by its managers (SwitchSharingOutside, at its
//     top folder). Where it is off (Place.NoSharingOutside) no link opens and none is made, no guest gets in and none is
//     invited; links and invitations stay, can be seen and removed, and work again when it is switched back on.
//     SwitchedOff says when that switch is the only thing in the way, so that whoever would otherwise be let in can be
//     told so.
//   - A project admin oversees what leaves the project and the space each person and shared folder uses (Oversee):
//     never content. A project admin may do nothing in anyone else's My files but what was shared with them.
package access

import (
	"slices"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

// A Person is who is asking: a member, a visitor who came with a public link, or a guest.
type Person struct {
	// UserID is the member's stable user id from the member list.
	UserID string
	// Role is the member's role on the project, from the member list: admin or member. Anything else means the person
	// is not in the member list, and may do nothing.
	Role members.Role
	// Link is, for a visitor, the public link they came with. Someone with a link is a visitor and nothing else:
	// whatever UserID, Role and Guest say, they are not a member, and may do only what the link lets them (see Link).
	Link *Link
	// Guest is, for a guest, who they are. A guest is a guest and nothing else: whatever UserID and Role say, they are not
	// a member, and may do only what was given to them as a guest. (A Link says first: with both, they are a visitor.)
	Guest *Guest
}

// A Guest is someone outside the project whom a member invited by email address.
type Guest struct {
	// ID is the guest's id in the index: what the invitations to them name.
	ID int64
}

// isMember says whether the person is in the member list: only members may do anything in the Drive itself, and only
// they are "everyone in the project". A visitor or a guest never is.
func (p Person) isMember() bool {
	return p.Link == nil && p.Guest == nil && p.UserID != "" && (p.Role == members.RoleAdmin || p.Role == members.RoleMember)
}

// A Link is a public link: anyone with its address may see the file or folder it is on, and everything inside it, and
// download them, without signing in.
type Link struct {
	// Item is the id of the item the link is on: the top of what its visitors may see. It is never a place's top
	// folder.
	Item int64
	// Kind is what the link lets its visitors do.
	Kind LinkKind
	// By is the member who made it, as the member list has them now (without a role once they have left it). In a My
	// files the link counts only while they may still share its item there.
	By Person
}

// LinkKind is what a public link lets its visitors do.
type LinkKind string

// ViewLink lets visitors see and download what it is on. (Upload-only links come with their own ticket.)
const ViewLink LinkKind = "view"

// PlaceKind is the kind of a place.
type PlaceKind string

const (
	// MyFiles is a member's own place, owned by them.
	MyFiles PlaceKind = "my-files"
	// SharedFolder is a top-level place owned by the project.
	SharedFolder PlaceKind = "shared-folder"
)

// A Role is what someone has at an item or a place.
type Role string

const (
	NoRole Role = ""
	// Viewer may open and download, and see who has access.
	Viewer Role = "viewer"
	// Editor may also upload, make folders, rename, move and delete, see and restore from the trash, and share further.
	Editor Role = "editor"
	// Manager may also decide who has access to a shared folder as a whole, and delete it. It is given only on a shared
	// folder as a whole.
	Manager Role = "manager"
	// Owner is the member whose "My files" it is, at every item in it. It is never given: it comes with the place.
	Owner Role = "owner"
)

// A Grant is access given to someone on a place's top folder or on one item in it.
type Grant struct {
	// On is the id of the item it was given on, or 0 for the place's top folder.
	On int64
	// Member is the user id of the member it was given to. It is empty for a grant to everyone in the project, and to a
	// guest.
	Member string
	// Everyone is true for a grant to everyone in the project: every member, for as long as they are one.
	Everyone bool
	// Guest is the id of the guest it was given to (an invitation), and 0 for a grant to a member or to everyone.
	Guest int64
	Role  Role
	// Ends is when it stops counting, and zero for a grant that does not end. Only an invitation is given one; a guest
	// who joins the project keeps theirs as a member (see Place.Now).
	Ends time.Time
	// By is, for an invitation, the member who gave it, as the member list has them now (without a role once they have
	// left it). In a My files an invitation counts only while they may still share its item there.
	By Person
}

// Counts says whether a grant counts for anything in a place of the given kind. These are also the only grants that
// can be given:
//
//   - viewer or editor, on any item, to a named member, to everyone in the project, or to a guest;
//   - on a shared folder as a whole (its top folder), viewer, editor or manager to a named member, and viewer or editor
//     to everyone;
//   - nothing on someone's "My files" as a whole: it is theirs alone, and only items in it are shared;
//   - nothing to a guest on any place as a whole: a guest is given a file or a folder, as a public link is.
//
// Anything else (an unknown role, a manager on an item, a grant to nobody, or to more than one kind of grantee at once,
// an end date on a grant to everyone) counts for nothing. Whether a grant has ended is for the time of the decision to
// say (Place.Now).
func Counts(kind PlaceKind, g Grant) bool {
	grantees := 0
	for _, to := range []bool{g.Everyone, g.Member != "", g.Guest != 0} {
		if to {
			grantees++
		}
	}
	if grantees != 1 || g.Guest < 0 {
		return false // to nobody, or to more than one kind of grantee at once
	}
	if g.On < 0 || (g.Everyone && !g.Ends.IsZero()) {
		return false
	}
	if g.On > 0 {
		return (kind == MyFiles || kind == SharedFolder) && (g.Role == Viewer || g.Role == Editor)
	}
	if kind != SharedFolder || g.Guest != 0 {
		return false
	}
	switch g.Role {
	case Viewer, Editor:
		return true
	case Manager:
		return !g.Everyone
	}
	return false
}

// A Place is where a request is, as far as access is concerned: a top-level place, the item in it the request is
// about, and the access given there.
type Place struct {
	Kind PlaceKind
	// Owner is the user id of the member whose "My files" this is. A shared folder has none: the project owns it, and
	// an Owner given for one counts for nothing.
	Owner string
	// OwnerLeft is true for a "My files" whose owner is no longer in the member list. It is held, unopened: nobody may
	// do anything there, whatever was shared from it.
	OwnerLeft bool
	// Deleted is true for a shared folder that is in the trash.
	Deleted bool
	// Path is the item the request is about, with the folders above it: their ids, from the folder in the place's top
	// folder down, the item last. It is empty when the request is about the place's top folder, or the place as a
	// whole. For an item in the trash it is the folders it goes back to when restored, then the item.
	Path []int64
	// DeletedBy is, for an item in the trash, the user id of the member who put it there (by deleting it, or by
	// replacing it with an upload), or empty when that is not known.
	DeletedBy string
	// Grants is the access given in this place. Only what was given on its top folder (On 0) or on an item of Path
	// counts: access given on one item never reaches its parent or its siblings. A caller may pass more.
	Grants []Grant
	// NoSharingOutside is true where sharing outside the project (public links and guests) is switched off: for the
	// whole project (by a project admin), or for this shared folder (by its managers). Then no link opens and none can be
	// made, no guest gets in and none can be invited; links and invitations already made stay, can be seen and removed,
	// and work again when it is switched back on.
	NoSharingOutside bool
	// Now is the time the decision is made: a grant with an end date counts only before it. Without it, no grant with an
	// end date counts.
	Now time.Time
	// Links are the public links on the item the request is about and on the folders above it, given when the caller
	// asks whether anyone with a link can see it (ByLink). Decide never reads them: a visitor is decided by the link
	// they came with.
	Links []Link
}

// Below is the same place, about the item id directly inside the item p is about (or inside its top folder), with
// the access given on that item added: how a listing describes each item in a folder. It is about a live item.
func (p *Place) Below(id int64, given []Grant) *Place {
	q := *p
	q.DeletedBy = ""
	q.Path = append(append(make([]int64, 0, len(p.Path)+1), p.Path...), id)
	q.Grants = append(append(make([]Grant, 0, len(p.Grants)+len(given)), p.Grants...), given...)
	return &q
}

// WithLinks is the same place with the public links given added to Links.
func (p *Place) WithLinks(links ...Link) *Place {
	q := *p
	q.Links = append(append(make([]Link, 0, len(p.Links)+len(links)), p.Links...), links...)
	return &q
}

// Up is the same place, about the folder depth levels down from its top folder on the way to the item (0: the top
// folder itself), with the same access given: how a path is checked one folder at a time. It is about a live folder.
func (p *Place) Up(depth int) *Place {
	q := *p
	q.DeletedBy = ""
	if depth < 0 {
		depth = 0
	}
	if depth < len(p.Path) {
		q.Path = p.Path[:depth:depth]
	}
	return &q
}

// An Action is something a person asks to do.
type Action string

const (
	// Enter is using the Drive at all: loading the interface, asking who you are, seeing which places you have and who
	// the members are. It concerns no place.
	Enter Action = "enter"
	// SeeUsage is seeing about how much space the Drive uses, as the sidebar's meter shows it. It concerns no place:
	// it is the whole project's figure, and says nothing about anyone's files.
	SeeUsage Action = "see-usage"
	// MakeSharedFolder is making a new shared folder. It concerns no place: the new one belongs to the project, and its
	// maker becomes its first manager.
	MakeSharedFolder Action = "make-shared-folder"
	// Oversee is, for a project admin, seeing what is shared outside the project (each public link and each guest's
	// invitation: the item's name, who shared it, with whom, when it was last used and how often) and the space each
	// person's My files and each shared folder uses. Never what is in anything. It concerns no place.
	Oversee Action = "oversee"
	// SwitchSharingOutside is switching sharing outside the project (public links and guests) off, or back on: at no
	// place, for the whole project, by a project admin; at a shared folder's top folder, for that shared folder, by its
	// managers. Nowhere else: not for a My files, not at an item.
	SwitchSharingOutside Action = "switch-sharing-outside"

	// List is seeing what is in a folder: the place's top folder, or a folder item.
	List Action = "list"
	// Download is reading content: a file's, or everything in a folder (the place's top folder, or a folder item) as
	// one zip. Access only widens down the tree, so whoever may download a folder may download everything in it.
	Download Action = "download"
	// Upload is putting a new file into a folder (the top folder, or a folder item), which can replace a file there
	// (the replaced one goes to the trash), or keep both under a new name. Moving something into a folder needs it too.
	// Every request of a resumable upload (starting it, each part, finishing and cancelling it) is an upload into its
	// folder.
	Upload Action = "upload"
	// MakeFolder is making a new folder in a folder.
	MakeFolder Action = "make-folder"
	// Rename is giving a file or folder a new name in its folder. It concerns an item, and needs the right to Upload
	// into its folder: a new name can clash with what is there.
	Rename Action = "rename"
	// Move is taking a file or folder out of its folder, which needs the right to Upload into that folder, to put it
	// into another folder of the same place, where the person must be allowed to Upload too. Moving it into another
	// place also needs MoveOut here and MoveIn there. It concerns an item.
	Move Action = "move"
	// MoveOut is taking a file or folder out of its place into another one: whoever may Move it, and is the owner of the
	// "My files" it is in. It goes only into a shared folder (MoveIn), where it becomes the project's, after a warning
	// that says who will see it there. Nothing leaves a shared folder this way, and nobody moves what was shared with
	// them out of someone else's "My files". It concerns an item.
	MoveOut Action = "move-out"
	// MoveIn is putting a file or folder from another place into a folder (the top folder, or a folder item): as an
	// upload into it, and only into a shared folder, which belongs to the project.
	MoveIn Action = "move-in"
	// Delete is moving a file or folder into its place's trash, out of its folder: it needs the right to Upload into
	// that folder. It concerns an item.
	Delete Action = "delete"
	// SeeTrash is seeing what is in a place's trash. It concerns the place's top folder.
	SeeTrash Action = "see-trash"
	// Restore is moving an item out of its place's trash, back into the folder it goes back to: whoever could have
	// deleted it from there may, if they may see the place's whole trash or put it there themselves. It concerns an
	// item in the trash, with the folders it goes back to as its path.
	Restore Action = "restore"

	// SeeAccess is seeing who has access to an item, or to a shared folder as a whole, and in which role.
	SeeAccess Action = "see-access"
	// Share is giving someone access to a file or folder, changing it or taking it away. It concerns an item.
	Share Action = "share"
	// ManageAccess is deciding who has access to a shared folder as a whole, and in which role. It concerns the place's
	// top folder.
	ManageAccess Action = "manage-access"
	// DeleteSharedFolder is moving a whole shared folder, with everything in it, into the trash.
	DeleteSharedFolder Action = "delete-shared-folder"
	// RestoreSharedFolder is bringing a shared folder back out of the trash. Whoever could have deleted it may.
	RestoreSharedFolder Action = "restore-shared-folder"

	// MakeLink is making a public link to a file or folder, or a new one in place of the one it has (whose address then
	// stops working): whoever may share it, while sharing outside the project is on there. It concerns an item.
	MakeLink Action = "make-link"
	// ManageLink is seeing the public link on a file or folder (who made it, when, and how often it was used) and
	// removing it: whoever may share it, also while sharing outside the project is off. It concerns an item.
	ManageLink Action = "manage-link"
	// InviteGuest is inviting a guest to a file or folder by their email address, or changing the role or the end date
	// of an invitation there: whoever may share it, while sharing outside the project is on there. (Taking an invitation
	// away is sharing, Share, also while it is off.) It concerns an item.
	InviteGuest Action = "invite-guest"

	// SeeThroughLink is, for a visitor, seeing an item through the public link they came with: a file's name and size,
	// or a folder's name and what is in it. It concerns the link's item or anything inside it.
	SeeThroughLink Action = "see-through-link"
	// DownloadThroughLink is, for a visitor, reading content through their link: downloading a file or opening it in
	// the browser, or downloading a folder as one zip.
	DownloadThroughLink Action = "download-through-link"

	// SignInAsGuest is asking for a code mailed to an address, signing in with it, and loading the guests' pages: anyone
	// who is not a member, under the guests' part of the public prefix. It concerns no place.
	SignInAsGuest Action = "sign-in-as-guest"
	// EnterAsGuest is, for a signed-in guest, using their pages at all: who they are, their "Shared with me", signing
	// out. It concerns no place.
	EnterAsGuest Action = "enter-as-guest"
	// ListAsGuest is, for a guest, seeing a file or a folder given to them, or anything inside it: its name, and what is
	// in a folder. DownloadAsGuest is reading its content: a file downloaded or opened in the browser, a folder as one
	// zip.
	ListAsGuest     Action = "list-as-guest"
	DownloadAsGuest Action = "download-as-guest"
	// UploadAsGuest and MakeFolderAsGuest are, for a guest editor, putting a new file (or a part of one) into a folder
	// given to them or inside it, and making a folder there.
	UploadAsGuest     Action = "upload-as-guest"
	MakeFolderAsGuest Action = "make-folder-as-guest"
	// RenameAsGuest, MoveAsGuest and DeleteAsGuest are, for a guest editor, renaming, moving (inside what was given to
	// them) and deleting into the place's trash what is inside a folder given to them: as for a member, they need the
	// right to upload into the folder it is in, so what they were given itself stays where its giver put it.
	RenameAsGuest Action = "rename-as-guest"
	MoveAsGuest   Action = "move-as-guest"
	DeleteAsGuest Action = "delete-as-guest"
)

// InAPlace is every action a member may do in a place, in the order an interface lists them.
var InAPlace = []Action{List, Download, Upload, MakeFolder, Rename, Move, MoveOut, MoveIn, Delete, SeeTrash, Restore,
	SeeAccess, Share, MakeLink, ManageLink, InviteGuest, ManageAccess, SwitchSharingOutside, DeleteSharedFolder, RestoreSharedFolder}

// Nowhere is every action that concerns no place only. (SwitchSharingOutside concerns no place for the whole project,
// and is among InAPlace for a shared folder.)
var Nowhere = []Action{Enter, SeeUsage, MakeSharedFolder, Oversee}

// ThroughALink is every action a visitor may do through a public link, and only a visitor.
var ThroughALink = []Action{SeeThroughLink, DownloadThroughLink}

// AsAGuest is every action a guest may do, and only a guest (SignInAsGuest also anyone who is not a member).
var AsAGuest = []Action{SignInAsGuest, EnterAsGuest, ListAsGuest, DownloadAsGuest, UploadAsGuest, MakeFolderAsGuest,
	RenameAsGuest, MoveAsGuest, DeleteAsGuest}

// AsAGuestAtAnItem is every action a guest may do at an item, in the order an interface lists them.
var AsAGuestAtAnItem = []Action{ListAsGuest, DownloadAsGuest, UploadAsGuest, MakeFolderAsGuest, RenameAsGuest,
	MoveAsGuest, DeleteAsGuest}

// Decide says whether person may do action at place: in its top folder when place.Path is empty, and at the last item
// of the path otherwise. place is nil for actions that concern no place. Anything not explicitly allowed is refused.
func Decide(person Person, action Action, place *Place) bool {
	if person.Link != nil {
		return throughLink(*person.Link, action, place)
	}
	if person.Guest != nil {
		return asGuest(*person.Guest, action, place)
	}
	if action == SignInAsGuest {
		// Asking for a code is for anyone outside the project; a member signs in through the reverse proxy.
		return place == nil && !person.isMember()
	}
	if !person.isMember() {
		return false
	}
	if place == nil {
		switch action {
		case Enter, SeeUsage, MakeSharedFolder:
			return true
		case Oversee, SwitchSharingOutside:
			// For the whole project: its admins.
			return person.Role == members.RoleAdmin
		}
		return false
	}
	role := RoleAt(person, place)
	if place.Deleted {
		return place.Kind == SharedFolder && len(place.Path) == 0 && action == RestoreSharedFolder && role == Manager
	}
	switch action {
	case MoveIn:
		// As an upload into the folder, and only into a shared folder: what comes in from elsewhere becomes the project's.
		return place.Kind == SharedFolder && Decide(person, Upload, place)
	case MoveOut:
		// Only out of one's own My files, by whoever may move it there: its owner.
		return len(place.Path) > 0 && role == Owner && Decide(person, Move, place)
	case MakeLink:
		// A link is sharing with anyone who has it: for whoever may share the item, and not where sharing outside the
		// project is off.
		return !place.NoSharingOutside && Decide(person, ManageLink, place)
	case ManageLink:
		return len(place.Path) > 0 && Decide(person, Share, place)
	case InviteGuest:
		// Inviting someone outside the project: as a link, for whoever may share the item, not where sharing outside the
		// project is off.
		return !place.NoSharingOutside && len(place.Path) > 0 && Decide(person, Share, place)
	}
	if len(place.Path) == 0 {
		return atTop[role][action]
	}
	if !atItem[role][action] {
		return false
	}
	switch action {
	case Rename, Move, Delete, Restore:
		// A change to the folder the item is in, or goes back to: as an upload into it. Access only widens down the
		// tree, so whoever may do that is an editor of the item too.
		if !Decide(person, Upload, place.Up(len(place.Path)-1)) {
			return false
		}
	}
	if action == Restore {
		// The trash of a place is its own: someone given part of a place restores only what they put there.
		return place.DeletedBy == person.UserID || Decide(person, SeeTrash, place.Up(0))
	}
	return true
}

// SwitchedOff says whether the person may not do action at place only because sharing outside the project is switched
// off there: with it on, they would be let in. That is whoever may share an item, making a link to it or inviting a
// guest; a visitor whose link would open it; a guest whose invitation would let them in. For anyone else, and for
// anything that would be refused anyway, it is false, so that saying so tells nobody more than they could see with it
// on.
func SwitchedOff(person Person, action Action, place *Place) bool {
	if place == nil || !place.NoSharingOutside || Decide(person, action, place) {
		return false
	}
	on := *place
	on.NoSharingOutside = false
	return Decide(person, action, &on)
}

// SharedWithMe says whether "Shared with me" lists, for the person, the live item place is about: they may open it, and
// not the folder it is in. Access only widens down the tree, so their access starts right there, given on the item
// itself to them or to everyone in the project, and they can open nothing above it: the place is not among theirs
// (their own My files, a shared folder they have a role on as a whole), and the names above it are not theirs to see.
// What is inside a shared folder is reached by opening it, and is not listed again, whatever role was given on it.
//
// Whatever no longer counts is not listed, as nothing else is open to them: a held My files, an item in a shared folder
// in the trash, a grant to someone else.
//
// For a guest it is the same, with what a guest may open: the highest point of a place an invitation to them reaches.
func SharedWithMe(person Person, place *Place) bool {
	if place == nil || len(place.Path) == 0 || place.Deleted {
		return false
	}
	list := List
	if person.Link == nil && person.Guest != nil {
		list = ListAsGuest
	}
	return Decide(person, list, place) && !Decide(person, list, place.Up(len(place.Path)-1))
}

// Finds says whether a search by name finds, for the person, the live item place is about: anything they may open,
// wherever it is (their own My files, a shared folder they have a role on, what was shared with them in part of a
// place), and nothing else: not what is beside or above what they were given, nothing in a held My files, nothing in a
// shared folder in the trash. A place's top folder is never a result: shared folders are found by name among the
// person's places.
func Finds(person Person, place *Place) bool {
	if place == nil || len(place.Path) == 0 || place.Deleted {
		return false
	}
	return Decide(person, List, place)
}

// throughLink decides for a visitor who came with link: only what the link lets them do, at its item and anything
// inside it, while it counts there. Nothing else, and none of what members do, whoever they are otherwise.
func throughLink(link Link, action Action, place *Place) bool {
	if place == nil || place.Deleted || place.NoSharingOutside || link.Item <= 0 {
		return false
	}
	switch place.Kind {
	case MyFiles:
		if place.Owner == "" || place.OwnerLeft {
			return false // a held My files: nothing shared from it opens
		}
	case SharedFolder:
	default:
		return false
	}
	// The link's item is the top of what its visitors see: it must be on the way down to what they ask for. Path is
	// from the place's top folder down, so whatever comes after it is inside it, and nothing beside or above it has it.
	at := slices.Index(place.Path, link.Item)
	if at < 0 {
		return false
	}
	// In a My files the link is its maker's share, made from their access there: it counts while they may still share
	// the item, and stops when that is taken away from them or they leave the project. A shared folder belongs to the
	// project, and so do the links made in it.
	if place.Kind == MyFiles && !Decide(link.By, Share, place.Up(at+1)) {
		return false
	}
	switch link.Kind {
	case ViewLink:
		return action == SeeThroughLink || action == DownloadThroughLink
	}
	return false
}

// asGuest decides for a guest: only what the actions for guests let them do, at an item given to them or anything
// inside it, by the role their invitations give them there (guestRole). Nothing at a place's top folder, and none of
// what members or visitors do.
func asGuest(guest Guest, action Action, place *Place) bool {
	switch action {
	case SignInAsGuest, EnterAsGuest:
		return place == nil
	}
	role := guestRole(guest, place)
	switch action {
	case ListAsGuest, DownloadAsGuest:
		return role == Viewer || role == Editor
	case UploadAsGuest, MakeFolderAsGuest:
		return role == Editor
	case RenameAsGuest, MoveAsGuest, DeleteAsGuest:
		// A change to the folder the item is in: as an upload into it, as for a member. What they were given itself is
		// in a folder they cannot open, so it stays where its giver put it, and a clashing name tells them nothing.
		return role == Editor && asGuest(guest, UploadAsGuest, place.Up(len(place.Path)-1))
	}
	return false
}

// guestRole is a guest's role at the item place is about: the highest of the invitations to them on it or on a folder
// above it that count there. An invitation counts in a live place (not a held My files, not a shared folder in the
// trash) where sharing outside the project is on, before its end date (Place.Now); in a My files only while its giver
// may still share its item there, as a link. Never anything at a place's top folder.
func guestRole(guest Guest, place *Place) Role {
	if place == nil || place.Deleted || place.NoSharingOutside || guest.ID <= 0 || len(place.Path) == 0 {
		return NoRole
	}
	switch place.Kind {
	case MyFiles:
		if place.Owner == "" || place.OwnerLeft {
			return NoRole // a held My files: nothing shared from it opens
		}
	case SharedFolder:
	default:
		return NoRole
	}
	best := NoRole
	for _, g := range place.Grants {
		if g.Guest != guest.ID || g.On <= 0 || !Counts(place.Kind, g) || !live(place, g) || rank[g.Role] <= rank[best] {
			continue
		}
		at := slices.Index(place.Path, g.On)
		if at < 0 {
			continue // not on the way down to the item: it reaches neither a parent nor a sibling
		}
		if place.Kind == MyFiles && !Decide(g.By, Share, place.Up(at+1)) {
			continue // its giver may no longer share it there, or has left the project
		}
		best = g.Role
	}
	return best
}

// live says whether a grant has not ended at the time of the decision. A grant with an end date counts only when that
// time is known, and before it.
func live(place *Place, g Grant) bool {
	return g.Ends.IsZero() || (!place.Now.IsZero() && place.Now.Before(g.Ends))
}

// Guests is every guest who can see what place is about now (whose role there is not NoRole), by id, in the order
// their invitations come in place.Grants.
func Guests(place *Place) []int64 {
	if place == nil {
		return nil
	}
	var out []int64
	for _, g := range place.Grants {
		if g.Guest > 0 && !slices.Contains(out, g.Guest) && guestRole(Guest{ID: g.Guest}, place) != NoRole {
			out = append(out, g.Guest)
		}
	}
	return out
}

// ByLink says whether anyone with a public link can see what place is about: a link on it, or on a folder above it,
// among place.Links, that lets its visitors in there now.
func ByLink(place *Place) bool {
	if place == nil {
		return false
	}
	for i := range place.Links {
		if Decide(Person{Link: &place.Links[i]}, SeeThroughLink, place) {
			return true
		}
	}
	return false
}

// What each role may do in a place's top folder. The owner of a "My files" has nothing to manage and nobody to show
// there: it is theirs alone, and only items in it are shared.
var atTop = map[Role]map[Action]bool{
	Viewer:  actions(List, Download, SeeAccess),
	Editor:  actions(List, Download, SeeAccess, Upload, MakeFolder, SeeTrash),
	Manager: actions(List, Download, SeeAccess, Upload, MakeFolder, SeeTrash, ManageAccess, SwitchSharingOutside, DeleteSharedFolder),
	Owner:   actions(List, Download, Upload, MakeFolder, SeeTrash),
}

// What each role may do at an item. A manager of a shared folder, and the owner of a "My files", are editors of
// everything in it. Renaming, moving, deleting and restoring also need the right to put things into the item's folder
// (see Decide).
var atItem = map[Role]map[Action]bool{
	Viewer:  asViewer,
	Editor:  asEditor,
	Manager: asEditor,
	Owner:   asEditor,
}

var (
	asViewer = actions(List, Download, SeeAccess)
	asEditor = actions(List, Download, SeeAccess, Upload, MakeFolder, Rename, Move, Delete, Restore, Share)
)

// RoleAt is the person's role at place: at the last item of its path, or in its top folder when the path is empty.
// It is the one place the Drive works it out:
//
//   - in their own "My files", its owner is the owner, everywhere in it;
//   - in a shared folder, everyone holding the admin role on the project is a manager, whatever they were given;
//   - otherwise it is the highest role given to the person, or to everyone in the project, that counts (see Counts)
//     on the top folder or on an item of the path. Access only widens down the tree: something given lower down adds
//     to what was given above, and never takes it away.
//
// It is NoRole for someone who is not a member, in a held "My files", and in a place of a kind access does not know. For
// a guest it is what their invitations give them there (see guestRole); a visitor has none.
func RoleAt(person Person, place *Place) Role {
	if person.Link == nil && person.Guest != nil {
		return guestRole(*person.Guest, place)
	}
	if !person.isMember() || place == nil {
		return NoRole
	}
	switch place.Kind {
	case MyFiles:
		if place.Owner == "" || place.OwnerLeft {
			return NoRole
		}
		if place.Owner == person.UserID {
			return Owner
		}
	case SharedFolder:
		if person.Role == members.RoleAdmin {
			return Manager
		}
	default:
		return NoRole
	}
	return given(place, func(g Grant) bool { return g.Everyone || (g.Member != "" && g.Member == person.UserID) })
}

// ToEveryone is the role every member has at place through access given to everyone in the project alone.
func ToEveryone(place *Place) Role {
	if place == nil || (place.Kind != MyFiles && place.Kind != SharedFolder) || (place.Kind == MyFiles && (place.Owner == "" || place.OwnerLeft)) {
		return NoRole
	}
	return given(place, func(g Grant) bool { return g.Everyone })
}

// given is the highest role among the grants that count at place (on its top folder or an item of its path, and not
// ended) and that to accepts.
func given(place *Place, to func(Grant) bool) Role {
	onPath := make(map[int64]bool, len(place.Path))
	for _, id := range place.Path {
		if id > 0 {
			onPath[id] = true
		}
	}
	best := NoRole
	for _, g := range place.Grants {
		if (g.On == 0 || onPath[g.On]) && Counts(place.Kind, g) && live(place, g) && to(g) && rank[g.Role] > rank[best] {
			best = g.Role
		}
	}
	return best
}

// rank orders the roles that can be given.
var rank = map[Role]int{NoRole: 0, Viewer: 1, Editor: 2, Manager: 3}

func actions(list ...Action) map[Action]bool {
	m := make(map[Action]bool, len(list))
	for _, a := range list {
		m[a] = true
	}
	return m
}
