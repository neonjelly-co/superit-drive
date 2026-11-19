// Package access is the Drive's one authorisation function. Every route asks Decide before it does anything, and
// nothing else in the Drive decides who may do what. (The survey of other drives found the same "escape the shared
// folder" hole in six projects, each caused by permission checks scattered over the code.)
//
// The package knows nothing about HTTP, the index or the storage. Callers describe who is asking, what they want to do
// and where, and get a yes or a no. That keeps the rules in one place where a table test can cover every case.
//
// Version 1 so far: a member may use the Drive and may list their own "My files". Shared folders, sharing, guests and
// public links add their rules here.
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
)

// Decide says whether person may do action in place. place is nil for actions that concern no place.
// Anything not explicitly allowed is refused.
func Decide(person Person, action Action, place *Place) bool {
	if person.UserID == "" {
		return false
	}
	switch action {
	case Enter:
		return place == nil
	case List:
		return place != nil && ownsMyFiles(person, place)
	}
	return false
}

// ownsMyFiles is true when place is the person's own "My files". Nobody else may open it: not other members and not
// project admins.
func ownsMyFiles(person Person, place *Place) bool {
	return place.Kind == MyFiles && place.Owner != "" && place.Owner == person.UserID
}
