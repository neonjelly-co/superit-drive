package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

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
	}
	if called {
		t.Error("a refused path reached the storage")
	}
}
