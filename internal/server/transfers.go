package server

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/storage"
	"github.com/neonjelly-co/superit-drive/internal/transfers"
)

// Transfers: the routes that move a file's bytes. Each holds one of the transfer slots while its bytes move (package
// transfers), so that at most transfers.Slots are in flight:
//
//   - a download (of a file or a zip) waits in line for its turn, up to turnWait, holding nothing but its connection
//     meanwhile;
//   - an upload, or a part of a resumable one, is answered at once with 503 and Retry-After when it is not its turn,
//     before any of its bytes are read (they would fill the connection's buffers while it waited), and its place in
//     line is kept for it: a resumable upload's by its id, an upload in one request by the `turn` it names. The
//     interface shows it as waiting, and sends it again.
//
// A transfer that moves no bytes for stall is ended, so a paused download or a stopped upload does not keep a slot.

const (
	// turnWait is how long a download waits in line for its turn before the Drive says it is busy.
	turnWait = 10 * time.Minute
	// stall is how long a transfer may move no bytes before the Drive ends it.
	stall = 2 * time.Minute
	// busyRetry is when an upload refused for want of a slot should be sent again, in seconds.
	busyRetry = "2"

	busyMessage    = "The Drive is busy with other uploads and downloads. This one waits its turn."
	restartMessage = "The Drive storage cannot continue an upload where it stopped, so this upload starts again from the beginning."
)

// classOf is whose line a transfer waits in: an outsider's (a visitor's or a guest's), for one of the few slots they
// may hold together, or a member's.
func classOf(c call) transfers.Class {
	if c.link != nil || c.guest != nil {
		return transfers.Outsiders
	}
	return transfers.Members
}

// waitTurn waits in line for a transfer slot: a visitor or a guest in the outsiders' own line, for one of the few slots
// they may hold. ok is false when the person was answered, or has left.
func (s *Server) waitTurn(w http.ResponseWriter, r *http.Request, c call) (release func(), ok bool) {
	release, err := s.slots.Wait(r.Context(), classOf(c), turnWait)
	if err == nil {
		return release, true
	}
	if r.Context().Err() == nil {
		w.Header().Set("Retry-After", "60")
		s.refuse(w, r, http.StatusServiceUnavailable, busyMessage)
	}
	return nil, false
}

// tryTurn takes a transfer slot if it is the upload's turn (key keeps its place in line), or answers 503. A guest's
// upload is in the outsiders' line.
func (s *Server) tryTurn(w http.ResponseWriter, c call, key string) (release func(), ok bool) {
	release, ok = s.slots.Try(classOf(c), key)
	if !ok {
		w.Header().Set("Retry-After", busyRetry)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": busyMessage, "busy": true})
	}
	return release, ok
}

// turnKey is what keeps an upload's place in line for a slot: a resumable upload's id; for an upload in one request,
// the `turn` the interface names it by (any short word of letters, digits, - and _), which counts only for the
// person who names it. Without one it keeps no place.
func turnKey(r *http.Request, c call) string {
	if c.upload != nil {
		return "upload:" + c.upload.ID
	}
	if t := r.URL.Query().Get("turn"); turnName.MatchString(t) {
		if c.guest != nil {
			return "files:guest:" + strconv.FormatInt(c.guest.ID, 10) + ":" + t
		}
		return "files:member:" + c.member.UserID + ":" + t
	}
	return ""
}

var turnName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// sending is w for a transfer's bytes: each write gives the person stall more to take them. done takes the deadline
// away again, as the connection may serve another request.
func sending(w http.ResponseWriter) (out io.Writer, done func()) {
	rc := http.NewResponseController(w)
	return deadlineWriter{w, rc}, func() { _ = rc.SetWriteDeadline(time.Time{}) }
}

type deadlineWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (d deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(stall))
	return d.w.Write(p)
}

// receiving is the request's body for a transfer's bytes: each read gives the person stall more to send them.
func receiving(w http.ResponseWriter, r *http.Request) (in io.Reader, done func()) {
	rc := http.NewResponseController(w)
	return deadlineReader{r.Body, rc}, func() { _ = rc.SetReadDeadline(time.Time{}) }
}

type deadlineReader struct {
	r  io.Reader
	rc *http.ResponseController
}

func (d deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetReadDeadline(time.Now().Add(stall))
	return d.r.Read(p)
}

// download sends a file's bytes under its original name, as an attachment of type application/octet-stream, so the
// browser saves it rather than showing it, whatever kind of file it is.
//
// A Range asking for one part of the file (a video seeking, a download resuming) is answered with that part (206); a
// part that starts after the end, with 416. With If-Range (the ETag or Last-Modified of an earlier answer, as a
// browser sends it to resume), the file is sent whole if it has changed since. Anything else in Range, such as several
// parts, is answered with the whole file, as HTTP allows. The storage is asked the same way, so it does the comparing.
//
//	GET /api/places/{place}/items/{item}/download
//	Range: bytes=1000-1999 | bytes=1000- | bytes=-1000      (optional)
//	If-Range: "…" | <date>                                  (optional)
func (s *Server) download(w http.ResponseWriter, r *http.Request, c call) {
	if c.item.Folder {
		s.refuse(w, r, http.StatusNotFound, "This is a folder. Download it as a zip instead.")
		return
	}
	s.sendFile(w, r, c, false)
}

// open sends a file's bytes to be shown in the browser, when its kind is one that opens there (opensInBrowser, by the
// extension of its name), with the content type and policy the list gives it. Any other file is sent exactly as
// download sends it: as an attachment, to be saved. Range and If-Range work as for a download, so a video or a sound
// seeks.
//
//	GET /api/places/{place}/items/{item}/open
func (s *Server) open(w http.ResponseWriter, r *http.Request, c call) {
	if c.item.Folder {
		s.refuse(w, r, http.StatusNotFound, "This is a folder. Open it in the list instead.")
		return
	}
	s.sendFile(w, r, c, true)
}

// sendFile sends the file c.item, to be shown in the browser when inline is true and its kind opens there, and as an
// attachment otherwise. It waits its turn for a transfer slot, as every download does.
func (s *Server) sendFile(w http.ResponseWriter, r *http.Request, c call, inline bool) {
	release, ok := s.waitTurn(w, r, c)
	if !ok {
		return
	}
	defer release()
	f, err := s.drive.Download(r.Context(), *c.place, *c.item, rangeOf(r.Header.Get("Range")), r.Header.Get("If-Range"))
	var past *drive.NoSuchRange
	switch {
	case errors.As(err, &past):
		if past.Size >= 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", past.Size))
		}
		s.refuse(w, r, http.StatusRequestedRangeNotSatisfiable, "This part of the file starts after its end.")
		return
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusNotFound, "This file is no longer on the Drive storage. Reload the page to see what is there now.")
		return
	case err != nil:
		s.failed(w, r, err)
		return
	}
	defer f.Body.Close()

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", disposition("attachment", c.item.Name))
	h.Set("Content-Security-Policy", sandboxed)
	if o, ok := openingOf(c.item.Name); inline && ok {
		h.Set("Content-Type", o.contentType)
		h.Set("Content-Disposition", disposition("inline", c.item.Name))
		h.Set("Content-Security-Policy", o.policy)
	}
	h.Set("Cache-Control", "private, no-store")
	h.Set("Accept-Ranges", "bytes")
	if f.ETag != "" {
		h.Set("ETag", f.ETag)
	}
	if f.Modified != "" {
		h.Set("Last-Modified", f.Modified)
	}
	status := http.StatusOK
	if f.Partial {
		status = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", f.First, f.Last, f.Total))
	}
	if f.Length >= 0 {
		// With the length announced, a download cut short shows as failed in the browser instead of as a short file.
		h.Set("Content-Length", strconv.FormatInt(f.Length, 10))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if !f.Partial || f.First == 0 {
		s.used(r, c) // not a part after its start: a video seeking, a download resuming
	}
	out, done := sending(w)
	defer done()
	if _, err := io.Copy(out, f.Body); err != nil {
		s.log.Info("a download ended before its last byte", "error", err)
	}
}

// rangeOf reads a Range header that asks for one part of a file: "bytes=100-199", "bytes=100-" or "bytes=-100". It
// is nil for anything else (several parts, another unit, a malformed or empty one): that is answered with the whole
// file.
func rangeOf(h string) *storage.Range {
	spec, ok := strings.CutPrefix(strings.TrimSpace(h), "bytes=")
	if !ok {
		return nil
	}
	from, to, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok || (from == "" && to == "") {
		return nil
	}
	number := func(v string) (int64, bool) {
		if v == "" || strings.Trim(v, "0123456789") != "" {
			return 0, false
		}
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	if from == "" {
		n, ok := number(to)
		if !ok || n == 0 {
			return nil
		}
		return &storage.Range{Suffix: n}
	}
	first, ok := number(from)
	if !ok {
		return nil
	}
	if to == "" {
		return &storage.Range{First: first, Last: -1}
	}
	last, ok := number(to)
	if !ok || last < first {
		return nil
	}
	return &storage.Range{First: first, Last: last}
}

// disposition is a Content-Disposition value: kind is "attachment" (the browser saves the file under name) or
// "inline" (it shows it, and saves it under name when asked to). A name that is not plain ASCII is sent in the RFC 6266
// filename* form, which every current browser reads.
func disposition(kind, name string) string {
	if v := mime.FormatMediaType(kind, map[string]string{"filename": name}); v != "" {
		return v
	}
	return kind
}

// zip sends a folder (the place's top folder, or a folder item), with everything in it, as one zip, streamed: the
// folder is at the top of the zip. With item (repeated), only those items of the folder (files, or folders with
// everything in them), at the top of the zip. The zip is named after the folder. It has no length and no parts: it is
// made as it is sent.
//
//	GET /api/places/{place}/zip[?item=12&item=15]
//	GET /api/places/{place}/items/{folder}/zip[?item=…]
//
// An item that is not directly in the folder is answered as one that does not exist. A folder with more than
// drive.MaxZipEntries files and folders is refused (400): download the folders inside it instead.
func (s *Server) zip(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "Only a folder downloads as a zip. Download the file itself instead.")
	if !ok {
		return
	}
	var only []int64
	if raw, given := r.URL.Query()["item"]; given {
		only = make([]int64, 0, len(raw))
		for _, v := range raw {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
				return
			}
			only = append(only, id)
		}
	}
	name := c.place.Name
	switch {
	case folder != nil:
		name = folder.Name
	case c.place.Kind == index.MyFiles:
		name = "My files"
	}
	headers := func() {
		h := w.Header()
		h.Set("Content-Type", "application/zip")
		h.Set("Content-Disposition", disposition("attachment", name+".zip"))
		h.Set("Content-Security-Policy", sandboxed)
		h.Set("Cache-Control", "private, no-store")
	}
	if r.Method == http.MethodHead {
		headers()
		w.WriteHeader(http.StatusOK)
		return
	}
	release, ok := s.waitTurn(w, r, c)
	if !ok {
		return
	}
	defer release()

	started := false
	out, done := sending(w)
	defer done()
	err := s.drive.Zip(r.Context(), *c.place, folder, name, only, func() io.Writer {
		headers()
		w.WriteHeader(http.StatusOK)
		started = true
		s.used(r, c)
		return out
	})
	switch {
	case err == nil:
	case started:
		// The zip is cut short: end the connection, so the browser shows the download as failed rather than keeping a
		// zip that looks whole.
		s.log.Info("a zip ended before its end", "error", err)
		panic(http.ErrAbortHandler)
	case errors.Is(err, index.ErrNoItem):
		s.refuse(w, r, http.StatusNotFound, notHere)
	case errors.Is(err, drive.ErrTooMany):
		s.refuse(w, r, http.StatusBadRequest, fmt.Sprintf("This holds more than %d files and folders, too many for one zip. "+
			"Download the folders inside it one at a time.", drive.MaxZipEntries))
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusNotFound, "This folder is no longer on the Drive storage. Go up a level to see what is there now.")
	default:
		s.failed(w, r, err)
	}
}

type uploadJSON struct {
	itemJSON
	// Replaced is true when the upload replaced a file of its name, which is now in the trash.
	Replaced bool `json:"replaced,omitempty"`
}

// upload stores a new file in a folder of a place and answers 201 with it.
//
//	POST /api/places/{place}/files?name=<the file's name>[&existing=replace|keep-both][&turn=<word>]
//	POST /api/places/{place}/items/{folder}/files?name=…
//	Content-Length: <the file's size>
//
//	<the file's bytes>
//
// The body is the file itself, not a form, and streams straight through to the storage, into the hidden uploads
// folder, in one all-or-nothing PUT; it is moved into its folder only when whole. Content-Length is required, so the
// storage can tell a whole file from a cut-off one. For large files, see startUpload: an upload in one request starts
// again from the beginning when cut off.
//
// When the name is taken, existing says what to do: replace the file there (it goes to the trash) or keep both (the
// upload gets a name with " (2)" or the like, and the answer has it). Without it, the answer is 409 with "exists" set to
// "file" or "folder", so the interface can ask; nothing is sent to the storage. A folder is never replaced.
//
// When it is not its turn for a transfer slot, the answer is 503 with "busy" and Retry-After, before any of the body is
// read: send it again with the same turn, which keeps its place in line.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "Files can only be uploaded into a folder.")
	if !ok {
		return
	}
	if r.ContentLength < 0 {
		s.refuse(w, r, http.StatusLengthRequired, "The upload did not say how large the file is, so nothing was stored. Try again.")
		return
	}
	name := r.URL.Query().Get("name")
	existing, ok := s.existing(w, r)
	if !ok {
		return
	}
	release, ok := s.tryTurn(w, c, turnKey(r, c))
	if !ok {
		return
	}
	defer release()
	body, done := receiving(w, r)
	defer done()
	up, err := s.drive.Upload(r.Context(), *c.place, folder, name, body, r.ContentLength, existing, c.by())
	switch {
	case err == nil:
		s.used(r, c)
		writeJSON(w, http.StatusCreated, uploadJSON{itemJSON: itemOf(up.Item), Replaced: up.Replaced != nil})
	case errors.Is(err, drive.ErrCutOff) || r.Context().Err() != nil:
		// The person's connection ended (or they cancelled); nothing was stored. Not the Drive's failure.
		s.log.Info("an upload ended before the whole file arrived; nothing was stored", "error", err)
		s.refuse(w, r, http.StatusBadRequest, "The upload stopped before the whole file arrived, so nothing was stored. Try again.")
	default:
		s.uploadFailed(w, r, err, name, existing)
	}
}

// existing reads what an upload does with a name that is taken. ok is false when the person was already answered.
func (s *Server) existing(w http.ResponseWriter, r *http.Request) (drive.Existing, bool) {
	existing := drive.Existing(r.URL.Query().Get("existing"))
	if existing != drive.Ask && existing != drive.Replace && existing != drive.KeepBoth {
		s.refuse(w, r, http.StatusBadRequest, "The upload asked for something the Drive does not do with a name that is taken.")
		return "", false
	}
	return existing, true
}

// uploadFailed answers an upload of a file called name, in one request or finished, that did not end in its folder.
func (s *Server) uploadFailed(w http.ResponseWriter, r *http.Request, err error, name string, existing drive.Existing) {
	var bad *drive.BadName
	var taken *drive.Taken
	switch {
	case errors.As(err, &bad):
		s.refuse(w, r, http.StatusBadRequest, bad.Error())
	case errors.As(err, &taken) && taken.Folder && existing == drive.Replace:
		s.refuseTaken(w, r, taken, "There is a folder called “"+taken.Name+"” here, and a file cannot replace a folder. Keep both instead.")
	case errors.As(err, &taken) && taken.Folder:
		s.refuseTaken(w, r, taken, "There is already a folder called “"+taken.Name+"” here.")
	case errors.As(err, &taken):
		s.refuseTaken(w, r, taken, "There is already a file called “"+taken.Name+"” here.")
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "“"+name+"” is being uploaded or changed right now. Try again when that is done.")
	case errors.Is(err, drive.ErrGone):
		s.refuse(w, r, http.StatusConflict, "This folder is no longer on the Drive storage. Reload the page and try again.")
	case errors.Is(err, drive.ErrFull):
		s.refuse(w, r, http.StatusInsufficientStorage, "The Drive storage is full, so this file was not uploaded.")
	default:
		s.failed(w, r, err)
	}
}

// sessionJSON is a resumable upload in progress.
type sessionJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Offset is how much of the file has arrived: where the next part starts.
	Offset int64 `json:"offset"`
	// Resumable says whether an upload cut off goes on from Offset. When false, the storage cannot write into part of
	// a file: the file is sent as one part, and starts again from 0 when cut off.
	Resumable bool `json:"resumable"`
	// PartSize is the most one part may hold: drive.PartSize when resumable, the whole file otherwise.
	PartSize int64 `json:"partSize"`
	// Waiting is true while the upload waits its turn for a transfer slot.
	Waiting bool `json:"waiting"`
}

func (s *Server) sessionOf(u index.Upload, offset int64, resumable bool) sessionJSON {
	j := sessionJSON{ID: u.ID, Name: u.Name, Size: u.Size, Offset: offset, Resumable: resumable, PartSize: u.Size,
		Waiting: s.slots.Waiting("upload:" + u.ID)}
	if resumable {
		j.PartSize = min(u.Size, drive.PartSize)
	}
	return j
}

// startUpload starts a resumable upload of a large file into a folder of a place, and answers 201 with it. Its bytes
// follow in parts (uploadPart), and it is finished (finishUpload) when they have all arrived. Only the person who
// started it can see, continue, finish or cancel it; for anyone else it does not exist. Every request of it is decided
// as an upload into its folder, so someone who loses the right to upload there can do nothing more with it, and it is
// removed after a day (drive.AbandonedAfter).
//
//	POST /api/places/{place}/uploads?name=<the file's name>&size=<its size in bytes>[&existing=replace|keep-both]
//	POST /api/places/{place}/items/{folder}/uploads?name=…&size=…
//
// The name is looked at now, so that a person is asked before the file travels: when it is taken and existing does
// not say what to do, the answer is 409 with "exists", as for an upload in one request.
func (s *Server) startUpload(w http.ResponseWriter, r *http.Request, c call) {
	folder, ok := s.folder(w, r, c, "Files can only be uploaded into a folder.")
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	size, err := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
	if err != nil || size <= 0 {
		s.refuse(w, r, http.StatusBadRequest, "The upload did not say how large the file is.")
		return
	}
	existing, ok := s.existing(w, r)
	if !ok {
		return
	}
	u, err := s.drive.StartUpload(r.Context(), *c.place, folder, name, size, existing, c.by())
	if err != nil {
		s.uploadFailed(w, r, err, name, existing)
		return
	}
	resumable, err := s.drive.CanResume(r.Context())
	if err != nil {
		s.failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.sessionOf(u, 0, resumable))
}

// uploadState says where an upload in progress stands: how much of it has arrived, and whether it waits its turn.
// After a part was cut off, this is where the next one starts.
//
//	GET /api/places/{place}/uploads/{upload}
func (s *Server) uploadState(w http.ResponseWriter, r *http.Request, c call) {
	offset, err := s.drive.UploadOffset(r.Context(), *c.upload)
	if err != nil {
		s.failed(w, r, err)
		return
	}
	resumable, err := s.drive.CanResume(r.Context())
	if err != nil {
		s.failed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sessionOf(*c.upload, offset, resumable))
}

// uploadPart writes one part of an upload in progress into its file on the storage, from offset, which must be where
// what has arrived ends, and answers with where it ends now.
//
//	PUT /api/places/{place}/uploads/{upload}?offset=<where the part starts>
//	Content-Length: <the part's size>        at most partSize; where the upload is not resumable, the whole file
//
//	<the part's bytes>
//
//	200 {"offset": …, "size": …}
//	409 {"error": …, "offset": <where what has arrived ends>}             the part does not start there: send from it
//	409 {"error": …, "offset": 0, "resumable": false}                     the storage turned out unable to resume:
//	                                                                       send the whole file again as one part
//	503 {"error": …, "busy": true} with Retry-After                       not its turn: send it again then
//
// A part cut off keeps what arrived, where the upload is resumable: ask where it stands, and go on from there.
func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request, c call) {
	if r.ContentLength < 0 {
		s.refuse(w, r, http.StatusLengthRequired, "The part did not say how large it is, so nothing was stored. Try again.")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 || r.ContentLength == 0 {
		s.refuse(w, r, http.StatusBadRequest, "The Drive could not read this request.")
		return
	}
	release, ok := s.tryTurn(w, c, turnKey(r, c))
	if !ok {
		return
	}
	defer release()
	body, done := receiving(w, r)
	defer done()
	end, err := s.drive.WritePart(r.Context(), *c.upload, offset, body, r.ContentLength)
	var wrong *drive.WrongOffset
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]int64{"offset": end, "size": c.upload.Size})
	case errors.As(err, &wrong):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "This part does not follow on from what has arrived.", "offset": wrong.Offset})
	case errors.Is(err, drive.ErrRestart):
		writeJSON(w, http.StatusConflict, map[string]any{"error": restartMessage, "offset": 0, "resumable": false})
	case errors.Is(err, drive.ErrBadPart):
		s.refuse(w, r, http.StatusBadRequest, "This part does not fit the file.")
	case errors.Is(err, drive.ErrBusy):
		s.refuse(w, r, http.StatusConflict, "Another part of this upload is still on its way. Ask where it stands, and go on from there.")
	case errors.Is(err, drive.ErrCutOff) || r.Context().Err() != nil:
		s.log.Info("a part of an upload ended before it all arrived", "error", err)
		s.refuse(w, r, http.StatusBadRequest, "The part stopped before it all arrived. Ask where the upload stands, and go on from there.")
	case errors.Is(err, drive.ErrFull):
		s.refuse(w, r, http.StatusInsufficientStorage, "The Drive storage is full, so this file was not uploaded.")
	default:
		s.failed(w, r, err)
	}
}

// finishUpload moves a whole upload into its folder and answers 201 with the file, as an upload in one request does.
// The name is looked at again: when it is taken, existing says what to do, and without it the answer is 409 with
// "exists", and the upload waits to be finished again. Before every byte has arrived it is 409 with "offset".
//
//	POST /api/places/{place}/uploads/{upload}/finish[?existing=replace|keep-both]
func (s *Server) finishUpload(w http.ResponseWriter, r *http.Request, c call) {
	existing, ok := s.existing(w, r)
	if !ok {
		return
	}
	up, err := s.drive.FinishUpload(r.Context(), *c.place, c.item, *c.upload, existing, c.by())
	var incomplete *drive.Incomplete
	switch {
	case err == nil:
		s.used(r, c)
		writeJSON(w, http.StatusCreated, uploadJSON{itemJSON: itemOf(up.Item), Replaced: up.Replaced != nil})
	case errors.As(err, &incomplete):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Not all of the file has arrived yet.", "offset": incomplete.Offset})
	default:
		s.uploadFailed(w, r, err, c.upload.Name, existing)
	}
}

// cancelUpload removes an upload in progress, with what of it has arrived, and answers 204.
//
//	DELETE /api/places/{place}/uploads/{upload}
func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request, c call) {
	if err := s.drive.CancelUpload(r.Context(), *c.upload); err != nil {
		s.failed(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
