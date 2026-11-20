//go:build memory

package memory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The walk through a large storage, and looks at a large folder, in the same 64 MiB container: the storage holds
// walkFiles files the walk takes in (one folder of the most a look reads, the rest in folders of walkPerFolder), and one
// folder of more than a look reads, which is left as it is. The walk keeps one folder's entries at a time, so its
// memory must not grow with the storage.
const (
	walkLargest   = 10_000 // the most one look reads (drive.MaxFolderEntries)
	walkFolders   = 200
	walkPerFolder = 200
	walkFiles     = walkLargest + walkFolders*walkPerFolder // 50,000
	walkTooLarge  = 20_000
)

func TestTheWalkStaysWithin64MiB(t *testing.T) {
	root := repoRoot(t)
	run(t, "docker", "build", "-q", "-t", driveImage, root)
	run(t, "docker", "build", "-q", "-t", storageImage, filepath.Join(root, "test", "webdav"))

	network := "superit-drive-walk-" + randomHex(4)
	run(t, "docker", "network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })
	storageID := run(t, "docker", "run", "-d", "--rm", "--label", "superit-drive-test=1", "--network", network,
		"--network-alias", "storage", storageImage)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", storageID).Run() })

	members := t.TempDir()
	list, _ := json.Marshal(map[string]any{"members": []map[string]string{
		{"userId": memberID, "email": "load@example.com", "displayName": "Load", "role": "member"}}})
	if err := os.WriteFile(filepath.Join(members, "members.json"), list, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(members, 0o755)

	// The files, straight onto the storage's disk, before the Drive starts: long names with accented letters, as
	// people's are.
	top := "/var/storage/files" + storageFolder + "/My files/load@example.com"
	script := `set -e
mkdir -p "$1/Largest" "$1/Too large"
cd "$1/Largest"; i=0; while [ $i -lt ` + strconv.Itoa(walkLargest) + ` ]; do : > "Számla 2026 – $i – Árvíztűrő tükörfúrógép.pdf"; i=$((i+1)); done
cd "$1/Too large"; i=0; while [ $i -lt ` + strconv.Itoa(walkTooLarge) + ` ]; do : > "Fénykép $i.jpg"; i=$((i+1)); done
f=0; while [ $f -lt ` + strconv.Itoa(walkFolders) + ` ]; do
  mkdir -p "$1/Ügyfél $f/Iratok"; cd "$1/Ügyfél $f/Iratok"
  i=0; while [ $i -lt ` + strconv.Itoa(walkPerFolder) + ` ]; do : > "Szerződés $f-$i – végleges változat.docx"; i=$((i+1)); done
  f=$((f+1))
done
chown -R www-data:www-data /var/storage/files`
	began := time.Now()
	run(t, "docker", "exec", storageID, "sh", "-c", script, "fill", top)
	t.Logf("%d files the walk takes in, and %d in a folder too large, put on the storage in %s", walkFiles, walkTooLarge,
		time.Since(began).Round(time.Second))

	secret := randomHex(32)
	driveID := run(t, "docker", "run", "-d", "--label", "superit-drive-test=1", "--network", network,
		"--memory="+limit, "--memory-swap="+limit, "-p", "127.0.0.1::8080", "-v", members+":/members:ro",
		"-e", "DRIVE_STORAGE_URL=http://storage", "-e", "DRIVE_STORAGE_USER="+storageUser,
		"-e", "DRIVE_STORAGE_PASSWORD="+storagePass, "-e", "DRIVE_STORAGE_FOLDER="+storageFolder,
		"-e", "DRIVE_ADDRESS="+driveAddress, "-e", "DRIVE_PROJECT_NAME=Walk test",
		"-e", "DRIVE_HEADER_SECRET="+secret, "-e", "DRIVE_MEMBERS_FILE=/members/members.json", driveImage)
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "60", driveID).CombinedOutput()
			t.Logf("the Drive's log, last lines:\n%s", logs)
		}
		_ = exec.Command("docker", "rm", "-f", driveID).Run()
	})
	drive := &client{base: "http://" + port(t, driveID, "8080"), secret: secret, http: &http.Client{Timeout: 5 * time.Minute}}
	drive.waitUntilUp(t)
	sampler := startSampler(t, driveID)
	place := drive.myFiles(t) // the member's My files: its folder on the storage holds everything above
	time.Sleep(2 * time.Second)
	t.Logf("idle: %s", sampler.latest())

	// The released Drive walks a minute after it starts, resting half a second after each folder.
	began = time.Now()
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
	t.Logf("the walk, from the Drive's start: %s", time.Since(began).Round(time.Second))
	t.Logf("its log line: %s", line)
	wantAdded := fmt.Sprintf("added=%d", walkFiles+2*walkFolders+2)
	if !strings.Contains(line, wantAdded) || !strings.Contains(line, "tooLarge=1") {
		t.Errorf("the walk's log line does not say %s and tooLarge=1", wantAdded)
	}

	// Eight people open the largest folder at once: a look at 10,000 entries, and a list of 10,000 items for each.
	var topList struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if status, err := drive.json(http.MethodGet, fmt.Sprintf("/api/places/%d/items", place), nil, 0, &topList); err != nil || status != http.StatusOK {
		t.Fatalf("listing My files: %d %v", status, err)
	}
	var largest int64
	for _, it := range topList.Items {
		if it.Name == "Largest" {
			largest = it.ID
		}
	}
	if largest == 0 {
		t.Fatalf("My files does not list Largest: %+v", topList.Items)
	}
	began = time.Now()
	var wg sync.WaitGroup
	counts := make([]int, 8)
	for i := range counts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var l struct {
				Items []json.RawMessage `json:"items"`
			}
			path := fmt.Sprintf("/api/places/%d/items/%d/items", place, largest)
			drive.json(http.MethodGet, path, nil, 0, &l)
			drive.json(http.MethodGet, path+"?wait", nil, 0, &l)
			counts[i] = len(l.Items)
		}()
	}
	wg.Wait()
	opening := sampler.mark()
	t.Logf("%d people opening the folder of %d files at once: %s, each listed %v items", len(counts), walkLargest,
		time.Since(began).Round(time.Millisecond), counts)
	for _, n := range counts {
		if n != walkLargest {
			t.Errorf("a list of Largest held %d items, want %d", n, walkLargest)
		}
	}

	state := strings.Fields(run(t, "docker", "inspect", "-f", "{{.State.OOMKilled}} {{.State.Running}} {{.RestartCount}}", driveID))
	if state[0] != "false" || state[1] != "true" {
		t.Fatalf("the Drive's container: OOMKilled %s, running %s", state[0], state[1])
	}
	cg := cgroup(t, driveID)
	peak, _ := strconv.ParseInt(strings.TrimSpace(cg["memory.peak"]), 10, 64)
	t.Logf("the Drive's container (limit %s): not killed, still running, restarts %s", limit, state[2])
	t.Logf("peak (cgroup memory.peak, the whole run): %s", mib(peak))
	t.Logf("highest during the walk:                  %s", walking)
	t.Logf("highest during 8 lists of 10,000 items:   %s", opening)
	t.Logf("memory.events: %s", strings.Join(strings.Fields(cg["memory.events"]), " "))
}
