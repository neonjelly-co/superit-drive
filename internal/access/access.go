// Package access is the Drive's one authorisation function. Every route asks Decide before it does anything, and
// nothing else in the Drive decides who may do what. (The survey of other drives found the same "escape the shared
// folder" hole in six projects, each caused by permission checks scattered over the code.)
//
// The package knows nothing about HTTP, the index or the storage. Callers describe who is asking, what they want to do
// and where, and get a yes or a no. That keeps the rules in one place where a table test can cover every case.
//
// Version 1 so far: a member may use the Drive and see about how much space it uses, and may do everything in their own
// "My files": list, download, upload, make folders, rename, move, delete into the trash, and see and restore from its
// trash. Shared folders, sharing, guests and public links add their rules here.
package access

import "github.com/neonjelly-co/superit-drive/internal/members"

// A Person is who is asking. Only members exist so far.
type Person struct {
	// UserID is the member's stable user id from the member list.
	UserID string
	// Role is the member's role on the project.
	Role members.Role
}

// PlaceKind is the kind of a place: a member's "My files" (shared folders come later).
type PlaceKind string

const MyFiles PlaceKind = "my-files"

// A Place is a top-level place in the Drive, as far as access is concerned.
type Place struct {
	Kind PlaceKind
	// Owner is the user id of the member whose "My files" this is.
	Owner string
}

// An Action is something a person asks to do.
type Action string

const (
	// Enter is using the Drive at all: loading the interface, asking who you are, seeing which places you have.
	// It concerns no place.
	Enter Action = "enter"
	// List is seeing what is in a folder of a place.
	List Action = "list"
	// Download is reading a file's content. A viewer may, once sharing exists ("open and download").
	Download Action = "download"
	// Upload is putting a new file into a folder of a place, which can replace a file there (the replaced one goes to
	// the trash). An editor may, once sharing exists; a viewer may not.
	Upload Action = "upload"
	// MakeFolder is making a new folder in a folder of a place. An editor may, once sharing exists.
	MakeFolder Action = "make-folder"
	// Rename is giving a file or folder a new name. An editor may, once sharing exists.
	Rename Action = "rename"
	// Move is putting a file or folder into another folder of the same place. An editor may, once sharing exists.
	// (Moving between places needs a decision on both, and comes with shared folders.)
	Move Action = "move"
	// Delete is moving a file or folder into its place's trash. An editor may, once sharing exists.
	Delete Action = "delete"
	// SeeTrash is seeing what is in a place's trash.
	SeeTrash Action = "see-trash"
	// Restore is moving something out of a place's trash, back into the place. Whoever could have deleted it may.
	Restore Action = "restore"
	// SeeUsage is seeing about how much space the Drive uses, as the sidebar's meter shows it. It concerns no place:
	// it is the whole project's figure, and says nothing about anyone's files.
	SeeUsage Action = "see-usage"
)

// Decide says whether person may do action in place. place is nil for actions that concern no place.
// Anything not explicitly allowed is refused.
func Decide(person Person, action Action, place *Place) bool {
	if person.UserID == "" {
		return false
	}
	switch action {
	case Enter, SeeUsage:
		return place == nil
	case List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore:
		return place != nil && ownsMyFiles(person, place)
	}
	return false
}

// ownsMyFiles is true when place is the person's own "My files". Nobody else may open it: not other members and not
// project admins.
func ownsMyFiles(person Person, place *Place) bool {
	return place.Kind == MyFiles && place.Owner != "" && place.Owner == person.UserID
}
