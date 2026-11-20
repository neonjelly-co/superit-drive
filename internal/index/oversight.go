package index

import "context"

// What a project admin oversees: everything shared outside the project (public links and guests' invitations), read a
// page at a time, and the space each place uses. These only read what is held; internal/access decides who may see it
// and whether each share lets anyone in now.

// LinksAfter returns at most limit public links whose id comes after after, by id, wherever their items are (in the
// trash too); more says there are others.
func (x *Index) LinksAfter(ctx context.Context, after int64, limit int) (links []Link, more bool, err error) {
	links, err = scanLinks(x.db.QueryContext(ctx, "SELECT "+linkColumns+`
		FROM links JOIN items ON items.id = links.item_id WHERE links.id > ? ORDER BY links.id LIMIT ?`, after, limit+1))
	if len(links) > limit {
		return links[:limit], true, err
	}
	return links, false, err
}

// An Invitation is a grant to a guest, with the place its item is in.
type Invitation struct {
	Place int64
	Grant
}

// InvitationsAfter returns at most limit invitations (grants to guests) whose id comes after after, by id, wherever
// their items are; more says there are others.
func (x *Index) InvitationsAfter(ctx context.Context, after int64, limit int) (out []Invitation, more bool, err error) {
	rows, err := x.db.QueryContext(ctx, "SELECT "+grantColumns+", grants.place_id FROM "+grantsJoined+`
		WHERE grants.grantee = 'guest' AND grants.id > ? ORDER BY grants.id LIMIT ?`, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var in Invitation
		if in.Grant, err = scanGrant(rows, &in.Place); err != nil {
			return nil, false, err
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// UsedByPlace is, by place id, the sum of the sizes of the files the index knows in each place, its trash included, as
// Used sums them for all places together. A place without files is not in it.
func (x *Index) UsedByPlace(ctx context.Context) (map[int64]int64, error) {
	rows, err := x.db.QueryContext(ctx, "SELECT place_id, IFNULL(SUM(size), 0) FROM items WHERE is_folder = 0 GROUP BY place_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var place, used int64
		if err := rows.Scan(&place, &used); err != nil {
			return nil, err
		}
		out[place] = used
	}
	return out, rows.Err()
}
