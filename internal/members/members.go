// Package members reads the project's member list from a file the Drive can read but not change. Whoever runs the
// Drive keeps the file current (Superit's daemon replaces it by rename whenever membership changes); the Drive
// notices a new version within about a second.
//
// The file is JSON:
//
//	{
//	  "members": [
//	    {"userId": "6f1c…", "email": "reka@example.com", "displayName": "Réka Kovács", "role": "admin"},
//	    {"userId": "a93b…", "email": "bence@example.com", "displayName": "Bence Tóth", "role": "member"}
//	  ]
//	}
//
// userId is the person's stable id from the platform: the Drive ties files and access to it, so a changed email
// address orphans nothing. role is "admin" or "member". Other fields are ignored.
package members

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Role is a member's role on the project.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// A Member is a person who belongs to the project.
type Member struct {
	UserID      string `json:"userId"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        Role   `json:"role"`
}

// List is one version of the member list.
type List struct {
	byID map[string]Member
}

// Lookup finds a member by their user id.
func (l *List) Lookup(userID string) (Member, bool) {
	m, ok := l.byID[userID]
	return m, ok
}

// Len is the number of members.
func (l *List) Len() int { return len(l.byID) }

// Parse reads one version of the member list. A list with a problem is refused whole: a half-understood list could
// lock members out or let removed people in.
func Parse(data []byte) (*List, error) {
	var file struct {
		Members *[]Member `json:"members"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("the member list is not valid JSON: %w", err)
	}
	if file.Members == nil {
		return nil, errors.New(`the member list has no "members" array`)
	}
	l := &List{byID: make(map[string]Member, len(*file.Members))}
	for i, m := range *file.Members {
		switch {
		case strings.TrimSpace(m.UserID) == "":
			return nil, fmt.Errorf("member %d in the list has no userId", i+1)
		case strings.TrimSpace(m.Email) == "":
			return nil, fmt.Errorf("member %q has no email", m.UserID)
		case m.Role != RoleAdmin && m.Role != RoleMember:
			return nil, fmt.Errorf("member %q has role %q; it must be %q or %q", m.UserID, m.Role, RoleAdmin, RoleMember)
		}
		if _, dup := l.byID[m.UserID]; dup {
			return nil, fmt.Errorf("member %q is in the list twice", m.UserID)
		}
		l.byID[m.UserID] = m
	}
	return l, nil
}

// Watcher holds the current member list and re-reads the file when it changes.
type Watcher struct {
	path    string
	log     *slog.Logger
	current atomic.Pointer[List]
	seen    os.FileInfo
	data    []byte
}

// PollInterval is how often the file is checked for a new version.
const PollInterval = time.Second

// Watch reads the member list and keeps re-reading it until ctx ends. The first read must succeed: the Drive does not
// start without knowing its members. A later version that cannot be read is logged and the previous one kept, so a
// broken write never locks everyone out; a missing file is treated the same way.
func Watch(ctx context.Context, path string, log *slog.Logger) (*Watcher, error) {
	w := &Watcher{path: path, log: log}
	if err := w.reload(); err != nil {
		return nil, err
	}
	go func() {
		t := time.NewTicker(PollInterval)
		defer t.Stop()
		lastProblem := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				err := w.reload()
				switch {
				case err != nil && err.Error() != lastProblem:
					w.log.Error("the member list could not be re-read; keeping the previous version", "error", err)
					lastProblem = err.Error()
				case err == nil:
					lastProblem = ""
				}
			}
		}
	}()
	return w, nil
}

// Current is the latest member list that could be read.
func (w *Watcher) Current() *List { return w.current.Load() }

// reload reads the file again if it looks different from the last read. The daemon replaces the file by rename, so a
// new version is a different file; comparing contents as well catches editors that rewrite in place.
func (w *Watcher) reload() error {
	info, err := os.Stat(w.path)
	if err != nil {
		return fmt.Errorf("cannot read the member list %s: %w", w.path, err)
	}
	if w.seen != nil && os.SameFile(w.seen, info) && info.ModTime().Equal(w.seen.ModTime()) && info.Size() == w.seen.Size() {
		return nil
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("cannot read the member list %s: %w", w.path, err)
	}
	w.seen = info
	if w.data != nil && bytes.Equal(data, w.data) {
		return nil
	}
	list, err := Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", w.path, err)
	}
	w.data = data
	previous := w.current.Swap(list)
	if previous != nil {
		w.log.Info("the member list changed", "members", list.Len())
	}
	return nil
}
