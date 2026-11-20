package drive

import (
	"context"
	"errors"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Sharing outside the project switched off, and what a project admin oversees. The access package decides who may
// switch it and what it stops; these only record it and read what is shared outside the project.

// ProjectOutsideOff says whether sharing outside the project is switched off for the whole project, since when and by
// whom (the zero Switch while it is on).
func (d *Drive) ProjectOutsideOff(ctx context.Context) (index.Switch, error) {
	return d.index.ProjectOutsideOff(ctx)
}

// SwitchOutside switches sharing outside the project off (off true), or back on, for the whole project (p nil) or for
// the shared folder p, by the member by, and logs it when it changed. For a shared folder it is a change inside it, so
// it takes the place's lock: a shared folder that went to the trash is not switched (ErrPlaceGone).
func (d *Drive) SwitchOutside(ctx context.Context, p *index.Place, off bool, by string) (changed bool, err error) {
	var place int64
	if p != nil {
		leave, err := d.inPlace(ctx, *p)
		if err != nil {
			return false, err
		}
		defer leave()
		place = p.ID
	}
	changed, err = d.index.SwitchOutside(ctx, place, off, d.now(), by)
	if errors.Is(err, index.ErrNoPlace) {
		return false, ErrPlaceGone
	}
	if err == nil && changed {
		state := "on"
		if off {
			state = "off"
		}
		if p == nil {
			d.log.Info("sharing outside the project was switched "+state+" for the whole project", "by", by)
		} else {
			d.log.Info("sharing outside the project was switched "+state+" for a shared folder", "place", place, "by", by)
		}
	}
	return changed, err
}

// Places returns every place, those in the trash included.
func (d *Drive) Places(ctx context.Context) ([]index.Place, error) {
	return d.index.Places(ctx)
}

// UsedByPlace is, by place id, about how much space each place's files take, its trash included (see Used).
func (d *Drive) UsedByPlace(ctx context.Context) (map[int64]int64, error) {
	return d.index.UsedByPlace(ctx)
}

// LinksAfter returns at most limit public links whose id comes after after, by id, with their uses counted so far;
// more says there are others.
func (d *Drive) LinksAfter(ctx context.Context, after int64, limit int) ([]index.Link, bool, error) {
	links, more, err := d.index.LinksAfter(ctx, after, limit)
	for i := range links {
		links[i] = d.withUses(links[i])
	}
	return links, more, err
}

// InvitationsAfter returns at most limit guests' invitations whose id comes after after, by id, with their uses
// counted so far; more says there are others.
func (d *Drive) InvitationsAfter(ctx context.Context, after int64, limit int) ([]index.Invitation, bool, error) {
	out, more, err := d.index.InvitationsAfter(ctx, after, limit)
	grants := make([]index.Grant, len(out))
	for i := range out {
		grants[i] = out[i].Grant
	}
	grants = d.withInvitationUses(grants)
	for i := range out {
		out[i].Grant = grants[i]
	}
	return out, more, err
}
