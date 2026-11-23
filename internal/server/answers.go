package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// A turn among the visitors' or the guests' requests (admission, visitors.go) is for work on the Drive's side: the
// index, the storage. It must never be kept by the person's own connection, or a handful of connections that send
// nothing, or take nothing, would keep everyone else out. So:
//
//   - a small body (the JSON of a change) is read before a guest's turn is taken (bodyFirst), and must arrive within
//     bodyWait, for members too (readBody);
//   - an answer is made while the turn is held, kept (heldAnswer), and sent after the turn is given back (sendHeld),
//     within answerWait. A list too long to keep (a guest's, of a folder of thousands) is sent as it is made instead,
//     its turn held, and must be taken within listWait.
//
// A transfer's bytes are neither: it leaves its turn once it is decided, and has its own deadlines (transfers.go).
const (
	// bodyWait is how long a small body may take to arrive.
	bodyWait = 15 * time.Second
	// guestBodyMost is the longest body a guest's change may have (an address and a code, a name, a folder's id): read
	// before any turn is taken, so kept small.
	guestBodyMost = 8 << 10
	// answerWait is how long a person may take to take an answer that was kept for them.
	answerWait = 30 * time.Second
	// listWait is how long a guest may take to take a list too long to keep.
	listWait = 2 * time.Minute
	// guestAnswerMost is the longest answer kept for a guest: a longer one (a long list) is sent as it is made.
	// visitorAnswerMost is the same for a visitor, whose longest page (visitorListLimit items) is well within it.
	guestAnswerMost   = 64 << 10
	visitorAnswerMost = 4 << 20
	// heldFree is the longest kept answer that is sent without counting: it fits the connection's own buffers. Longer
	// ones wait for whoever takes them, and all of those together may hold heldAtOnce bytes: past it, one more is told
	// the Drive is busy. So what waits for slow readers is bounded, and only long pages can ever be made to wait.
	heldFree   = 64 << 10
	heldAtOnce = 8 << 20
)

// heldAnswer keeps an answer, instead of sending it, until sendHeld: up to most bytes. One more byte and it is sent
// as it is made from then on (spilled), within listWait.
type heldAnswer struct {
	http.ResponseWriter // where it goes in the end; its headers are set there directly
	most                int
	status              int
	body                bytes.Buffer
	spilled             bool
}

// Unwrap lets http.ResponseController reach the connection (readBody's deadline).
func (h *heldAnswer) Unwrap() http.ResponseWriter { return h.ResponseWriter }

func (h *heldAnswer) WriteHeader(status int) {
	if h.status == 0 {
		h.status = status
	}
}

func (h *heldAnswer) Write(p []byte) (int, error) {
	if h.spilled {
		return h.ResponseWriter.Write(p)
	}
	if h.status == 0 {
		h.status = http.StatusOK
	}
	if h.body.Len()+len(p) <= h.most {
		return h.body.Write(p)
	}
	h.spilled = true
	_ = http.NewResponseController(h.ResponseWriter).SetWriteDeadline(time.Now().Add(listWait))
	h.ResponseWriter.WriteHeader(h.status)
	if _, err := h.ResponseWriter.Write(h.body.Bytes()); err != nil {
		return 0, err
	}
	h.body = bytes.Buffer{}
	return h.ResponseWriter.Write(p)
}

// sendHeld sends the answer h kept, to be taken within answerWait. Call it once the turn is given back. Nothing was
// kept when the request went on as a transfer, or was not answered at all.
func (s *Server) sendHeld(r *http.Request, h *heldAnswer) {
	w := h.ResponseWriter
	rc := http.NewResponseController(w)
	if h.spilled {
		if rc.Flush() == nil {
			_ = rc.SetWriteDeadline(time.Time{}) // the connection may serve another request
		}
		return
	}
	if h.status == 0 {
		return
	}
	if n := int64(h.body.Len()); n > heldFree {
		if s.heldGoing.Add(n) > heldAtOnce {
			s.heldGoing.Add(-n)
			_ = rc.SetWriteDeadline(time.Now().Add(answerWait))
			w.Header().Del("Content-Security-Policy") // the page's own, if it set one: the answer is not that page
			w.Header().Set("Content-Security-Policy", policyFor(r.URL.Path))
			w.Header().Set("Retry-After", "5")
			s.refuse(w, r, http.StatusServiceUnavailable, visitorsBusy)
			if rc.Flush() == nil {
				_ = rc.SetWriteDeadline(time.Time{})
			}
			return
		}
		defer s.heldGoing.Add(-n)
	}
	_ = rc.SetWriteDeadline(time.Now().Add(answerWait))
	w.WriteHeader(h.status)
	if _, err := w.Write(h.body.Bytes()); err != nil {
		return // they did not take it in time, or have gone: the connection ends
	}
	if rc.Flush() == nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
}

// bodyFirst reads a request's small body (at most most bytes, within bodyWait) and puts it back for the handler to
// read, so that nothing waits for it afterwards. ok is false when the person was already answered.
func (s *Server) bodyFirst(w http.ResponseWriter, r *http.Request, most int64) (ok bool) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Body == nil || r.Body == http.NoBody {
		return true
	}
	if r.ContentLength > most {
		w.Header().Set("Connection", "close") // what they send is not read
		s.refuse(w, r, http.StatusRequestEntityTooLarge, unreadable)
		return false
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyWait))
	b, err := io.ReadAll(io.LimitReader(r.Body, most+1))
	if err != nil || int64(len(b)) > most {
		// The deadline stays: nothing more is waited for on this connection.
		w.Header().Set("Connection", "close")
		status := http.StatusBadRequest
		if err == nil {
			status = http.StatusRequestEntityTooLarge
		}
		s.refuse(w, r, status, unreadable)
		return false
	}
	_ = rc.SetReadDeadline(time.Time{})
	r.Body = io.NopCloser(bytes.NewReader(b))
	return true
}

// readSmall reads a small JSON body into v, which must arrive within bodyWait. A body that did not arrive in time
// ends the connection.
func readSmall(w http.ResponseWriter, r *http.Request, v any) error {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyWait))
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v)
	var syntax *json.SyntaxError
	var kind *json.UnmarshalTypeError
	if err == nil || errors.Is(err, io.EOF) || errors.As(err, &syntax) || errors.As(err, &kind) {
		_ = rc.SetReadDeadline(time.Time{}) // it arrived, right or not
		return err
	}
	w.Header().Set("Connection", "close")
	return err
}
