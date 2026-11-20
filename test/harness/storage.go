package harness

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The login of the stand-in storage, set in test/webdav/Dockerfile.
const (
	storageUser     = "drive"
	storagePassword = "drive-test-password"
	storageImage    = "superit-drive-test-webdav"
)

// container is the stand-in Drive storage: Apache with mod_dav in Docker.
type container struct {
	id  string
	url string // http://127.0.0.1:port
}

func startStorage() (*container, error) {
	_, here, _, _ := runtime.Caller(0)
	dockerfileDir := filepath.Join(filepath.Dir(here), "..", "webdav")
	if out, err := exec.Command("docker", "build", "-q", "-t", storageImage, dockerfileDir).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker build: %v: %s", err, out)
	}
	out, err := exec.Command("docker", "run", "-d", "--rm", "--label", "superit-drive-test=1",
		"-p", "127.0.0.1::80", storageImage).Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	c := &container{id: strings.TrimSpace(string(out))}
	port, err := exec.Command("docker", "port", c.id, "80/tcp").Output()
	if err != nil {
		c.stop()
		return nil, fmt.Errorf("docker port: %w", err)
	}
	c.url = "http://" + strings.TrimSpace(strings.Split(string(port), "\n")[0])

	// Wait until it answers a listing.
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequest("PROPFIND", c.url+"/", nil)
		req.SetBasicAuth(storageUser, storagePassword)
		req.Header.Set("Depth", "0")
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusMultiStatus {
				return c, nil
			}
		}
		if time.Now().After(deadline) {
			c.stop()
			return nil, fmt.Errorf("the storage container did not answer within 30 seconds (last error: %v)", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *container) stop() {
	_ = exec.Command("docker", "rm", "-f", c.id).Run()
}

// Storage is one Drive's folder on the stand-in storage, reached directly over WebDAV with the storage login, the way
// someone with that login (or Superit support) would see it. Paths are relative to the Drive's folder and use real
// names: "My files/reka@example.com/notes.txt".
//
// This is deliberately a separate, minimal WebDAV client: tests must not check the Drive's own client against itself.
type Storage struct {
	root     string // address of the Drive's folder, without a trailing slash
	user     string
	password string
}

// An Entry is a file or folder as the storage lists it.
type Entry struct {
	Name     string
	Folder   bool
	Size     int64
	Modified time.Time
}

func (s *Storage) address(p string, folder bool) string {
	var b strings.Builder
	b.WriteString(s.root)
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			b.WriteString("/" + url.PathEscape(seg))
		}
	}
	if folder {
		b.WriteString("/")
	}
	return b.String()
}

func (s *Storage) do(t *testing.T, method, address string, body io.Reader, header http.Header) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.SetBasicAuth(s.user, s.password)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// MakeFolder makes the folder at p and every folder above it, up to and including the Drive's folder.
func (s *Storage) MakeFolder(t *testing.T, p string) {
	t.Helper()
	parts := []string{""}
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			parts = append(parts, parts[len(parts)-1]+"/"+seg)
		}
	}
	for _, part := range parts {
		status, body := s.do(t, "MKCOL", s.address(part, true), nil, nil)
		if status != http.StatusCreated && status != http.StatusMethodNotAllowed {
			t.Fatalf("MKCOL %s: %d %s", part, status, body)
		}
	}
}

// Put writes a file at p, making the folders above it.
func (s *Storage) Put(t *testing.T, p, content string) {
	t.Helper()
	if dir := filepath.ToSlash(filepath.Dir(p)); dir != "." {
		s.MakeFolder(t, dir)
	} else {
		s.MakeFolder(t, "")
	}
	status, body := s.do(t, http.MethodPut, s.address(p, false), strings.NewReader(content), nil)
	if status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("PUT %s: %d %s", p, status, body)
	}
}

// Get reads the file at p. ok is false when there is no such file.
func (s *Storage) Get(t *testing.T, p string) (content []byte, ok bool) {
	t.Helper()
	status, body := s.do(t, http.MethodGet, s.address(p, false), nil, nil)
	switch status {
	case http.StatusOK:
		return body, true
	case http.StatusNotFound:
		return nil, false
	}
	t.Fatalf("GET %s: %d %s", p, status, body)
	return nil, false
}

// Delete removes the file or folder at p.
func (s *Storage) Delete(t *testing.T, p string, folder bool) {
	t.Helper()
	status, body := s.do(t, http.MethodDelete, s.address(p, folder), nil, nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("DELETE %s: %d %s", p, status, body)
	}
}

// List returns what is directly inside the folder at p. ok is false when there is no such folder.
func (s *Storage) List(t *testing.T, p string) (entries []Entry, ok bool) {
	t.Helper()
	address := s.address(p, true)
	status, body := s.do(t, "PROPFIND", address, nil, http.Header{"Depth": {"1"}})
	if status == http.StatusNotFound {
		return nil, false
	}
	if status != http.StatusMultiStatus {
		t.Fatalf("PROPFIND %s: %d %s", p, status, body)
	}
	var ms struct {
		Responses []struct {
			Href  string `xml:"DAV: href"`
			Props []struct {
				Collection   *struct{} `xml:"DAV: resourcetype>collection"`
				Length       string    `xml:"DAV: getcontentlength"`
				LastModified string    `xml:"DAV: getlastmodified"`
			} `xml:"DAV: propstat>prop"`
		} `xml:"DAV: response"`
	}
	if err := xml.Unmarshal(body, &ms); err != nil {
		t.Fatal(err)
	}
	self, _ := url.Parse(address)
	for _, r := range ms.Responses {
		u, err := self.Parse(r.Href)
		if err != nil || strings.TrimSuffix(u.Path, "/") == strings.TrimSuffix(self.Path, "/") {
			continue
		}
		e := Entry{Name: filepath.Base(strings.TrimSuffix(u.Path, "/"))}
		for _, prop := range r.Props {
			if prop.Collection != nil {
				e.Folder = true
			}
			if n, err := strconv.ParseInt(prop.Length, 10, 64); err == nil {
				e.Size = n
			}
			if m, err := http.ParseTime(prop.LastModified); err == nil {
				e.Modified = m
			}
		}
		entries = append(entries, e)
	}
	return entries, true
}

// Move moves the file or folder at from to to, on the storage directly (MOVE, never replacing): what is moved keeps
// the time it last changed, as a storage that answered wrong for a while and then right again would show it.
func (s *Storage) Move(t *testing.T, from, to string, folder bool) {
	t.Helper()
	status, body := s.do(t, "MOVE", s.address(from, folder), nil, http.Header{
		"Destination": {s.address(to, false)},
		"Overwrite":   {"F"},
	})
	if status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("MOVE %s to %s: %d %s", from, to, status, body)
	}
}

// Fill puts n empty files called prefix0, prefix1 and so on into the folder at dir (made if missing), straight into
// the stand-in storage's own disk: thousands in a second, where one request each would take minutes.
func (s *Storage) Fill(t *testing.T, dir string, n int, prefix string) {
	t.Helper()
	root := s.disk() // this Drive's folder
	folder := root
	for _, seg := range strings.Split(dir, "/") {
		if seg != "" {
			folder += "/" + seg
		}
	}
	script := `set -e; mkdir -p "$1"; cd "$1"; i=0; while [ $i -lt "$2" ]; do : > "$3$i"; i=$((i+1)); done; ` +
		`chown -R www-data:www-data "$4"`
	out, err := exec.Command("docker", "exec", shared.storage.id, "sh", "-c", script, "fill",
		folder, strconv.Itoa(n), prefix, root).CombinedOutput()
	if err != nil {
		t.Fatalf("filling %s with %d files: %v: %s", dir, n, err, out)
	}
}

// A Snapshot is a copy of a Drive's folder on the stand-in storage as it was when the copy was taken, kept on the
// storage's own disk outside what WebDAV serves, as a Storage Box keeps its snapshots.
type Snapshot struct {
	dir string
	// Taken is when it was taken, by this computer's clock: a rollback to it declares this time.
	Taken time.Time
}

// disk is the Drive's folder on the stand-in storage's own disk.
func (s *Storage) disk() string {
	return "/var/storage/files" + strings.TrimPrefix(s.root, shared.storage.url)
}

// Snapshot copies the Drive's folder (made first, empty, when there is none) with everything in it, keeping every
// file's and folder's time of last change, as a snapshot of a Storage Box keeps them.
func (s *Storage) Snapshot(t *testing.T) Snapshot {
	t.Helper()
	snap := Snapshot{dir: "/var/storage/snapshots/" + randomHex(8), Taken: time.Now()}
	script := `set -e; mkdir -p "$1" /var/storage/snapshots; chown www-data:www-data "$1"; cp -a "$1" "$2"`
	if out, err := exec.Command("docker", "exec", shared.storage.id, "sh", "-c", script, "snapshot", s.disk(), snap.dir).CombinedOutput(); err != nil {
		t.Fatalf("taking a snapshot of %s: %v: %s", s.disk(), err, out)
	}
	return snap
}

// RollBack puts the Drive's folder back to the snapshot, as rolling a Storage Box back does: everything written since
// is gone, and everything there then is back as it was, with its times. The snapshot can be rolled back to again. Stop
// the Drive first, as the daemon does.
func (s *Storage) RollBack(t *testing.T, snap Snapshot) {
	t.Helper()
	script := `set -e; rm -rf "$1"; cp -a "$2" "$1"`
	if out, err := exec.Command("docker", "exec", shared.storage.id, "sh", "-c", script, "rollback", s.disk(), snap.dir).CombinedOutput(); err != nil {
		t.Fatalf("rolling %s back: %v: %s", s.disk(), err, out)
	}
}

// PutEscaped writes a file at the address escaped, a path already escaped for the wire ("My%20files/r/a%0Ab.txt"),
// whose name a path of real names cannot say: one with a line break, or bytes that are not text.
func (s *Storage) PutEscaped(t *testing.T, escaped, content string) {
	t.Helper()
	status, body := s.do(t, http.MethodPut, s.root+"/"+escaped, strings.NewReader(content), nil)
	if status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("PUT %s: %d %s", escaped, status, body)
	}
}
