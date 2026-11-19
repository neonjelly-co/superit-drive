// Package harness runs the Drive the way it runs for real, for tests that act only as a browser or an outsider would.
// It builds the Drive binary from this repository and starts it, with nothing but environment variables, against a
// throwaway Apache mod_dav container (the stand-in Drive storage), a temporary data folder and a member list file.
//
// Tests talk to the Drive over HTTP with the headers the reverse proxy would add (Drive.As), and look at the storage
// over WebDAV with the storage login (Drive.Storage). They never look inside the Drive.
//
//	func TestMain(m *testing.M) { os.Exit(harness.Run(m)) }
//
//	func TestSomething(t *testing.T) {
//		t.Parallel()
//		reka := harness.Member{UserID: "u-reka", Email: "reka@example.com", DisplayName: "Réka Kovács", Role: "member"}
//		d := harness.Start(t, reka)
//		d.Storage.Put(t, "My files/reka@example.com/notes.txt", "hello")
//		res := d.As(reka).Get(t, "/api/places")
//		…
//	}
//
// One storage container serves a whole test package. Each Drive gets its own folder on it, so tests can run in
// parallel. Docker must be running; `go test -short` skips these tests.
package harness

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// shared is set up once per test package, by the first test that starts a Drive.
var shared struct {
	once    sync.Once
	err     error
	dir     string
	binary  string
	storage *container
}

// Run runs the tests and cleans up after them. Call it from TestMain.
func Run(m *testing.M) int {
	flag.Parse()
	code := m.Run()
	if shared.storage != nil {
		shared.storage.stop()
	}
	if shared.dir != "" {
		os.RemoveAll(shared.dir)
	}
	return code
}

// setup builds the Drive and starts the stand-in storage. It runs inside the first test (not in TestMain), because
// only what a test does there is seen by `go test`'s cache: see consultSources.
func setup() error {
	if err := consultSources(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "superit-drive-test-")
	if err != nil {
		return err
	}
	shared.dir = dir
	shared.binary = filepath.Join(dir, "superit-drive")
	build := exec.Command("go", "build", "-o", shared.binary, "github.com/neonjelly-co/superit-drive/cmd/superit-drive")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building the Drive failed: %v\n%s", err, out)
	}
	shared.storage, err = startStorage()
	if err != nil {
		return fmt.Errorf("starting the stand-in storage failed (is Docker running?): %w", err)
	}
	return nil
}

// consultSources looks at every file and folder of the repository. `go test` reuses a cached result only while the
// files a test looked at are unchanged, and it cannot see what `go build` reads. Without this, a change to the Drive
// would leave these tests reporting their old, cached result.
func consultSources() error {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(here), "..", "..")
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		_, err = os.Stat(path)
		return err
	})
}

// A Member is one entry of the member list file, in the file's own format.
type Member struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"` // "admin" or "member"
}

// Options changes how a Drive is started.
type Options struct {
	Members []Member
	// Env sets or replaces environment variables of the Drive. An empty value leaves the variable out.
	Env map[string]string
}

// Drive is one running Drive.
type Drive struct {
	// URL is the Drive's address, such as http://127.0.0.1:54321.
	URL string
	// DataDir is the Drive's data folder.
	DataDir string
	// Storage is the Drive's folder on the stand-in storage, reached directly with the storage login.
	Storage *Storage
	// Secret is the header secret the Drive was given.
	Secret string
	// Address is the Drive's own address as people reach it (DRIVE_ADDRESS). A browser on the Drive's page sends it
	// as Origin.
	Address string

	membersFile string
	cmd         *exec.Cmd
	logs        *logBuffer
	exited      chan struct{}
}

// Start starts a Drive whose member list holds members. It stops when the test ends.
func Start(t *testing.T, members ...Member) *Drive {
	t.Helper()
	return StartWith(t, Options{Members: members})
}

// StartWith starts a Drive with options. It stops when the test ends.
func StartWith(t *testing.T, o Options) *Drive {
	t.Helper()
	d := launch(t, o)
	deadline := time.After(15 * time.Second)
	for d.URL == "" {
		select {
		case <-d.exited:
			t.Fatalf("the Drive stopped while starting:\n%s", d.Logs())
		case <-deadline:
			t.Fatalf("the Drive did not start within 15 seconds:\n%s", d.Logs())
		case <-time.After(20 * time.Millisecond):
			if m := listening.FindStringSubmatch(d.Logs()); m != nil {
				d.URL = "http://" + m[1]
			}
		}
	}
	return d
}

// StartFailing starts a Drive that is expected to refuse to start, and returns what it printed.
func StartFailing(t *testing.T, o Options) string {
	t.Helper()
	d := launch(t, o)
	select {
	case <-d.exited:
	case <-time.After(15 * time.Second):
		t.Fatalf("the Drive was expected to stop, but it is still running:\n%s", d.Logs())
	}
	if d.cmd.ProcessState.Success() {
		t.Fatalf("the Drive stopped without an error:\n%s", d.Logs())
	}
	return d.Logs()
}

var listening = regexp.MustCompile(`msg=listening address=(\S+)`)

// ready builds the Drive and starts the stand-in storage, once per test package.
func ready(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("needs Docker; skipped with -short")
	}
	shared.once.Do(func() { shared.err = setup() })
	if shared.err != nil {
		t.Fatal("harness:", shared.err)
	}
}

// StorageAt is the folder at folder on the stand-in storage (such as "/drive-1a2b"): the storage settings that point
// a Drive or the storage check at it, and the folder itself, reached with the storage login. Nothing is made.
func StorageAt(t *testing.T, folder string) (map[string]string, *Storage) {
	t.Helper()
	ready(t)
	env := map[string]string{
		"DRIVE_STORAGE_URL":      shared.storage.url,
		"DRIVE_STORAGE_USER":     storageUser,
		"DRIVE_STORAGE_PASSWORD": storagePassword,
		"DRIVE_STORAGE_FOLDER":   folder,
	}
	return env, &Storage{root: shared.storage.url + strings.TrimSuffix(folder, "/"), user: storageUser, password: storagePassword}
}

// Unique is a folder name no other test uses, such as "check-1a2b3c4d5e6f".
func Unique(prefix string) string { return prefix + "-" + randomHex(6) }

// Command runs the Drive's binary with args and only the environment variables in env, as a container would, and
// returns what it printed (standard output, then standard error) and its exit code.
func Command(t *testing.T, env map[string]string, args ...string) (output string, exitCode int) {
	t.Helper()
	ready(t)
	cmd := exec.Command(shared.binary, args...)
	cmd.Env = []string{}
	for k, v := range env {
		if v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("superit-drive %s did not end within 3 minutes:\n%s%s", strings.Join(args, " "), &stdout, &stderr)
	}
	return stdout.String() + stderr.String(), cmd.ProcessState.ExitCode()
}

func launch(t *testing.T, o Options) *Drive {
	t.Helper()
	ready(t)
	dir := t.TempDir()
	folder := "drive-" + randomHex(6)
	d := &Drive{
		DataDir:     filepath.Join(dir, "data"),
		Secret:      randomHex(32),
		membersFile: filepath.Join(dir, "members", "members.json"),
		Storage:     &Storage{root: shared.storage.url + "/" + folder, user: storageUser, password: storagePassword},
		logs:        &logBuffer{},
		exited:      make(chan struct{}),
	}
	if err := os.MkdirAll(filepath.Dir(d.membersFile), 0o755); err != nil {
		t.Fatal(err)
	}
	d.SetMembers(t, o.Members...)

	env := map[string]string{
		"DRIVE_STORAGE_URL":      shared.storage.url,
		"DRIVE_STORAGE_USER":     storageUser,
		"DRIVE_STORAGE_PASSWORD": storagePassword,
		"DRIVE_STORAGE_FOLDER":   "/" + folder,
		"DRIVE_ADDRESS":          "http://drive.test",
		"DRIVE_PROJECT_NAME":     "Kovács Accounting",
		"DRIVE_HEADER_SECRET":    d.Secret,
		"DRIVE_MEMBERS_FILE":     d.membersFile,
		"DRIVE_DATA_DIR":         d.DataDir,
		"DRIVE_LISTEN":           "127.0.0.1:0",
	}
	for k, v := range o.Env {
		env[k] = v
	}
	d.Address = env["DRIVE_ADDRESS"]
	d.cmd = exec.Command(shared.binary)
	d.cmd.Env = []string{} // only what is set here: the Drive must start from its settings alone
	for k, v := range env {
		if v != "" {
			d.cmd.Env = append(d.cmd.Env, k+"="+v)
		}
	}
	d.cmd.Stdout, d.cmd.Stderr = d.logs, d.logs
	if err := d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = d.cmd.Wait(); close(d.exited) }()
	t.Cleanup(func() {
		d.stop()
		if t.Failed() {
			t.Logf("the Drive's log:\n%s", d.Logs())
		}
	})
	return d
}

func (d *Drive) stop() {
	select {
	case <-d.exited:
		return
	default:
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.exited:
	case <-time.After(10 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.exited
	}
}

// Logs is everything the Drive printed so far.
func (d *Drive) Logs() string { return d.logs.String() }

// SetMembers replaces the member list file the way the daemon does: write a new file, then rename it over the old one.
func (d *Drive) SetMembers(t *testing.T, members ...Member) {
	t.Helper()
	if members == nil {
		members = []Member{}
	}
	data, err := json.MarshalIndent(map[string]any{"members": members}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tmp := d.membersFile + "." + randomHex(4) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, d.membersFile); err != nil {
		t.Fatal(err)
	}
}

// As is a member's browser on the Drive's own page, reached through the reverse proxy: every request carries the
// headers the proxy adds for a signed-in member (the secret and their user id, and the gateway's other identity
// headers, as the real one sends), and every request that is not a GET or HEAD also carries the Drive's address as
// Origin, as a browser adds it.
func (d *Drive) As(m Member) *Client {
	c := d.WithHeaders(http.Header{
		"Drive-Secret":   {d.Secret},
		"Remote-User-Id": {m.UserID},
		"Remote-Email":   {m.Email},
		"Remote-Name":    {m.DisplayName},
		"Remote-Role":    {m.Role},
	})
	c.origin = d.Address
	return c
}

// WithHeaders is a client that sends exactly these headers, as anyone reaching the Drive could.
func (d *Drive) WithHeaders(h http.Header) *Client {
	return &Client{base: d.URL, headers: h}
}

// Client makes requests to a Drive.
type Client struct {
	base    string
	headers http.Header
	origin  string // sent as Origin on requests that are not GET or HEAD; "" for none
}

// From is the same client on a page at origin instead: a browser that another site's page made send the requests.
// An empty origin sends none.
func (c *Client) From(origin string) *Client {
	return &Client{base: c.base, headers: c.headers, origin: origin}
}

// Response is what the Drive answered.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into v.
func (r Response) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("the answer is not JSON (%v): %s", err, r.Body)
	}
}

// Get requests path.
func (c *Client) Get(t *testing.T, path string) Response {
	t.Helper()
	return c.Do(t, http.MethodGet, path, nil)
}

// Do makes a request.
func (c *Client) Do(t *testing.T, method, path string, body io.Reader) Response {
	t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range c.headers {
		req.Header[k] = v
	}
	if c.origin != "" && method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", c.origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: res.StatusCode, Header: res.Header, Body: data}
}

// Eventually waits up to five seconds for ok to hold.
func Eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// logBuffer collects the Drive's output; it is written by the process and read by the test at the same time.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Clone(l.buf.String())
}
