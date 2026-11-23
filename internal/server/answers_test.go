package server

import (
	"net/http"
	"net/http/httptest"
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
