package transfers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"
)

// taken waits until the request waiting in ch has its slot, or fails.
func taken(t *testing.T, what string, ch <-chan func()) func() {
	t.Helper()
	select {
	case release := <-ch:
		return release
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not get a slot", what)
		return nil
	}
}

func stillWaiting(t *testing.T, what string, ch <-chan func()) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s got a slot, but should still be waiting", what)
	case <-time.After(50 * time.Millisecond):
	}
}

// waitIn starts a member's request waiting in line, and sends its release when it gets a slot.
func waitIn(g *Gate, ctx context.Context) <-chan func() { return waitAs(g, ctx, Members) }

// waitAs starts a request of class c waiting in line, and sends its release when it gets a slot.
func waitAs(g *Gate, ctx context.Context, c Class) <-chan func() {
	ch := make(chan func(), 1)
	go func() {
		release, err := g.Wait(ctx, c, time.Minute)
		if err == nil {
			ch <- release
		}
	}()
	time.Sleep(20 * time.Millisecond) // so the order of arrival is the order of the calls
	return ch
}

func TestAtMostSlotsAreInFlightAndTheRestWaitInTheOrderTheyCame(t *testing.T) {
	g := NewGate(2, 2)
	a, _ := g.Wait(context.Background(), Members, time.Second)
	b, ok := g.Try(Members, "")
	if !ok {
		t.Fatal("the second slot was not free")
	}
	if _, ok := g.Try(Members, ""); ok {
		t.Fatal("a third transfer took a slot")
	}
	first, second, third := waitIn(g, context.Background()), waitIn(g, context.Background()), waitIn(g, context.Background())
	stillWaiting(t, "the first in line", first)

	a()
	a() // giving a slot back twice gives back one
	c := taken(t, "the first in line", first)
	stillWaiting(t, "the second in line", second)
	stillWaiting(t, "the third in line", third)
	b()
	taken(t, "the second in line", second)
	stillWaiting(t, "the third in line", third)
	c()
	taken(t, "the third in line", third)
}

func TestARequestThatStopsWaitingLosesItsPlace(t *testing.T) {
	g := NewGate(1, 1)
	a, _ := g.Wait(context.Background(), Members, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() { _, err := g.Wait(ctx, Members, time.Minute); gaveUp <- err }()
	time.Sleep(20 * time.Millisecond)
	next := waitIn(g, context.Background())
	cancel()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	a()
	taken(t, "the one behind it", next)

	// Waiting too long is ErrBusy.
	if _, err := g.Wait(context.Background(), Members, 30*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

func TestAKeyKeepsItsPlaceInLine(t *testing.T) {
	g := NewGate(1, 1)
	g.hold = 200 * time.Millisecond
	a, _ := g.Try(Members, "")
	if _, ok := g.Try(Members, "upload-1"); ok {
		t.Fatal("upload-1 took a slot that was not free")
	}
	if !g.Waiting("upload-1") {
		t.Fatal("upload-1 is not in line")
	}
	behind := waitIn(g, context.Background())
	if _, ok := g.Try(Members, "upload-2"); ok {
		t.Fatal("upload-2 took a slot")
	}

	// Its turn comes: the slot is kept for it, not for the request behind it nor for anyone without the key.
	a()
	stillWaiting(t, "the request behind upload-1", behind)
	if _, ok := g.Try(Members, ""); ok {
		t.Fatal("a try without the key took the slot kept for upload-1")
	}
	if g.Waiting("upload-1") {
		t.Fatal("upload-1 is still said to be waiting")
	}
	release, ok := g.Try(Members, "upload-1")
	if !ok {
		t.Fatal("upload-1 did not get the slot kept for it")
	}
	release()
	b := taken(t, "the request behind upload-1", behind)

	// upload-2 does not come for its slot: after Hold it goes on.
	next := waitIn(g, context.Background())
	b()
	stillWaiting(t, "the request behind upload-2", next)
	taken(t, "the request behind upload-2, once upload-2's turn has passed", next)
	if g.Waiting("upload-2") {
		t.Fatal("upload-2 is still in line")
	}
}

func TestAKeyNotTriedAgainLosesItsPlace(t *testing.T) {
	g := NewGate(1, 1)
	clock := time.Now()
	g.now = func() time.Time { return clock }
	a, _ := g.Try(Members, "")
	g.Try(Members, "gone")
	g.Try(Members, "back")
	behind := waitIn(g, context.Background())

	clock = clock.Add(Forget / 2)
	g.Try(Members, "back") // tried again: it keeps its place
	clock = clock.Add(Forget/2 + time.Second)
	a()
	if g.Waiting("gone") {
		t.Fatal("the forgotten key is still in line")
	}
	release, ok := g.Try(Members, "back")
	if !ok {
		t.Fatal("the key tried again did not get its turn")
	}
	stillWaiting(t, "the request behind", behind)
	release()
	taken(t, "the request behind", behind)
}

// Outsiders hold at most their share of the slots, and wait in a line of their own even while other slots are free;
// members can always use the rest, and all of them while no outsider holds one.
func TestOutsidersHoldAtMostTheirShareAndMembersTheRest(t *testing.T) {
	g := NewGate(4, 2)
	var outsiders []func()
	for range 2 {
		release, ok := g.Try(Outsiders, "")
		if !ok {
			t.Fatal("an outsider did not get a slot within their share")
		}
		outsiders = append(outsiders, release)
	}
	if _, ok := g.Try(Outsiders, ""); ok {
		t.Fatal("a third outsider took a slot beyond their share")
	}
	third := waitAs(g, context.Background(), Outsiders)
	stillWaiting(t, "the third outsider, with two slots free", third)
	// Members take the other two at once, past the outsider waiting.
	m1, ok1 := g.Try(Members, "")
	m2, ok2 := g.Try(Members, "")
	if !ok1 || !ok2 {
		t.Fatal("members could not use the slots outsiders may not")
	}
	member := waitIn(g, context.Background())

	// An outsider's slot comes back: the member waiting gets it, before the outsider who came earlier.
	outsiders[0]()
	m3 := taken(t, "the member waiting", member)
	stillWaiting(t, "the third outsider, while a member got the slot", third)
	// A member's slot comes back with no member waiting: the outsider gets it, as outsiders hold one only.
	m1()
	o3 := taken(t, "the third outsider", third)

	// With no outsider holding one, members use every slot.
	outsiders[1]()
	o3()
	m2()
	m3()
	var all []func()
	for i := range 4 {
		release, ok := g.Try(Members, "")
		if !ok {
			t.Fatalf("member %d of 4 did not get a slot while no outsider holds one", i+1)
		}
		all = append(all, release)
	}
	outsider := waitAs(g, context.Background(), Outsiders)
	stillWaiting(t, "an outsider while members hold every slot", outsider)
	all[0]()
	taken(t, "the outsider, once a member is done", outsider)
}

// A key of an outsider keeps its place in the outsiders' line, and a slot kept for it counts among theirs.
func TestAnOutsidersKeyWaitsInTheirLine(t *testing.T) {
	g := NewGate(2, 1)
	o, _ := g.Try(Outsiders, "")
	if _, ok := g.Try(Outsiders, "upload-1"); ok {
		t.Fatal("a second outsider took a slot beyond their share")
	}
	if !g.Waiting("upload-1") {
		t.Fatal("the outsider's key is not in line")
	}
	o()
	if g.Waiting("upload-1") {
		t.Fatal("the outsider's key is still waiting after an outsider's slot came back")
	}
	// The slot kept for it is the outsiders' one: another outsider waits, a member does not.
	if _, ok := g.Try(Outsiders, ""); ok {
		t.Fatal("another outsider took a slot while one is kept for an outsider")
	}
	m, ok := g.Try(Members, "")
	if !ok {
		t.Fatal("a member could not use the other slot")
	}
	release, ok := g.Try(Outsiders, "upload-1")
	if !ok {
		t.Fatal("the outsider's key did not get the slot kept for it")
	}
	release()
	m()
}

// A crowd of outsiders costs a bounded number of waiting connections: beyond MaxOutsidersWaiting, they are told at
// once that the Drive is busy. Members are not bounded this way.
func TestAtMostSoManyOutsidersWait(t *testing.T) {
	g := NewGate(1, 1)
	hold, _ := g.Try(Members, "")
	ctx, cancel := context.WithCancel(context.Background())
	var waiting []<-chan error
	for range MaxOutsidersWaiting {
		ch := make(chan error, 1)
		go func() { _, err := g.Wait(ctx, Outsiders, time.Minute); ch <- err }()
		waiting = append(waiting, ch)
	}
	inLine := func() int { g.mu.Lock(); defer g.mu.Unlock(); return g.waitingOutsiders }
	for deadline := time.Now().Add(2 * time.Second); inLine() < MaxOutsidersWaiting; {
		if time.Now().After(deadline) {
			t.Fatalf("%d outsiders wait, want %d", inLine(), MaxOutsidersWaiting)
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	if _, err := g.Wait(context.Background(), Outsiders, time.Minute); !errors.Is(err, ErrBusy) || time.Since(start) > time.Second {
		t.Fatalf("one outsider too many: got %v after %v, want ErrBusy at once", err, time.Since(start))
	}
	member := waitIn(g, context.Background())
	stillWaiting(t, "a member, with the outsiders' line full", member)
	// Those who give up make room again.
	cancel()
	for _, ch := range waiting {
		if err := <-ch; !errors.Is(err, context.Canceled) {
			t.Fatalf("an outsider who gave up: %v", err)
		}
	}
	if n := inLine(); n != 0 {
		t.Fatalf("%d outsiders are counted as waiting after all of them left", n)
	}
	hold()
	taken(t, "the member", member)
}

func TestEveryConnectionsBuffersAreCapped(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	out, err := Dial(time.Second)(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in := <-accepted
	defer in.Close()

	for what, c := range map[string]net.Conn{"a connection the Drive accepted": in, "a connection the Drive made": out} {
		raw, err := c.(*net.TCPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		for name, opt := range map[string]int{"receive": syscall.SO_RCVBUF, "send": syscall.SO_SNDBUF} {
			var size int
			var serr error
			if err := raw.Control(func(fd uintptr) { size, serr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, opt) }); err != nil || serr != nil {
				t.Fatal(err, serr)
			}
			// Linux reports twice what it was given.
			if size != SocketBuffer && size != 2*SocketBuffer {
				t.Errorf("%s: its %s buffer is %d bytes, want %d", what, name, size, SocketBuffer)
			}
		}
	}
}

// Outsiders' keys cost a bounded amount of memory, however many new ones strangers send: beyond MaxOutsidersWaiting
// kept at once, an outsider's new key keeps no place. A key not tried again within Forget makes room. Members' keys are
// not bounded this way.
func TestAtMostSoManyOutsidersKeepAPlace(t *testing.T) {
	g := NewGate(2, 1)
	clock := time.Now()
	g.now = func() time.Time { return clock }
	hold, _ := g.Try(Outsiders, "")
	for i := range MaxOutsidersWaiting {
		if _, ok := g.Try(Outsiders, fmt.Sprintf("upload-%d", i)); ok {
			t.Fatal("an outsider took a slot beyond their share")
		}
	}
	for i := range MaxOutsidersWaiting {
		if !g.Waiting(fmt.Sprintf("upload-%d", i)) {
			t.Fatalf("outsider %d is not in line", i)
		}
	}
	if _, ok := g.Try(Outsiders, "one-too-many"); ok || g.Waiting("one-too-many") {
		t.Fatal("one outsider's key too many keeps a place")
	}
	// Members are not bounded by it.
	m, _ := g.Try(Members, "")
	for i := range 2 * MaxOutsidersWaiting {
		g.Try(Members, fmt.Sprintf("member-%d", i))
	}
	if !g.Waiting(fmt.Sprintf("member-%d", 2*MaxOutsidersWaiting-1)) {
		t.Fatal("a member's key is bounded by the outsiders'")
	}
	// Half are tried again in time; the rest are forgotten, and make room for new ones.
	clock = clock.Add(Forget / 2)
	for i := range MaxOutsidersWaiting / 2 {
		g.Try(Outsiders, fmt.Sprintf("upload-%d", i))
	}
	clock = clock.Add(Forget/2 + time.Second)
	if _, ok := g.Try(Outsiders, "later"); ok || !g.Waiting("later") {
		t.Fatal("a new outsider's key keeps no place once the forgotten ones made room")
	}
	if g.Waiting(fmt.Sprintf("upload-%d", MaxOutsidersWaiting-1)) || !g.Waiting("upload-0") {
		t.Fatal("the wrong keys were forgotten")
	}
	g.mu.Lock()
	kept := g.keyedOutsiders
	g.mu.Unlock()
	if kept != MaxOutsidersWaiting/2+1 {
		t.Fatalf("%d outsiders' keys counted, want %d", kept, MaxOutsidersWaiting/2+1)
	}
	// Their turn still comes in the order they came (the members' keys, not tried again, are forgotten too).
	hold()
	if !g.Waiting("later") || g.Waiting("upload-0") {
		t.Fatal("the first outsider in line did not get its turn")
	}
	release, ok := g.Try(Outsiders, "upload-0")
	if !ok {
		t.Fatal("the first outsider's key did not get the slot kept for it")
	}
	release()
	m()
}
