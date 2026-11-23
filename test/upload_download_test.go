package test

import (
	"bytes"
	"crypto/rand"
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

// upload sends a file the way the interface does: its bytes as the body of a POST, its name in the address.
func upload(t *testing.T, c *harness.Client, placeID int64, name string, content []byte) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodPost, uploadPath(placeID, name), bytes.NewReader(content))
}

func uploadPath(placeID int64, name string) string {
	return fmt.Sprintf("/api/places/%d/files?name=%s", placeID, url.QueryEscape(name))
}

func downloadPath(placeID, itemID int64) string {
	return fmt.Sprintf("/api/places/%d/items/%d/download", placeID, itemID)
}

// uploaded uploads a file and fails the test unless the Drive took it.
func uploaded(t *testing.T, c *harness.Client, placeID int64, name string, content []byte) itemJSON {
	t.Helper()
	res := upload(t, c, placeID, name, content)
	if res.Status != http.StatusCreated {
		t.Fatalf("uploading %q: got %d %s, want 201", name, res.Status, res.Body)
	}
	var it itemJSON
	res.JSON(t, &it)
	return it
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// errorOf is the message in an error answer.
func errorOf(t *testing.T, res harness.Response) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	res.JSON(t, &e)
	return e.Error
}

const rekasTop = "My files/reka@example.com"

func TestAnUploadedFileIsInTheListAndOnTheStorageUnderItsRealName(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	name := `Réka's "budget" #2 & 100% done?.xlsx`
	content := randomBytes(t, 100_000)
	it := uploaded(t, d.As(reka), placeID, name, content)
	if it.Name != name || it.Kind != "file" || it.Size == nil || *it.Size != int64(len(content)) || it.Changed == nil {
		t.Fatalf("the Drive answered %+v, want the new file with its name, size and changed date", it)
	}

	// In the list at once, as the interface asks for it after an upload.
	_, items := myFiles(t, d, reka)
	if len(items) != 1 || items[0].ID != it.ID || items[0].Name != name || *items[0].Size != int64(len(content)) {
		t.Fatalf("the list holds %+v, want only the uploaded file", items)
	}

	// On the storage, in Réka's top folder, under its real name, with the same bytes.
	onStorage, _ := d.Storage.List(t, rekasTop)
	if len(onStorage) != 1 || onStorage[0].Name != name || onStorage[0].Folder {
		t.Fatalf("Réka's folder on the storage holds %+v, want only %q", onStorage, name)
	}
	got, ok := d.Storage.Get(t, rekasTop+"/"+name)
	if !ok || !bytes.Equal(got, content) {
		t.Fatalf("the file on the storage holds %d bytes, want the %d uploaded", len(got), len(content))
	}
}

func TestADownloadHasTheOriginalNameAndTheSameBytes(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	files := []struct {
		name string
		size int
	}{
		{"Tax notes 2026.pdf", 30_000},
		{"Árvíztűrő tükörfúrógép.txt", 10},
		{"日本語の資料.docx", 2_000},
		{"Holiday 🏖️ photos.zip", 5 << 20}, // larger than any buffer on the way
		{"empty.txt", 0},
		{`quote " and back\slash.txt`, 7},
		{"plus+semicolon;comma,equals=.csv", 7},
		{"percent %41 stays %41.txt", 7}, // must not be decoded on the way to the storage or back
		{"  spaces around  ", 7},
		{".hidden", 7},
		{"no extension", 7},
		{strings.Repeat("a", 251) + ".txt", 7}, // exactly 255 bytes
	}
	for _, f := range files {
		content := randomBytes(t, f.size)
		it := uploaded(t, d.As(reka), placeID, f.name, content)

		res := d.As(reka).Get(t, downloadPath(placeID, it.ID))
		if res.Status != http.StatusOK {
			t.Errorf("%q: got %d %s", f.name, res.Status, res.Body)
			continue
		}
		disposition, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != f.name {
			t.Errorf("%q: Content-Disposition %q, want an attachment named %q", f.name, res.Header.Get("Content-Disposition"), f.name)
		}
		if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%q: Content-Type %q, want application/octet-stream so the browser saves it", f.name, ct)
		}
		if cl := res.Header.Get("Content-Length"); cl != strconv.Itoa(f.size) {
			t.Errorf("%q: Content-Length %q, want %d", f.name, cl, f.size)
		}
		if !bytes.Equal(res.Body, content) {
			t.Errorf("%q: downloaded %d bytes that differ from the %d uploaded", f.name, len(res.Body), len(content))
		}
	}

	// A file put on the storage directly downloads too, once the Drive has seen it.
	d.Storage.Put(t, rekasTop+"/From the storage.txt", "put there directly")
	_, items := myFiles(t, d, reka)
	i := slices.IndexFunc(items, func(it itemJSON) bool { return it.Name == "From the storage.txt" })
	if i < 0 {
		t.Fatalf("the list does not show the file put on the storage: %q", names(items))
	}
	if res := d.As(reka).Get(t, downloadPath(placeID, items[i].ID)); res.Status != http.StatusOK || string(res.Body) != "put there directly" {
		t.Fatalf("got %d %q", res.Status, res.Body)
	}
}

// cutOffUpload starts an upload that says it is size bytes long, sends only the first sent bytes and hangs up, as a
// browser does when its connection drops.
func cutOffUpload(t *testing.T, d *harness.Drive, m harness.Member, placeID int64, name string, size, sent int) {
	t.Helper()
	address, _ := url.Parse(d.URL)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\nDrive-Secret: %s\r\nRemote-User-Id: %s\r\nOrigin: %s\r\n"+
		"Content-Type: application/octet-stream\r\nContent-Length: %d\r\n\r\n",
		uploadPath(placeID, name), address.Host, d.Secret, m.UserID, d.Address, size)
	if _, err := conn.Write(randomBytes(t, sent)); err != nil {
		t.Fatal(err)
	}
}

func TestAnUploadIsStoredWholeOrNotAtAll(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	// The connection drops a quarter of the way in.
	cutOffUpload(t, d, reka, placeID, "Cut off.bin", 4<<20, 1<<20)

	// Nothing of it was kept: once the Drive has let go of the name, an upload of the same name is a new file. (Had
	// any part of the cut-off one been stored, this would be refused as already there, for good.)
	whole := randomBytes(t, 4<<20)
	var res harness.Response
	harness.Eventually(t, "the name is free again", func() bool {
		res = upload(t, d.As(reka), placeID, "Cut off.bin", whole)
		return res.Status != http.StatusConflict
	})
	if res.Status != http.StatusCreated {
		t.Fatalf("got %d %s", res.Status, res.Body)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/Cut off.bin"); !bytes.Equal(got, whole) {
		t.Fatalf("the storage holds %d bytes, want the %d of the whole upload", len(got), len(whole))
	}

	// An upload that does not say how long it is (a chunked body) could not be told apart from a cut-off one: refused.
	chunked := io.MultiReader(strings.NewReader("no "), strings.NewReader("length"))
	res = d.As(reka).Do(t, http.MethodPost, uploadPath(placeID, "No length.txt"), chunked)
	if res.Status != http.StatusLengthRequired {
		t.Errorf("an upload without a length: got %d %s, want 411", res.Status, res.Body)
	}

	onStorage, _ := d.Storage.List(t, rekasTop)
	if len(onStorage) != 1 || onStorage[0].Name != "Cut off.bin" || onStorage[0].Size != int64(len(whole)) {
		t.Fatalf("Réka's folder holds %+v, want only the whole upload", onStorage)
	}
}

func TestANameLongerThan255BytesIsRefusedWithAMessageAPersonCanUnderstand(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	tooLong := map[string]string{
		"256 plain letters":                strings.Repeat("a", 252) + ".txt",
		"128 accented letters (256 bytes)": strings.Repeat("é", 128),
		"63 emoji and !.pdf (257 bytes)":   strings.Repeat("📄", 63) + "!.pdf",
	}
	for what, name := range tooLong {
		res := upload(t, d.As(reka), placeID, name, []byte("content"))
		message := errorOf(t, res)
		if res.Status != http.StatusBadRequest || !strings.Contains(message, "too long") || !strings.Contains(message, "255") {
			t.Errorf("%s: got %d %q, want 400 saying the name is too long and what the limit is", what, res.Status, message)
		}
	}

	// Other names that cannot be stored are refused with a reason too.
	bad := []string{"", ".", "..", "a/b.txt", "line\nbreak.txt", "tab\t.txt", "nul\x00.txt", "\xff\xfe.txt"}
	for _, name := range bad {
		res := upload(t, d.As(reka), placeID, name, []byte("content"))
		if message := errorOf(t, res); res.Status != http.StatusBadRequest || message == "" {
			t.Errorf("%q: got %d %q, want 400 with a reason", name, res.Status, message)
		}
	}
	if onStorage, _ := d.Storage.List(t, rekasTop); len(onStorage) != 0 {
		t.Fatalf("refused names reached the storage: %+v", onStorage)
	}

	// At the limit is fine, counted in bytes: 127 accented letters and one plain one make 255.
	for _, name := range []string{strings.Repeat("b", 255), strings.Repeat("é", 127) + "x"} {
		uploaded(t, d.As(reka), placeID, name, []byte("content"))
		if _, ok := d.Storage.Get(t, rekasTop+"/"+name); !ok {
			t.Errorf("a %d-byte name is not on the storage", len(name))
		}
	}
}

func TestAnUploadNeverReplacesWhatIsThere(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	uploaded(t, d.As(reka), placeID, "a.txt", []byte("first"))
	res := upload(t, d.As(reka), placeID, "a.txt", []byte("second"))
	if message := errorOf(t, res); res.Status != http.StatusConflict || !strings.Contains(message, "a.txt") {
		t.Errorf("uploading a.txt again: got %d %q, want 409 naming the file", res.Status, message)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/a.txt"); string(got) != "first" {
		t.Errorf("a.txt now holds %q, want the first upload", got)
	}

	// The storage decides what is there, even before the Drive has listed it: a file and a folder put there directly.
	d.Storage.Put(t, rekasTop+"/direct.txt", "put directly")
	d.Storage.MakeFolder(t, rekasTop+"/Drafts")
	for _, name := range []string{"direct.txt", "Drafts"} {
		if res := upload(t, d.As(reka), placeID, name, []byte("upload")); res.Status != http.StatusConflict {
			t.Errorf("uploading %q over what is on the storage: got %d %s, want 409", name, res.Status, res.Body)
		}
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/direct.txt"); string(got) != "put directly" {
		t.Errorf("direct.txt now holds %q", got)
	}
	if entries, ok := d.Storage.List(t, rekasTop+"/Drafts"); !ok || len(entries) != 0 {
		t.Errorf("the folder Drafts changed: %+v", entries)
	}

	// Two uploads of one name at once: neither holds the name while its bytes arrive, so the first to be whole is
	// stored, and the other, whole later, finds the name taken, as any upload of a taken name does. It replaces nothing,
	// and leaves nothing behind.
	body, write := io.Pipe()
	slow := slowUpload(d, reka, placeID, "same.txt", body, 6)
	if _, err := write.Write([]byte("slo")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the slow upload is now writing to the storage
	if res := upload(t, d.As(reka), placeID, "same.txt", []byte("quick!")); res.Status != http.StatusCreated {
		t.Errorf("an upload of a name whose other upload is still arriving: got %d %s, want 201", res.Status, res.Body)
	}
	_, _ = write.Write([]byte("wer"))
	_ = write.Close()
	if status := <-slow; status != http.StatusConflict {
		t.Fatalf("the upload that was whole second: got %d, want 409", status)
	}
	if got, _ := d.Storage.Get(t, rekasTop+"/same.txt"); string(got) != "quick!" {
		t.Errorf("same.txt holds %q, want the upload that was whole first", got)
	}
	harness.Eventually(t, "the uploads folder is empty", func() bool { return len(inUploads(t, d.Storage)) == 0 })
}

// slowUpload starts an upload of length bytes read from body as they come, as a member's browser on the Drive's page,
// and sends its status when it ends (0 if it could not be made).
func slowUpload(d *harness.Drive, m harness.Member, placeID int64, name string, body io.Reader, length int64) <-chan int {
	done := make(chan int, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, d.URL+uploadPath(placeID, name), body)
		if err != nil {
			done <- 0
			return
		}
		req.ContentLength = length
		req.Header.Set("Drive-Secret", d.Secret)
		req.Header.Set("Remote-User-Id", m.UserID)
		req.Header.Set("Origin", d.Address)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- 0
			return
		}
		res.Body.Close()
		done <- res.StatusCode
	}()
	return done
}

// Réka is a project admin: "My files" is closed to admins too.
func TestAnotherMemberCannotListDownloadOrUploadIntoAMembersFiles(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	bencesPlace, _ := myFiles(t, d, bence)
	rekasPlace, _ := myFiles(t, d, reka)
	payslip := uploaded(t, d.As(bence), bencesPlace, "Payslip November.pdf", []byte("private"))
	notes := uploaded(t, d.As(reka), rekasPlace, "Réka's notes.txt", []byte("also private"))

	for _, c := range []struct {
		who             harness.Member
		place, ownPlace int64
		item            itemJSON
	}{
		{reka, bencesPlace, rekasPlace, payslip},
		{bence, rekasPlace, bencesPlace, notes},
	} {
		// Listing and downloading through the other member's place answer exactly as a place that does not exist.
		missingPlace := d.As(c.who).Get(t, downloadPath(rekasPlace+bencesPlace+100, c.item.ID))
		for _, path := range []string{fmt.Sprintf("/api/places/%d/items", c.place), downloadPath(c.place, c.item.ID)} {
			res := d.As(c.who).Get(t, path)
			if res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missingPlace.Body) {
				t.Errorf("%s: %s answered %d %s; a place that does not exist answers %d %s",
					c.who.Email, path, res.Status, res.Body, missingPlace.Status, missingPlace.Body)
			}
		}
		// The other member's item through one's own place is not found either, as an item that does not exist.
		missingItem := d.As(c.who).Get(t, downloadPath(c.ownPlace, payslip.ID+notes.ID+100))
		res := d.As(c.who).Get(t, downloadPath(c.ownPlace, c.item.ID))
		if res.Status != http.StatusNotFound || !bytes.Equal(res.Body, missingItem.Body) {
			t.Errorf("%s: another member's item through their own place answered %d %s; a missing item answers %d %s",
				c.who.Email, res.Status, res.Body, missingItem.Status, missingItem.Body)
		}
		// Nor can they put a file into it.
		if res := upload(t, d.As(c.who), c.place, "Planted.txt", []byte("x")); res.Status != http.StatusNotFound {
			t.Errorf("%s: uploading into another member's place: got %d %s, want 404", c.who.Email, res.Status, res.Body)
		}
	}

	for top, want := range map[string]string{"My files/bence@example.com": "Payslip November.pdf", rekasTop: "Réka's notes.txt"} {
		if onStorage, _ := d.Storage.List(t, top); len(onStorage) != 1 || onStorage[0].Name != want {
			t.Errorf("%s holds %+v, want only %q", top, onStorage, want)
		}
	}
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		if res := d.As(reka).Get(t, fmt.Sprintf("/api/places/%d/items/%s/download", rekasPlace, bad)); res.Status != http.StatusNotFound {
			t.Errorf("item %q: got %d, want 404", bad, res.Status)
		}
	}
}

// Members are signed in by the gateway's cookie, so a page on another site can make a member's browser send a request
// to the Drive. Every change must come from the Drive's own page (DRIVE_ADDRESS); reading needs no such proof.
func TestAChangeFromAnotherSiteIsRefused(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)

	for what, origin := range map[string]string{
		"another site":                       "https://evil.example",
		"the Drive's name with another port": "http://drive.test:8080",
		"the Drive's name over https":        "https://drive.test",
		"a page without an origin":           "null",
		"no Origin at all":                   "",
	} {
		res := upload(t, d.As(reka).From(origin), placeID, "Planted by "+what+".txt", []byte("x"))
		if message := errorOf(t, res); res.Status != http.StatusForbidden || !strings.Contains(message, "own page") {
			t.Errorf("an upload from %s: got %d %q, want 403", what, res.Status, message)
		}
	}
	if onStorage, _ := d.Storage.List(t, rekasTop); len(onStorage) != 0 {
		t.Fatalf("a refused change reached the storage: %+v", onStorage)
	}

	it := uploaded(t, d.As(reka).From(d.Address), placeID, "From the Drive's page.txt", []byte("ok"))
	if res := d.As(reka).From("https://evil.example").Get(t, downloadPath(placeID, it.ID)); res.Status != http.StatusOK {
		t.Errorf("a download, which changes nothing: got %d %s, want 200", res.Status, res.Body)
	}
}

func TestDownloadingSomethingThatIsNotAFileThere(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	d.Storage.MakeFolder(t, rekasTop+"/Drafts")
	placeID, _ := myFiles(t, d, reka)
	gone := uploaded(t, d.As(reka), placeID, "Gone.txt", []byte("soon gone"))
	_, items := myFiles(t, d, reka)
	folder := items[slices.IndexFunc(items, func(it itemJSON) bool { return it.Name == "Drafts" })]

	// Removed from the storage directly, behind the Drive's back.
	d.Storage.Delete(t, rekasTop+"/Gone.txt", false)
	res := d.As(reka).Get(t, downloadPath(placeID, gone.ID))
	if message := errorOf(t, res); res.Status != http.StatusNotFound || !strings.Contains(message, "no longer") {
		t.Errorf("a file gone from the storage: got %d %q, want 404 saying it is gone", res.Status, message)
	}

	res = d.As(reka).Get(t, downloadPath(placeID, folder.ID))
	if message := errorOf(t, res); res.Status != http.StatusNotFound || !strings.Contains(message, "folder") {
		t.Errorf("a folder: got %d %q, want 404 saying it is a folder", res.Status, message)
	}
}
