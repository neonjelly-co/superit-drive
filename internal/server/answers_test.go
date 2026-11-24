package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// notTaking is a connection whose person takes nothing of the answer until let is closed.
type notTaking struct {
	*httptest.ResponseRecorder
	writing chan<- struct{}
	let     <-chan struct{}
}

func (n notTaking) Write(p []byte) (int, error) {
	n.writing <- struct{}{}
	<-n.let
	return n.ResponseRecorder.Write(p)
}

// Long answers kept for people who do not take them are bounded together: past heldAtOnce bytes, one more long answer
// is told the Drive is busy, while short ones (which fit a connection's own buffers) go on.
func TestLongAnswersNotTakenAreBoundedTogether(t *testing.T) {
	s := bareServer(t)
	const page = "/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	kept := func(w http.ResponseWriter, size int) *heldAnswer {
		h := &heldAnswer{ResponseWriter: w, most: visitorAnswerMost}
		h.WriteHeader(http.StatusOK)
		if _, err := h.Write(make([]byte, size)); err != nil {
			t.Fatal(err)
		}
		return h
	}
	const long = 1 << 20
	writing, let := make(chan struct{}), make(chan struct{})
	var sent sync.WaitGroup
	for range heldAtOnce / long {
		sent.Add(1)
		go func() {
			defer sent.Done()
			w := notTaking{httptest.NewRecorder(), writing, let}
			s.sendHeld(httptest.NewRequest(http.MethodGet, page, nil), kept(w, long))
		}()
	}
	for range heldAtOnce / long {
		select {
		case <-writing:
		case <-time.After(5 * time.Second):
			t.Fatal("the long answers were not all being sent")
		}
	}

	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", "the page's own")
	s.sendHeld(httptest.NewRequest(http.MethodGet, page, nil), kept(rec, long))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || rec.Body.Len() >= long {
		t.Fatalf("one long answer too many: got %d with %d bytes, want 503 with Retry-After", rec.Code, rec.Body.Len())
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != visitorPolicy {
		t.Fatalf("the busy answer went out with the policy %q, want the visitors'", got)
	}
	rec = httptest.NewRecorder()
	s.sendHeld(httptest.NewRequest(http.MethodGet, page, nil), kept(rec, heldFree))
	if rec.Code != http.StatusOK || rec.Body.Len() != heldFree {
		t.Fatalf("a short answer, with long ones past their bound: got %d with %d bytes", rec.Code, rec.Body.Len())
	}

	close(let)
	sent.Wait()
	if n := s.heldGoing.Load(); n != 0 {
		t.Fatalf("%d bytes still counted as waiting after every answer was taken", n)
	}
	rec = httptest.NewRecorder()
	s.sendHeld(httptest.NewRequest(http.MethodGet, page, nil), kept(rec, long))
	if rec.Code != http.StatusOK || rec.Body.Len() != long {
		t.Fatalf("a long answer once the others were taken: got %d with %d bytes", rec.Code, rec.Body.Len())
	}
}

// An answer longer than may be kept is sent as it is made from then on, whole and in order.
func TestAnAnswerTooLongToKeepIsSentAsItIsMade(t *testing.T) {
	rec := httptest.NewRecorder()
	h := &heldAnswer{ResponseWriter: rec, most: 10}
	h.WriteHeader(http.StatusCreated)
	_, _ = h.Write([]byte("12345678"))
	if rec.Body.Len() != 0 {
		t.Fatalf("sent %q before it was too long to keep", rec.Body.String())
	}
	_, _ = h.Write([]byte("9abc"))
	_, _ = h.Write([]byte("def"))
	bareServer(t).sendHeld(httptest.NewRequest(http.MethodGet, "/s/guest/api/me", nil), h)
	if rec.Code != http.StatusCreated || rec.Body.String() != "123456789abcdef" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

// Many asks for a sign-in link for one address take one place among those waiting; many addresses take as many places
// as there are, and the rest are dropped, answered the same.
func TestAsksForOneAddressWaitOnce(t *testing.T) {
	s := bareServer(t)
	ask := func(address string) int {
		req := httptest.NewRequest(http.MethodPost, GuestPrefix+"/api/code", strings.NewReader(`{"address":"`+address+`"}`))
		rec := httptest.NewRecorder()
		s.askForCode(rec, req, call{})
		return rec.Code
	}
	for range 500 {
		if got := ask("Anna@Client.Example"); got != http.StatusAccepted {
			t.Fatalf("asking for Anna: got %d", got)
		}
	}
	if n := len(s.codes); n != 1 {
		t.Fatalf("%d asks wait after 500 for one address, want 1", n)
	}
	for i := range 2 * codesWaiting {
		if got := ask(fmt.Sprintf("guest%d@client.example", i)); got != http.StatusAccepted {
			t.Fatalf("asking for guest %d: got %d", i, got)
		}
	}
	if n := len(s.codes); n != codesWaiting {
		t.Fatalf("%d asks wait, want as many as may (%d)", n, codesWaiting)
	}
	// Once the sender has come to Anna's, she may wait again.
	first := <-s.codes
	s.askingMu.Lock()
	delete(s.asking, first.address)
	s.askingMu.Unlock()
	ask("anna@client.example")
	if n := len(s.codes); n != codesWaiting || first.address != "anna@client.example" {
		t.Fatalf("%d asks wait once Anna's was taken and she asked again (the first was for %q)", n, first.address)
	}
}

// A request that says it has a body and never sends it is answered all the same, and its connection ends: Go's server
// would otherwise wait for the body, without a deadline, before it sent the answer.
func TestABodyThatNeverComesIsNotWaitedFor(t *testing.T) {
	was := unreadWait
	unreadWait = 200 * time.Millisecond
	t.Cleanup(func() { unreadWait = was })
	srv := httptest.NewUnstartedServer(bareServer(t).Handler())
	srv.Config.ReadHeaderTimeout = 10 * time.Second // as the Drive's own server: no deadline once the headers are in
	srv.Start()
	t.Cleanup(srv.Close)

	for _, c := range []struct {
		request string
		status  string
	}{
		{"GET /api/me HTTP/1.1\r\nHost: files.example.com\r\nContent-Length: 1\r\n\r\n", "400"},
		{"GET /s/nothing HTTP/1.1\r\nHost: files.example.com\r\nTransfer-Encoding: chunked\r\n\r\n", "400"},
		{"HEAD /s/nothing HTTP/1.1\r\nHost: files.example.com\r\nContent-Length: 1\r\n\r\n", "400"},
		{"POST /api/me HTTP/1.1\r\nHost: files.example.com\r\nContent-Length: 1\r\n\r\n", "403"},
		{"POST /s/nothing HTTP/1.1\r\nHost: files.example.com\r\nContent-Length: 1\r\n\r\n", "405"},
	} {
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.WriteString(conn, c.request); err != nil {
			t.Fatal(err)
		}
		// The answer, and then the end of the connection: nothing waits for the body.
		all, err := io.ReadAll(conn)
		conn.Close()
		line, _, _ := strings.Cut(string(all), "\r\n")
		if err != nil || !strings.HasPrefix(line, "HTTP/1.1 "+c.status) {
			t.Errorf("%q: answered %q (%v), want %s and the connection ended", strings.SplitN(c.request, "\r\n", 2)[0], line, err, c.status)
		}
	}
}
