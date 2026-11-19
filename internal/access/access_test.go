package access

import (
	"fmt"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/members"
)

// The table grows with every kind of person, place, role and action: add each new one to the lists below, and every
// combination is tried. Only the combinations in allowed may pass; everything else must be refused.
func TestDecide(t *testing.T) {
	reka := Person{UserID: "u-reka", Role: members.RoleMember}
	admin := Person{UserID: "u-admin", Role: members.RoleAdmin}
	nobody := Person{}

	rekasFiles := &Place{Kind: MyFiles, Owner: "u-reka"}
	adminsFiles := &Place{Kind: MyFiles, Owner: "u-admin"}
	ownerless := &Place{Kind: MyFiles}

	people := map[string]Person{"member": reka, "admin": admin, "no user id": nobody}
	places := map[string]*Place{"no place": nil, "Réka's My files": rekasFiles, "the admin's My files": adminsFiles, "My files without an owner": ownerless}
	actions := []Action{Enter, SeeUsage, List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore}

	// Every combination that is allowed. Everything else must be refused.
	allowed := map[string]bool{
		"member enter no place":     true,
		"admin enter no place":      true,
		"member see-usage no place": true,
		"admin see-usage no place":  true,
	}
	// In their own My files, a member may do everything; nobody else may do anything there, admins included.
	for _, a := range []Action{List, Download, Upload, MakeFolder, Rename, Move, Delete, SeeTrash, Restore} {
		allowed[fmt.Sprintf("member %s Réka's My files", a)] = true
		allowed[fmt.Sprintf("admin %s the admin's My files", a)] = true
	}

	for pn, p := range people {
		for ln, l := range places {
			for _, a := range actions {
				key := fmt.Sprintf("%s %s %s", pn, a, ln)
				if got := Decide(p, a, l); got != allowed[key] {
					t.Errorf("%s: got %v, want %v", key, got, allowed[key])
				}
				delete(allowed, key)
			}
		}
	}
	for key := range allowed {
		t.Errorf("the table allows %q, which is not a combination the test tried", key)
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	p := Person{UserID: "u1", Role: members.RoleAdmin}
	if Decide(p, Action("share"), &Place{Kind: MyFiles, Owner: "u1"}) {
		t.Fatal("an action without a rule must be refused")
	}
}
