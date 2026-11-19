package server

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// "Shared with me": what others shared with the person that they reach only through that share. Its route is in
// routes.go with all the others.

// personJSON is a member as a list names them.
type personJSON struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

func personJSONOf(m members.Member) *personJSON {
	return &personJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName}
}

// sharedJSON is one item of "Shared with me": the item as a listing gives it (what the person may do with it, who can
// see it), the place it is in by id and kind only (never a shared folder's name: the person cannot open it), whose My
// files it is in, their role at it, and who shared it with them and when.
type sharedJSON struct {
	listedJSON
	Place     int64       `json:"place"`
	PlaceKind string      `json:"placeKind"` // "my-files" or "shared-folder"
	Owner     *personJSON `json:"owner,omitempty"`
	Role      access.Role `json:"role"`
	// SharedBy is who gave the access on it that the person has (to them by name before to everyone), when they are
	// still in the member list; ToEveryone is true when it was given to everyone in the project. Shared is when.
	SharedBy   *personJSON `json:"sharedBy,omitempty"`
	ToEveryone bool        `json:"toEveryone,omitempty"`
	Shared     time.Time   `json:"shared"`
}

// sharedWithMe lists what was shared with the person, by name or with everyone in the project, that they reach only
// through that share: in someone else's My files, or in a shared folder they have no role on as a whole. Each is the
// highest item they can open on its way down (access.SharedWithMe): what is inside a shared folder is reached by opening
// it, and is not listed again, whatever was given on it. Nothing in their own My files, or in a shared folder in their
// places, is listed. Whatever no longer counts (a held My files, an item or a shared folder in the trash, an item gone
// from the storage) is left out, as the access package decides it on the whole way down to each item, every time.
// The most recently shared first.
//
//	GET /api/shared-with-me
func (s *Server) sharedWithMe(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()
	granted, err := s.drive.GrantedTo(ctx, c.member.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	list := s.members.Current()
	places := map[int64]*index.Place{}
	out := []sharedJSON{}
	for _, g := range granted {
		p, seen := places[g.Place]
		if !seen {
			found, err := s.drive.Place(ctx, g.Place)
			switch {
			case errors.Is(err, index.ErrNoPlace):
			case err != nil:
				s.fail(w, r, err)
				return
			default:
				p = &found
			}
			places[g.Place] = p
		}
		if p == nil {
			continue // a shared folder in the trash, or gone for good
		}
		it, err := s.drive.Item(ctx, *p, g.Item)
		if errors.Is(err, index.ErrNoItem) {
			continue // in the trash, inside a folder in the trash, or gone
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		path := idsOf(pathTo(it))
		grants, err := s.drive.GrantsOn(ctx, *p, path)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		where := s.accessPlace(*p, path, grants)
		if !access.SharedWithMe(who, where) {
			continue
		}
		e := sharedJSON{
			listedJSON: listedJSON{itemJSON: itemOf(it), Can: can(who, where), SeenBy: seenBy(list, where)},
			Place:      p.ID, PlaceKind: string(p.Kind), Role: access.RoleAt(who, where),
		}
		if p.Kind == index.MyFiles {
			if m, ok := list.Lookup(p.Owner); ok {
				e.Owner = personJSONOf(m)
			}
		}
		// Their access starts at this item, so it was given on it: to them by name, or to everyone.
		var by *index.Grant
		for i, g := range grants {
			mine := g.Item == it.ID && (g.Member == c.member.UserID || g.Everyone) && access.Counts(where.Kind, accessGrant(g))
			if mine && (by == nil || (by.Everyone && !g.Everyone)) {
				by = &grants[i]
			}
		}
		if by != nil {
			e.ToEveryone, e.Shared = by.Everyone, by.Given
			if m, ok := list.Lookup(by.By); ok {
				e.SharedBy = personJSONOf(m)
			}
		}
		out = append(out, e)
	}
	slices.SortStableFunc(out, func(a, b sharedJSON) int { return b.Shared.Compare(a.Shared) })
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
