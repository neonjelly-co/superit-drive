package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
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
		if _, err := c.OpenPart(context.Background(), p, 0, 1); !errors.Is(err, ErrBadPath) {
			t.Errorf("OpenPart(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.PutPart(context.Background(), p, 0, strings.NewReader("x"), 1, 1); !errors.Is(err, ErrBadPath) {
			t.Errorf("PutPart(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.Move(context.Background(), p, "a.txt"); !errors.Is(err, ErrBadPath) {
			t.Errorf("Move(%q, a.txt): got %v, want ErrBadPath", p, err)
		}
		if err := c.Move(context.Background(), "a.txt", p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Move(a.txt, %q): got %v, want ErrBadPath", p, err)
		}
		if err := c.MoveFolder(context.Background(), p, "a"); !errors.Is(err, ErrBadPath) {
			t.Errorf("MoveFolder(%q, a): got %v, want ErrBadPath", p, err)
		}
		if err := c.MoveFolder(context.Background(), "a", p); !errors.Is(err, ErrBadPath) {
			t.Errorf("MoveFolder(a, %q): got %v, want ErrBadPath", p, err)
		}
		if err := c.CreateFolder(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("CreateFolder(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.Delete(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Delete(%q): got %v, want ErrBadPath", p, err)
		}
		if err := c.DeleteFolder(context.Background(), p); !errors.Is(err, ErrBadPath) {
			t.Errorf("DeleteFolder(%q): got %v, want ErrBadPath", p, err)
		}
	}
	// Nor is the Drive's own folder ever deleted.
	for _, dir := range []string{"", "/", "//"} {
		if err := c.DeleteFolder(context.Background(), dir); err == nil {
			t.Errorf("DeleteFolder(%q) was not refused", dir)
		}
	}
	if called {
		t.Error("a refused path reached the storage")
	}
}

// Reading part of a file is tested against real Apache through the storage check in the test/ package. This covers
// what is asked for and how each answer is read.
func TestOpenPartAsksForTheRangeAndChecksTheAnswer(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		contentRange string
		wantErr      error // nil for success; errOther for an error of its own
		wantSize     int64
	}{
		{"the part", http.StatusPartialContent, "bytes 1000-1999/262144", nil, 1000},
		{"the part, length unknown", http.StatusPartialContent, "bytes 1000-1999/*", nil, 1000},
		{"a shorter part where the file ends", http.StatusPartialContent, "bytes 1000-1499/1500", nil, 500},
		{"the whole file", http.StatusOK, "", ErrNoRanges, 0},
		{"another part", http.StatusPartialContent, "bytes 0-999/262144", errOther, 0},
		{"a longer part", http.StatusPartialContent, "bytes 1000-2999/262144", errOther, 0},
		{"no Content-Range", http.StatusPartialContent, "", errOther, 0},
		{"no file", http.StatusNotFound, "", ErrNotFound, 0},
		{"a refused login", http.StatusUnauthorized, "", ErrUnauthorized, 0},
		{"a refusal", http.StatusForbidden, "", errOther, 0},
	}
	for _, c := range cases {
		var path, rangeHeader, encoding string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, rangeHeader, encoding = r.URL.EscapedPath(), r.Header.Get("Range"), r.Header.Get("Accept-Encoding")
			if c.contentRange != "" {
				w.Header().Set("Content-Range", c.contentRange)
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(strings.Repeat("x", int(c.wantSize))))
		}))
		base, _ := url.Parse(srv.URL)
		f, err := New(base, "u", "p", "/drive").OpenPart(context.Background(), "My files/r/a b.bin", 1000, 1000)
		if err == nil {
			data, _ := io.ReadAll(f.Body)
			f.Body.Close()
			if f.Size != c.wantSize || int64(len(data)) != c.wantSize {
				t.Errorf("%s: got %d bytes announced as %d, want %d", c.name, len(data), f.Size, c.wantSize)
			}
		}
		srv.Close()
		checkErr(t, c.name, err, c.wantErr)
		if path != "/drive/My%20files/r/a%20b.bin" || rangeHeader != "bytes=1000-1999" || encoding != "identity" {
			t.Errorf("%s: asked for %s with Range %q and Accept-Encoding %q", c.name, path, rangeHeader, encoding)
		}
	}
}

func TestPutPartSaysWhereThePartGoes(t *testing.T) {
	var method, contentRange, ifNoneMatch, body string
	var length int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, contentRange, ifNoneMatch, length = r.Method, r.Header.Get("Content-Range"), r.Header.Get("If-None-Match"), r.ContentLength
		data, _ := io.ReadAll(r.Body)
		body = string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	c := New(base, "u", "p", "/drive")
	if err := c.PutPart(context.Background(), "a.bin", 4, strings.NewReader("BBBB"), 4, 8); err != nil {
		t.Fatal(err)
	}
	// No If-None-Match: the file is there, and that is the point.
	if method != http.MethodPut || contentRange != "bytes 4-7/8" || ifNoneMatch != "" || length != 4 || body != "BBBB" {
		t.Errorf("sent %s with Content-Range %q, If-None-Match %q, Content-Length %d, body %q",
			method, contentRange, ifNoneMatch, length, body)
	}
	if err := c.PutPart(context.Background(), "a.bin", 6, strings.NewReader("BBBB"), 4, 8); err == nil {
		t.Error("a part that runs past the end of the file was sent")
	}
}

func TestMoveNeverReplaces(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusCreated, nil},
		{http.StatusNoContent, nil},
		{http.StatusPreconditionFailed, ErrExists}, // Overwrite: F and the name is taken
		{http.StatusNotFound, ErrNotFound},         // nothing to move
		{http.StatusConflict, ErrNotFound},         // the folder above the new name is missing
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, errOther},
		{http.StatusBadGateway, errOther}, // Apache: the destination is on another server
	}
	for _, c := range cases {
		var method, path, destination, overwrite string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, path, destination, overwrite = r.Method, r.URL.EscapedPath(), r.Header.Get("Destination"), r.Header.Get("Overwrite")
			w.WriteHeader(c.status)
		}))
		base, _ := url.Parse(srv.URL)
		err := New(base, "u", "p", "/drive").Move(context.Background(), "My files/r/a.txt", "My files/r/Réka's b.txt")
		srv.Close()
		checkErr(t, strconv.Itoa(c.status), err, c.want)
		if method != "MOVE" || path != "/drive/My%20files/r/a.txt" ||
			destination != srv.URL+"/drive/My%20files/r/R%C3%A9ka%27s%20b.txt" || overwrite != "F" {
			t.Errorf("%d: sent %s %s with Destination %q and Overwrite %q", c.status, method, path, destination, overwrite)
		}
	}
}

// A folder is moved by its address with a trailing slash, to an address without one. How Apache answers each case
// was measured on the stand-in storage (test/webdav): a file already at the new place is 412, a folder there is a
// redirect without a Location, a folder addressed as a file is a redirect to its address with a slash, a missing
// folder above the new place is 404 or 409, and moving a folder into itself is 403.
func TestMoveFolderAddressesTheFolderAndNeverReplaces(t *testing.T) {
	cases := []struct {
		status   int
		location string
		want     error
	}{
		{http.StatusCreated, "", nil},
		{http.StatusNoContent, "", nil},
		{http.StatusPreconditionFailed, "", ErrExists},
		{http.StatusMovedPermanently, "", ErrExists},
		{http.StatusNotFound, "", ErrNotFound},
		{http.StatusConflict, "", ErrNotFound},
		{http.StatusForbidden, "", errOther}, // into itself
		{http.StatusInternalServerError, "", errOther},
	}
	for _, c := range cases {
		var method, path, destination, overwrite string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method, path, destination, overwrite = r.Method, r.URL.EscapedPath(), r.Header.Get("Destination"), r.Header.Get("Overwrite")
			if c.location != "" {
				w.Header().Set("Location", c.location)
			}
			w.WriteHeader(c.status)
		}))
		base, _ := url.Parse(srv.URL)
		err := New(base, "u", "p", "/drive").MoveFolder(context.Background(), "My files/r/Old drafts", "My files/r/Drafts 2026")
		srv.Close()
		checkErr(t, strconv.Itoa(c.status), err, c.want)
		if method != "MOVE" || path != "/drive/My%20files/r/Old%20drafts/" ||
			destination != srv.URL+"/drive/My%20files/r/Drafts%202026" || overwrite != "F" {
			t.Errorf("%d: sent %s %s with Destination %q and Overwrite %q", c.status, method, path, destination, overwrite)
		}
	}
	base, _ := url.Parse("http://127.0.0.1:1")
	for _, top := range []string{"", "/"} {
		if err := New(base, "u", "p", "/drive").MoveFolder(context.Background(), top, "x"); err == nil {
			t.Errorf("MoveFolder(%q) was not refused", top)
		}
	}
}

// A file's address that turns out to be a folder's is answered by Apache with a redirect to the folder's address: that
// is "no file there", not "something is already at the new place".
func TestMoveOfAFileThatIsAFolder(t *testing.T) {
	for _, c := range []struct {
		location string
		want     error
	}{
		{"/drive/My%20files/r/a.txt/", ErrNotFound},
		{"", ErrExists},
		{"/drive/My%20files/r/b.txt/", ErrExists},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.location != "" {
				w.Header().Set("Location", c.location)
			}
			w.WriteHeader(http.StatusMovedPermanently)
		}))
		base, _ := url.Parse(srv.URL)
		err := New(base, "u", "p", "/drive").Move(context.Background(), "My files/r/a.txt", "My files/r/b.txt")
		srv.Close()
		checkErr(t, "redirect to "+c.location, err, c.want)
	}
}

func TestCreateFolderMakesOneFolderAndRefusesWhatIsThere(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusCreated, nil},
		{http.StatusMethodNotAllowed, ErrExists}, // a folder is there
		{http.StatusMovedPermanently, ErrExists},
		{http.StatusConflict, ErrNotFound}, // the folder above is missing
		{http.StatusInsufficientStorage, ErrFull},
		{http.StatusBadRequest, errOther}, // Apache: a file is there
		{http.StatusUnauthorized, ErrUnauthorized},
	}
	for _, c := range cases {
		var requests []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.EscapedPath())
			w.WriteHeader(c.status)
		}))
		base, _ := url.Parse(srv.URL)
		err := New(base, "u", "p", "/drive").CreateFolder(context.Background(), "My files/r/New folder")
		srv.Close()
		checkErr(t, strconv.Itoa(c.status), err, c.want)
		if !slices.Equal(requests, []string{"MKCOL /drive/My%20files/r/New%20folder/"}) {
			t.Errorf("%d: sent %q, want one MKCOL of the folder", c.status, requests)
		}
	}
}

func TestDeleteAddressesFilesAndFolders(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNoContent, nil},
		{http.StatusOK, nil},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusMultiStatus, errOther}, // some of a folder could not be deleted
		{http.StatusForbidden, errOther},
	}
	for _, c := range cases {
		var paths []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				paths = append(paths, r.URL.EscapedPath())
			}
			w.WriteHeader(c.status)
		}))
		base, _ := url.Parse(srv.URL)
		store := New(base, "u", "p", "/drive")
		checkErr(t, "file, "+strconv.Itoa(c.status), store.Delete(context.Background(), "My files/r/a.txt"), c.want)
		checkErr(t, "folder, "+strconv.Itoa(c.status), store.DeleteFolder(context.Background(), "My files/r/Old"), c.want)
		srv.Close()
		if !slices.Equal(paths, []string{"/drive/My%20files/r/a.txt", "/drive/My%20files/r/Old/"}) {
			t.Errorf("%d: deleted %q", c.status, paths)
		}
	}
}

// A redirect is reported with where it pointed: an http address that a server sends to https, for one.
func TestAnUnknownAnswerSaysWhatItWas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://dav.example.com/", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	_, err := New(base, "u", "p", "/").List(context.Background(), "")
	var status *StatusError
	if !errors.As(err, &status) || status.Status != "301 Moved Permanently" || status.Location != "https://dav.example.com/" {
		t.Fatalf("got %v", err)
	}
}

// errOther stands for an error of the method's own, none of the ones a caller tells apart.
var errOther = errors.New("an error of its own")

func checkErr(t *testing.T, name string, err, want error) {
	t.Helper()
	known := []error{ErrExists, ErrNotFound, ErrFull, ErrUnauthorized, ErrNoRanges, ErrBadPath}
	switch want {
	case nil:
		if err != nil {
			t.Errorf("%s: got %v, want success", name, err)
		}
	case errOther:
		if err == nil || slices.ContainsFunc(known, func(k error) bool { return errors.Is(err, k) }) {
			t.Errorf("%s: got %v, want an error of its own", name, err)
		}
	default:
		if !errors.Is(err, want) {
			t.Errorf("%s: got %v, want %v", name, err, want)
		}
	}
}
