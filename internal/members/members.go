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
//
// The email address also decides whether someone given access is a member or a guest: an address in the list is a
// member's. Addresses are compared as Address writes them, and no two members may have the same one.
package members

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
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
	// byEmail holds each member's user id by their email address, as Address writes it.
	byEmail map[string]string
}

// Lookup finds a member by their user id.
func (l *List) Lookup(userID string) (Member, bool) {
	m, ok := l.byID[userID]
	return m, ok
}

// ByEmail finds the member whose email address is address, both compared as Address writes them.
func (l *List) ByEmail(address string) (Member, bool) {
	id, ok := l.byEmail[Address(address)]
	if !ok {
		return Member{}, false
	}
	return l.Lookup(id)
}

// Address is an email address as the Drive compares it, to tell a member from a guest and one guest from another:
// without spaces at either end, and in lower case. Nothing else is changed: dots, "+" tags and the rest make different
// addresses, as they can be different mailboxes, and are never merged.
func Address(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// Len is the number of members.
func (l *List) Len() int { return len(l.byID) }

// All returns every member, by display name (or email address, for one without a name), then user id.
func (l *List) All() []Member {
	out := make([]Member, 0, len(l.byID))
	for _, m := range l.byID {
		out = append(out, m)
	}
	key := func(m Member) string {
		if m.DisplayName != "" {
			return strings.ToLower(m.DisplayName)
		}
		return strings.ToLower(m.Email)
	}
	slices.SortFunc(out, func(a, b Member) int {
		if c := strings.Compare(key(a), key(b)); c != 0 {
			return c
		}
		return strings.Compare(a.UserID, b.UserID)
	})
	return out
}

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
	l := &List{byID: make(map[string]Member, len(*file.Members)), byEmail: make(map[string]string, len(*file.Members))}
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
		// Whether an address is a member's decides who is a member and who a guest: it must say one person.
		if other, dup := l.byEmail[Address(m.Email)]; dup {
			return nil, fmt.Errorf("members %q and %q have the same email address", other, m.UserID)
		}
		l.byID[m.UserID] = m
		l.byEmail[Address(m.Email)] = m.UserID
	}
	return l, nil
}

// Watcher holds the current member list and re-reads the file when it changes.
type Watcher struct {
	path    string
	log     *slog.Logger
	current atomic.Pointer[List]
	seen    os.FileInfo
	// problem is why the file as last read was refused, nil when it was taken: said again while the file stays as it is.
	problem error
	data    []byte
}

// PollInterval is how often the file is checked for a new version.
const PollInterval = time.Second

// StaleReminder is how often the log says again that the member list cannot be read, while it cannot.
const StaleReminder = 10 * time.Minute

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
		var since, said time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				err := w.reload()
				switch {
				// Said when it starts or changes, and again every StaleReminder while it lasts: until the list reads again,
				// nobody removed from it loses their access, and that must not rest on one line someone missed.
				case err != nil && (err.Error() != lastProblem || time.Since(said) >= StaleReminder):
					if lastProblem == "" {
						since = time.Now()
					}
					w.log.Error("the member list could not be re-read; keeping the previous version: whoever was removed from "+
						"the list since keeps their access until it reads again", "error", err, "since", since.UTC().Format(time.RFC3339))
					lastProblem, said = err.Error(), time.Now()
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
		return w.problem // the file as it was last read: still refused, if it was
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("cannot read the member list %s: %w", w.path, err)
	}
	w.seen = info
	if w.data != nil && bytes.Equal(data, w.data) {
		w.problem = nil
		return nil
	}
	list, err := Parse(data)
	if err != nil {
		w.problem = fmt.Errorf("%s: %w", w.path, err)
		return w.problem
	}
	w.data, w.problem = data, nil
	previous := w.current.Swap(list)
	if previous != nil {
		w.log.Info("the member list changed", "members", list.Len())
	}
	return nil
}
