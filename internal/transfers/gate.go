package transfers

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBusy is returned by Gate.Wait when its turn did not come in time.
var ErrBusy = errors.New("every transfer slot is taken")

// Gate lets at most a fixed number of transfers be in flight. The rest wait their turn, in the order they came.
//
// There are two ways to wait:
//
//   - Wait: the request itself waits in line. Downloads wait this way: while they wait, nothing of theirs is held but
//     an idle connection.
//   - Try, with a key: the request is refused at once when it is not its turn, and its key keeps its place in line.
//     Uploads wait this way, because an upload kept waiting would have its bytes fill the connection's buffers
//     meanwhile, and because the interface can then show it as waiting. When the key's turn comes, a slot is kept for
//     it for Hold; trying again with the key takes it. A key not tried again within Forget loses its place.
type Gate struct {
	mu   sync.Mutex
	free int
	// line is everyone waiting for a slot, in the order they came. It is empty whenever a slot is free.
	line []*waiter
	// keys is every key in line, or holding a slot kept for it.
	keys map[string]*waiter

	hold, forget time.Duration
	now          func() time.Time
}

// How long a slot is kept for a key whose turn came, and how long a key keeps its place without being tried again.
const (
	Hold   = 30 * time.Second
	Forget = time.Minute
)

type waiter struct {
	key   string        // "" for a request waiting itself
	ready chan struct{} // closed when a request waiting itself gets its slot
	given bool          // the slot is the waiter's
	left  bool          // a request waiting itself gave up its place
	tried time.Time     // when the key was last tried
	keep  *time.Timer   // gives back a slot kept for a key that does not come for it
}

// NewGate makes a gate with slots slots.
func NewGate(slots int) *Gate {
	return &Gate{free: slots, keys: map[string]*waiter{}, hold: Hold, forget: Forget, now: time.Now}
}

// Wait takes a slot, waiting in line for its turn until ctx ends or max has passed (then ErrBusy). Call release when
// the transfer is over.
func (g *Gate) Wait(ctx context.Context, max time.Duration) (release func(), err error) {
	g.mu.Lock()
	if g.free > 0 {
		g.free--
		g.mu.Unlock()
		return g.releaser(), nil
	}
	w := &waiter{ready: make(chan struct{})}
	g.line = append(g.line, w)
	g.mu.Unlock()

	timer := time.NewTimer(max)
	defer timer.Stop()
	select {
	case <-w.ready:
		return g.releaser(), nil
	case <-ctx.Done():
		err = ctx.Err()
	case <-timer.C:
		err = ErrBusy
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if w.given { // its turn came just as it gave up: the slot goes on
		g.free++
		g.handOn()
	} else {
		w.left = true
	}
	return nil, err
}

// Try takes a slot if one is free, or if one is kept for key. Otherwise ok is false, and key (when not empty) keeps
// its place in line, or keeps waiting in it: try again with it. Call release when the transfer is over.
func (g *Gate) Try(key string) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if w := g.keys[key]; key != "" && w != nil {
		if w.given {
			w.keep.Stop()
			delete(g.keys, key)
			return g.releaser(), true
		}
		w.tried = g.now()
		return nil, false
	}
	if g.free > 0 {
		g.free--
		return g.releaser(), true
	}
	if key != "" {
		w := &waiter{key: key, tried: g.now()}
		g.keys[key] = w
		g.line = append(g.line, w)
	}
	return nil, false
}

// Waiting says whether key is in line, its turn not yet come.
func (g *Gate) Waiting(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.keys[key]
	return w != nil && !w.given
}

// releaser gives the slot back, once however often it is called.
func (g *Gate) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.free++
			g.handOn()
		})
	}
}

// handOn gives free slots to those first in line: a request waiting itself gets it at once, a key has it kept for
// it. Those who left, and keys forgotten, lose their place. g.mu must be held.
func (g *Gate) handOn() {
	for g.free > 0 && len(g.line) > 0 {
		w := g.line[0]
		g.line[0] = nil
		g.line = g.line[1:]
		switch {
		case w.left:
		case w.key == "":
			g.free--
			w.given = true
			close(w.ready)
		case g.now().Sub(w.tried) > g.forget:
			delete(g.keys, w.key)
		default:
			g.free--
			w.given = true
			w.keep = time.AfterFunc(g.hold, func() { g.giveBack(w) })
		}
	}
}

// giveBack gives back a slot kept for a key that did not come for it in time.
func (g *Gate) giveBack(w *waiter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.keys[w.key] != w {
		return // it came for it meanwhile
	}
	delete(g.keys, w.key)
	g.free++
	g.handOn()
}
