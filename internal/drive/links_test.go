package drive

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// A link's token is 192 random bits in base64url: 32 characters a path can hold, never the same twice.
func TestALinksTokenIsLongAndRandom(t *testing.T) {
	form := regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	if TokenLength != 32 || TokenBytes*8 < 128 {
		t.Fatalf("a token is %d characters of %d bits", TokenLength, TokenBytes*8)
	}
	seen := map[string]bool{}
	for range 10_000 {
		tok := newToken()
		if !form.MatchString(tok) {
			t.Fatalf("a token %q is not 32 characters of base64url", tok)
		}
		if seen[tok] {
			t.Fatalf("the token %q came twice", tok)
		}
		seen[tok] = true
	}
}

// The index holds only the token's hash; a token is found only whole and exact, and every other string, a removed
// link's token included, is not found the same way.
func TestALinkIsFoundOnlyByItsWholeToken(t *testing.T) {
	ctx := context.Background()
	x, err := index.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	clock := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	d := New(x, nil, Options{Now: func() time.Time { return clock }})
	p, err := x.AddMyFiles(ctx, "u-bence", "My files/bence@example.com")
	if err != nil {
		t.Fatal(err)
	}
	it, err := x.PutItem(ctx, p.ID, 0, index.Seen{Name: "Clients", Folder: true, Changed: clock})
	if err != nil {
		t.Fatal(err)
	}
	l, token, err := d.MakeLink(ctx, p, it, "view", "u-bence", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.LinkByToken(ctx, token); err != nil || got.ID != l.ID {
		t.Fatalf("the link by its token: %+v, %v", got, err)
	}
	for _, wrong := range []string{"", token[:31], token + "A", token[1:] + token[:1], string(tokenHash(token)), "x"} {
		if _, err := d.LinkByToken(ctx, wrong); !errors.Is(err, index.ErrNoLink) {
			t.Errorf("the token %q found a link: %v", wrong, err)
		}
	}
	if err := d.RemoveLink(ctx, p, it); err != nil {
		t.Fatal(err)
	}
	if _, err := d.LinkByToken(ctx, token); !errors.Is(err, index.ErrNoLink) {
		t.Fatalf("a removed link's token: %v", err)
	}

	// Uses are counted in memory, shown as they are, and written to the index without losing any.
	l, token, err = d.MakeLink(ctx, p, it, "view", "u-bence", false)
	if err != nil {
		t.Fatal(err)
	}
	d.UsedLink(l)
	clock = clock.Add(time.Minute)
	d.UsedLink(l)
	if got, _ := d.LinkByToken(ctx, token); got.Uses != 2 || !got.Used.Equal(clock) {
		t.Fatalf("the link after two uses, before they are written: %+v", got)
	}
	if err := d.WriteUses(ctx); err != nil {
		t.Fatal(err)
	}
	d.UsedLink(l)
	if got, _ := d.LinkOn(ctx, it); got.Uses != 3 {
		t.Fatalf("the link after a third use: %+v", got)
	}
	if err := d.WriteUses(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.LinkOn(ctx, it.ID); got.Uses != 3 || !got.Used.Equal(clock) {
		t.Fatalf("the uses in the index: %+v", got)
	}
}
