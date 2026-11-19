package transfers

import (
	"context"
	"errors"
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

// waitIn starts a request waiting in line, and sends its release when it gets a slot.
func waitIn(g *Gate, ctx context.Context) <-chan func() {
	ch := make(chan func(), 1)
	go func() {
		release, err := g.Wait(ctx, time.Minute)
		if err == nil {
			ch <- release
		}
	}()
	time.Sleep(20 * time.Millisecond) // so the order of arrival is the order of the calls
	return ch
}

func TestAtMostSlotsAreInFlightAndTheRestWaitInTheOrderTheyCame(t *testing.T) {
	g := NewGate(2)
	a, _ := g.Wait(context.Background(), time.Second)
	b, ok := g.Try("")
	if !ok {
		t.Fatal("the second slot was not free")
	}
	if _, ok := g.Try(""); ok {
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
	g := NewGate(1)
	a, _ := g.Wait(context.Background(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	gaveUp := make(chan error, 1)
	go func() { _, err := g.Wait(ctx, time.Minute); gaveUp <- err }()
	time.Sleep(20 * time.Millisecond)
	next := waitIn(g, context.Background())
	cancel()
	if err := <-gaveUp; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	a()
	taken(t, "the one behind it", next)

	// Waiting too long is ErrBusy.
	if _, err := g.Wait(context.Background(), 30*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
}

func TestAKeyKeepsItsPlaceInLine(t *testing.T) {
	g := NewGate(1)
	g.hold = 200 * time.Millisecond
	a, _ := g.Try("")
	if _, ok := g.Try("upload-1"); ok {
		t.Fatal("upload-1 took a slot that was not free")
	}
	if !g.Waiting("upload-1") {
		t.Fatal("upload-1 is not in line")
	}
	behind := waitIn(g, context.Background())
	if _, ok := g.Try("upload-2"); ok {
		t.Fatal("upload-2 took a slot")
	}

	// Its turn comes: the slot is kept for it, not for the request behind it nor for anyone without the key.
	a()
	stillWaiting(t, "the request behind upload-1", behind)
	if _, ok := g.Try(""); ok {
		t.Fatal("a try without the key took the slot kept for upload-1")
	}
	if g.Waiting("upload-1") {
		t.Fatal("upload-1 is still said to be waiting")
	}
	release, ok := g.Try("upload-1")
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
	g := NewGate(1)
	clock := time.Now()
	g.now = func() time.Time { return clock }
	a, _ := g.Try("")
	g.Try("gone")
	g.Try("back")
	behind := waitIn(g, context.Background())

	clock = clock.Add(Forget / 2)
	g.Try("back") // tried again: it keeps its place
	clock = clock.Add(Forget/2 + time.Second)
	a()
	if g.Waiting("gone") {
		t.Fatal("the forgotten key is still in line")
	}
	release, ok := g.Try("back")
	if !ok {
		t.Fatal("the key tried again did not get its turn")
	}
	stillWaiting(t, "the request behind", behind)
	release()
	taken(t, "the request behind", behind)
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
