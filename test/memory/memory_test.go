//go:build memory

package memory

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// What the test does, and how much.
const (
	fileSize  = 256 << 20 // each file moved: four times the memory limit
	downloads = 40        // at once
	uploads   = 10        // at once, with the downloads
	// Of the downloads, this many read slowly, as browsers on slow lines do: the Drive's buffers toward them stay full.
	slowDownloads = 20
	slowRate      = 8 << 20 // bytes a second
	zips          = 2       // more downloads, after: a folder holding a file of fileSize, as one zip
	partSize      = 32 << 20

	limit         = "64m"
	driveImage    = "superit-drive:memory-test"
	storageImage  = "superit-drive-test-webdav"
	storageUser   = "drive"
	storagePass   = "drive-test-password"
	driveAddress  = "http://drive.test"
	memberID      = "u-memory"
	storageFolder = "/memory"
)

func TestTheDriveStaysWithin64MiB(t *testing.T) {
	root := repoRoot(t)
	run(t, "docker", "build", "-q", "-t", driveImage, root)
	run(t, "docker", "build", "-q", "-t", storageImage, filepath.Join(root, "test", "webdav"))

	network := "superit-drive-memory-" + randomHex(4)
	run(t, "docker", "network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })

	storageID := run(t, "docker", "run", "-d", "--rm", "--label", "superit-drive-test=1", "--network", network,
		"--network-alias", "storage", "-p", "127.0.0.1::80", storageImage)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", storageID).Run() })
	storage := &webdav{root: "http://" + port(t, storageID, "80") + storageFolder}

	members := t.TempDir()
	list, _ := json.Marshal(map[string]any{"members": []map[string]string{
		{"userId": memberID, "email": "load@example.com", "displayName": "Load", "role": "member"}}})
	if err := os.WriteFile(filepath.Join(members, "members.json"), list, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(members, 0o755)
	secret := randomHex(32)
	driveID := run(t, "docker", "run", "-d", "--label", "superit-drive-test=1", "--network", network,
		"--memory="+limit, "--memory-swap="+limit, "-p", "127.0.0.1::8080", "-v", members+":/members:ro",
		"-e", "DRIVE_STORAGE_URL=http://storage", "-e", "DRIVE_STORAGE_USER="+storageUser,
		"-e", "DRIVE_STORAGE_PASSWORD="+storagePass, "-e", "DRIVE_STORAGE_FOLDER="+storageFolder,
		"-e", "DRIVE_ADDRESS="+driveAddress, "-e", "DRIVE_PROJECT_NAME=Memory test",
		"-e", "DRIVE_HEADER_SECRET="+secret, "-e", "DRIVE_MEMBERS_FILE=/members/members.json", driveImage)
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "60", driveID).CombinedOutput()
			t.Logf("the Drive's log, last lines:\n%s", logs)
		}
		_ = exec.Command("docker", "rm", "-f", driveID).Run()
	})
	drive := &client{base: "http://" + port(t, driveID, "8080"), secret: secret, http: &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: 64, DisableCompression: true, ResponseHeaderTimeout: 15 * time.Minute}}}
	drive.waitUntilUp(t)

	logs, _ := exec.Command("docker", "logs", driveID).CombinedOutput()
	for _, line := range strings.Split(string(logs), "\n") {
		if strings.Contains(line, "msg=listening") {
			t.Logf("the Drive started: %s", line)
		}
	}
	if !strings.Contains(string(logs), "memoryLimit=") {
		t.Fatalf("the Drive did not say its memory limit:\n%s", logs)
	}

	sampler := startSampler(t, driveID)
	time.Sleep(2 * time.Second)
	idle := sampler.latest()
	t.Logf("idle: %s", idle)

	place := drive.myFiles(t)
	big := drive.newFolder(t, place, "Big")

	// One large upload, cut off midway through a part and resumed from where it stopped.
	seed := pattern{seed: 1, size: fileSize}
	began := time.Now()
	seedItem := drive.uploadResumable(t, place, big, "Seed.bin", seed, true)
	t.Logf("one upload of %d MiB, cut off once and resumed: %s", fileSize>>20, time.Since(began).Round(time.Millisecond))
	if got := storage.sum(t, "My files/load@example.com/Big/Seed.bin"); got != seed.sum() {
		t.Fatalf("Seed.bin on the storage differs from what was uploaded")
	}
	afterOne := sampler.mark()

	// 40 downloads and 10 uploads at once.
	type outcome struct {
		what string
		err  error
	}
	results := make(chan outcome, downloads+uploads)
	var wg sync.WaitGroup
	began = time.Now()
	for i := range downloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rate := 0
			if i < slowDownloads {
				rate = slowRate
			}
			sum, n, err := drive.download(place, seedItem, rate)
			if err == nil && (n != fileSize || sum != seed.sum()) {
				err = fmt.Errorf("got %d bytes that differ from Seed.bin", n)
			}
			results <- outcome{fmt.Sprintf("download %d", i+1), err}
		}()
	}
	sent := make([]pattern, uploads)
	for j := range uploads {
		sent[j] = pattern{seed: uint64(100 + j), size: fileSize}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := drive.tryUploadResumable(place, big, fmt.Sprintf("Upload %d.bin", j+1), sent[j], false)
			results <- outcome{fmt.Sprintf("upload %d", j+1), err}
		}()
	}
	wg.Wait()
	close(results)
	took := time.Since(began)
	for r := range results {
		if r.err != nil {
			t.Errorf("%s: %v", r.what, r.err)
		}
	}
	underLoad := sampler.mark()
	t.Logf("%d downloads (%d of them read at %d MiB/s) and %d uploads of %d MiB each, at once: %s", downloads,
		slowDownloads, slowRate>>20, uploads, fileSize>>20, took.Round(time.Millisecond))
	for j, p := range sent {
		if got := storage.sum(t, fmt.Sprintf("My files/load@example.com/Big/Upload %d.bin", j+1)); got != p.sum() {
			t.Errorf("Upload %d.bin on the storage differs from what was uploaded", j+1)
		}
	}

	// Zips of a folder holding a large file, at once.
	zipFolder := drive.newFolder(t, place, "Zipped")
	drive.uploadResumable(t, place, zipFolder, "Inside.bin", seed, false)
	began = time.Now()
	zipErrs := make(chan error, zips)
	for range zips {
		go func() { zipErrs <- drive.checkZip(place, zipFolder, "Zipped/Inside.bin", seed) }()
	}
	for range zips {
		if err := <-zipErrs; err != nil {
			t.Errorf("a zip: %v", err)
		}
	}
	t.Logf("%d zips of a folder holding %d MiB, at once: %s", zips, fileSize>>20, time.Since(began).Round(time.Millisecond))
	zipping := sampler.mark()

	state := strings.Fields(run(t, "docker", "inspect", "-f", "{{.State.OOMKilled}} {{.State.Running}} {{.RestartCount}}", driveID))
	if state[0] != "false" || state[1] != "true" {
		t.Fatalf("the Drive's container: OOMKilled %s, running %s", state[0], state[1])
	}
	cg := cgroup(t, driveID)
	peak, _ := strconv.ParseInt(strings.TrimSpace(cg["memory.peak"]), 10, 64)
	t.Logf("the Drive's container (limit %s): not killed, still running, restarts %s", limit, state[2])
	t.Logf("peak (cgroup memory.peak, the whole run): %s", mib(peak))
	t.Logf("highest during one resumed upload:      %s", afterOne)
	t.Logf("highest during 40 downloads + 10 uploads: %s", underLoad)
	t.Logf("highest during %d zips:                   %s", zips, zipping)
	t.Logf("memory.events: %s", strings.Join(strings.Fields(cg["memory.events"]), " "))
	t.Logf("(anon is the Drive's own memory, sock the kernel's network buffers, file the page cache, which the kernel " +
		"takes back before it would kill anything)")
}

// A sample is the container's memory at one moment, from its cgroup.
type sample struct {
	current, anon, file, sock, kernel int64
}

func (s sample) String() string {
	return fmt.Sprintf("%s (anon %s, sock %s, file %s, kernel %s)", mib(s.current), mib(s.anon), mib(s.sock), mib(s.file), mib(s.kernel))
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

// sampler reads the container's memory every 200 ms, from a privileged container that can see the cgroups of the
// Docker host.
type sampler struct {
	mu      sync.Mutex
	last    sample
	highest sample // since the last mark
}

func startSampler(t *testing.T, id string) *sampler {
	script := fmt.Sprintf(`d=/cg/docker/%s; [ -d $d ] || d=$(find /cg -type d -name '*%s*' | head -1)
while true; do echo "$(cat $d/memory.current) $(grep -E '^(anon|file|sock|kernel) ' $d/memory.stat | tr '\n' ' ')"; sleep 0.2; done`, id, id)
	name := "superit-drive-memory-sampler-" + randomHex(4)
	cmd := exec.Command("docker", "run", "--rm", "--name", name, "--privileged", "-v", "/sys/fs/cgroup:/cg:ro",
		"--label", "superit-drive-test=1", "alpine:3", "sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run() // stopping docker run alone leaves the container running
		_ = cmd.Wait()
	})
	s := &sampler{}
	go func() {
		lines := bufio.NewScanner(out)
		for lines.Scan() {
			f := strings.Fields(lines.Text())
			if len(f) < 9 {
				continue
			}
			var x sample
			x.current, _ = strconv.ParseInt(f[0], 10, 64)
			for i := 1; i+1 < len(f); i += 2 {
				n, _ := strconv.ParseInt(f[i+1], 10, 64)
				switch f[i] {
				case "anon":
					x.anon = n
				case "file":
					x.file = n
				case "sock":
					x.sock = n
				case "kernel":
					x.kernel = n
				}
			}
			s.mu.Lock()
			s.last = x
			if x.current > s.highest.current {
				s.highest = x
			}
			s.mu.Unlock()
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for s.latest().current == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no memory sample from the Drive's cgroup within 20 seconds")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s
}

func (s *sampler) latest() sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// mark returns the highest sample since the last mark, and starts again.
func (s *sampler) mark() sample {
	time.Sleep(400 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.highest
	s.highest = sample{}
	return h
}

// cgroup reads files of the container's cgroup.
func cgroup(t *testing.T, id string) map[string]string {
	out := run(t, "docker", "run", "--rm", "--privileged", "-v", "/sys/fs/cgroup:/cg:ro", "alpine:3", "sh", "-c",
		fmt.Sprintf(`d=/cg/docker/%s; [ -d $d ] || d=$(find /cg -type d -name '*%s*' | head -1); echo "$(cat $d/memory.peak)"; echo ---; cat $d/memory.events`, id, id))
	peak, events, _ := strings.Cut(out, "---")
	return map[string]string{"memory.peak": peak, "memory.events": events}
}

// A pattern is a file's content: size bytes made from seed, any part of which can be made again from where it starts.
type pattern struct {
	seed uint64
	size int64
}

const block = 1 << 20

func (p pattern) block(i int64, buf []byte) {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:8], p.seed)
	binary.LittleEndian.PutUint64(key[8:16], uint64(i))
	_, _ = mrand.NewChaCha8(key).Read(buf)
}

// from reads the pattern from offset to its end.
func (p pattern) from(offset int64) io.Reader { return &patternReader{p: p, at: offset, current: -1} }

var sums sync.Map

func (p pattern) sum() string {
	if s, ok := sums.Load(p); ok {
		return s.(string)
	}
	h := sha256.New()
	_, _ = io.Copy(h, p.from(0))
	s := hex.EncodeToString(h.Sum(nil))
	sums.Store(p, s)
	return s
}

type patternReader struct {
	p       pattern
	at      int64
	current int64
	buf     []byte
}

func (r *patternReader) Read(b []byte) (int, error) {
	if r.at >= r.p.size {
		return 0, io.EOF
	}
	i := r.at / block
	if i != r.current {
		if r.buf == nil {
			r.buf = make([]byte, block)
		}
		r.p.block(i, r.buf)
		r.current = i
	}
	off := r.at - i*block
	end := min(int64(block), r.p.size-i*block)
	n := copy(b, r.buf[off:end])
	r.at += int64(n)
	return n, nil
}

// client is a member's browser on the Drive's page, through the reverse proxy.
type client struct {
	base, secret string
	http         *http.Client
}

func (c *client) request(method, path string, body io.Reader, length int64) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = length
	}
	req.Header.Set("Drive-Secret", c.secret)
	req.Header.Set("Remote-User-Id", memberID)
	if method != http.MethodGet {
		req.Header.Set("Origin", driveAddress)
	}
	return c.http.Do(req)
}

func (c *client) json(method, path string, body io.Reader, length int64, v any) (int, error) {
	res, err := c.request(method, path, body, length)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if v != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(data, v); err != nil {
			return res.StatusCode, fmt.Errorf("%w: %s", err, data)
		}
	}
	if res.StatusCode >= 300 && v != nil {
		_ = json.Unmarshal(data, v)
	}
	return res.StatusCode, nil
}

func (c *client) waitUntilUp(t *testing.T) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, err := c.json(http.MethodGet, "/api/me", nil, 0, nil)
		if err == nil && status == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Drive did not answer within 30 seconds: %d %v", status, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (c *client) myFiles(t *testing.T) int64 {
	var got struct {
		Places []struct {
			ID   int64
			Kind string
		}
	}
	if status, err := c.json(http.MethodGet, "/api/places", nil, 0, &got); err != nil || status != http.StatusOK {
		t.Fatalf("places: %d %v", status, err)
	}
	return got.Places[0].ID
}

func (c *client) newFolder(t *testing.T, place int64, name string) int64 {
	body, _ := json.Marshal(map[string]string{"name": name})
	var it struct{ ID int64 }
	status, err := c.json(http.MethodPost, fmt.Sprintf("/api/places/%d/folders", place), bytes.NewReader(body), int64(len(body)), &it)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("making %s: %d %v", name, status, err)
	}
	return it.ID
}

func (c *client) uploadResumable(t *testing.T, place, folder int64, name string, p pattern, cutOff bool) int64 {
	id, err := c.tryUploadResumable(place, folder, name, p, cutOff)
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return id
}

// tryUploadResumable uploads p as the interface does a large file: started, sent in parts, each sent again when the
// Drive says it is not its turn, and finished. With cutOff, the second part is cut off a third of the way and the
// upload goes on from where the Drive says it stands.
func (c *client) tryUploadResumable(place, folder int64, name string, p pattern, cutOff bool) (int64, error) {
	q := url.Values{"name": {name}, "size": {strconv.FormatInt(p.size, 10)}}
	var u struct {
		ID        string
		Offset    int64
		Resumable bool
		PartSize  int64
	}
	status, err := c.json(http.MethodPost, fmt.Sprintf("/api/places/%d/items/%d/uploads?%s", place, folder, q.Encode()), nil, 0, &u)
	if err != nil || status != http.StatusCreated {
		return 0, fmt.Errorf("starting: %d %v", status, err)
	}
	if !u.Resumable {
		return 0, fmt.Errorf("the stand-in storage can resume, but the Drive says it cannot")
	}
	session := fmt.Sprintf("/api/places/%d/uploads/%s", place, u.ID)
	offset, parts := int64(0), 0
	for offset < p.size {
		n := min(u.PartSize, p.size-offset)
		parts++
		if cutOff && parts == 2 {
			if err := c.cutOffPart(session, p, offset, n, n/3); err != nil {
				return 0, err
			}
			var state struct{ Offset int64 }
			time.Sleep(500 * time.Millisecond)
			if status, err := c.json(http.MethodGet, session, nil, 0, &state); err != nil || status != http.StatusOK {
				return 0, fmt.Errorf("where it stands after a cut-off: %d %v", status, err)
			}
			if state.Offset <= offset {
				return 0, fmt.Errorf("after a part was cut off a third of the way, the upload stands at %d, not past %d", state.Offset, offset)
			}
			offset = state.Offset
			continue
		}
		var got struct {
			Offset int64
			Busy   bool
		}
		status, err := c.json(http.MethodPut, fmt.Sprintf("%s?offset=%d", session, offset), io.LimitReader(p.from(offset), n), n, &got)
		switch {
		case err != nil:
			return 0, err
		case status == http.StatusServiceUnavailable && got.Busy:
			time.Sleep(time.Second) // not its turn: send it again
			parts--
			continue
		case status != http.StatusOK || got.Offset != offset+n:
			return 0, fmt.Errorf("a part from %d: %d, now at %d", offset, status, got.Offset)
		}
		offset = got.Offset
	}
	var it struct{ ID int64 }
	if status, err := c.json(http.MethodPost, session+"/finish", nil, 0, &it); err != nil || status != http.StatusCreated {
		return 0, fmt.Errorf("finishing: %d %v", status, err)
	}
	return it.ID, nil
}

// cutOffPart sends the first sent bytes of a part of p of n bytes from offset, and hangs up, as a browser does when
// its connection drops.
func (c *client) cutOffPart(session string, p pattern, offset, n, sent int64) error {
	address, _ := url.Parse(c.base)
	conn, err := net.Dial("tcp", address.Host)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "PUT %s?offset=%d HTTP/1.1\r\nHost: %s\r\nDrive-Secret: %s\r\nRemote-User-Id: %s\r\nOrigin: %s\r\nContent-Length: %d\r\n\r\n",
		session, offset, address.Host, c.secret, memberID, driveAddress, n)
	if _, err := io.Copy(conn, io.LimitReader(p.from(offset), sent)); err != nil {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}

// download reads a file, at rate bytes a second (0: as fast as it comes), and returns what it read and its sum.
func (c *client) download(place, item int64, rate int) (string, int64, error) {
	res, err := c.request(http.MethodGet, fmt.Sprintf("/api/places/%d/items/%d/download", place, item), nil, 0)
	if err != nil {
		return "", 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("got %d", res.StatusCode)
	}
	var body io.Reader = res.Body
	if rate > 0 {
		body = &throttled{r: res.Body, rate: rate, start: time.Now()}
	}
	h := sha256.New()
	n, err := io.Copy(h, body)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

type throttled struct {
	r     io.Reader
	rate  int
	start time.Time
	read  int64
}

func (t *throttled) Read(b []byte) (int, error) {
	n, err := t.r.Read(b[:min(len(b), 64<<10)])
	t.read += int64(n)
	if due := t.start.Add(time.Duration(float64(t.read) / float64(t.rate) * float64(time.Second))); time.Until(due) > 0 {
		time.Sleep(time.Until(due))
	}
	return n, err
}

// checkZip downloads a folder as a zip, into a file, and checks the one entry named name holds p.
func (c *client) checkZip(place, folder int64, name string, p pattern) error {
	res, err := c.request(http.MethodGet, fmt.Sprintf("/api/places/%d/items/%d/zip", place, folder), nil, 0)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("got %d", res.StatusCode)
	}
	f, err := os.CreateTemp("", "superit-drive-memory-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		return err
	}
	zr, err := zip.OpenReader(f.Name())
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, e := range zr.File {
		if e.Name != name {
			continue
		}
		r, err := e.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != p.sum() {
			return fmt.Errorf("%s in the zip differs", name)
		}
		return nil
	}
	return fmt.Errorf("the zip holds no %s", name)
}

// webdav reads the stand-in storage directly, with the storage login.
type webdav struct{ root string }

func (w *webdav) sum(t *testing.T, p string) string {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		segs = append(segs, url.PathEscape(s))
	}
	req, _ := http.NewRequest(http.MethodGet, w.root+"/"+strings.Join(segs, "/"), nil)
	req.SetBasicAuth(storageUser, storagePass)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s on the storage: %d", p, res.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(h, res.Body); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		var stderr []byte
		if e, ok := err.(*exec.ExitError); ok {
			stderr = e.Stderr
		}
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, stderr)
	}
	return strings.TrimSpace(string(out))
}

func port(t *testing.T, id, inside string) string {
	return strings.Split(run(t, "docker", "port", id, inside+"/tcp"), "\n")[0]
}

func repoRoot(t *testing.T) string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(here), "..", "..")
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
