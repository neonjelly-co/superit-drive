// Package test holds the Drive's tests through its HTTP interface. Each test starts the real Drive binary against a
// throwaway Apache mod_dav container (see package harness) and acts only as a browser or an outsider would: requests
// with the reverse proxy's headers for members, and WebDAV with the storage login to see what is on the storage.
// Nothing here looks inside the Drive.
package test

import (
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
	}
	itemJSON struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Kind     string  `json:"kind"`
		Size     *int64  `json:"size"`
		Changed  *string `json:"changed"`
		Replaced bool    `json:"replaced"` // uploads only
		// Listings only: what the person may do with it, and who can see it.
		Can    []string    `json:"can"`
		SeenBy *seenByJSON `json:"seenBy"`
	}
	seenByJSON struct {
		Everyone bool     `json:"everyone"`
		Members  []string `json:"members"`
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
