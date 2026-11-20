package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
)

// Search by name, across everything the person can see and nothing else. Its route is in routes.go with all the
// others.
//
// What a search could tell about names the person cannot see, and how it does not:
//
//   - It looks only where they can see everything (their own My files, the shared folders they have a role on, and
//     with everything in it each item shared with them in a place they cannot open, as "Shared with me" lists them),
//     each decided through access before the index is asked. So the work it does, and the time it takes, depends only
//     on what they can see: a name they cannot see is never read, let alone matched.
//   - Every result is decided again through access (access.Finds), on the whole way down to it, as any request is.
//   - The count, the order and "more" come only from what is found there: the best matches first, at most searchLimit
//     of them, and more says a longer search would find others. There are no pages to step through.
//   - A result's path names only the folders above it that the person can open; a shared folder's name only when they
//     can open it.

// searchLimit is the most results a search answers with.
const searchLimit = 50

// foundJSON is one result of a search: the item as a listing gives it (what the person may do with it, who can see
// it), its place by id and kind, their role at it, and where it is as they may see it.
type foundJSON struct {
	listedJSON
	Place     int64  `json:"place"`
	PlaceKind string `json:"placeKind"` // "my-files" or "shared-folder"
	// PlaceName is the shared folder's name, when the person can open it as a whole (FromTop); never otherwise.
	PlaceName string `json:"placeName,omitempty"`
	// Owner is whose My files it is in, when that is not the person's own.
	Owner *personJSON `json:"owner,omitempty"`
	Role  access.Role `json:"role"`
	// Path is the folders it is in that the person can open, from the highest of them down to its own; empty when it is
	// in the place's top folder, or when they can open none of them (it was shared with them on its own). FromTop says
	// whether the path starts at the place's top folder.
	Path    []folderJSON `json:"path"`
	FromTop bool         `json:"fromTop"`
}

// search finds files and folders by name (not by what is in them) across everything the person can see: in any case,
// with or without accents (index.SearchKey), each word searched for somewhere in the name. It answers with the best
// matches first, at most searchLimit; more is true when there are others, which a longer search finds.
//
//	GET /api/search?q=<part of a name>
//	200 {"items": [{"id", "name", "kind", "size", "changed", "opens", "can", "seenBy",    the item, as a listing gives it
//	                "place", "placeKind", "placeName", "owner", "role", "path", "fromTop"}],   where it is, as they may see it
//	     "more": false}
//	400 an empty search, or one longer than any name can be
func (s *Server) search(w http.ResponseWriter, r *http.Request, c call) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	switch {
	case len(q) > drive.MaxNameBytes:
		s.refuse(w, r, http.StatusBadRequest, "This is longer than any name can be. Search for a part of the name.")
		return
	case index.SearchKey(q) == "":
		s.refuse(w, r, http.StatusBadRequest, "Type a part of a name to search for.")
		return
	}
	scope, err := s.searchScope(ctx, c.member)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// More than will be answered, so that one result the index still had but access no longer finds (it went to the
	// trash meanwhile, say) does not hide one that is there.
	found, err := s.drive.Search(ctx, scope, q, 2*searchLimit+1)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	who := personOf(c.member)
	list := s.members.Current()
	places := map[int64]*index.Place{}
	seen := map[int64]bool{}
	out := []foundJSON{}
	more := false
	for _, f := range found {
		if seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		p, ok := places[f.PlaceID]
		if !ok {
			got, err := s.drive.Place(ctx, f.PlaceID)
			switch {
			case errors.Is(err, index.ErrNoPlace):
			case err != nil:
				s.fail(w, r, err)
				return
			default:
				p = &got
			}
			places[f.PlaceID] = p
		}
		if p == nil {
			continue // a shared folder that went to the trash meanwhile
		}
		it, err := s.drive.Item(ctx, *p, f.ID)
		if errors.Is(err, index.ErrNoItem) {
			continue // in the trash, or gone, since the search found it
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
		if !access.Finds(who, where) {
			continue
		}
		if len(out) == searchLimit {
			more = true
			break
		}
		open, fromTop := openPath(who, where, it.Above)
		e := foundJSON{
			listedJSON: listedJSON{itemJSON: itemOf(it), Can: can(who, where), SeenBy: seenBy(list, where)},
			Place:      p.ID, PlaceKind: string(p.Kind), Role: access.RoleAt(who, where),
			Path: pathOf(open), FromTop: fromTop,
		}
		if fromTop && p.Kind == index.SharedFolder {
			e.PlaceName = p.Name
		}
		if p.Kind == index.MyFiles && p.Owner != c.member.UserID {
			if m, ok := list.Lookup(p.Owner); ok {
				e.Owner = personJSONOf(m)
			}
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "more": more})
}

// searchScope is where a search looks for the member, each part decided through access: every place they can open as
// a whole (their own My files, if they have one yet, and the shared folders they have a role on), and each item shared
// with them in a place they cannot open, with everything in it (as "Shared with me" lists them: none inside another,
// none inside a place of the first kind).
func (s *Server) searchScope(ctx context.Context, m members.Member) (index.Scope, error) {
	var scope index.Scope
	var candidates []index.Place
	mine, err := s.drive.FindMyFiles(ctx, m.UserID) // a search makes nothing: no My files yet, nothing in it
	switch {
	case errors.Is(err, index.ErrNoPlace):
	case err != nil:
		return scope, err
	default:
		candidates = append(candidates, mine)
	}
	shared, err := s.drive.SharedFolders(ctx)
	if err != nil {
		return scope, err
	}
	given, err := s.drive.TopGrantsFor(ctx, m.UserID)
	if err != nil {
		return scope, err
	}
	who := personOf(m)
	for _, p := range append(candidates, shared...) {
		if access.Decide(who, access.List, s.accessPlace(p, nil, given[p.ID])) {
			scope.Places = append(scope.Places, p.ID)
		}
	}
	shares, err := s.sharedWith(ctx, m)
	if err != nil {
		return scope, err
	}
	for _, sh := range shares {
		scope.Items = append(scope.Items, index.Granted{Place: sh.place.ID, Item: sh.item.ID})
	}
	return scope, nil
}
