package access

import (
	"fmt"
	"slices"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Everyone the table tries. "member" is Réka, a member without the admin role; "admin" holds it.
var people = map[string]Person{
	"member":     {UserID: "u-reka", Role: members.RoleMember},
	"admin":      {UserID: "u-admin", Role: members.RoleAdmin},
	"no user id": {},
}

// Every place the table tries, as the person asking sees it: Given is the role the index says they were given there.
// The same place is listed once per role, so every person meets every role.
var places = map[string]*Place{
	"no place": nil,

	"Réka's My files":                {Kind: MyFiles, Owner: "u-reka"},
	"the admin's My files":           {Kind: MyFiles, Owner: "u-admin"},
	"Bence's My files":               {Kind: MyFiles, Owner: "u-bence"},
	"Bence's My files, given viewer": {Kind: MyFiles, Owner: "u-bence", Given: Viewer},
	"Bence's My files, given editor": {Kind: MyFiles, Owner: "u-bence", Given: Editor},
	// A role given on someone's My files counts for nothing: it is theirs alone, admins included.
	"Bence's My files, given manager": {Kind: MyFiles, Owner: "u-bence", Given: Manager},
	"My files without an owner":       {Kind: MyFiles},
	"Réka's My files, deleted":        {Kind: MyFiles, Owner: "u-reka", Deleted: true},

	"a shared folder, no role":         {Kind: SharedFolder},
	"a shared folder, given viewer":    {Kind: SharedFolder, Given: Viewer},
	"a shared folder, given editor":    {Kind: SharedFolder, Given: Editor},
	"a shared folder, given manager":   {Kind: SharedFolder, Given: Manager},
	"a shared folder, an unknown role": {Kind: SharedFolder, Given: "owner"},
	// A shared folder belongs to the project: who made it, or any Owner, counts for nothing.
	"a shared folder owned by Réka, no role":  {Kind: SharedFolder, Owner: "u-reka"},
	"a shared folder owned by the admin":      {Kind: SharedFolder, Owner: "u-admin"},
	"a deleted shared folder, no role":        {Kind: SharedFolder, Deleted: true},
	"a deleted shared folder, given viewer":   {Kind: SharedFolder, Deleted: true, Given: Viewer},
	"a deleted shared folder, given editor":   {Kind: SharedFolder, Deleted: true, Given: Editor},
	"a deleted shared folder, given manager":  {Kind: SharedFolder, Deleted: true, Given: Manager},
	"a deleted shared folder owned by Réka":   {Kind: SharedFolder, Deleted: true, Owner: "u-reka"},
	"a place of a kind access does not know":  {Kind: "team-folder", Owner: "u-reka", Given: Manager},
	"a place of no kind, owned and given all": {Owner: "u-reka", Given: Manager},
}

// Every action there is. A test below fails when one is added to the package without being added here.
var everyAction = []Action{Enter, SeeUsage, MakeSharedFolder, List, Download, Upload, MakeFolder, Rename, Move, Delete,
	SeeTrash, Restore, SeeAccess, ManageAccess, DeleteSharedFolder, RestoreSharedFolder}

// What the spec gives each role on a shared folder, each including the one before it.
var (
	inOwnMyFiles = []Action{List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore}
	asViewer     = []Action{List, Download, SeeAccess}
	asEditor     = append(slices.Clone(asViewer), Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore)
	asManager    = append(slices.Clone(asEditor), ManageAccess, DeleteSharedFolder)
)

// The table grows with every kind of person, place, role and action: add each new one to the lists above, and every
// combination is tried. Only the combinations in allowed may pass; everything else must be refused.
func TestDecide(t *testing.T) {
	allowed := map[string]bool{}
	allow := func(person, place string, actions ...Action) {
		for _, a := range actions {
			allowed[fmt.Sprintf("%s %s %s", person, a, place)] = true
		}
	}
	for _, p := range []string{"member", "admin"} {
		allow(p, "no place", Enter, SeeUsage, MakeSharedFolder)
	}
	// In their own My files a member may do everything; nobody else may do anything there, admins included, whatever
	// role they were given.
	allow("member", "Réka's My files", inOwnMyFiles...)
	allow("admin", "the admin's My files", inOwnMyFiles...)
	// In a shared folder, a member may do what their role allows, and nothing without one.
	allow("member", "a shared folder, given viewer", asViewer...)
	allow("member", "a shared folder, given editor", asEditor...)
	allow("member", "a shared folder, given manager", asManager...)
	// A project admin is a manager of every shared folder, whatever they were given.
	for _, place := range []string{"a shared folder, no role", "a shared folder, given viewer", "a shared folder, given editor",
		"a shared folder, given manager", "a shared folder, an unknown role", "a shared folder owned by Réka, no role",
		"a shared folder owned by the admin"} {
		allow("admin", place, asManager...)
	}
	// A shared folder in the trash can only be restored, by its managers and the project admins.
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

// The rules have to hang together, for every person and place the table knows. These are the decisions about the
// trash, replacing and roles that the spec leaves to be read from "whoever could have deleted it may restore it".
func TestTheRulesHangTogether(t *testing.T) {
	for pn, p := range people {
		for ln, l := range places {
			may := func(a Action) bool { return Decide(p, a, l) }
			where := fmt.Sprintf("%s in %s", pn, ln)
			// An upload can replace a file, which puts the one that was there into the trash: only someone who may delete
			// may upload. Keeping both is an upload too.
			if may(Upload) && !may(Delete) {
				t.Errorf("%s may upload, so replace, but not delete", where)
			}
			// Whoever could have deleted something may see the trash and restore it, and nobody else.
			if may(Delete) != may(Restore) || may(Delete) != may(SeeTrash) {
				t.Errorf("%s: delete %v, restore %v, see the trash %v: want all or none", where, may(Delete), may(Restore), may(SeeTrash))
			}
			// Nobody acts on what they cannot see.
			for _, a := range []Action{Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore, SeeAccess, ManageAccess, DeleteSharedFolder} {
				if may(a) && !may(List) {
					t.Errorf("%s may %s but not list", where, a)
				}
			}
			// Whoever decides who has access, or may delete the whole shared folder, may do everything else in it.
			if may(ManageAccess) || may(DeleteSharedFolder) {
				for _, a := range asManager {
					if !may(a) {
						t.Errorf("%s manages it but may not %s", where, a)
					}
				}
			}
			// Nobody but the owner may do anything in a "My files".
			if l != nil && l.Kind == MyFiles && l.Owner != p.UserID {
				for _, a := range everyAction {
					if may(a) {
						t.Errorf("%s may %s in someone else's My files", where, a)
					}
				}
			}
			// Whoever could delete a shared folder may restore it from the trash.
			if l != nil && l.Kind == SharedFolder && !l.Deleted {
				deleted := *l
				deleted.Deleted = true
				if may(DeleteSharedFolder) != Decide(p, RestoreSharedFolder, &deleted) {
					t.Errorf("%s: may delete it %v, may restore it %v", where, may(DeleteSharedFolder), Decide(p, RestoreSharedFolder, &deleted))
				}
			}
		}
	}
}

func TestRoleIn(t *testing.T) {
	member := people["member"]
	admin := people["admin"]
	cases := []struct {
		person Person
		place  *Place
		want   Role
	}{
		{member, &Place{Kind: SharedFolder}, NoRole},
		{member, &Place{Kind: SharedFolder, Given: Viewer}, Viewer},
		{member, &Place{Kind: SharedFolder, Given: Editor}, Editor},
		{member, &Place{Kind: SharedFolder, Given: Manager}, Manager},
		{member, &Place{Kind: SharedFolder, Given: "owner"}, NoRole},
		{member, &Place{Kind: SharedFolder, Owner: "u-reka"}, NoRole},
		{admin, &Place{Kind: SharedFolder}, Manager},
		{admin, &Place{Kind: SharedFolder, Given: Viewer}, Manager},
		{admin, &Place{Kind: SharedFolder, Deleted: true}, Manager},
		{admin, &Place{Kind: MyFiles, Owner: "u-bence", Given: Manager}, NoRole},
		{member, &Place{Kind: MyFiles, Owner: "u-reka", Given: Manager}, NoRole},
		{admin, nil, NoRole},
		{Person{Role: members.RoleAdmin}, &Place{Kind: SharedFolder}, NoRole},
	}
	for _, c := range cases {
		if got := RoleIn(c.person, c.place); got != c.want {
			t.Errorf("RoleIn(%+v, %+v) = %q, want %q", c.person, c.place, got, c.want)
		}
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
	for _, place := range []*Place{nil, {Kind: MyFiles, Owner: "u1"}, {Kind: SharedFolder, Given: Manager}} {
		if Decide(admin, Action("share"), place) {
			t.Fatalf("an action without a rule must be refused (in %+v)", place)
		}
	}
}
