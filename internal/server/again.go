package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Deciding again. guard decides on a request when it comes; a transfer goes on long after: a download or a zip waits
// for its slot, up to turnWait; a zip is made as it is sent, for as long as its reader takes it; an upload's bytes
// arrive for as long as its sender sends them. What was decided when the request came may no longer hold by then: the
// link removed, the invitation taken away or ended, sharing outside the project switched off, a member's access taken
// away. So a transfer is decided again, on everything read again, when what it waited for has come: a download when
// its turn for a slot comes, a zip then and before each thing it adds (and now and then while one large file goes out),
// an upload in one request once its file is whole, holding its place, just before the file is put into its folder (as
// a file sent in through an upload-only link is: stillSendingIn).

// errNoLongerMay says that a transfer decided on again may no longer do what it was let in for: answered as what is
// not there (errSwitchedOffNow, when only sharing outside the project switched off stops it).
var errNoLongerMay = errors.New("the person may no longer do this there")

// againEvery is how long a zip may go on sending one file before it is decided again.
const againEvery = 15 * time.Second

// again decides once more on a call about a place that guard let in, with everything read again: who is asking as they
// are now (the visitor's link, by its id, so a link made in its place is another; the guest's session; the member as the
// member list has them), the place, the item the call is about (none: the place's top folder), and what is given on the
// way down to it. It returns the call as it is now; errNoLongerMay or errSwitchedOffNow when access.Decide no longer
// lets them do what the route is.
func (s *Server) again(ctx context.Context, r *http.Request, c call) (call, error) {
	now := call{action: c.action, upload: c.upload}
	placeID := c.place.ID
	switch {
	case c.link != nil:
		l, err := s.drive.LinkByID(ctx, c.link.ID)
		if errors.Is(err, index.ErrNoLink) {
			return call{}, errNoLongerMay
		}
		if err != nil {
			return call{}, err
		}
		now.link, now.person = &l, visitorOf(l, s.members.Current())
		placeID = l.Place // a link follows its item into another place
	case c.guest != nil:
		g, _, err := s.guestSession(r)
		if err != nil {
			return call{}, err
		}
		if g == nil || g.ID != c.guest.ID {
			return call{}, errNoLongerMay
		}
		now.guest, now.person = g, access.Person{Guest: &access.Guest{ID: g.ID}}
	default:
		m, ok := s.members.Current().Lookup(c.member.UserID)
		if !ok {
			return call{}, errNoLongerMay
		}
		now.member, now.person = m, personOf(m)
	}
	p, err := s.drive.Place(ctx, placeID)
	if errors.Is(err, index.ErrNoPlace) {
		return call{}, errNoLongerMay // a shared folder in the trash
	}
	if err != nil {
		return call{}, err
	}
	now.place = &p
	var path []index.Item
	if c.item != nil {
		it, err := s.drive.Item(ctx, p, c.item.ID)
		if errors.Is(err, index.ErrNoItem) {
			return call{}, errNoLongerMay // in the trash, gone, or in another place
		}
		if err != nil {
			return call{}, err
		}
		now.item, path = &it, pathTo(it)
	}
	if now.grants, err = s.drive.GrantsOn(ctx, p, idsOf(path)); err != nil {
		return call{}, err
	}
	now.access = s.accessPlace(p, idsOf(path), now.grants)
	if !access.Decide(now.person, now.action, now.access) {
		if access.SwitchedOff(now.person, now.action, now.access) {
			return call{}, errSwitchedOffNow
		}
		return call{}, errNoLongerMay
	}
	return now, nil
}

// noLonger answers a transfer that again refused, as guard answers one it refuses: what is not there, or what is not
// available while sharing outside the project is switched off; anything else went wrong in the Drive.
func (s *Server) noLonger(w http.ResponseWriter, r *http.Request, c call, err error) {
	switch {
	case errors.Is(err, errSwitchedOffNow):
		s.switchedOff(w, r, c)
	case errors.Is(err, errNoLongerMay):
		s.refuse(w, r, http.StatusNotFound, notHere)
	default:
		s.fail(w, r, err)
	}
}

// inTurn waits in line for a download's transfer slot (waitTurn), and decides on it again once its turn has come: what
// it waited for may have stopped being its to take. It returns the call as it is now. ok is false when the person was
// answered, or has left.
func (s *Server) inTurn(w http.ResponseWriter, r *http.Request, c call) (now call, release func(), ok bool) {
	release, ok = s.waitTurn(w, r, c)
	if !ok {
		return call{}, nil, false
	}
	now, err := s.again(r.Context(), r, c)
	if err != nil {
		release()
		s.noLonger(w, r, c, err)
		return call{}, nil, false
	}
	return now, release, true
}

// whileStill is out for a zip's bytes, decided again (still) whenever againEvery has passed since it last was: a zip
// that is long in sending one large file ends within that, and the time its reader may take over one write (stall), of
// its link or invitation stopping.
func whileStill(ctx context.Context, out io.Writer, still func(context.Context) error) *stillWriter {
	return &stillWriter{ctx: ctx, out: out, still: still, decided: time.Now()}
}

type stillWriter struct {
	ctx     context.Context
	out     io.Writer
	still   func(context.Context) error
	decided time.Time
}

// again decides again now, and counts againEvery from now.
func (w *stillWriter) again(ctx context.Context) error {
	w.decided = time.Now()
	return w.still(ctx)
}

func (w *stillWriter) Write(p []byte) (int, error) {
	if time.Since(w.decided) >= againEvery {
		if err := w.again(w.ctx); err != nil {
			return 0, err
		}
	}
	return w.out.Write(p)
}
