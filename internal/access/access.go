// Package access is the Drive's one authorisation function. Every route asks Decide before it does anything, and
// nothing else in the Drive decides who may do what. (The survey of other drives found the same "escape the shared
// folder" hole in six projects, each caused by permission checks scattered over the code.)
//
// The package knows nothing about HTTP, the index or the storage. Callers describe who is asking, what they want to do
// and where, and get a yes or a no. That keeps the rules in one place where a table test can cover every case.
//
// Version 1 so far:
//
//   - A member may use the Drive, see about how much space it uses, and make a shared folder.
//   - In their own "My files" a member may do everything: list, download, upload, make folders, rename, move, delete
//     into the trash, and see and restore from its trash. Nobody else may do anything there, project admins included.
//   - A shared folder belongs to the project, not to a person. What someone may do in it follows from their role
//     there: viewer, editor or manager, each able to do what the one before it can and more. Everyone holding the
//     admin role on the project is a manager of every shared folder. Without a role, a member may do nothing there.
//   - A shared folder in the trash can only be restored, by a manager.
//
// Sharing items with members, guests and public links add their rules here.
package access

import "github.com/neonjelly-co/superit-drive/internal/members"

// A Person is who is asking. Only members exist so far.
type Person struct {
	// UserID is the member's stable user id from the member list.
	UserID string
	// Role is the member's role on the project.
	Role members.Role
}

// PlaceKind is the kind of a place.
type PlaceKind string

const (
	// MyFiles is a member's own place, owned by them.
	MyFiles PlaceKind = "my-files"
	// SharedFolder is a top-level place owned by the project.
	SharedFolder PlaceKind = "shared-folder"
)

// A Role is what someone was given on a shared folder.
type Role string

const (
	NoRole Role = ""
	// Viewer may open and download.
	Viewer Role = "viewer"
	// Editor may also upload, make folders, rename, move and delete, and see and restore from the trash.
	Editor Role = "editor"
	// Manager may also decide who has access to the shared folder, and delete it.
	Manager Role = "manager"
)

// A Place is a top-level place in the Drive, as far as access is concerned, seen by the person asking.
type Place struct {
	Kind PlaceKind
	// Owner is the user id of the member whose "My files" this is. A shared folder has none: the project owns it, and
	// an Owner given for one counts for nothing.
	Owner string
	// Deleted is true for a shared folder that is in the trash.
	Deleted bool
	// Given is the role the person asking was given on this shared folder, as the index holds it, or NoRole. It counts
	// only on a shared folder: nobody is given anything on someone's "My files" (yet).
	Given Role
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

	// List is seeing what is in a folder of a place.
	List Action = "list"
	// Download is reading a file's content.
	Download Action = "download"
	// Upload is putting a new file into a folder of a place, which can replace a file there (the replaced one goes to
	// the trash), or keep both under a new name.
	Upload Action = "upload"
	// MakeFolder is making a new folder in a folder of a place.
	MakeFolder Action = "make-folder"
	// Rename is giving a file or folder a new name.
	Rename Action = "rename"
	// Move is putting a file or folder into another folder of the same place. (Moving between places needs a decision
	// on both, and comes with the move warning.)
	Move Action = "move"
	// Delete is moving a file or folder into its place's trash.
	Delete Action = "delete"
	// SeeTrash is seeing what is in a place's trash.
	SeeTrash Action = "see-trash"
	// Restore is moving something out of a place's trash, back into the place. Whoever could have deleted it may.
	Restore Action = "restore"

	// SeeAccess is seeing who has access to a shared folder, and in which role.
	SeeAccess Action = "see-access"
	// ManageAccess is giving someone a role on a shared folder, or taking it away.
	ManageAccess Action = "manage-access"
	// DeleteSharedFolder is moving a whole shared folder, with everything in it, into the trash.
	DeleteSharedFolder Action = "delete-shared-folder"
	// RestoreSharedFolder is bringing a shared folder back out of the trash. Whoever could have deleted it may.
	RestoreSharedFolder Action = "restore-shared-folder"
)

// InAPlace is every action that happens in a place, in the order an interface lists them.
var InAPlace = []Action{List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore,
	SeeAccess, ManageAccess, DeleteSharedFolder, RestoreSharedFolder}

// Decide says whether person may do action in place. place is nil for actions that concern no place.
// Anything not explicitly allowed is refused.
func Decide(person Person, action Action, place *Place) bool {
	if person.UserID == "" {
		return false
	}
	if place == nil {
		return action == Enter || action == SeeUsage || action == MakeSharedFolder
	}
	switch place.Kind {
	case MyFiles:
		return !place.Deleted && ownsMyFiles(person, place) && inMyFiles[action]
	case SharedFolder:
		role := RoleIn(person, place)
		if place.Deleted {
			return action == RestoreSharedFolder && role == Manager
		}
		return may(role, action)
	}
	return false
}

// inMyFiles is everything the owner of a "My files" may do there. There is nothing to manage: it is theirs alone.
var inMyFiles = actions(List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore)

// What each role may do in a shared folder, on top of what the roles before it may.
var (
	viewerMay  = actions(List, Download, SeeAccess)
	editorMay  = actions(Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore)
	managerMay = actions(ManageAccess, DeleteSharedFolder)
)

func may(role Role, action Action) bool {
	switch role {
	case Manager:
		return managerMay[action] || editorMay[action] || viewerMay[action]
	case Editor:
		return editorMay[action] || viewerMay[action]
	case Viewer:
		return viewerMay[action]
	}
	return false
}

// RoleIn is the person's role on a shared folder: manager for everyone holding the admin role on the project, whatever
// they were given, and otherwise what they were given there. It is NoRole anywhere else: "My files" belongs to its
// owner alone, and a role nobody knows counts for nothing.
func RoleIn(person Person, place *Place) Role {
	if person.UserID == "" || place == nil || place.Kind != SharedFolder {
		return NoRole
	}
	if person.Role == members.RoleAdmin {
		return Manager
	}
	switch place.Given {
	case Viewer, Editor, Manager:
		return place.Given
	}
	return NoRole
}

// ownsMyFiles is true when place is the person's own "My files". Nobody else may open it: not other members and not
// project admins.
func ownsMyFiles(person Person, place *Place) bool {
	return place.Kind == MyFiles && place.Owner != "" && place.Owner == person.UserID
}

func actions(list ...Action) map[Action]bool {
	m := make(map[Action]bool, len(list))
	for _, a := range list {
		m[a] = true
	}
	return m
}
