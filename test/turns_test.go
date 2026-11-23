package test

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// stalledConnections opens n connections to the Drive, each sending request and then nothing more, and taking nothing of the
// answer: they stay open until the test ends.
func stalledConnections(t *testing.T, d *harness.Drive, n int, request string) {
	t.Helper()
	address, err := url.Parse(d.URL)
	if err != nil {
		t.Fatal(err)
	}
	// A small buffer on this side, set before connecting, so that an answer the connection does not take is not taken
	// by this computer for it either.
	dialer := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var err error
		if e := c.Control(func(fd uintptr) { err = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096) }); e != nil {
			return e
		}
		return err
	}}
	for range n {
		conn, err := dialer.Dial("tcp", address.Host)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if _, err := conn.Write([]byte(request)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond) // every one of them has reached the Drive
}

// The guests' requests are worked on sixteen at a time. A request whose body never arrives must not keep one of those
// turns: its body is read before a turn is taken, so any number of them leave the guests' pages and sign-in working.
func TestABodyThatDoesNotArriveKeepsNoGuestsTurn(t *testing.T) {
	d := harness.Start(t, reka)
	for _, path := range []string{"/s/guest/api/code", "/s/guest/api/sign-in"} {
		stalledConnections(t, d, 20, "POST "+path+" HTTP/1.1\r\nHost: drive\r\nOrigin: "+d.Address+
			"\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{")
	}
	start := time.Now()
	anyone := visitor(d)
	if res := anyone.Get(t, "/s/guest/api/me"); res.Status != http.StatusUnauthorized {
		t.Fatalf("asking who is signed in, with 40 bodies that do not arrive: got %d %s, want 401", res.Status, res.Body)
	}
	if res := anyone.From(d.Address).Do(t, http.MethodPost, "/s/guest/api/code", strings.NewReader(`{"address":"guest@example.org"}`)); res.Status != http.StatusAccepted {
		t.Fatalf("asking for a code, with 40 bodies that do not arrive: got %d %s, want 202", res.Status, res.Body)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("answered after %v: the stalled requests kept it waiting", took)
	}

	// A body longer than a guest's change ever is, is refused unread.
	res := anyone.From(d.Address).Do(t, http.MethodPost, "/s/guest/api/sign-in", strings.NewReader(`{"address":"`+strings.Repeat("a", 16<<10)+`"}`))
	if res.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("a 16 KiB body to sign in: got %d, want 413", res.Status)
	}
}

// Visitors' requests are worked on sixteen at a time too. A visitor's page is made while its turn is held and sent
// after the turn is given back, so visitors who do not take a long page keep nobody waiting.
func TestAPageThatIsNotTakenKeepsNoVisitorsTurn(t *testing.T) {
	d := harness.Start(t, reka)
	c := d.As(reka)
	placeID, _ := myFiles(t, d, reka)
	folder := madeFolder(t, c, placeID, 0, "Long")
	// Enough folders with long names that the page does not fit a connection's buffers.
	var wg sync.WaitGroup
	names := make(chan string)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range names {
				if res := newFolder(t, d.As(reka), placeID, folder.ID, name); res.Status != http.StatusCreated {
					t.Errorf("making %q: got %d %s", name, res.Status, res.Body)
				}
			}
		}()
	}
	for i := range 800 {
		names <- fmt.Sprintf("%03d-%s", i, strings.Repeat("n", 230))
	}
	close(names)
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	_, page := madeLink(t, d, c, placeID, folder.ID, false)
	whole := visitor(d).Get(t, page)
	if whole.Status != http.StatusOK || len(whole.Body) < 400<<10 {
		t.Fatalf("the long page: got %d with %d bytes, want 200 and more than a connection's buffers hold", whole.Status, len(whole.Body))
	}

	stalledConnections(t, d, 16, "GET "+page+" HTTP/1.1\r\nHost: drive\r\n\r\n")
	start := time.Now()
	if res := visitor(d).Get(t, "/s/"+strings.Repeat("A", 32)); res.Status != http.StatusNotFound {
		t.Fatalf("another link's page, with 16 long pages not taken: got %d, want 404 (a link that is not there)", res.Status)
	}
	other := madeFolder(t, c, placeID, 0, "Short")
	_, short := madeLink(t, d, c, placeID, other.ID, false)
	if res := visitor(d).Get(t, short); res.Status != http.StatusOK {
		t.Fatalf("a short page, with 16 long pages not taken: got %d %s", res.Status, res.Body)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("answered after %v: the pages not taken kept it waiting", took)
	}
	// How much is kept for those who do not take their pages is bounded too: internal/server/answers_test.go.
}
