package transfers

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBusy is returned by Gate.Wait when its turn did not come in time, or when too many outsiders wait already.
var ErrBusy = errors.New("every transfer slot is taken")

// A Class is who a transfer is for. Members may use every slot; outsiders (visitors who came with a public link, and
// later guests) may hold at most a few of them at once, so that people from outside can never take the Drive away from
// the project's own members.
type Class int

const (
	// Members are the project's members, signed in through the reverse proxy.
	Members Class = iota
	// Outsiders are everyone else: visitors with a public link.
	Outsiders
)

// Gate lets at most a fixed number of transfers be in flight, of which outsiders may hold at most a smaller number.
// The rest wait their turn, each class in a line of its own, in the order they came.
//
// When a slot is free, a member takes it; an outsider takes it only while outsiders hold fewer than their share. When
// one is given back, the first member waiting gets it; only when no member waits does the first outsider (while
// outsiders hold fewer than their share). So outsiders never keep members waiting beyond the slots outsiders may hold,
// and members may use every slot while no outsider needs one.
//
// There are two ways to wait:
//
//   - Wait: the request itself waits in line. Downloads wait this way: while they wait, nothing of theirs is held but
//     an idle connection. At most MaxOutsidersWaiting outsiders wait this way at once; more are told at once that the
//     Drive is busy, so that a crowd from outside costs a bounded number of connections.
//   - Try, with a key: the request is refused at once when it is not its turn, and its key keeps its place in line.
//     Uploads wait this way, because an upload kept waiting would have its bytes fill the connection's buffers
//     meanwhile, and because the interface can then show it as waiting. When the key's turn comes, a slot is kept for
//     it for Hold; trying again with the key takes it. A key not tried again within Forget loses its place. At most
//     MaxOutsidersWaiting outsiders' keys are kept at once (those whose turn came included): beyond that, an outsider's
//     new key is refused without a place, as if it had none, so that strangers sending new keys cost a bounded amount of
//     memory however fast they send them.
type Gate struct {
	mu   sync.Mutex
	free int
	// outsiders is how many slots outsiders hold, those kept for an outsider's key included; never more than share.
	outsiders, share int
	// lines are those waiting for a slot, one line per class, in the order they came. The members' line is empty
	// whenever a slot is free; the outsiders' line whenever a slot is free and outsiders hold fewer than their share.
	lines [2][]*waiter
	// waitingOutsiders counts the outsiders waiting in line themselves (Wait).
	waitingOutsiders int
	// keys is every key in line, or holding a slot kept for it; keyedOutsiders how many of them are outsiders'.
	keys           map[string]*waiter
	keyedOutsiders int

	hold, forget time.Duration
	now          func() time.Time
}

// How long a slot is kept for a key whose turn came, and how long a key keeps its place without being tried again.
const (
	Hold   = 30 * time.Second
	Forget = time.Minute
)

// MaxOutsidersWaiting is the most outsiders that wait in line themselves at once, and the most outsiders' keys kept at
// once.
const MaxOutsidersWaiting = 64

type waiter struct {
	class Class
	key   string        // "" for a request waiting itself
	ready chan struct{} // closed when a request waiting itself gets its slot
	given bool          // the slot is the waiter's
	left  bool          // a request waiting itself gave up its place
	tried time.Time     // when the key was last tried
	keep  *time.Timer   // gives back a slot kept for a key that does not come for it
}

// NewGate makes a gate with slots slots, of which outsiders may hold at most outsiderSlots.
func NewGate(slots, outsiderSlots int) *Gate {
	return &Gate{free: slots, share: min(outsiderSlots, slots), keys: map[string]*waiter{}, hold: Hold, forget: Forget, now: time.Now}
}

// Wait takes a slot for a transfer of class c, waiting in line for its turn until ctx ends or max has passed (then
// ErrBusy). An outsider who would be one too many in line gets ErrBusy at once. Call release when the transfer is over.
func (g *Gate) Wait(ctx context.Context, c Class, max time.Duration) (release func(), err error) {
	g.mu.Lock()
	if g.mayTake(c) {
		g.take(c)
		g.mu.Unlock()
		return g.releaser(c), nil
	}
	if c == Outsiders {
		if g.waitingOutsiders >= MaxOutsidersWaiting {
			g.mu.Unlock()
			return nil, ErrBusy
		}
		g.waitingOutsiders++
	}
	w := &waiter{class: c, ready: make(chan struct{})}
	g.lines[c] = append(g.lines[c], w)
	g.mu.Unlock()

	timer := time.NewTimer(max)
	defer timer.Stop()
	select {
	case <-w.ready:
		return g.releaser(c), nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if w.given { // its turn came just as it gave up: the slot goes on
		g.giveBack(c)
		g.handOn()
	} else {
		w.left = true
		if c == Outsiders {
			g.waitingOutsiders--
		}
	}
	return nil, err
}

// Try takes a slot for a transfer of class c if one is free for it, or if one is kept for key. Otherwise ok is false,
// and key (when not empty) keeps its place in line, or keeps waiting in it: try again with it. Call release when the
// transfer is over.
func (g *Gate) Try(c Class, key string) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w := g.keys[key]; key != "" && w != nil {
		if w.given {
			w.keep.Stop()
			g.dropKey(w)
			return g.releaser(w.class), true
		}
		w.tried = g.now()
		return nil, false
	}
	if g.mayTake(c) {
		g.take(c)
		return g.releaser(c), true
	}
	if key != "" {
		if c == Outsiders && g.keyedOutsiders >= MaxOutsidersWaiting {
			g.forgetOutsiders()
			if g.keyedOutsiders >= MaxOutsidersWaiting {
				return nil, false // too many outsiders keep a place already: this one keeps none
			}
		}
		w := &waiter{class: c, key: key, tried: g.now()}
		g.keys[key] = w
		if c == Outsiders {
			g.keyedOutsiders++
		}
		g.lines[c] = append(g.lines[c], w)
	}
	return nil, false
}

// dropKey forgets the key of w, if it is still w's. g.mu must be held.
func (g *Gate) dropKey(w *waiter) {
	if g.keys[w.key] != w {
		return
	}
	delete(g.keys, w.key)
	if w.class == Outsiders {
		g.keyedOutsiders--
	}
}

// forgetOutsiders takes out of the outsiders' line the keys not tried again within forget, which would otherwise lose
// their place only when their turn comes. g.mu must be held.
func (g *Gate) forgetOutsiders() {
	line := g.lines[Outsiders][:0]
	for _, w := range g.lines[Outsiders] {
		if w.key != "" && g.now().Sub(w.tried) > g.forget {
			g.dropKey(w)
			continue
		}
		line = append(line, w)
	}
	clear(g.lines[Outsiders][len(line):]) // let go of those taken out
	g.lines[Outsiders] = line
}

// Waiting says whether key is in line, its turn not yet come.
func (g *Gate) Waiting(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.keys[key]
	return w != nil && !w.given
}

// mayTake says whether a transfer of class c may take a free slot at once: a member whenever one is free (nobody is in
// their line then), an outsider while outsiders also hold fewer than their share (and then nobody is in theirs). g.mu
// must be held.
func (g *Gate) mayTake(c Class) bool {
	return g.free > 0 && (c == Members || g.outsiders < g.share)
}

func (g *Gate) take(c Class) {
	g.free--
	if c == Outsiders {
		g.outsiders++
	}
}

func (g *Gate) giveBack(c Class) {
	g.free++
	if c == Outsiders {
		g.outsiders--
	}
}

// releaser gives the slot back, once however often it is called.
func (g *Gate) releaser(c Class) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.giveBack(c)
			g.handOn()
		})
	}
}

// handOn gives free slots to those first in line, members before outsiders, and outsiders only while they hold fewer
// than their share: a request waiting itself gets it at once, a key has it kept for it. Those who left, and keys
// forgotten, lose their place. g.mu must be held.
func (g *Gate) handOn() {
	for g.free > 0 {
		c := Members
		if len(g.lines[Members]) == 0 {
			if len(g.lines[Outsiders]) == 0 || g.outsiders >= g.share {
				return
			}
			c = Outsiders
		}
		w := g.lines[c][0]
		g.lines[c][0] = nil
		g.lines[c] = g.lines[c][1:]
		switch {
		case w.left:
		case w.key == "":
			g.take(c)
			w.given = true
			if c == Outsiders {
				g.waitingOutsiders--
			}
			close(w.ready)
		case g.now().Sub(w.tried) > g.forget:
			g.dropKey(w)
		default:
			g.take(c)
			w.given = true
			w.keep = time.AfterFunc(g.hold, func() { g.notCome(w) })
		}
	}
}

// notCome gives back a slot kept for a key that did not come for it in time.
func (g *Gate) notCome(w *waiter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.keys[w.key] != w {
		return // it came for it meanwhile
	}
	g.dropKey(w)
	g.giveBack(w.class)
	g.handOn()
}
