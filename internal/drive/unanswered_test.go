package drive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// fakeDAV is a small WebDAV server in memory: paths under /drive, folders and files.
type fakeDAV struct {
	mu      sync.Mutex
	folders map[string]bool
	files   map[string]string
	// loseMove: the next MOVE whose source contains this is done, and its answer lost (the connection is cut).
	loseMove string
	log      []string
	// hook, when set, is called with each request's method and path before it is answered.
	hook func(method, path string)
}

func newFakeDAV() *fakeDAV {
	return &fakeDAV{folders: map[string]bool{"/drive": true}, files: map[string]string{}}
}

func (f *fakeDAV) clean(p string) string { return strings.TrimSuffix(p, "/") }

func (f *fakeDAV) exists(p string) bool { return f.folders[p] || f.hasFile(p) }
func (f *fakeDAV) hasFile(p string) bool {
	_, ok := f.files[p]
	return ok
}

func (f *fakeDAV) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p := range f.folders {
		out = append(out, p+"/")
	}
	for p := range f.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook(r.Method, f.clean(r.URL.Path))
	}
	f.mu.Lock()
	p := f.clean(r.URL.Path)
	f.log = append(f.log, r.Method+" "+p)
	parent := p[:strings.LastIndex(p, "/")]
	switch r.Method {
	case "MKCOL":
		switch {
		case f.exists(p):
			w.WriteHeader(http.StatusMethodNotAllowed)
		case !f.folders[parent]:
			w.WriteHeader(http.StatusConflict)
		default:
			f.folders[p] = true
			w.WriteHeader(http.StatusCreated)
		}
	case "PUT":
		body, _ := io.ReadAll(r.Body)
		switch {
		case f.exists(p) && r.Header.Get("If-None-Match") == "*":
			w.WriteHeader(http.StatusPreconditionFailed)
		case !f.folders[parent]:
			w.WriteHeader(http.StatusConflict)
		default:
			f.files[p] = string(body)
			w.WriteHeader(http.StatusCreated)
		}
	case "GET":
		if c, ok := f.files[p]; ok {
			_, _ = w.Write([]byte(c))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	case "DELETE":
		if !f.exists(p) {
			w.WriteHeader(http.StatusNotFound)
			break
		}
		delete(f.files, p)
		delete(f.folders, p)
		for q := range f.files {
			if strings.HasPrefix(q, p+"/") {
				delete(f.files, q)
			}
		}
		for q := range f.folders {
			if strings.HasPrefix(q, p+"/") {
				delete(f.folders, q)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case "MOVE":
		du, _ := url.Parse(r.Header.Get("Destination"))
		to := f.clean(du.Path)
		toParent := to[:strings.LastIndex(to, "/")]
		switch {
		case !f.exists(p):
			w.WriteHeader(http.StatusNotFound)
		case f.exists(to):
			w.WriteHeader(http.StatusPreconditionFailed)
		case !f.folders[toParent]:
			w.WriteHeader(http.StatusConflict)
		default:
			if c, ok := f.files[p]; ok {
				delete(f.files, p)
				f.files[to] = c
			} else {
				delete(f.folders, p)
				f.folders[to] = true
				for q, c := range f.files {
					if strings.HasPrefix(q, p+"/") {
						delete(f.files, q)
						f.files[to+q[len(p):]] = c
					}
				}
				for q := range f.folders {
					if strings.HasPrefix(q, p+"/") {
						delete(f.folders, q)
						f.folders[to+q[len(p):]] = true
					}
				}
			}
			if f.loseMove != "" && strings.Contains(p, f.loseMove) {
				// The storage has moved it; its answer never reaches the Drive.
				f.loseMove = ""
				f.mu.Unlock()
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(http.StatusCreated)
		}
	case "PROPFIND":
		if !f.exists(p) {
			w.WriteHeader(http.StatusNotFound)
			break
		}
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">`)
		one := func(q string) {
			href := (&url.URL{Path: q}).EscapedPath()
			if f.folders[q] {
				fmt.Fprintf(&b, `<D:response><D:href>%s/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`, href)
			} else {
				fmt.Fprintf(&b, `<D:response><D:href>%s</D:href><D:propstat><D:prop><D:resourcetype/><D:getcontentlength>%d</D:getcontentlength><D:getlastmodified>Mon, 02 Jan 2006 15:04:05 GMT</D:getlastmodified></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`, href, len(f.files[q]))
			}
		}
		one(p)
		if r.Header.Get("Depth") == "1" {
			for q := range f.folders {
				if q != p && q[:strings.LastIndex(q, "/")] == p {
					one(q)
				}
			}
			for q := range f.files {
				if q[:strings.LastIndex(q, "/")] == p {
					one(q)
				}
			}
		}
		b.WriteString(`</D:multistatus>`)
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(b.String()))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	f.mu.Unlock()
}

func driveOnFake(t *testing.T) (*Drive, *index.Index, *fakeDAV) {
	t.Helper()
	f := newFakeDAV()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	x, err := index.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	d := New(x, storage.New(u, "u", "p", "drive"), Options{})
	t.Cleanup(d.Stop)
	return d, x, f
}

func (f *fakeDAV) holds(content string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var at []string
	for p, c := range f.files {
		if c == content {
			at = append(at, p)
		}
	}
	return at
}

// Delete: the MOVE into the trash is done by the storage, its answer is lost. What does the Drive do next?
func TestADeleteWhoseMoveWasNotAnsweredKeepsTheFile(t *testing.T) {
	bg := context.Background()
	d, x, f := driveOnFake(t)
	p, err := d.MyFiles(bg, memberOf("u-reka", "reka@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	up, err := d.Upload(bg, p, nil, "Contract.pdf", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"},
		func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if at := f.holds("PRECIOUS"); len(at) != 1 {
		t.Fatalf("before: %v", at)
	}
	f.mu.Lock()
	f.loseMove = "Contract.pdf"
	f.log = nil
	f.mu.Unlock()
	_, err = d.Delete(bg, p, up.Item, "u-reka")
	t.Logf("Delete answered: %v", err)
	at := f.holds("PRECIOUS")
	t.Logf("the file's bytes are now at: %v; storage: %v", at, f.all())
	if _, ierr := x.Item(bg, p.ID, up.Item.ID); ierr == nil {
		t.Logf("the index still lists the item as live")
	}
	if len(at) == 0 {
		t.Fatalf("DATA LOST: the file is nowhere on the storage, neither in its folder nor in the trash")
	}
}

// DeleteSharedFolder: the same, for a whole shared folder.
func TestASharedFolderDeletedWithoutAnAnswerIsKept(t *testing.T) {
	bg := context.Background()
	d, _, f := driveOnFake(t)
	p, err := d.NewSharedFolder(bg, "Clients", "u-reka")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Upload(bg, p, nil, fmt.Sprintf("f%d.txt", i), strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"},
			func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil }); err != nil {
			t.Fatal(err)
		}
	}
	f.mu.Lock()
	f.loseMove = "Shared folders/Clients"
	f.mu.Unlock()
	_, err = d.DeleteSharedFolder(bg, p)
	t.Logf("DeleteSharedFolder answered: %v", err)
	at := f.holds("PRECIOUS")
	t.Logf("the files' bytes are now at: %v; storage: %v", at, f.all())
	if len(at) == 0 {
		t.Fatalf("DATA LOST: the whole shared folder is nowhere on the storage")
	}
}

// Upload with Replace: the old file is moved aside (putAside), the answer to that MOVE is lost.
func TestAReplacedFileWhoseMoveWasNotAnsweredIsKept(t *testing.T) {
	bg := context.Background()
	d, _, f := driveOnFake(t)
	p, err := d.MyFiles(bg, memberOf("u-reka", "reka@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	decide := func(context.Context) (index.Place, *index.Item, error) { return p, nil, nil }
	if _, err := d.Upload(bg, p, nil, "Contract.pdf", strings.NewReader("PRECIOUS"), 8, Ask, By{Member: "u-reka"}, decide); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.loseMove = "My files/reka@example.com/Contract.pdf"
	f.mu.Unlock()
	_, err = d.Upload(bg, p, nil, "Contract.pdf", strings.NewReader("NEW"), 3, Replace, By{Member: "u-reka"}, decide)
	t.Logf("Upload(replace) answered: %v", err)
	t.Logf("old at %v, new at %v; storage: %v", f.holds("PRECIOUS"), f.holds("NEW"), f.all())
	if len(f.holds("PRECIOUS")) == 0 {
		t.Fatalf("DATA LOST: the replaced file is nowhere, and the upload failed (new at %v)", f.holds("NEW"))
	}
}

var _ = time.Second

func memberOf(id, email string) members.Member { return members.Member{UserID: id, Email: email} }
