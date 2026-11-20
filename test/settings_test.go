package test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

func TestTheDriveDoesNotStartWithoutItsSettings(t *testing.T) {
	t.Parallel()
	required := []string{
		"DRIVE_STORAGE_URL", "DRIVE_STORAGE_USER", "DRIVE_STORAGE_PASSWORD", "DRIVE_ADDRESS",
		"DRIVE_PROJECT_NAME", "DRIVE_HEADER_SECRET", "DRIVE_MEMBERS_FILE", "DRIVE_DATA_DIR",
	}
	unset := map[string]string{}
	for _, name := range required {
		unset[name] = ""
	}
	out := harness.StartFailing(t, harness.Options{Members: []harness.Member{reka}, Env: unset})
	for _, name := range required {
		if !strings.Contains(out, name) {
			t.Errorf("the Drive did not say that %s is missing:\n%s", name, out)
		}
	}
}

func TestTheDriveDoesNotStartWithoutAMemberList(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nowhere.json")
	out := harness.StartFailing(t, harness.Options{Env: map[string]string{"DRIVE_MEMBERS_FILE": missing}})
	if !strings.Contains(out, "nowhere.json") {
		t.Errorf("the Drive did not name the missing member list:\n%s", out)
	}
}

// The storage login comes from the settings. With an index, a Drive whose login the storage refuses starts, and says so
// when it needs the storage; without one (a new install, or a new server), it does not start: it cannot find out whether
// the storage holds a copy of its index to make it from (the container is started again until it can).
func TestTheStorageLoginComesFromTheSettings(t *testing.T) {
	t.Parallel()
	out := harness.StartFailing(t, harness.Options{
		Members: []harness.Member{reka},
		Env:     map[string]string{"DRIVE_STORAGE_PASSWORD": "not-the-password"},
	})
	if !strings.Contains(out, "the storage could not be read") || !strings.Contains(out, "refused the Drive's login") {
		t.Fatalf("a new Drive with a wrong storage password did not say the storage refused its login:\n%s", out)
	}

	d := harness.Start(t, reka, bence)
	if res := d.As(reka).Get(t, "/api/places"); res.Status != http.StatusOK {
		t.Fatalf("places with the right password: %d %s", res.Status, res.Body)
	}
	d.Restart(t, map[string]string{"DRIVE_STORAGE_PASSWORD": "not-the-password"})
	res := d.As(bence).Get(t, "/api/places") // his first time: his My files is made on the storage
	if res.Status != http.StatusBadGateway || !strings.Contains(string(res.Body), "refused the Drive's login") {
		t.Fatalf("got %d %s, want 502 saying the storage refused the login", res.Status, res.Body)
	}
}

func TestThereIsNoSettingsPage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka) // Réka is a project admin
	for _, path := range []string{"/settings", "/admin", "/api/settings", "/api/admin/settings"} {
		if res := d.As(reka).Get(t, path); res.Status != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, res.Status)
		}
	}
}
