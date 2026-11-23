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
// redirected PUT into a GET. Nothing the client does replaces anything already on the storage: PUT sends
// If-None-Match: *, MOVE sends Overwrite: F, and MKCOL never replaces by its nature.
//
// A listing is read as it arrives, one entry at a time, so a folder of any size costs the Drive no more memory than the
// entries it keeps (List stops at the most it is asked to read; Each keeps none).
//
// A server may close an idle connection just as the client sends a request on it (its keep-alive time ran out). The
// client then cannot tell whether the server acted on the request, so it sends again only what is safe to send twice:
// reading (GET, PROPFIND), which Go sends again by itself on a new connection. Every request that changes the storage
// (PUT, MKCOL, MOVE, DELETE) goes on a new connection of its own instead, which a server never closes for being idle,
// and is never sent twice.
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

	"github.com/neonjelly-co/superit-drive/internal/transfers"
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
	// ErrTooMany is returned by List when a folder holds more than the most it was asked to read.
	ErrTooMany = errors.New("the folder holds more than the Drive reads at once")
	// ErrRefused is what a *StatusError is when the storage answered 403 Forbidden: it will not do this at this path,
	// whoever asks. Apache answers so for every name that starts with ".ht", such as ".htaccess".
	ErrRefused = errors.New("the storage refused it")
)

// A StatusError is an answer from the storage that has no meaning of its own here, such as 403 Forbidden.
type StatusError struct {
	Doing    string // what the client was doing, such as "writing"
	Path     string // the path, relative to the Drive's folder; MakeFolder gives a folder's whole path on the server
	Status   string // the answer's status, such as "403 Forbidden"
	Location string // where the answer pointed, for a redirect
	// Refused is true when the answer was 403 Forbidden: the error is then ErrRefused too.
	Refused bool
}

// Is says that a refusal (403 Forbidden) is ErrRefused.
func (e *StatusError) Is(target error) bool { return target == ErrRefused && e.Refused }

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
	// http reads (GET, PROPFIND) over connections kept open between requests; change sends every request that changes
	// the storage on a new connection of its own (see the package's comment).
	http, change *http.Client
}

// idleFor is how long a connection to the storage is kept open between requests. It is shorter than the keep-alive
// time of Apache (5 seconds by default), so the Drive itself usually closes a connection before the server does.
const idleFor = 4 * time.Second

// New makes a client for the Drive's folder (folder, such as "/" or "/drive") on the WebDAV server at address.
func New(address *url.URL, user, password, folder string) *Client {
	server := &url.URL{Scheme: address.Scheme, Host: address.Host}
	root := escapeSegments(path.Join(address.Path, folder))

	// Each connection's network buffers are capped, to hold the Drive's memory figure (package transfers).
	reading := http.DefaultTransport.(*http.Transport).Clone()
	reading.DialContext = transfers.Dial(30 * time.Second)
	reading.MaxIdleConnsPerHost = 8
	reading.IdleConnTimeout = idleFor
	reading.ResponseHeaderTimeout = time.Minute
	changing := reading.Clone()
	changing.DisableKeepAlives = true

	// Answer every redirect as it is. Apache redirects a folder's address without the trailing slash, and Go would
	// follow a redirected PUT or PROPFIND as a GET and report that GET's answer instead.
	noRedirects := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{server: server, root: root, user: user, password: password,
		http:   &http.Client{Transport: reading, CheckRedirect: noRedirects},
		change: &http.Client{Transport: changing, CheckRedirect: noRedirects}}
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

// List returns what is directly inside the folder at dir ("" is the Drive's own folder), at most max entries: a folder
// holding more is ErrTooMany, after reading no more than that. It returns ErrNotFound when the folder does not exist.
func (c *Client) List(ctx context.Context, dir string, max int) ([]Entry, error) {
	var entries []Entry
	err := c.each(ctx, dir, int64(max)*maxEntryBytes+maxListingOverhead, func(e Entry) error {
		if len(entries) >= max {
			return fmt.Errorf("listing %q: %w", dir, ErrTooMany)
		}
		entries = append(entries, e)
		return nil
	})
	return entries, err
}

// Each calls each for every entry directly inside the folder at dir, as the listing arrives, and keeps none of them:
// for a folder of any size, such as the hidden uploads folder, that is looked through rather than kept. An error from
// each stops the listing and is returned. It returns ErrNotFound when the folder does not exist.
func (c *Client) Each(ctx context.Context, dir string, each func(Entry) error) error {
	return c.each(ctx, dir, -1, each)
}

// How many bytes of a listing List reads at most: maxEntryBytes per entry it may keep (Apache sends about 600 for one,
// and under 2,000 for one with a name of 255 bytes of accented letters), and maxListingOverhead for the rest.
const (
	maxEntryBytes      = 8 << 10
	maxListingOverhead = 64 << 10
)

// each reads the listing of dir as it arrives, at most limit bytes of it (-1: no limit), and calls each for every entry
// directly inside it.
func (c *Client) each(ctx context.Context, dir string, limit int64, each func(Entry) error) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	target := c.folderURL(dir)
	self := segments(target.EscapedPath())
	res, err := c.propfind(ctx, target, "1", limit, func(r response) error {
		u, err := target.Parse(r.Href)
		if err != nil {
			return nil
		}
		segs := segments(u.EscapedPath())
		if equal(segs, self) || len(segs) != len(self)+1 || !equal(segs[:len(self)], self) {
			return nil // the folder itself, or something that is not directly inside it
		}
		if e, ok := r.entry(segs[len(segs)-1]); ok {
			return each(e)
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrTooMany):
		return err
	case err != nil:
		return fmt.Errorf("listing %q on the storage: %w", dir, err)
	case res != nil:
		return statusError("listing", dir, res)
	}
	return nil
}

// errListingTooLong stops reading a listing longer than its limit.
var errListingTooLong = errors.New("the listing is longer than the Drive reads")

// propfind asks for the properties the Drive reads, at depth "0" (the item itself) or "1" (and what is directly
// inside), and on a 207 answer calls each for every response in it as it is read, reading at most limit bytes (-1: no
// limit); a listing cut short, or longer than that, is an error. On any other answer it returns that answer, whose body
// is already closed, for the caller to tell what it means.
//
// Reading changes nothing, so Go may send it again by itself on a new connection when the server closed the one it was
// sent on before answering (Idempotency-Key, never sent, marks it so).
func (c *Client) propfind(ctx context.Context, target *url.URL, depth string, limit int64, each func(response) error) (*http.Response, error) {
	const body = `<?xml version="1.0" encoding="utf-8"?>` +
		`<D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontentlength/><D:getlastmodified/></D:prop></D:propfind>`
	req, err := c.request(ctx, "PROPFIND", target, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", depth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header["Idempotency-Key"] = nil
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer drain(res)
	if res.StatusCode != http.StatusMultiStatus {
		return res, nil
	}
	var in io.Reader = res.Body
	if limit >= 0 {
		in = &limitedListing{r: res.Body, left: limit}
	}
	dec := xml.NewDecoder(in)
	multistatus := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errListingTooLong) {
				return nil, fmt.Errorf("%w: %w", ErrTooMany, err)
			}
			return nil, fmt.Errorf("the answer is not a whole WebDAV listing: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Space != "DAV:" {
			continue
		}
		switch start.Name.Local {
		case "multistatus":
			multistatus = true
		case "response":
			var r response
			if err := dec.DecodeElement(&r, &start); err != nil {
				if errors.Is(err, errListingTooLong) {
					return nil, fmt.Errorf("%w: %w", ErrTooMany, err)
				}
				return nil, fmt.Errorf("the answer is not a whole WebDAV listing: %w", err)
			}
			if err := each(r); err != nil {
				return nil, err
			}
		}
	}
	if !multistatus {
		return nil, errors.New("the answer is not a WebDAV listing")
	}
	return nil, nil
}

// limitedListing reads at most left bytes of a listing, then fails with errListingTooLong (where io.LimitReader would
// end it quietly, as if it were whole).
type limitedListing struct {
	r    io.Reader
	left int64
}

func (l *limitedListing) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, errListingTooLong
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}

// Stat returns what is at p, without a trailing slash. It returns ErrNotFound when nothing is. A folder there is
// returned as a folder whether the server describes it or (as Apache does) redirects to the address with a slash.
func (c *Client) Stat(ctx context.Context, p string) (Entry, error) {
	if err := checkPath(p); err != nil {
		return Entry{}, err
	}
	name := path.Base("/" + p)
	var found *Entry
	res, err := c.propfind(ctx, c.fileURL(p), "0", maxEntryBytes+maxListingOverhead, func(r response) error {
		if e, ok := r.entry(name); ok && found == nil {
			found = &e
		}
		return nil
	})
	switch {
	case err != nil:
		return Entry{}, fmt.Errorf("looking up %q on the storage: %w", p, err)
	case res != nil && isRedirect(res.StatusCode):
		return Entry{Name: name, Folder: true}, nil
	case res != nil:
		return Entry{}, statusError("looking up", p, res)
	case found == nil:
		return Entry{}, fmt.Errorf("looking up %q on the storage: the answer holds no properties", p)
	}
	return *found, nil
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
	res, err := c.change.Do(req)
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
	res, err := c.change.Do(req)
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
		first, end, _, ok := parseContentRange(res.Header.Get("Content-Range"))
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

// parseContentRange reads a Content-Range such as "bytes 100-199/1000" or "bytes 0-9/*": the first and last byte, and
// the file's whole length (-1 for "*").
func parseContentRange(v string) (first, last, total int64, ok bool) {
	spec, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, 0, 0, false
	}
	span, whole, found := strings.Cut(spec, "/")
	from, to, found2 := strings.Cut(span, "-")
	if !found || !found2 {
		return 0, 0, 0, false
	}
	first, err1 := strconv.ParseInt(from, 10, 64)
	last, err2 := strconv.ParseInt(to, 10, 64)
	total = -1
	var err3 error
	if whole != "*" {
		total, err3 = strconv.ParseInt(whole, 10, 64)
	}
	ok = err1 == nil && err2 == nil && err3 == nil && first >= 0 && last >= first && (total < 0 || last < total)
	return first, last, total, ok
}

// A Range is the bytes of a file a download asks for: from First to Last, or from First to the end when Last is
// negative; or, when Suffix is above 0, the last Suffix bytes.
type Range struct {
	First, Last int64
	Suffix      int64
}

// header is the range as a Range header writes it.
func (r Range) header() string {
	switch {
	case r.Suffix > 0:
		return fmt.Sprintf("bytes=-%d", r.Suffix)
	case r.Last < 0:
		return fmt.Sprintf("bytes=%d-", r.First)
	}
	return fmt.Sprintf("bytes=%d-%d", r.First, r.Last)
}

// answers says whether bytes first to last of a file of total bytes are what r asks for.
func (r Range) answers(first, last, total int64) bool {
	if total < 0 || last != min(r.end(total), total-1) {
		return false
	}
	if r.Suffix > 0 {
		return first == max(0, total-r.Suffix)
	}
	return first == r.First
}

func (r Range) end(total int64) int64 {
	if r.Suffix > 0 || r.Last < 0 {
		return total - 1
	}
	return r.Last
}

// A Reading is a file being read from the storage: whole, or the part of it a Range asked for. Close its Body when
// done.
type Reading struct {
	Body io.ReadCloser
	// Length is how many bytes Body holds, as the server announced it, or -1 when it did not.
	Length int64
	// Partial is true when Body holds bytes First to Last of a file of Total bytes, and false when it holds the whole
	// file: the server may send the whole file instead of a part (when If-Range no longer matches, or when it does not
	// answer Range requests), and HTTP allows it.
	Partial            bool
	First, Last, Total int64
	// ETag and Modified are the file's validators as the server sent them (ETag and Last-Modified), for a browser to
	// resume a download with (If-Range); empty when it sent none.
	ETag, Modified string
}

// ErrNoSuchRange is returned by Read when the range asked for starts after the file's end (416).
var ErrNoSuchRange = errors.New("the range asked for is not in the file")

// Read starts reading the file at p: whole when r is nil, otherwise the part r asks for, unless ifRange (an If-Range
// value a browser sent, or "") no longer matches the file, when the server sends it whole. It returns ErrNotFound when
// there is no file there (a folder included), and ErrNoSuchRange when the part starts after its end. A part the server
// sends is checked against what was asked for.
func (c *Client) Read(ctx context.Context, p string, r *Range, ifRange string) (*Reading, error) {
	if err := checkPath(p); err != nil {
		return nil, err
	}
	req, err := c.request(ctx, http.MethodGet, c.fileURL(p), nil)
	if err != nil {
		return nil, err
	}
	// A compressed answer would count its range, and its length, in compressed bytes.
	req.Header.Set("Accept-Encoding", "identity")
	if r != nil {
		req.Header.Set("Range", r.header())
		if ifRange != "" {
			req.Header.Set("If-Range", ifRange)
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading %q on the storage: %w", p, err)
	}
	reading := &Reading{Body: res.Body, Length: res.ContentLength, ETag: res.Header.Get("ETag"), Modified: res.Header.Get("Last-Modified")}
	switch {
	case res.StatusCode == http.StatusOK:
		return reading, nil
	case res.StatusCode == http.StatusPartialContent && r != nil:
		first, last, total, ok := parseContentRange(res.Header.Get("Content-Range"))
		if !ok || !r.answers(first, last, total) || (res.ContentLength >= 0 && res.ContentLength != last-first+1) {
			drain(res)
			return nil, fmt.Errorf("reading part of %q: asked for %s, the storage sent %q (%d bytes)", p, r.header(),
				res.Header.Get("Content-Range"), res.ContentLength)
		}
		reading.Partial, reading.First, reading.Last, reading.Total = true, first, last, total
		reading.Length = last - first + 1
		return reading, nil
	}
	defer drain(res)
	switch {
	case res.StatusCode == http.StatusRequestedRangeNotSatisfiable && r != nil:
		return nil, fmt.Errorf("reading %s of %q: %w", r.header(), p, ErrNoSuchRange)
	case isRedirect(res.StatusCode):
		return nil, fmt.Errorf("reading %q: it is a folder: %w", p, ErrNotFound)
	}
	return nil, statusError("reading", p, res)
}

// Move moves the file at from to to, on the server: no bytes of the file travel. It never replaces anything: it sends
// Overwrite: F, and returns ErrExists when something is at to. ErrNotFound means there is no file at from (a folder
// there included), or the folder above to is missing.
func (c *Client) Move(ctx context.Context, from, to string) error {
	return c.move(ctx, from, to, c.fileURL(from))
}

// MoveFolder moves the folder at from, with everything in it, to to, on the server. Like Move, it never replaces
// anything and returns ErrExists when something is at to; ErrNotFound means there is no folder at from, or the folder
// above to is missing. Moving a folder into itself is refused by the server with an error of its own.
//
// The folder is addressed with a trailing slash (Apache answers a MOVE of a folder without one with a redirect), and
// its new place without one, so that a file or a folder already there is answered as such (Apache: 412 for a file, a
// redirect for a folder).
func (c *Client) MoveFolder(ctx context.Context, from, to string) error {
	if len(escapeSegments(from)) == 0 {
		return errors.New("the Drive's own folder on the storage is never moved")
	}
	return c.move(ctx, from, to, c.folderURL(from))
}

func (c *Client) move(ctx context.Context, from, to string, source *url.URL) error {
	if err := checkPath(from); err != nil {
		return err
	}
	if err := checkPath(to); err != nil {
		return err
	}
	req, err := c.request(ctx, "MOVE", source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Destination", c.fileURL(to).String())
	req.Header.Set("Overwrite", "F")
	res, err := c.change.Do(req)
	if err != nil {
		return fmt.Errorf("moving %q on the storage: %w", from, err)
	}
	defer drain(res)
	switch {
	case res.StatusCode == http.StatusCreated || res.StatusCode == http.StatusNoContent:
		return nil
	case res.StatusCode == http.StatusPreconditionFailed:
		return fmt.Errorf("moving %q to %q: %w", from, to, ErrExists)
	case res.StatusCode == http.StatusConflict || res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("moving %q to %q: it is not there, or the folder above the new place is missing: %w", from, to, ErrNotFound)
	case isRedirect(res.StatusCode) && sameAddress(res.Header.Get("Location"), c.folderURL(from)):
		// Apache's answer to a file's address that is a folder's: there is no file at from.
		return fmt.Errorf("moving %q: it is a folder: %w", from, ErrNotFound)
	case isRedirect(res.StatusCode):
		// Apache's answer when a folder is already at to.
		return fmt.Errorf("moving %q to %q: a folder is there: %w", from, to, ErrExists)
	}
	return statusError("moving", from, res)
}

// sameAddress says whether location (as a server sent it, absolute or not) is the address u.
func sameAddress(location string, u *url.URL) bool {
	if location == "" {
		return false
	}
	l, err := u.Parse(location)
	return err == nil && equal(segments(l.EscapedPath()), segments(u.EscapedPath())) &&
		strings.HasSuffix(l.EscapedPath(), "/") == strings.HasSuffix(u.EscapedPath(), "/")
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
	res, err := c.change.Do(req)
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

// CreateFolder makes the one folder at dir, for a person who asked for a new folder: unlike MakeFolder, something
// already there is ErrExists, and a missing folder above it is ErrNotFound rather than made.
func (c *Client) CreateFolder(ctx context.Context, dir string) error {
	if err := checkPath(dir); err != nil {
		return err
	}
	if len(escapeSegments(dir)) == 0 {
		return fmt.Errorf("making the Drive's own folder: %w", ErrExists)
	}
	req, err := c.request(ctx, "MKCOL", c.folderURL(dir), nil)
	if err != nil {
		return err
	}
	res, err := c.change.Do(req)
	if err != nil {
		return fmt.Errorf("making the folder %q on the storage: %w", dir, err)
	}
	defer drain(res)
	switch {
	case res.StatusCode == http.StatusCreated:
		return nil
	case res.StatusCode == http.StatusMethodNotAllowed || isRedirect(res.StatusCode):
		// 405: a folder is already there (RFC 4918). Apache answers 400 when a file is; callers look first.
		return fmt.Errorf("making the folder %q: %w", dir, ErrExists)
	case res.StatusCode == http.StatusConflict:
		return fmt.Errorf("making the folder %q: the folder above it is missing: %w", dir, ErrNotFound)
	case res.StatusCode == http.StatusInsufficientStorage:
		return fmt.Errorf("making the folder %q: %w", dir, ErrFull)
	}
	return statusError("making the folder", dir, res)
}

func (c *Client) makeOne(ctx context.Context, u *url.URL) error {
	req, err := c.request(ctx, "MKCOL", u, nil)
	if err != nil {
		return err
	}
	res, err := c.change.Do(req)
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
	e := &StatusError{Doing: doing, Path: name, Status: res.Status, Refused: res.StatusCode == http.StatusForbidden}
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

// The parts of one response in a WebDAV multistatus answer that the Drive reads.
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
