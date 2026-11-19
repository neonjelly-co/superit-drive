package members

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	l, err := Parse([]byte(`{"members":[
		{"userId":"u1","email":"reka@example.com","displayName":"Réka Kovács","role":"admin","extra":true},
		{"userId":"u2","email":"bence@example.com","displayName":"Bence Tóth","role":"member"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := l.Lookup("u1"); !ok || m.Role != RoleAdmin || m.DisplayName != "Réka Kovács" {
		t.Fatalf("u1: %+v %v", m, ok)
	}
	if _, ok := l.Lookup("u3"); ok {
		t.Fatal("u3 is not a member")
	}
}

func TestParseRefusesAListWithAProblem(t *testing.T) {
	cases := map[string]string{
		"not json":     `{`,
		"no members":   `{}`,
		"no user id":   `{"members":[{"email":"a@b.c","role":"member"}]}`,
		"no email":     `{"members":[{"userId":"u1","role":"member"}]}`,
		"unknown role": `{"members":[{"userId":"u1","email":"a@b.c","role":"owner"}]}`,
		"twice":        `{"members":[{"userId":"u1","email":"a@b.c","role":"member"},{"userId":"u1","email":"d@e.f","role":"member"}]}`,
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestWatchPicksUpANewVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "members.json")
	write := func(data string) {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"members":[{"userId":"u1","email":"a@example.com","role":"member"}]}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := Watch(ctx, path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.Current().Lookup("u2"); ok {
		t.Fatal("u2 should not be a member yet")
	}

	write(`{"members":[{"userId":"u2","email":"b@example.com","role":"member"}]}`)
	waitFor(t, func() bool { _, ok := w.Current().Lookup("u2"); return ok })
	if _, ok := w.Current().Lookup("u1"); ok {
		t.Fatal("u1 was removed")
	}

	// A broken version keeps the previous list.
	write(`{"members": [`)
	time.Sleep(2 * PollInterval)
	if _, ok := w.Current().Lookup("u2"); !ok {
		t.Fatal("a broken file must not drop the previous list")
	}
}

func TestWatchNeedsAReadableFileAtStart(t *testing.T) {
	_, err := Watch(context.Background(), filepath.Join(t.TempDir(), "missing.json"), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "missing.json") {
		t.Fatalf("expected an error naming the file, got %v", err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * PollInterval)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAllListsEveryMemberByName(t *testing.T) {
	l, err := Parse([]byte(`{"members":[
		{"userId":"u3","email":"zoe@example.com","role":"member"},
		{"userId":"u1","email":"reka@example.com","displayName":"Réka Kovács","role":"admin"},
		{"userId":"u2","email":"bence@example.com","displayName":"bence Tóth","role":"member"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range l.All() {
		got = append(got, m.UserID)
	}
	if want := []string{"u2", "u1", "u3"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
