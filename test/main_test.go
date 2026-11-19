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

// The people in these tests. Réka holds the admin role on the project.
var (
	reka  = harness.Member{UserID: "6f1c0e2a-reka", Email: "reka@example.com", DisplayName: "Réka Kovács", Role: "admin"}
	bence = harness.Member{UserID: "a93b77d1-bence", Email: "bence@example.com", DisplayName: "Bence Tóth", Role: "member"}
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
		Places []struct {
			ID   int64  `json:"id"`
			Kind string `json:"kind"`
		} `json:"places"`
	}
	itemsJSON struct {
		Items []itemJSON `json:"items"`
		// Path is the folders from the top of the place down to the one listed; empty for the top folder.
		Path []folderJSON `json:"path"`
	}
	itemJSON struct {
		ID       int64   `json:"id"`
		Name     string  `json:"name"`
		Kind     string  `json:"kind"`
		Size     *int64  `json:"size"`
		Changed  *string `json:"changed"`
		Replaced bool    `json:"replaced"` // uploads only
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
