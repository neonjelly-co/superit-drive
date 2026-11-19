package test

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// A resumable upload in progress, as the API describes it.
type sessionJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Offset    int64  `json:"offset"`
	Resumable bool   `json:"resumable"`
	PartSize  int64  `json:"partSize"`
	Waiting   bool   `json:"waiting"`
}

const uploadsFolder = ".superit-drive/uploads"

// The interface's calls for a resumable upload, as the interface makes them. folderID 0 is the place's top folder.

func uploadsPath(placeID, folderID int64) string {
	if folderID == 0 {
		return fmt.Sprintf("/api/places/%d/uploads", placeID)
	}
	return fmt.Sprintf("/api/places/%d/items/%d/uploads", placeID, folderID)
}

func sessionPath(placeID int64, id string) string { return fmt.Sprintf("/api/places/%d/uploads/%s", placeID, id) }

func startUpload(t *testing.T, c *harness.Client, placeID, folderID int64, name string, size int, existing string) harness.Response {
	t.Helper()
	q := url.Values{"name": {name}, "size": {strconv.Itoa(size)}}
	if existing != "" {
		q.Set("existing", existing)
	}
	return c.Do(t, http.MethodPost, uploadsPath(placeID, folderID)+"?"+q.Encode(), nil)
}

func startedUpload(t *testing.T, c *harness.Client, placeID, folderID int64, name string, size int) sessionJSON {
	t.Helper()
	res := startUpload(t, c, placeID, folderID, name, size, "")
	if res.Status != http.StatusCreated {
		t.Fatalf("starting an upload of %q: got %d %s, want 201", name, res.Status, res.Body)
	}
	var s sessionJSON
	res.JSON(t, &s)
	return s
}

func sendPart(t *testing.T, c *harness.Client, placeID int64, id string, offset int, part []byte) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodPut, fmt.Sprintf("%s?offset=%d", sessionPath(placeID, id), offset), bytes.NewReader(part))
}

// sentPart sends a part and fails the test unless the Drive took it.
func sentPart(t *testing.T, c *harness.Client, placeID int64, id string, offset int, part []byte) {
	t.Helper()
	res := sendPart(t, c, placeID, id, offset, part)
	var got struct{ Offset int64 }
	res.JSON(t, &got)
	if res.Status != http.StatusOK || got.Offset != int64(offset+len(part)) {
		t.Fatalf("a part of %d bytes from %d: got %d %s, want 200 ending at %d", len(part), offset, res.Status, res.Body, offset+len(part))
	}
}

func stateOf(t *testing.T, c *harness.Client, placeID int64, id string) sessionJSON {
	t.Helper()
	res := c.Get(t, sessionPath(placeID, id))
	if res.Status != http.StatusOK {
		t.Fatalf("where the upload stands: got %d %s", res.Status, res.Body)
	}
	var s sessionJSON
	res.JSON(t, &s)
	return s
}

func finishUpload(t *testing.T, c *harness.Client, placeID int64, id, existing string) harness.Response {
	t.Helper()
	path := sessionPath(placeID, id) + "/finish"
	if existing != "" {
		path += "?existing=" + existing
	}
	return c.Do(t, http.MethodPost, path, nil)
}

// offsetOf is the "offset" in an answer.
func offsetOf(t *testing.T, res harness.Response) int64 {
	t.Helper()
	var got struct {
		Offset *int64 `json:"offset"`
	}
	res.JSON(t, &got)
	if got.Offset == nil {
		t.Fatalf("the answer says no offset: %s", res.Body)
	}
	return *got.Offset
}

// cutOffPart starts sending a part of size bytes from offset, sends only the bytes in sent and hangs up, as a browser
// does when its connection drops.
func cutOffPart(t *testing.T, d *harness.Drive, m harness.Member, placeID int64, id string, offset, size int, sent []byte) {
	t.Helper()
	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "PUT %s?offset=%d HTTP/1.1\r\nHost: %s\r\nDrive-Secret: %s\r\nRemote-User-Id: %s\r\nOrigin: %s\r\n"+
		"Content-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n",
		sessionPath(placeID, id), offset, address.Host, d.Secret, m.UserID, d.Address, size)
	if _, err := conn.Write(sent); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
}

// settledOffset is where an upload stands once the Drive has stopped writing to it.
func settledOffset(t *testing.T, c *harness.Client, placeID int64, id string) int64 {
	t.Helper()
	last := int64(-1)
	var at int64
	harness.Eventually(t, "the upload has stopped growing", func() bool {
		at = stateOf(t, c, placeID, id).Offset
		same := at == last
		last = at
		time.Sleep(150 * time.Millisecond)
		return same
	})
	return at
}

// inUploads is what the hidden uploads folder holds: each file's name and size.
func inUploads(t *testing.T, s *harness.Storage) map[string]int64 {
	t.Helper()
	entries, _ := s.List(t, uploadsFolder)
	out := map[string]int64{}
	for _, e := range entries {
		out[e.Name] = e.Size
	}
	return out
}

func TestALargeUploadResumesFromWhereItStopped(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	reports := madeFolder(t, c, placeID, 0, "Reports")
	content := randomBytes(t, 10<<20)
	const part = 3 << 20

	u := startedUpload(t, c, placeID, reports.ID, "Year 2026.bin", len(content))
	if len(u.ID) != 32 || u.Name != "Year 2026.bin" || u.Size != int64(len(content)) || u.Offset != 0 || !u.Resumable ||
		u.PartSize != int64(len(content)) || u.Waiting {
		t.Fatalf("the upload as it starts: %+v", u)
	}
	sentPart(t, c, placeID, u.ID, 0, content[:part])

	// Its bytes arrive in the hidden uploads folder; nothing is in its folder until it is whole.
	if got := inUploads(t, d.Storage); len(got) != 1 || got[u.ID] != part {
		t.Fatalf("the uploads folder holds %v, want the upload's %d bytes under its id", got, part)
	}
	if got := onStorage(t, d, rekasTop+"/Reports"); len(got) != 0 {
		t.Fatalf("Reports on the storage holds %q before the upload is whole", got)
	}
	if l := list(t, c, placeID, reports.ID); len(l.Items) != 0 {
		t.Fatalf("Reports lists %q before the upload is whole", names(l.Items))
	}

	// The connection drops a third of the way through the next part: what arrived is kept.
	cutOffPart(t, d, reka, placeID, u.ID, part, part, content[part:part+1<<20])
	at := settledOffset(t, c, placeID, u.ID)
	if at <= part || at > part+1<<20 {
		t.Fatalf("after the cut-off part the upload stands at %d, want past %d and at most %d", at, part, part+1<<20)
	}

	// A part that does not start where what arrived ends is refused, saying where that is; so is finishing early.
	if res := sendPart(t, c, placeID, u.ID, part, content[part:2*part]); res.Status != http.StatusConflict || offsetOf(t, res) != at {
		t.Fatalf("a part sent again from %d: got %d %s, want 409 saying %d", part, res.Status, res.Body, at)
	}
	if res := finishUpload(t, c, placeID, u.ID, ""); res.Status != http.StatusConflict || offsetOf(t, res) != at {
		t.Fatalf("finishing before the whole file arrived: got %d %s, want 409 saying %d", res.Status, res.Body, at)
	}
	// A part that runs past the end of the file does not fit.
	if res := sendPart(t, c, placeID, u.ID, len(content)-10, randomBytes(t, 20)); res.Status != http.StatusBadRequest {
		t.Fatalf("a part past the end of the file: got %d %s, want 400", res.Status, res.Body)
	}

	// It goes on from there, and is moved into its folder, whole.
	for off := int(at); off < len(content); off += part {
		sentPart(t, c, placeID, u.ID, off, content[off:min(off+part, len(content))])
	}
	res := finishUpload(t, c, placeID, u.ID, "")
	var it itemJSON
	res.JSON(t, &it)
	if res.Status != http.StatusCreated || it.Name != "Year 2026.bin" || it.Size == nil || *it.Size != int64(len(content)) {
		t.Fatalf("finishing: got %d %s", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Reports/Year 2026.bin"); !bytes.Equal(got, content) {
		t.Fatalf("the file in Reports holds %d bytes that differ from the %d uploaded", len(got), len(content))
	}
	if got := inUploads(t, d.Storage); len(got) != 0 {
		t.Fatalf("the uploads folder still holds %v", got)
	}
	if l := list(t, c, placeID, reports.ID); len(l.Items) != 1 || l.Items[0].ID != it.ID {
		t.Fatalf("Reports lists %+v", l.Items)
	}
	if res := c.Get(t, downloadPath(placeID, it.ID)); !bytes.Equal(res.Body, content) {
		t.Fatalf("the download differs from the upload")
	}
	// The upload is over: there is nothing more to send to it.
	if res := c.Get(t, sessionPath(placeID, u.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("the finished upload: got %d %s, want 404", res.Status, res.Body)
	}
	if res := startUpload(t, c, placeID, reports.ID, "Year 2026.bin", 5, ""); res.Status != http.StatusConflict {
		t.Fatalf("starting an upload of a name that is taken: got %d %s, want 409, asked before the file travels", res.Status, res.Body)
	}
}

// An upload in one request also arrives in the hidden uploads folder, and is moved into its folder only when whole: a
// half-finished file never shows there, and one cut off leaves nothing anywhere.
func TestAnUploadInOneRequestNeverShowsHalfFinished(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	uploaded(t, c, placeID, "Already here.txt", []byte("x"))

	body, write := io.Pipe()
	done := slowUpload(d, reka, placeID, "Slow.bin", body, 6)
	if _, err := write.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // on its way to the storage
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Already here.txt"}) {
		t.Fatalf("Réka's top folder on the storage holds %q while an upload is on its way", got)
	}
	if _, items := myFiles(t, d, reka); !slices.Equal(names(items), []string{"Already here.txt"}) {
		t.Fatalf("Réka's list holds %q while an upload is on its way", names(items))
	}
	_, _ = write.Write([]byte("def"))
	_ = write.Close()
	if status := <-done; status != http.StatusCreated {
		t.Fatalf("the upload: got %d", status)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Slow.bin"); string(got) != "abcdef" {
		t.Fatalf("Slow.bin holds %q", got)
	}

	cutOffUpload(t, d, reka, placeID, "Cut off.bin", 4<<20, 1<<20)
	time.Sleep(300 * time.Millisecond)
	if got := onStorage(t, d, rekasTop); !slices.Equal(got, []string{"Already here.txt", "Slow.bin"}) {
		t.Fatalf("Réka's top folder on the storage holds %q after an upload was cut off", got)
	}
	harness.Eventually(t, "the uploads folder is empty", func() bool { return len(inUploads(t, d.Storage)) == 0 })
}

// An upload in progress is its uploader's alone: nobody else can see where it stands, continue, finish or cancel it,
// the owner of the place and project admins included. Every request of it is decided as an upload into its folder is,
// so someone whose access is taken away can do nothing more with it; its bytes are removed a day later.
func TestAnUploadInProgressIsItsUploadersAlone(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	x := bencesFiles(t, d)
	ok(t, "sharing Bakery with Dóra as editor", share(t, b, x.place, x.bakery.ID, dora, "editor"))
	content := randomBytes(t, 3<<20)

	// Dóra uploads into Bakery, which she may add to, and not into Clients.
	if res := startUpload(t, o, x.place, x.clients.ID, "Scan.pdf", len(content), ""); res.Status != http.StatusNotFound {
		t.Fatalf("Dóra starting an upload into Clients: got %d %s, want 404", res.Status, res.Body)
	}
	hers := startedUpload(t, o, x.place, x.bakery.ID, "Scan.pdf", len(content))
	sentPart(t, o, x.place, hers.ID, 0, content[:1<<20])
	his := startedUpload(t, b, x.place, x.bakery.ID, "His.pdf", 3)

	nobody := "0123456789abcdef0123456789abcdef"
	tries := func(id string) map[string]func(c *harness.Client) harness.Response {
		return map[string]func(c *harness.Client) harness.Response{
			"seeing where it stands": func(c *harness.Client) harness.Response { return c.Get(t, sessionPath(x.place, id)) },
			"sending a part":         func(c *harness.Client) harness.Response { return sendPart(t, c, x.place, id, 1<<20, []byte("planted")) },
			"finishing it":           func(c *harness.Client) harness.Response { return finishUpload(t, c, x.place, id, "replace") },
			"cancelling it":          func(c *harness.Client) harness.Response { return c.Do(t, http.MethodDelete, sessionPath(x.place, id), nil) },
		}
	}
	for _, c := range []struct {
		who string
		as  *harness.Client
		id  string
	}{
		{"Bence, whose My files it is, with Dóra's upload", b, hers.ID},
		{"Réka, a project admin, with Dóra's upload", d.As(reka), hers.ID},
		{"Dóra with Bence's upload", o, his.ID},
		{"Dóra with an upload that does not exist", o, nobody},
	} {
		for what, try := range tries(c.id) {
			sameAs(t, c.who+": "+what, try(c.as), tries(nobody)[what](c.as))
		}
	}
	// Both go on as if nothing happened.
	if s := stateOf(t, o, x.place, hers.ID); s.Offset != 1<<20 {
		t.Fatalf("Dóra's upload stands at %d", s.Offset)
	}
	sentPart(t, b, x.place, his.ID, 0, []byte("his"))
	if res := finishUpload(t, b, x.place, his.ID, ""); res.Status != http.StatusCreated {
		t.Fatalf("Bence finishing his upload: got %d %s", res.Status, res.Body)
	}

	// Bence takes Bakery back: from Dóra's next request, her upload is as one that does not exist, and nothing of it
	// reaches Bakery.
	ok(t, "taking Bakery back from Dóra", unshare(t, b, x.place, x.bakery.ID, dora))
	for what, try := range tries(hers.ID) {
		sameAs(t, "Dóra, after Bakery was taken back: "+what, try(o), tries(nobody)[what](o))
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/", "His.pdf"}) {
		t.Fatalf("Bakery on the storage holds %q", got)
	}
	// What arrived stays in the uploads folder, untouched, until a day has passed with nothing arriving.
	if got := inUploads(t, d.Storage); len(got) != 1 || got[hers.ID] != 1<<20 {
		t.Fatalf("the uploads folder holds %v", got)
	}
	d.Restart(t, map[string]string{harness.TestClockShift: "25h"})
	harness.Eventually(t, "Dóra's abandoned upload is removed", func() bool { return len(inUploads(t, d.Storage)) == 0 })
	ok(t, "giving Bakery to Dóra again", share(t, d.As(bence), x.place, x.bakery.ID, dora, "editor"))
	sameAs(t, "Dóra, given Bakery again, a day later", d.As(dora).Get(t, sessionPath(x.place, hers.ID)),
		d.As(dora).Get(t, sessionPath(x.place, nobody)))
}

// An upload nothing arrives for in a day is removed, with its bytes, and so is anything in the uploads folder the Drive
// does not know (left by an upload it did not finish); until then it goes on.
func TestAnUnfinishedUploadIsRemovedAfterADay(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	content := randomBytes(t, 2<<20)
	u := startedUpload(t, c, placeID, 0, "Video.mp4", len(content))
	sentPart(t, c, placeID, u.ID, 0, content[:1<<20])
	d.Storage.Put(t, uploadsFolder+"/left over", "from an upload the Drive did not finish")

	d.Restart(t, map[string]string{harness.TestClockShift: "23h"})
	if s := stateOf(t, d.As(reka), placeID, u.ID); s.Offset != 1<<20 {
		t.Fatalf("23 hours later the upload stands at %d", s.Offset)
	}
	if got := inUploads(t, d.Storage); len(got) != 2 {
		t.Fatalf("23 hours later the uploads folder holds %v", got)
	}

	d.Restart(t, map[string]string{harness.TestClockShift: "25h"})
	harness.Eventually(t, "the uploads folder is emptied", func() bool { return len(inUploads(t, d.Storage)) == 0 })
	if res := d.As(reka).Get(t, sessionPath(placeID, u.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("the abandoned upload: got %d %s, want 404", res.Status, res.Body)
	}
	if _, items := myFiles(t, d, reka); len(items) != 0 {
		t.Fatalf("Réka's list holds %q", names(items))
	}
	if !strings.Contains(d.Logs(), "removed uploads abandoned for a day") {
		t.Fatalf("the Drive did not say what it removed:\n%s", d.Logs())
	}
}

// Cancelling an upload removes what of it arrived.
func TestCancellingAnUpload(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	u := startedUpload(t, c, placeID, 0, "Big.iso", 1<<20)
	sentPart(t, c, placeID, u.ID, 0, randomBytes(t, 1000))
	if res := c.Do(t, http.MethodDelete, sessionPath(placeID, u.ID), nil); res.Status != http.StatusNoContent {
		t.Fatalf("cancelling: got %d %s", res.Status, res.Body)
	}
	if got := inUploads(t, d.Storage); len(got) != 0 {
		t.Fatalf("the uploads folder holds %v", got)
	}
	if res := c.Get(t, sessionPath(placeID, u.ID)); res.Status != http.StatusNotFound {
		t.Fatalf("the cancelled upload: got %d %s, want 404", res.Status, res.Body)
	}
}

// Finishing an upload looks at its name again: what was put there meanwhile is asked about, and replace and keep both
// work as for any upload.
func TestFinishingAnUploadLooksAtItsNameAgain(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	content := randomBytes(t, 100_000)

	u := startedUpload(t, c, placeID, 0, "Plan.pdf", len(content))
	sentPart(t, c, placeID, u.ID, 0, content)
	small := uploaded(t, c, placeID, "Plan.pdf", []byte("put there meanwhile"))
	res := finishUpload(t, c, placeID, u.ID, "")
	var conflict conflictJSON
	res.JSON(t, &conflict)
	if res.Status != http.StatusConflict || conflict.Exists != "file" || conflict.Name != "Plan.pdf" {
		t.Fatalf("finishing over a file put there meanwhile: got %d %s, want 409 asking", res.Status, res.Body)
	}
	res = finishUpload(t, c, placeID, u.ID, "keep-both")
	var kept itemJSON
	res.JSON(t, &kept)
	if res.Status != http.StatusCreated || kept.Name != "Plan (2).pdf" {
		t.Fatalf("finishing, keeping both: got %d %s", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Plan (2).pdf"); !bytes.Equal(got, content) {
		t.Fatal("Plan (2).pdf is not the upload")
	}

	// Replacing: the file there goes to the trash, the upload takes its place and its id.
	again := randomBytes(t, 50_000)
	if res := startUpload(t, c, placeID, 0, "Plan.pdf", len(again), ""); res.Status != http.StatusConflict {
		t.Fatalf("starting an upload of a name that is taken: got %d %s, want 409, asked before the file travels", res.Status, res.Body)
	}
	res = startUpload(t, c, placeID, 0, "Plan.pdf", len(again), "replace")
	var r sessionJSON
	res.JSON(t, &r)
	sentPart(t, c, placeID, r.ID, 0, again)
	res = finishUpload(t, c, placeID, r.ID, "replace")
	var replaced itemJSON
	res.JSON(t, &replaced)
	if res.Status != http.StatusCreated || !replaced.Replaced || replaced.ID != small.ID {
		t.Fatalf("finishing, replacing: got %d %s, want the item of id %d", res.Status, res.Body, small.ID)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Plan.pdf"); !bytes.Equal(got, again) {
		t.Fatal("Plan.pdf is not the upload that replaced it")
	}
	if trash := trashOf(t, c, placeID); len(trash) != 1 || !trash[0].Replaced || trash[0].Size != int64(len("put there meanwhile")) {
		t.Fatalf("the trash holds %+v", trash)
	}
	// A folder is never replaced.
	madeFolder(t, c, placeID, 0, "Drafts")
	res = startUpload(t, c, placeID, 0, "Drafts", 10, "replace")
	res.JSON(t, &conflict)
	if res.Status != http.StatusConflict || conflict.Exists != "folder" {
		t.Fatalf("starting an upload to replace a folder: got %d %s", res.Status, res.Body)
	}
}

// Where the storage cannot write into part of a file (PUT with Content-Range), an upload is sent as one part, and one
// cut off starts again from the beginning: the Drive says so, as "resumable": false, and keeps nothing of it.
func TestWhereTheStorageCannotResumeAnUploadStartsAgain(t *testing.T) {
	t.Parallel()
	for _, folder := range []string{"refuses-content-range", "ignores-content-range"} {
		env, store := harness.StorageAt(t, "/"+folder+"/"+harness.Unique("drive"))
		d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: env})
		placeID, _ := myFiles(t, d, reka)
		c := d.As(reka)
		content := randomBytes(t, 4<<20)

		u := startedUpload(t, c, placeID, 0, "Big.bin", len(content))
		if u.Resumable || u.PartSize != int64(len(content)) {
			t.Fatalf("%s: the upload as it starts: %+v, want it not resumable, in one part", folder, u)
		}
		if res := sendPart(t, c, placeID, u.ID, 0, content[:1000]); res.Status != http.StatusBadRequest {
			t.Fatalf("%s: a part of the file: got %d %s, want 400", folder, res.Status, res.Body)
		}
		cutOffPart(t, d, reka, placeID, u.ID, 0, len(content), content[:1<<20])
		if at := settledOffset(t, c, placeID, u.ID); at != 0 {
			t.Fatalf("%s: after the cut-off it stands at %d, want 0: it starts again", folder, at)
		}
		for name, size := range inUploads(t, store) {
			if size != 0 {
				t.Fatalf("%s: the uploads folder keeps %d bytes of the cut-off upload in %q", folder, size, name)
			}
		}
		sentPart(t, c, placeID, u.ID, 0, content)
		if res := finishUpload(t, c, placeID, u.ID, ""); res.Status != http.StatusCreated {
			t.Fatalf("%s: finishing: got %d %s", folder, res.Status, res.Body)
		}
		if got, _ := store.Get(t, rekasTop+"/Big.bin"); !bytes.Equal(got, content) {
			t.Fatalf("%s: Big.bin differs from the upload", folder)
		}
		if !strings.Contains(d.Logs(), "cannot write part of a file") {
			t.Fatalf("%s: the Drive did not say why uploads start again:\n%s", folder, d.Logs())
		}
	}
}

func TestDownloadsHonourRange(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	content := randomBytes(t, 100_000)
	it := uploaded(t, c, placeID, "Film.mp4", content)
	path := downloadPath(placeID, it.ID)
	time.Sleep(1100 * time.Millisecond) // Apache gives a file changed this very second only a weak ETag

	whole := c.Get(t, path)
	etag, modified := whole.Header.Get("ETag"), whole.Header.Get("Last-Modified")
	// Browsers resume a download with If-Range and the ETag, when it is strong.
	if whole.Status != http.StatusOK || !bytes.Equal(whole.Body, content) || whole.Header.Get("Accept-Ranges") != "bytes" ||
		etag == "" || strings.HasPrefix(etag, "W/") || modified == "" {
		t.Fatalf("the whole file: %d, Accept-Ranges %q, ETag %q, Last-Modified %q", whole.Status,
			whole.Header.Get("Accept-Ranges"), etag, modified)
	}

	for _, r := range []struct {
		rng, ifRange string
		status       int
		from, to     int // the part sent, from and to (not included)
	}{
		{"bytes=0-99", "", http.StatusPartialContent, 0, 100},
		{"bytes=99000-", "", http.StatusPartialContent, 99000, 100_000},
		{"bytes=-500", "", http.StatusPartialContent, 99500, 100_000},
		{"bytes=99990-200000", "", http.StatusPartialContent, 99990, 100_000},
		{"bytes=10-19", etag, http.StatusPartialContent, 10, 20},
		// The file changed since the browser's copy: all of it.
		{"bytes=10-19", `"an older version"`, http.StatusOK, 0, 100_000},
		// Several parts, another unit, or nonsense: all of it, as HTTP allows.
		{"bytes=0-9,20-29", "", http.StatusOK, 0, 100_000},
		{"lines=0-9", "", http.StatusOK, 0, 100_000},
		{"bytes=9-0", "", http.StatusOK, 0, 100_000},
		{"bytes=+5-", "", http.StatusOK, 0, 100_000},
	} {
		cc := c.With("Range", r.rng)
		if r.ifRange != "" {
			cc = cc.With("If-Range", r.ifRange)
		}
		res := cc.Get(t, path)
		want := content[r.from:r.to]
		if res.Status != r.status || !bytes.Equal(res.Body, want) || res.Header.Get("Content-Length") != strconv.Itoa(len(want)) {
			t.Errorf("Range %q, If-Range %q: got %d with %d bytes (Content-Length %q), want %d with bytes %d to %d",
				r.rng, r.ifRange, res.Status, len(res.Body), res.Header.Get("Content-Length"), r.status, r.from, r.to)
			continue
		}
		wantRange := ""
		if r.status == http.StatusPartialContent {
			wantRange = fmt.Sprintf("bytes %d-%d/%d", r.from, r.to-1, len(content))
		}
		if got := res.Header.Get("Content-Range"); got != wantRange {
			t.Errorf("Range %q: Content-Range %q, want %q", r.rng, got, wantRange)
		}
		if disposition := res.Header.Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment") {
			t.Errorf("Range %q: Content-Disposition %q", r.rng, disposition)
		}
	}

	// A part that starts after the end.
	res := c.With("Range", "bytes=100000-").Get(t, path)
	if res.Status != http.StatusRequestedRangeNotSatisfiable || res.Header.Get("Content-Range") != "bytes */100000" {
		t.Fatalf("a part after the end: got %d, Content-Range %q", res.Status, res.Header.Get("Content-Range"))
	}
	// HEAD says what a download would get, and sends nothing.
	res = c.Do(t, http.MethodHead, path, nil)
	if res.Status != http.StatusOK || res.Header.Get("Content-Length") != "100000" || res.Header.Get("Accept-Ranges") != "bytes" || len(res.Body) != 0 {
		t.Fatalf("HEAD: got %d, Content-Length %q", res.Status, res.Header.Get("Content-Length"))
	}
}

// zipEntries reads a zip: each entry's name and what it holds; folders end in "/".
func zipEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a zip (%v): %d bytes", err, len(data))
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if strings.HasSuffix(f.Name, "/") != f.FileInfo().IsDir() {
			t.Errorf("%q: a folder %v", f.Name, f.FileInfo().IsDir())
		}
		out[f.Name] = string(b)
	}
	return out
}

func zipPath(placeID, folderID int64, items ...int64) string {
	p := fmt.Sprintf("/api/places/%d/zip", placeID)
	if folderID != 0 {
		p = fmt.Sprintf("/api/places/%d/items/%d/zip", placeID, folderID)
	}
	q := url.Values{}
	for _, id := range items {
		q.Add("item", strconv.FormatInt(id, 10))
	}
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p
}

func TestAFolderOrASelectionDownloadsAsOneZip(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	clients := madeFolder(t, c, placeID, 0, "Clients")
	bakery := madeFolder(t, c, placeID, clients.ID, "Bakery Ltd")
	madeFolder(t, c, placeID, clients.ID, "Empty")
	offer := randomBytes(t, 300_000)
	offerItem := uploadedInto(t, c, placeID, bakery.ID, "Offer – árvíztűrő.pdf", offer)
	uploadedInto(t, c, placeID, clients.ID, `Back\slash.txt`, []byte("notes"))
	top := uploadedInto(t, c, placeID, 0, "Top.txt", []byte("top"))
	d.Storage.Put(t, rekasTop+"/Clients/Bakery Ltd/Put directly.txt", "on the storage") // in the zip without being opened

	res := c.Get(t, zipPath(placeID, clients.ID))
	if res.Status != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" || savedAs(res) != "Clients.zip" {
		t.Fatalf("Clients as a zip: got %d, %q, %q", res.Status, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
	}
	want := map[string]string{
		"Clients/": "", "Clients/Bakery Ltd/": "", "Clients/Empty/": "", "Clients/Back_slash.txt": "notes",
		"Clients/Bakery Ltd/Offer – árvíztűrő.pdf": string(offer), "Clients/Bakery Ltd/Put directly.txt": "on the storage",
	}
	if got := zipEntries(t, res.Body); !mapsEqual(got, want) {
		t.Fatalf("Clients.zip holds %q", keys(got))
	}

	// The whole place, named after it.
	res = c.Get(t, zipPath(placeID, 0))
	if savedAs(res) != "My files.zip" {
		t.Fatalf("My files as a zip: %q", res.Header.Get("Content-Disposition"))
	}
	if got := zipEntries(t, res.Body); len(got) != 8 || got["My files/Top.txt"] != "top" || got["My files/Clients/Bakery Ltd/Offer – árvíztűrő.pdf"] != string(offer) {
		t.Fatalf("My files.zip holds %q", keys(got))
	}

	// A selection: those items at the top of the zip.
	res = c.Get(t, zipPath(placeID, 0, top.ID, clients.ID))
	if got := zipEntries(t, res.Body); len(got) != 7 || got["Top.txt"] != "top" || got["Clients/Back_slash.txt"] != "notes" {
		t.Fatalf("a selection of Top.txt and Clients holds %q", keys(got))
	}
	if got := zipEntries(t, c.Get(t, zipPath(placeID, bakery.ID, offerItem.ID)).Body); len(got) != 1 || got["Offer – árvíztűrő.pdf"] != string(offer) {
		t.Fatalf("a selection of Offer.pdf in Bakery holds %q", keys(got))
	}
	// Something that is not directly in the folder is answered as something that does not exist.
	sameAs(t, "a selection with an item from a folder below", c.Get(t, zipPath(placeID, 0, top.ID, offerItem.ID)),
		c.Get(t, zipPath(placeID, 0, top.ID, offerItem.ID+1000)))
	if res := c.Get(t, zipPath(placeID, offerItem.ID)); res.Status != http.StatusBadRequest {
		t.Fatalf("a file as a zip: got %d %s, want 400", res.Status, res.Body)
	}
	if res := c.Get(t, zipPath(placeID, 0)+"?item=x"); res.Status != http.StatusBadRequest {
		t.Fatalf("a selection that is not ids: got %d %s, want 400", res.Status, res.Body)
	}

	// Someone given Bakery as viewer downloads it as a zip, and nothing above it.
	ok(t, "sharing Bakery with Bence", share(t, c, placeID, bakery.ID, bence, "viewer"))
	if got := zipEntries(t, d.As(bence).Get(t, zipPath(placeID, bakery.ID)).Body); len(got) != 3 {
		t.Fatalf("Bence's Bakery Ltd.zip holds %q", keys(got))
	}
	missing := d.As(bence).Get(t, zipPath(placeID, offerItem.ID+1000))
	sameAs(t, "Bence zipping Clients", d.As(bence).Get(t, zipPath(placeID, clients.ID)), missing)
	sameAs(t, "Bence zipping Réka's My files", d.As(bence).Get(t, zipPath(placeID, 0)), d.As(bence).Get(t, zipPath(placeID+1000, 0)))
}

// savedAs is the name an answer has the browser save it under.
func savedAs(res harness.Response) string {
	disposition, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" {
		return ""
	}
	return params["filename"]
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// heldDownload starts a download and reads only its answer's head, so it holds a transfer slot until closed. It
// returns the connection and the status, or 0 when no answer came within wait.
func heldDownload(t *testing.T, d *harness.Drive, m harness.Member, path string, wait time.Duration) (net.Conn, int) {
	t.Helper()
	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nDrive-Secret: %s\r\nRemote-User-Id: %s\r\n\r\n", path, address.Host, d.Secret, m.UserID)
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	status, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return conn, 0
	}
	n, _ := strconv.Atoi(strings.Fields(status)[1])
	return conn, n
}

// At most 32 transfers are in flight. Further downloads wait their turn in line; further uploads are told at once to
// wait, keep their place in line, and get their turn when it comes. A file opened in the browser (a video, here) is a
// transfer like a download: the first of the 32 is one, and the 33rd download gets its turn when it ends.
func TestAtMost32TransfersAreInFlightAndTheRestWaitTheirTurn(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	big := uploaded(t, c, placeID, "Big.mp4", randomBytes(t, 16<<20)) // more than the network buffers on the way hold
	u := startedUpload(t, c, placeID, 0, "Resumable.bin", 10)

	var held []net.Conn
	for i := range 32 {
		path := downloadPath(placeID, big.ID)
		if i == 0 {
			path = openPath(placeID, big.ID)
		}
		conn, status := heldDownload(t, d, reka, path, 5*time.Second)
		if status != http.StatusOK {
			t.Fatalf("transfer %d (%s): got %d", len(held)+1, path, status)
		}
		held = append(held, conn)
	}

	// A 33rd download waits in line: no answer yet.
	waiting, done := make(chan int, 1), make(chan net.Conn, 1)
	go func() {
		conn, status := heldDownload(t, d, reka, downloadPath(placeID, big.ID), 20*time.Second)
		done <- conn
		waiting <- status
	}()
	time.Sleep(time.Second)
	select {
	case s := <-waiting:
		t.Fatalf("a 33rd download was answered at once: %d", s)
	default:
	}
	// An upload is told at once, and keeps its place behind it.
	res := c.Do(t, http.MethodPost, uploadPath(placeID, "Small.txt")+"&turn=small-1", strings.NewReader("small"))
	if res.Status != http.StatusServiceUnavailable || res.Header.Get("Retry-After") == "" || !strings.Contains(string(res.Body), `"busy":true`) {
		t.Fatalf("an upload while every slot is taken: got %d %s, Retry-After %q", res.Status, res.Body, res.Header.Get("Retry-After"))
	}
	res = sendPart(t, c, placeID, u.ID, 0, []byte("0123456789"))
	if res.Status != http.StatusServiceUnavailable {
		t.Fatalf("a part while every slot is taken: got %d %s", res.Status, res.Body)
	}
	if s := stateOf(t, c, placeID, u.ID); !s.Waiting {
		t.Fatalf("the resumable upload's state: %+v, want it waiting", s)
	}

	// The opened video ends: the 33rd gets its turn.
	held[0].Close()
	if s := <-waiting; s != http.StatusOK {
		t.Fatalf("the 33rd download, once a slot was free: got %d", s)
	}
	(<-done).Close()
	// The next slot is kept for the upload in one request, first in line now: without its turn, another upload waits.
	harness.Eventually(t, "the upload's turn has come", func() bool {
		return sendPart(t, c, placeID, u.ID, 0, []byte("0123456789")).Status == http.StatusServiceUnavailable &&
			upload(t, c, placeID, "Other.txt", []byte("x")).Status == http.StatusServiceUnavailable
	})
	res = c.Do(t, http.MethodPost, uploadPath(placeID, "Small.txt")+"&turn=small-1", strings.NewReader("small"))
	if res.Status != http.StatusCreated {
		t.Fatalf("the upload, sent again in its turn: got %d %s", res.Status, res.Body)
	}
	// And the next for the resumable upload.
	held[1].Close()
	harness.Eventually(t, "the part's turn has come", func() bool { return !stateOf(t, c, placeID, u.ID).Waiting })
	sentPart(t, c, placeID, u.ID, 0, []byte("0123456789"))
	if res := finishUpload(t, c, placeID, u.ID, ""); res.Status != http.StatusCreated {
		t.Fatalf("finishing: got %d %s", res.Status, res.Body)
	}
}
