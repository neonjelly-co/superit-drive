//go:build memory

package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A copy of a large index, made while people use the Drive, in the same 64 MiB container: the storage holds
// copyFolders folders of copyPerFolder files each (more than 100,000 items once the walk has taken them in); then,
// while downloads run (half of them read slowly) and people keep opening a folder of 10,000 files, a share makes the
// Drive copy its index into its data folder and onto the storage. The copy streams from disk to disk and from disk to
// the storage, so its memory must not grow with the index.
const (
	copyFolders    = 11
	copyPerFolder  = 10_000 // the most one look reads, so the walk takes every folder in
	copyItems      = copyFolders * copyPerFolder
	copyFileSize   = 64 << 20
	copyDownloads  = 16 // at once, over and over, until the copy is made
	copyListers    = 4  // people opening a folder of 10,000 files, over and over
	copyMemberName = "load@example.com"
)

func TestACopyOfALargeIndexStaysWithin64MiB(t *testing.T) {
	root := repoRoot(t)
	run(t, "docker", "build", "-q", "-t", driveImage, root)
	run(t, "docker", "build", "-q", "-t", storageImage, filepath.Join(root, "test", "webdav"))

	network := "superit-drive-copy-" + randomHex(4)
	run(t, "docker", "network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })
	storageID := run(t, "docker", "run", "-d", "--rm", "--label", "superit-drive-test=1", "--network", network,
		"--network-alias", "storage", "-p", "127.0.0.1::80", storageImage)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", storageID).Run() })
	storage := &webdav{root: "http://" + port(t, storageID, "80") + storageFolder}

	members := t.TempDir()
	list, _ := json.Marshal(map[string]any{"members": []map[string]string{
		{"userId": memberID, "email": copyMemberName, "displayName": "Load", "role": "member"}}})
	if err := os.WriteFile(filepath.Join(members, "members.json"), list, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(members, 0o755)

	// The files, straight onto the storage's disk, before the Drive starts: long names with accented letters.
	top := "/var/storage/files" + storageFolder + "/My files/" + copyMemberName
	script := `set -e
f=0; while [ $f -lt ` + strconv.Itoa(copyFolders) + ` ]; do
  mkdir -p "$1/Ügyfél $f"; cd "$1/Ügyfél $f"
  i=0; while [ $i -lt ` + strconv.Itoa(copyPerFolder) + ` ]; do : > "Számla $f-$i – Árvíztűrő tükörfúrógép, végleges változat.pdf"; i=$((i+1)); done
  f=$((f+1))
done
chown -R www-data:www-data /var/storage/files`
	began := time.Now()
	run(t, "docker", "exec", storageID, "sh", "-c", script, "fill", top)
	t.Logf("%d files put on the storage in %s", copyItems, time.Since(began).Round(time.Second))

	secret := randomHex(32)
	driveID := run(t, "docker", "run", "-d", "--label", "superit-drive-test=1", "--network", network,
		"--memory="+limit, "--memory-swap="+limit, "-p", "127.0.0.1::8080", "-v", members+":/members:ro",
		"-e", "DRIVE_STORAGE_URL=http://storage", "-e", "DRIVE_STORAGE_USER="+storageUser,
		"-e", "DRIVE_STORAGE_PASSWORD="+storagePass, "-e", "DRIVE_STORAGE_FOLDER="+storageFolder,
		"-e", "DRIVE_ADDRESS="+driveAddress, "-e", "DRIVE_PROJECT_NAME=Copy test",
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
	sampler := startSampler(t, driveID)
	place := drive.myFiles(t)
	time.Sleep(2 * time.Second)
	t.Logf("idle: %s", sampler.latest())

	// The released Drive walks a minute after it starts, and takes every file in.
	ended := regexp.MustCompile(`msg="the walk through the storage ended".*`)
	var line string
	for deadline := time.Now().Add(15 * time.Minute); line == ""; time.Sleep(2 * time.Second) {
		if time.Now().After(deadline) {
			t.Fatal("the walk did not end within 15 minutes")
		}
		logs, _ := exec.Command("docker", "logs", driveID).CombinedOutput()
		line = ended.FindString(string(logs))
	}
	walking := sampler.mark()
	if want := fmt.Sprintf("added=%d", copyItems+copyFolders); !strings.Contains(line, want) {
		t.Fatalf("the walk's log line does not say %s: %s", want, line)
	}
	t.Logf("the walk: %s", line)

	// A file to download, and the folder people open.
	seed := pattern{seed: 7, size: copyFileSize}
	big := drive.newFolder(t, place, "Big")
	seedItem := drive.uploadResumable(t, place, big, "Seed.bin", seed, false)
	var topList struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if status, err := drive.json(http.MethodGet, fmt.Sprintf("/api/places/%d/items", place), nil, 0, &topList); err != nil || status != http.StatusOK {
		t.Fatalf("listing My files: %d %v", status, err)
	}
	var first int64
	for _, it := range topList.Items {
		if it.Name == "Ügyfél 0" {
			first = it.ID
		}
	}
	if first == 0 {
		t.Fatalf("My files does not list Ügyfél 0")
	}
	before := sampler.mark()

	// People use the Drive; a share makes the Drive copy its index (half a minute after the last access change).
	copied := regexp.MustCompile(`msg="the index was copied into the data folder and onto the storage" why="access changed".*`)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var downloaded, listed atomic.Int64
	var failures sync.Map
	for i := range copyDownloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rate := 0
			if i%2 == 0 {
				rate = slowRate
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				sum, n, err := drive.download(place, seedItem, rate)
				if err == nil && (n != copyFileSize || sum != seed.sum()) {
					err = fmt.Errorf("got %d bytes that differ from Seed.bin", n)
				}
				if err != nil {
					failures.Store(fmt.Sprintf("download %d", i), err)
					return
				}
				downloaded.Add(1)
			}
		}()
	}
	for i := range copyListers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/api/places/%d/items/%d/items", place, first)
			for {
				select {
				case <-stop:
					return
				default:
				}
				var l struct {
					Items []json.RawMessage `json:"items"`
				}
				status, err := drive.json(http.MethodGet, path, nil, 0, &l)
				if err == nil && (status != http.StatusOK || len(l.Items) != copyPerFolder) {
					err = fmt.Errorf("got %d with %d items", status, len(l.Items))
				}
				if err != nil {
					failures.Store(fmt.Sprintf("lister %d", i), err)
					return
				}
				listed.Add(1)
			}
		}()
	}
	time.Sleep(5 * time.Second)
	logs, _ := exec.Command("docker", "logs", driveID).CombinedOutput()
	copiesBefore := len(copied.FindAllString(string(logs), -1))
	share, _ := json.Marshal(map[string]string{"role": "viewer"})
	if status, err := drive.json(http.MethodPut, fmt.Sprintf("/api/places/%d/items/%d/access/everyone", place, first),
		bytes.NewReader(share), int64(len(share)), nil); err != nil || status != http.StatusOK {
		t.Fatalf("sharing Ügyfél 0 with everyone: %d %v", status, err)
	}
	began = time.Now()
	line = ""
	for deadline := time.Now().Add(5 * time.Minute); line == ""; time.Sleep(time.Second) {
		if time.Now().After(deadline) {
			t.Fatal("the index was not copied within five minutes of a share")
		}
		logs, _ := exec.Command("docker", "logs", driveID).CombinedOutput()
		if all := copied.FindAllString(string(logs), -1); len(all) > copiesBefore {
			line = all[len(all)-1]
		}
	}
	time.Sleep(3 * time.Second) // the load goes on a little after the copy
	close(stop)
	wg.Wait()
	copying := sampler.mark()
	failures.Range(func(k, v any) bool {
		t.Errorf("%s: %v", k, v)
		return true
	})
	t.Logf("the copy, %s after the share: %s", time.Since(began).Round(time.Second), line)
	t.Logf("meanwhile: %d downloads of %d MiB and %d lists of %d items", downloaded.Load(), copyFileSize>>20, listed.Load(), copyPerFolder)

	// The copy on the storage is whole, and its size is the one the log gives.
	m := regexp.MustCompile(`bytes=(\d+) storage=(\S+)`).FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the copy's log line does not say its size and where it is: %s", line)
	}
	if got := storage.size(t, m[2]); got <= 0 {
		t.Errorf("the copy is not on the storage at %s", m[2])
	} else {
		t.Logf("the copy: %s bytes in the data folder, %d bytes on the storage", m[1], got)
	}
	if n, _ := strconv.ParseInt(m[1], 10, 64); n < copyItems*100 {
		t.Errorf("a copy of %s bytes cannot hold %d items", m[1], copyItems)
	}

	state := strings.Fields(run(t, "docker", "inspect", "-f", "{{.State.OOMKilled}} {{.State.Running}} {{.RestartCount}}", driveID))
	if state[0] != "false" || state[1] != "true" {
		t.Fatalf("the Drive's container: OOMKilled %s, running %s", state[0], state[1])
	}
	cg := cgroup(t, driveID)
	peak, _ := strconv.ParseInt(strings.TrimSpace(cg["memory.peak"]), 10, 64)
	t.Logf("the Drive's container (limit %s): not killed, still running, restarts %s", limit, state[2])
	t.Logf("peak (cgroup memory.peak, the whole run, the walk included): %s", mib(peak))
	t.Logf("highest during the walk of %d files:        %s", copyItems, walking)
	t.Logf("highest before the copy, uploading Seed.bin: %s", before)
	t.Logf("highest while people used the Drive and it copied its index: %s", copying)
	t.Logf("memory.events: %s", strings.Join(strings.Fields(cg["memory.events"]), " "))
}

// size is the length of the file at p on the storage, or -1 when it is not there.
func (w *webdav) size(t *testing.T, p string) int64 {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		segs = append(segs, url.PathEscape(s))
	}
	req, _ := http.NewRequest(http.MethodHead, w.root+"/"+strings.Join(segs, "/"), nil)
	req.SetBasicAuth(storageUser, storagePass)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return -1
	}
	return res.ContentLength
}
