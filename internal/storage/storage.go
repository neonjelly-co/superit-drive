// Package storage talks to the Drive storage over WebDAV: a Hetzner Storage Box bought from Superit, or the
// customer's own WebDAV server. It is the only package that speaks WebDAV.
//
// Paths are slash-separated and relative to the Drive's folder on the storage (DRIVE_STORAGE_FOLDER), with real
// names: "My files/reka@example.com/Tax notes.pdf". The client escapes them for the wire.
//
// Only what a Storage Box is known to do is used: PROPFIND with Depth 0 or 1 (a recursive listing is refused), MKCOL,
// PUT, GET, GET of a byte range, MOVE, DELETE, and PUT with Content-Range into part of a file (measured on a Storage
// Box; other servers may not do it, which `superit-drive check-storage` finds out). Folders are always addressed with
// a trailing slash; without it Apache answers with a redirect. The client never follows a redirect: Go would turn a
// redirected PUT into a GET.
package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// Errors a caller may want to tell apart.
var (
	ErrNotFound     = errors.New("not found on the storage")
	ErrUnauthorized = errors.New("the storage refused the Drive's login")
	// ErrExists is returned by Put and Move when something is already at the path.
	ErrExists = errors.New("already on the storage")
	// ErrFull is returned when the storage answers that it has no room (507 Insufficient Storage).
	ErrFull = errors.New("the storage is full")
	// ErrBadPath is returned for a path with a "." or ".." in it, which could reach outside the Drive's folder.
	ErrBadPath = errors.New(`a path on the storage cannot contain "." or ".."`)
	// ErrNoRanges is returned by OpenPart when the server sends the whole file instead of the part asked for.
	ErrNoRanges = errors.New("the storage sent the whole file instead of part of it")
)

// A StatusError is an answer from the storage that has no meaning of its own here, such as 403 Forbidden.
type StatusError struct {
	Doing    string // what the client was doing, such as "writing"
	Path     string // the path, relative to the Drive's folder; MakeFolder gives a folder's whole path on the server
	Status   string // the answer's status, such as "403 Forbidden"
	Location string // where the answer pointed, for a redirect
}

func (e *StatusError) Error() string {
	s := fmt.Sprintf("%s %q on the storage: it answered %s", e.Doing, e.Path, e.Status)
	if e.Location != "" {
		s += ", pointing to " + e.Location
	}
	return s
}

// Client is a WebDAV client for one folder on one server.
type Client struct {
	server   *url.URL // scheme and host of the WebDAV server
	root     []string // escaped path segments of the Drive's folder on the server
	user     string
	password string
	http     *http.Client
}

// New makes a client for the Drive's folder (folder, such as "/" or "/drive") on the WebDAV server at address.
func New(address *url.URL, user, password, folder string) *Client {
	server := &url.URL{Scheme: address.Scheme, Host: address.Host}
	root := escapeSegments(path.Join(address.Path, folder))

	// One transport for every call to the storage. Large transfers will need their socket buffers capped here to hold
	// the Drive's memory figure.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = time.Minute

	client := &http.Client{
		Transport: transport,
		// Answer every redirect as it is. Apache redirects a folder's address without the trailing slash, and Go would
		// follow a redirected PUT or PROPFIND as a GET and report that GET's answer instead.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{server: server, root: root, user: user, password: password, http: client}
}

// Address is the address of the Drive's folder on the storage, for log lines. It holds no login.
func (c *Client) Address() string { return c.folderURL("").String() }

// An Entry is one file or folder in a folder on the storage.
type Entry struct {
	Name    string
	Folder  bool
	Size    int64 // bytes; 0 for folders
	Changed time.Time
}

// List returns what is directly inside the folder at dir ("" is the Drive's own folder). It returns ErrNotFound when
// the folder does not exist.
func (c *Client) List(ctx context.Context, dir string) ([]Entry, error) {
	if err := checkPath(dir); err != nil {
		return nil, err
	}
	target := c.folderURL(dir)
	ms, res, err := c.propfind(ctx, target, "1")
	if err != nil {
		return nil, fmt.Errorf("listing %q on the storage: %w", dir, err)
	}
	if ms == nil {
		return nil, statusError("listing", dir, res)
	}
	self := segments(target.EscapedPath())
	entries := make([]Entry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		u, err := target.Parse(r.Href)
		if err != nil {
			continue
		}
		segs := segments(u.EscapedPath())
		if equal(segs, self) || len(segs) != len(self)+1 || !equal(segs[:len(self)], self) {
			continue // the folder itself, or something that is not directly inside it
		}
		e, ok := r.entry(segs[len(segs)-1])
		if ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// propfind asks for the properties the Drive reads, at depth "0" (the item itself) or "1" (and what is directly
// inside). On a 207 answer it returns the decoded listing; on any other it returns a nil listing and the answer, whose
// body is already closed, for the caller to tell what it means.
func (c *Client) propfind(ctx context.Context, target *url.URL, depth string) (*multistatus, *http.Response, error) {
	const body = `<?xml version="1.0" encoding="utf-8"?>` +
		`<D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontentlength/><D:getlastmodified/></D:prop></D:propfind>`
	req, err := c.request(ctx, "PROPFIND", target, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer drain(res)
	if res.StatusCode != http.StatusMultiStatus {
		return nil, res, nil
	}
	var ms multistatus
	if err := xml.NewDecoder(res.Body).Decode(&ms); err != nil {
		return nil, nil, fmt.Errorf("the answer is not a WebDAV listing: %w", err)
	}
	return &ms, res, nil
}

// Stat returns what is at p, without a trailing slash. It returns ErrNotFound when nothing is. A folder there is
// returned as a folder whether the server describes it or (as Apache does) redirects to the address with a slash.
func (c *Client) Stat(ctx context.Context, p string) (Entry, error) {
	if err := checkPath(p); err != nil {
		return Entry{}, err
	}
	name := path.Base("/" + p)
	ms, res, err := c.propfind(ctx, c.fileURL(p), "0")
	if err != nil {
		return Entry{}, fmt.Errorf("looking up %q on the storage: %w", p, err)
	}
	if ms == nil {
		if isRedirect(res.StatusCode) {
			return Entry{Name: name, Folder: true}, nil
		}
		return Entry{}, statusError("looking up", p, res)
	}
	for _, r := range ms.Responses {
		if e, ok := r.entry(name); ok {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("looking up %q on the storage: the answer holds no properties", p)
}

// Put writes a new file at p from body, which must hold exactly size bytes, in one request. The server writes the
// whole file or nothing: a request cut short leaves nothing behind (measured on a Storage Box and on Apache's
// mod_dav_fs, which writes to a hidden temporary file and renames it into place when the last byte arrives).
//
// Put never replaces anything. It sends If-None-Match: *, so a server that honours it refuses when the path is taken,
// and it returns ErrExists when the server says so, including a folder's redirect. A server that ignores the header
// would replace a file of the same name: callers check with Stat first. ErrNotFound means the folder above p is
// missing; ErrFull that the storage has no room.
//
// If reading body fails, the request is abandoned before its end and the error is returned.
func (c *Client) Put(ctx context.Context, p string, body io.Reader, size int64) error {
	if err := checkPath(p); err != nil {
		return err
	}
	if size == 0 {
		body = http.NoBody // so the request says Content-Length: 0 instead of sending an empty chunked body
	}
	req, err := c.request(ctx, http.MethodPut, c.fileURL(p), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("writing %q on the storage: %w", p, err)
	}
	defer drain(res)
	switch {
	case res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK:
		return nil
	case res.StatusCode == http.StatusPreconditionFailed || isRedirect(res.StatusCode):
		return fmt.Errorf("writing %q: %w", p, ErrExists)
	case res.StatusCode == http.StatusConflict:
		return fmt.Errorf("writing %q: the folder above it is missing: %w", p, ErrNotFound)
	case res.StatusCode == http.StatusInsufficientStorage:
		return fmt.Errorf("writing %q: %w", p, ErrFull)
	}
	return statusError("writing", p, res)
}

// PutPart writes body, which must hold exactly size bytes, into the file at p from offset, and keeps what is before
// offset: an upload that stopped resumes this way, from the length the file already has. total is the length of the
// whole file. It sends PUT with Content-Range, which a server may refuse or, worse, ignore and replace the file with
// body alone (RFC 9110 allows both); `superit-drive check-storage` finds out which a server does.
func (c *Client) PutPart(ctx context.Context, p string, offset int64, body io.Reader, size, total int64) error {
	if err := checkPath(p); err != nil {
		return err
	}
	if offset < 0 || size <= 0 || offset+size > total {
		return fmt.Errorf("writing %q: %d bytes from %d do not fit in a file of %d", p, size, offset, total)
	}
	req, err := c.request(ctx, http.MethodPut, c.fileURL(p), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+size-1, total))
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("writing part of %q on the storage: %w", p, err)
	}
	defer drain(res)
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("writing part of %q: the folder above it is missing: %w", p, ErrNotFound)
	case http.StatusInsufficientStorage:
		return fmt.Errorf("writing part of %q: %w", p, ErrFull)
	}
	return statusError("writing part of", p, res)
}

// A File is a file being read from the storage. Close it when done.
type File struct {
	Body io.ReadCloser
	// Size is the file's length in bytes as the server announced it, or -1 when it did not.
	Size int64
}

// Open starts reading the whole file at p. It returns ErrNotFound when there is no file there (a folder included).
func (c *Client) Open(ctx context.Context, p string) (*File, error) {
	if err := checkPath(p); err != nil {
		return nil, err
	}
	req, err := c.request(ctx, http.MethodGet, c.fileURL(p), nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading %q on the storage: %w", p, err)
	}
	if res.StatusCode == http.StatusOK {
		return &File{Body: res.Body, Size: res.ContentLength}, nil
	}
	defer drain(res)
	if isRedirect(res.StatusCode) {
		return nil, fmt.Errorf("reading %q: it is a folder: %w", p, ErrNotFound)
	}
	return nil, statusError("reading", p, res)
}

// OpenPart starts reading length bytes of the file at p, from offset; fewer when the file ends sooner. It returns
// ErrNoRanges when the server sends the whole file instead, and ErrNotFound when there is no file there.
func (c *Client) OpenPart(ctx context.Context, p string, offset, length int64) (*File, error) {
	if err := checkPath(p); err != nil {
		return nil, err
	}
	if offset < 0 || length <= 0 {
		return nil, fmt.Errorf("reading %q: there is no part of %d bytes from %d", p, length, offset)
	}
	req, err := c.request(ctx, http.MethodGet, c.fileURL(p), nil)
	if err != nil {
		return nil, err
	}
	last := offset + length - 1
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, last))
	// A compressed answer would count its range in compressed bytes.
	req.Header.Set("Accept-Encoding", "identity")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading part of %q on the storage: %w", p, err)
	}
	if res.StatusCode == http.StatusPartialContent {
		first, end, ok := parseContentRange(res.Header.Get("Content-Range"))
		if !ok || first != offset || end > last {
			drain(res)
			return nil, fmt.Errorf("reading part of %q: asked for bytes %d to %d, the storage sent %q", p, offset, last,
				res.Header.Get("Content-Range"))
		}
		return &File{Body: res.Body, Size: end - first + 1}, nil
	}
	defer drain(res)
	switch {
	case res.StatusCode == http.StatusOK:
		return nil, fmt.Errorf("reading part of %q: %w", p, ErrNoRanges)
	case isRedirect(res.StatusCode):
		return nil, fmt.Errorf("reading part of %q: it is a folder: %w", p, ErrNotFound)
	}
	return nil, statusError("reading part of", p, res)
}

// parseContentRange reads the first and last byte of a Content-Range such as "bytes 100-199/1000" or "bytes 0-9/*".
func parseContentRange(v string) (first, last int64, ok bool) {
	spec, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, _, found := strings.Cut(spec, "/")
	from, to, found2 := strings.Cut(span, "-")
	if !found || !found2 {
		return 0, 0, false
	}
	first, err1 := strconv.ParseInt(from, 10, 64)
	last, err2 := strconv.ParseInt(to, 10, 64)
	return first, last, err1 == nil && err2 == nil && first >= 0 && last >= first
}

// Move moves the file at from to to, on the server: no bytes of the file travel. It never replaces anything: it sends
// Overwrite: F, and returns ErrExists when something is at to. ErrNotFound means there is no file at from, or the
// folder above to is missing.
func (c *Client) Move(ctx context.Context, from, to string) error {
	if err := checkPath(from); err != nil {
		return err
	}
	if err := checkPath(to); err != nil {
		return err
	}
	req, err := c.request(ctx, "MOVE", c.fileURL(from), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Destination", c.fileURL(to).String())
	req.Header.Set("Overwrite", "F")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("moving %q on the storage: %w", from, err)
	}
	defer drain(res)
	switch res.StatusCode {
	case http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusPreconditionFailed:
		return fmt.Errorf("moving %q to %q: %w", from, to, ErrExists)
	case http.StatusConflict:
		return fmt.Errorf("moving %q to %q: the folder above it is missing: %w", from, to, ErrNotFound)
	}
	return statusError("moving", from, res)
}

// Delete removes the file at p. ErrNotFound means there was nothing there.
func (c *Client) Delete(ctx context.Context, p string) error {
	if err := checkPath(p); err != nil {
		return err
	}
	return c.delete(ctx, p, c.fileURL(p))
}

// DeleteFolder removes the folder at dir and everything in it. It never removes the Drive's own folder ("").
func (c *Client) DeleteFolder(ctx context.Context, dir string) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	if len(escapeSegments(dir)) == 0 {
		return errors.New("the Drive's own folder on the storage is never deleted")
	}
	return c.delete(ctx, dir, c.folderURL(dir))
}

func (c *Client) delete(ctx context.Context, p string, u *url.URL) error {
	req, err := c.request(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("deleting %q on the storage: %w", p, err)
	}
	defer drain(res)
	switch res.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusMultiStatus: // a folder of which some things could not be deleted
		return fmt.Errorf("deleting %q on the storage: some of what is in it could not be deleted", p)
	}
	return statusError("deleting", p, res)
}

func isRedirect(status int) bool { return status >= 300 && status < 400 }

// MakeFolder makes the folder at dir and any folders above it that are missing, up to and including the Drive's own
// folder. A folder that is already there is fine.
func (c *Client) MakeFolder(ctx context.Context, dir string) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	all := append(append([]string{}, c.root...), escapeSegments(dir)...)
	for i := 1; i <= len(all); i++ {
		if err := c.makeOne(ctx, c.url(all[:i], true)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) makeOne(ctx context.Context, u *url.URL) error {
	req, err := c.request(ctx, "MKCOL", u, nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("making the folder %q on the storage: %w", u.Path, err)
	}
	defer drain(res)
	switch res.StatusCode {
	case http.StatusCreated, http.StatusMethodNotAllowed: // made, or already there
		return nil
	}
	return statusError("making the folder", u.Path, res)
}

func (c *Client) request(ctx context.Context, method string, u *url.URL, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)
	return req, nil
}

// folderURL is the address of the folder at dir, with a trailing slash.
func (c *Client) folderURL(dir string) *url.URL {
	return c.url(append(append([]string{}, c.root...), escapeSegments(dir)...), true)
}

// fileURL is the address of the file at p, without a trailing slash.
func (c *Client) fileURL(p string) *url.URL {
	return c.url(append(append([]string{}, c.root...), escapeSegments(p)...), false)
}

// url is the address of the item at the escaped path segments segs on the server.
func (c *Client) url(segs []string, folder bool) *url.URL {
	raw := "/" + strings.Join(segs, "/")
	if folder && len(segs) > 0 {
		raw += "/"
	}
	u := *c.server
	u.RawPath = raw
	u.Path, _ = url.PathUnescape(raw)
	return &u
}

// checkPath refuses a path that could step out of the folder it is relative to. Every method that takes a path checks
// it first.
func checkPath(p string) error {
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return fmt.Errorf("%q: %w", p, ErrBadPath)
		}
	}
	return nil
}

// escapeSegments splits a slash-separated path into escaped segments, dropping empty ones.
func escapeSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, url.PathEscape(s))
		}
	}
	return out
}

// segments splits an escaped path into its segments with the escaping undone, so "/My%20files/" and "/My files"
// compare equal however a server chose to escape them.
func segments(escaped string) []string {
	var out []string
	for _, s := range strings.Split(escaped, "/") {
		if s == "" {
			continue
		}
		if u, err := url.PathUnescape(s); err == nil {
			s = u
		}
		out = append(out, s)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statusError(doing, name string, res *http.Response) error {
	switch res.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return fmt.Errorf("%s %q: %w", doing, name, ErrNotFound)
	}
	e := &StatusError{Doing: doing, Path: name, Status: res.Status}
	if isRedirect(res.StatusCode) {
		e.Location = res.Header.Get("Location")
	}
	return e
}

// drain reads what is left of a response so its connection can be used again, up to a limit.
func drain(res *http.Response) {
	_, _ = io.CopyN(io.Discard, res.Body, 64<<10)
	_ = res.Body.Close()
}

// The parts of a WebDAV multistatus answer the Drive reads.
type multistatus struct {
	Responses []response `xml:"DAV: response"`
}

type response struct {
	Href     string     `xml:"DAV: href"`
	Propstat []propstat `xml:"DAV: propstat"`
}

type propstat struct {
	Status string `xml:"DAV: status"`
	Prop   struct {
		ResourceType struct {
			Collection *struct{} `xml:"DAV: collection"`
		} `xml:"DAV: resourcetype"`
		ContentLength string `xml:"DAV: getcontentlength"`
		LastModified  string `xml:"DAV: getlastmodified"`
	} `xml:"DAV: prop"`
}

// entry reads the properties the server could give (those under a "200 OK" status).
func (r response) entry(name string) (Entry, bool) {
	e := Entry{Name: name}
	found := false
	for _, ps := range r.Propstat {
		if !strings.Contains(ps.Status, " 200 ") {
			continue
		}
		found = true
		if ps.Prop.ResourceType.Collection != nil {
			e.Folder = true
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLength), 10, 64); err == nil {
			e.Size = n
		}
		if t, err := http.ParseTime(strings.TrimSpace(ps.Prop.LastModified)); err == nil {
			e.Changed = t.UTC()
		}
	}
	if e.Folder {
		e.Size = 0
	}
	return e, found
}
