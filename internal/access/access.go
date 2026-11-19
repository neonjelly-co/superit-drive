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
//   - A viewer may open, download and see who has access; an editor may also upload, make folders, rename, move and
//     delete, restore what it could have deleted, and share further. Only managers decide who has access to a shared
//     folder as a whole, and only they may delete it.
//   - A shared folder in the trash can only be restored, by a manager.
//   - A "My files" whose owner has left the member list is held: nobody may do anything there.
//
// Guests and public links add their rules here.
package access

import "github.com/neonjelly-co/superit-drive/internal/members"

// A Person is who is asking. Only members exist so far.
type Person struct {
	// UserID is the member's stable user id from the member list.
	UserID string
	// Role is the member's role on the project, from the member list: admin or member. Anything else means the person
	// is not in the member list, and may do nothing.
	Role members.Role
}

// isMember says whether the person is in the member list: only members may do anything, and only they are "everyone
// in the project".
func (p Person) isMember() bool {
	return p.UserID != "" && (p.Role == members.RoleAdmin || p.Role == members.RoleMember)
}

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
	// Member is the user id of the member it was given to. It is empty for a grant to everyone in the project.
	Member string
	// Everyone is true for a grant to everyone in the project: every member, for as long as they are one.
	Everyone bool
	Role     Role
}

// Counts says whether a grant counts for anything in a place of the given kind. These are also the only grants that
// can be given:
//
//   - viewer or editor, on any item, to a named member or to everyone in the project;
//   - on a shared folder as a whole (its top folder), viewer, editor or manager to a named member, and viewer or editor
//     to everyone;
//   - nothing on someone's "My files" as a whole: it is theirs alone, and only items in it are shared.
//
// Anything else (an unknown role, a manager on an item, a grant to nobody) counts for nothing.
func Counts(kind PlaceKind, g Grant) bool {
	if g.Everyone == (g.Member != "") {
		return false // to everyone and to a member at once, or to nobody
	}
	if g.On < 0 {
		return false
	}
	if g.On > 0 {
		return (kind == MyFiles || kind == SharedFolder) && (g.Role == Viewer || g.Role == Editor)
	}
	if kind != SharedFolder {
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
	// Grants is the access given in this place. Only what was given on its top folder (On 0) or on an item of Path
	// counts: access given on one item never reaches its parent or its siblings. A caller may pass more.
	Grants []Grant
}

// Below is the same place, about the item id directly inside the item p is about (or inside its top folder), with
// the access given on that item added: how a listing describes each item in a folder.
func (p *Place) Below(id int64, given []Grant) *Place {
	q := *p
	q.Path = append(append(make([]int64, 0, len(p.Path)+1), p.Path...), id)
	q.Grants = append(append(make([]Grant, 0, len(p.Grants)+len(given)), p.Grants...), given...)
	return &q
}

// Up is the same place, about the folder depth levels down from its top folder on the way to the item (0: the top
// folder itself), with the same access given: how a path is checked one folder at a time.
func (p *Place) Up(depth int) *Place {
	q := *p
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

	// List is seeing what is in a folder: the place's top folder, or a folder item.
	List Action = "list"
	// Download is reading a file's content. It concerns an item.
	Download Action = "download"
	// Upload is putting a new file into a folder (the top folder, or a folder item), which can replace a file there
	// (the replaced one goes to the trash), or keep both under a new name. Moving something into a folder needs it too.
	Upload Action = "upload"
	// MakeFolder is making a new folder in a folder.
	MakeFolder Action = "make-folder"
	// Rename is giving a file or folder a new name. It concerns an item.
	Rename Action = "rename"
	// Move is taking a file or folder out of its folder, to put it into another folder of the same place, where the
	// person must be allowed to Upload. (Moving between places needs a decision on both, and comes with the move
	// warning.) It concerns an item.
	Move Action = "move"
	// Delete is moving a file or folder into its place's trash. It concerns an item.
	Delete Action = "delete"
	// SeeTrash is seeing what is in a place's trash. It concerns the place's top folder.
	SeeTrash Action = "see-trash"
	// Restore is moving an item out of its place's trash, back into the folder it was deleted from. Whoever could have
	// deleted it may. It concerns an item in the trash.
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
)

// InAPlace is every action that happens in a place, in the order an interface lists them.
var InAPlace = []Action{List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore,
	SeeAccess, Share, ManageAccess, DeleteSharedFolder, RestoreSharedFolder}

// Decide says whether person may do action at place: in its top folder when place.Path is empty, and at the last item
// of the path otherwise. place is nil for actions that concern no place. Anything not explicitly allowed is refused.
func Decide(person Person, action Action, place *Place) bool {
	if !person.isMember() {
		return false
	}
	if place == nil {
		return action == Enter || action == SeeUsage || action == MakeSharedFolder
	}
	role := RoleAt(person, place)
	if place.Deleted {
		return place.Kind == SharedFolder && len(place.Path) == 0 && action == RestoreSharedFolder && role == Manager
	}
	if len(place.Path) == 0 {
		return atTop[role][action]
	}
	return atItem[role][action]
}

// What each role may do in a place's top folder. The owner of a "My files" has nothing to manage and nobody to show
// there: it is theirs alone, and only items in it are shared.
var atTop = map[Role]map[Action]bool{
	Viewer:  actions(List, SeeAccess),
	Editor:  actions(List, SeeAccess, Upload, MakeFolder, SeeTrash),
	Manager: actions(List, SeeAccess, Upload, MakeFolder, SeeTrash, ManageAccess, DeleteSharedFolder),
	Owner:   actions(List, Upload, MakeFolder, SeeTrash),
}

// What each role may do at an item. A manager of a shared folder, and the owner of a "My files", are editors of
// everything in it.
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
// It is NoRole for someone who is not a member, in a held "My files", and in a place of a kind access does not know.
func RoleAt(person Person, place *Place) Role {
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
	return given(place, func(g Grant) bool { return g.Everyone || g.Member == person.UserID })
}

// ToEveryone is the role every member has at place through access given to everyone in the project alone.
func ToEveryone(place *Place) Role {
	if place == nil || (place.Kind != MyFiles && place.Kind != SharedFolder) || (place.Kind == MyFiles && (place.Owner == "" || place.OwnerLeft)) {
		return NoRole
	}
	return given(place, func(g Grant) bool { return g.Everyone })
}

// given is the highest role among the grants that count at place (on its top folder or an item of its path) and that
// to accepts.
func given(place *Place, to func(Grant) bool) Role {
	onPath := make(map[int64]bool, len(place.Path))
	for _, id := range place.Path {
		if id > 0 {
			onPath[id] = true
		}
	}
	best := NoRole
	for _, g := range place.Grants {
		if (g.On == 0 || onPath[g.On]) && Counts(place.Kind, g) && to(g) && rank[g.Role] > rank[best] {
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
