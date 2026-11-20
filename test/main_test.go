// Package test holds the Drive's tests through its HTTP interface. Each test starts the real Drive binary against a
// throwaway Apache mod_dav container (see package harness) and acts only as a browser or an outsider would: requests
// with the reverse proxy's headers for members, and WebDAV with the storage login to see what is on the storage.
// Nothing here looks inside the Drive.
package test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

func TestMain(m *testing.M) { os.Exit(harness.Run(m)) }

// The people in these tests. Réka holds the admin role on the project; Bence and Dóra do not.
var (
	reka  = harness.Member{UserID: "6f1c0e2a-reka", Email: "reka@example.com", DisplayName: "Réka Kovács", Role: "admin"}
	bence = harness.Member{UserID: "a93b77d1-bence", Email: "bence@example.com", DisplayName: "Bence Tóth", Role: "member"}
	dora  = harness.Member{UserID: "c4d2e9f0-dora", Email: "dora@example.com", DisplayName: "Dóra Nagy", Role: "member"}
)

// notHere is the one answer for a place or an item that is not there, or that the person has no access to.
const notHere = "This is not in the Drive, or you have no access to it."

// nextSecond waits until the storage's own clock, and this computer's, are past the second they were in. The storage
// keeps the time a file or folder last changed to the second, and the Drive takes an item that went from the storage and
// came back exactly as it was (the same name, kind, size and time) for the same item: something made anew a second
// later, as it would be outside a test, is not. The storage's clock (in its container) can differ from this computer's.
func nextSecond(t *testing.T, d *harness.Drive) {
	t.Helper()
	here := time.Now().Truncate(time.Second)
	var first time.Time
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		probe := fmt.Sprintf("clock probe %d", time.Now().UnixNano())
		d.Storage.Put(t, probe, "")
		var at time.Time
		entries, _ := d.Storage.List(t, "")
		for _, e := range entries {
			if e.Name == probe {
				at = e.Modified
			}
		}
		d.Storage.Delete(t, probe, false)
		if first.IsZero() {
			first = at
		}
		if at.After(first) && at.After(here) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the storage's clock did not move past %s", first)
		}
	}
}

// What the API answers.
type (
	meJSON struct {
		UserID      string `json:"userId"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
		Project     string `json:"project"`
	}
	placesJSON struct {
		Places []placeJSON `json:"places"`
	}
	placeJSON struct {
		ID   int64    `json:"id"`
		Kind string   `json:"kind"` // "my-files" or "shared-folder"
		Name string   `json:"name"`
		Role string   `json:"role"`
		Can  []string `json:"can"`
	}
	// accessJSON is one line of "who can see this", for an item or a shared folder as a whole.
	accessJSON struct {
		Everyone    bool        `json:"everyone"`
		Link        bool        `json:"link"` // anyone with a public link on it, or on a folder above it
		UserID      string      `json:"userId"`
		Email       string      `json:"email"`
		DisplayName string      `json:"displayName"`
		Role        string      `json:"role"`
		Owner       bool        `json:"owner"`
		Admin       bool        `json:"admin"`
		Given       bool        `json:"given"`
		Inherited   bool        `json:"inherited"`
		From        *folderJSON `json:"from"`
		Former      bool        `json:"former"`
	}
	deletedPlaceJSON struct {
		ID      int64     `json:"id"`
		Kind    string    `json:"kind"`
		Name    string    `json:"name"`
		Size    int64     `json:"size"`
		Deleted time.Time `json:"deleted"`
		Removed time.Time `json:"removed"`
	}
	itemsJSON struct {
		Items []itemJSON `json:"items"`
		// Path is the folders the person can open, from the highest of them down to the one listed; empty for the top
		// folder. FromTop says whether it starts at the place's top folder.
		Path    []folderJSON `json:"path"`
		FromTop bool         `json:"fromTop"`
		Role    string       `json:"role"`
		Can     []string     `json:"can"`
		// Checking says the Drive is looking at the folder on the storage in the background: ask again with ?wait.
		Checking bool `json:"checking"`
	}
	itemJSON struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Kind     string  `json:"kind"`
		Size     *int64  `json:"size"`
		Changed  *string `json:"changed"`
		Replaced bool    `json:"replaced"` // uploads only
		Opens    bool    `json:"opens"`    // files that open in the browser
		// Listings only: what the person may do with it, and who can see it.
		Can    []string    `json:"can"`
		SeenBy *seenByJSON `json:"seenBy"`
	}
	seenByJSON struct {
		Everyone bool     `json:"everyone"`
		Members  []string `json:"members"`
		Link     bool     `json:"link"` // anyone with a public link on it, or on a folder above it
	}
	folderJSON struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	trashJSON struct {
		ID       int64        `json:"id"`
		Name     string       `json:"name"`
		Kind     string       `json:"kind"`
		Size     int64        `json:"size"`
		Deleted  time.Time    `json:"deleted"`
		Removed  time.Time    `json:"removed"`
		Replaced bool         `json:"replaced"`
		From     []folderJSON `json:"from"`
	}
	restoredJSON struct {
		Item itemJSON     `json:"item"`
		Path []folderJSON `json:"path"`
	}
	usageJSON struct {
		Used     *int64 `json:"used"`
		Capacity *int64 `json:"capacity"`
	}
	conflictJSON struct {
		Error  string `json:"error"`
		Exists string `json:"exists"`
		Name   string `json:"name"`
	}
)
