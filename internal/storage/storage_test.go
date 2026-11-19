package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

// Writing is tested against real Apache in the test/ package. This covers how each answer a WebDAV server could give
// to a PUT is read, and that a redirect is never followed: Go would follow it as a GET and report that GET's answer.
func TestPutReadsEveryAnswer(t *testing.T) {
	known := []error{ErrExists, ErrNotFound, ErrFull, ErrUnauthorized}
	other := errors.New("an error of its own")
	cases := []struct {
		status int
		want   error // nil for success
	}{
		{http.StatusCreated, nil},
		{http.StatusNoContent, nil},
		{http.StatusPreconditionFailed, ErrExists}, // If-None-Match: * and the name is taken
		{http.StatusMovedPermanently, ErrExists},   // Apache: a folder of that name
		{http.StatusConflict, ErrNotFound},         // the folder above is missing
		{http.StatusInsufficientStorage, ErrFull},  // no room
		{http.StatusUnauthorized, ErrUnauthorized}, // the login was refused
		{http.StatusInternalServerError, other},    // anything else
		{http.StatusForbidden, other},
	}
	for _, c := range cases {
		var method, ifNoneMatch, body string
		var length int64
		gets := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				gets++
				return
			}
			method, ifNoneMatch, length = r.Method, r.Header.Get("If-None-Match"), r.ContentLength
			data, _ := io.ReadAll(r.Body)
			body = string(data)
			if c.status == http.StatusMovedPermanently {
				w.Header().Set("Location", r.URL.Path+"/")
			}
			w.WriteHeader(c.status)
		}))
		base, _ := url.Parse(srv.URL)
		err := New(base, "u", "p", "/drive").Put(context.Background(), "My files/r/a.txt", strings.NewReader("hello"), 5)
		srv.Close()

		switch c.want {
		case nil:
			if err != nil {
				t.Errorf("%d: got %v, want success", c.status, err)
			}
		case other:
			if err == nil || slices.ContainsFunc(known, func(k error) bool { return errors.Is(err, k) }) {
				t.Errorf("%d: got %v, want an error of its own", c.status, err)
			}
		default:
			if !errors.Is(err, c.want) {
				t.Errorf("%d: got %v, want %v", c.status, err, c.want)
			}
		}
		if method != http.MethodPut || ifNoneMatch != "*" || length != 5 || body != "hello" {
			t.Errorf("%d: sent %s with If-None-Match %q, Content-Length %d, body %q", c.status, method, ifNoneMatch, length, body)
		}
		if gets != 0 {
			t.Errorf("%d: the client followed the answer with a GET", c.status)
		}
	}
}

// An empty file is sent with Content-Length: 0, not as a chunked body some servers refuse.
func TestPutSendsAnEmptyFileWithItsLength(t *testing.T) {
	var lengths []string
	var chunked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lengths = r.Header.Values("Content-Length")
		chunked = len(r.TransferEncoding) > 0
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	if err := New(base, "u", "p", "/").Put(context.Background(), "empty.txt", strings.NewReader(""), 0); err != nil {
		t.Fatal(err)
	}
	if chunked || len(lengths) != 1 || lengths[0] != "0" {
		t.Errorf("sent Content-Length %q, chunked %v", lengths, chunked)
	}
}

// Listing is tested against real Apache in the test/ package. This covers what other WebDAV servers do differently:
// absolute addresses in href, upper-case escapes, properties a server could not give.
func TestListReadsWhatOtherServersSend(t *testing.T) {
	var gotPath, gotDepth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotDepth = r.URL.EscapedPath(), r.Header.Get("Depth")
		if user, pass, _ := r.BasicAuth(); user != "u" || pass != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response><d:href>http://` + r.Host + `/base/drive/My%20files/R%C3%A9ka/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
  <d:response><d:href>/base/drive/My%20files/R%c3%a9ka/Tax%20notes.pdf</d:href>
    <d:propstat><d:prop><d:resourcetype/><d:getcontentlength>1234</d:getcontentlength>
      <d:getlastmodified>Wed, 18 Nov 2026 16:20:00 GMT</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
  <d:response><d:href>/base/drive/My%20files/R%C3%A9ka/Drafts/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
    <d:propstat><d:prop><d:getcontentlength/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>
  </d:response>
  <d:response><d:href>/base/drive/My%20files/R%C3%A9ka/Drafts/deeper.txt</d:href>
    <d:propstat><d:prop><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
</d:multistatus>`))
	}))
	defer srv.Close()

	base, _ := url.Parse(srv.URL + "/base")
	c := New(base, "u", "p", "/drive")
	entries, err := c.List(context.Background(), "My files/Réka")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/base/drive/My%20files/R%C3%A9ka/" || gotDepth != "1" {
		t.Errorf("asked for %s with Depth %q", gotPath, gotDepth)
	}
	want := []Entry{
		{Name: "Tax notes.pdf", Size: 1234, Changed: time.Date(2026, 11, 18, 16, 20, 0, 0, time.UTC)},
		{Name: "Drafts", Folder: true},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, entries[i], want[i])
		}
	}

	if _, err := New(base, "u", "wrong", "/drive").List(context.Background(), ""); err != ErrUnauthorized {
		t.Errorf("a refused login: got %v, want ErrUnauthorized", err)
	}
}

func TestPathsCannotLeaveTheDrivesFolder(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	c := New(base, "u", "p", "/drive")
	for _, p := range []string{"..", "../other", "My files/../..", "My files/./x"} {
		if _, err := c.List(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("List(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.MakeFolder(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("MakeFolder(%q): got %v, want ErrBadPath", p, err)
		}
		if _, err := c.Stat(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Stat(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.Put(context.Background(), p, strings.NewReader("x"), 1); !errors.Is(err, ErrBadPath) {
			t.Errorf("Put(%q): got %v, want ErrBadPath", p, err)
		}
		if _, err := c.Open(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Open(%q): got %v, want ErrBadPath", p, err)
		}
	}
	if called {
		t.Error("a refused path reached the storage")
	}
}
