package index

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

var limits = CodeLimits{ValidFor: 10 * time.Minute, Tries: 5, PerGuest: 5, Overall: 100, Window: time.Hour, Wrong: 100}

func code(c string) []byte { return bytes.Repeat([]byte(c[:1]), 32) }

// An invitation is a grant to a guest on an item: invited again it gets the new role and end date, a guest is recorded
// once per address, and a guest goes with their last invitation, however it goes (taken away, its item removed for
// good), and their sessions and codes with them.
func TestAGuestGoesWithTheirLastInvitation(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	offer := put(t, x, bence, clients.ID, "Offer.pdf", false, 84)
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)

	anna, added, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "viewer", time.Time{}, now, "u-bence")
	if err != nil || !added || anna.ID == 0 || anna.Address != "anna@client.example" {
		t.Fatalf("inviting Anna: %+v %v %v", anna, added, err)
	}
	ends := now.Add(48 * time.Hour).Truncate(time.Millisecond)
	if again, added, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "editor", ends, now, "u-reka"); err != nil || added || again != anna {
		t.Fatalf("inviting Anna again: %+v %v %v", again, added, err)
	}
	if _, _, err := x.Invite(ctx, bence.ID, notes.ID, "anna@client.example", "viewer", time.Time{}, now, "u-bence"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.Invite(ctx, bence.ID, 0, "anna@client.example", "viewer", time.Time{}, now, "u-bence"); err == nil {
		t.Fatal("a guest was invited to a place as a whole")
	}
	got, err := x.GrantsOn(ctx, bence.ID, []int64{clients.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("the grants on Clients: %+v %v", got, err)
	}
	if g := got[0]; g.Guest != anna.ID || g.GuestAddress != anna.Address || g.Member != "" || g.Everyone || g.Role != "editor" ||
		!g.Ends.Equal(ends) || g.By != "u-reka" || g.ID == 0 {
		t.Fatalf("Anna's invitation to Clients: %+v", g)
	}
	if granted, _ := x.GrantedToGuest(ctx, anna.ID); len(granted) != 2 {
		t.Fatalf("what was given to Anna: %+v", granted)
	}
	if err := x.ChangeInvitation(ctx, bence.ID, notes.ID, anna.ID, "editor", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := x.ChangeInvitation(ctx, bence.ID, offer.ID, anna.ID, "editor", time.Time{}); !errors.Is(err, ErrNoItem) {
		t.Fatalf("changing an invitation that is not there: %v", err)
	}
	if err := x.StartSession(ctx, anna.ID, code("s"), now, now.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if ok, err := x.IssueCode(ctx, anna.ID, code("c"), now, limits); err != nil || !ok {
		t.Fatalf("a code for Anna: %v %v", ok, err)
	}

	// Taking one invitation away leaves her; removing Clients' item for good takes the last, and her with it.
	if err := x.TakeAway(ctx, bence.ID, notes.ID, Grantee{Guest: anna.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.SessionOf(ctx, code("s"), now); err != nil {
		t.Fatalf("Anna's session after one invitation went: %v", err)
	}
	if _, err := x.db.ExecContext(ctx, "DELETE FROM items WHERE id = ?", clients.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.GuestByAddress(ctx, anna.Address); !errors.Is(err, ErrNoGuest) {
		t.Fatalf("Anna after her last invitation went with its item: %v", err)
	}
	if _, _, err := x.SessionOf(ctx, code("s"), now); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Anna's session after her last invitation went: %v", err)
	}
	var codes int
	if err := x.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM guest_codes").Scan(&codes); err != nil || codes != 0 {
		t.Fatalf("codes left: %d %v", codes, err)
	}
	// Invited anew, she is a new guest.
	again, _, err := x.Invite(ctx, bence.ID, notes.ID, "anna@client.example", "viewer", time.Time{}, now, "u-bence")
	if err != nil || again.ID == anna.ID {
		t.Fatalf("Anna invited anew: %+v %v", again, err)
	}
}

// A session counts until it ends, and while something given to its guest has not ended; a guest keeps a few.
func TestAGuestsSession(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	anna, _, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "viewer", now.Add(time.Hour), now, "u-bence")
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{"a", "b", "c"} {
		if err := x.StartSession(ctx, anna.ID, code(c), now.Add(time.Duration(i)*time.Second), now.Add(30*24*time.Hour), 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := x.SessionOf(ctx, code("a"), now); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the oldest of three sessions, two kept: %v", err)
	}
	if g, ends, err := x.SessionOf(ctx, code("c"), now); err != nil || g != anna || !ends.After(now) {
		t.Fatalf("the newest session: %+v %v %v", g, ends, err)
	}
	if _, _, err := x.SessionOf(ctx, code("c"), now.Add(time.Hour)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the session once her only invitation ended: %v", err)
	}
	if _, _, err := x.SessionOf(ctx, code("c"), now.Add(31*24*time.Hour)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the session after it ended: %v", err)
	}
	if err := x.TidyGuests(ctx, now.Add(2*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.SessionOf(ctx, code("c"), now); !errors.Is(err, ErrNoSession) {
		t.Fatalf("the session after tidying, once her invitation ended: %v", err)
	}
}

// A code works once, for its time, while it is the guest's newest, and not after five wrong tries; at most five are
// sent an hour to one guest. Wrong tries of live codes are counted for everyone together; every try writes the same.
func TestAGuestsCodes(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	anna, _, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "viewer", time.Time{}, now, "u-bence")
	if err != nil {
		t.Fatal(err)
	}
	try := func(address, c string, at time.Time, l CodeLimits) TryResult {
		t.Helper()
		g, r, err := x.TryCode(ctx, address, code(c), at, l)
		if err != nil {
			t.Fatal(err)
		}
		if (r == SignedIn) != (g == anna) {
			t.Fatalf("a try answered %v with %+v", r, g)
		}
		return r
	}
	issue := func(c string, at time.Time) bool {
		t.Helper()
		ok, err := x.IssueCode(ctx, anna.ID, code(c), at, limits)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	if r := try(anna.Address, "1", now, limits); r != Wrong {
		t.Fatalf("a try without a code: %v", r)
	}
	if !issue("1", now) {
		t.Fatal("the first code was not issued")
	}
	if r := try(anna.Address, "2", now, limits); r != Wrong {
		t.Fatalf("a wrong code: %v", r)
	}
	if r := try("someone@else.example", "1", now, limits); r != Wrong {
		t.Fatalf("her code for another address: %v", r)
	}
	if r := try(anna.Address, "1", now.Add(11*time.Minute), limits); r != Wrong {
		t.Fatalf("her code after 11 minutes: %v", r)
	}
	if r := try(anna.Address, "1", now.Add(time.Minute), limits); r != SignedIn {
		t.Fatalf("her code: %v", r)
	}
	if r := try(anna.Address, "1", now.Add(time.Minute), limits); r != Wrong {
		t.Fatalf("her code once used: %v", r)
	}

	// Asking again spends the code before; five wrong tries kill one.
	issue("3", now)
	issue("4", now)
	if r := try(anna.Address, "3", now, limits); r != Wrong {
		t.Fatalf("an older code: %v", r)
	}
	for range 4 {
		try(anna.Address, "5", now, limits)
	}
	if r := try(anna.Address, "4", now, limits); r != Wrong {
		t.Fatalf("her newest code after five wrong tries: %v", r)
	}
	// Five codes an hour: she has had three; two more, then none until the hour has passed.
	if !issue("6", now) || !issue("7", now.Add(30*time.Minute)) || issue("x", now.Add(31*time.Minute)) {
		t.Fatal("the fourth or fifth code an hour was not issued, or a sixth was")
	}
	if !issue("8", now.Add(61*time.Minute)) {
		t.Fatal("a code an hour later was not issued")
	}

	// Everyone's wrong tries together: past the limit, no try is taken, a right one neither, until the hour has passed.
	few := limits
	few.Wrong = 3
	later := now.Add(2 * time.Hour)
	issue("9", later)
	for range 2 {
		if r := try(anna.Address, "0", later, few); r != Wrong {
			t.Fatalf("a wrong try: %v", r)
		}
	}
	// Tries of addresses without a code are not counted.
	for range 5 {
		try("nobody@example.com", "0", later, few)
	}
	try(anna.Address, "0", later, few)
	if r := try(anna.Address, "9", later, few); r != TooManyTries {
		t.Fatalf("her code after three wrong tries, three allowed: %v", r)
	}
	issue("9", later.Add(61*time.Minute))
	if r := try(anna.Address, "9", later.Add(62*time.Minute), few); r != SignedIn {
		t.Fatalf("her code an hour later: %v", r)
	}
	// Overall, a hundred codes an hour.
	all := limits
	all.Overall = 2
	at := later.Add(3 * time.Hour)
	if ok, _ := x.IssueCode(ctx, anna.ID, code("a"), at, all); !ok {
		t.Fatal("a code")
	}
	other, _, _ := x.Invite(ctx, bence.ID, clients.ID, "other@client.example", "viewer", time.Time{}, now, "u-bence")
	if ok, _ := x.IssueCode(ctx, other.ID, code("b"), at, all); !ok {
		t.Fatal("another guest's code")
	}
	if ok, _ := x.IssueCode(ctx, other.ID, code("c"), at, all); ok {
		t.Fatal("a third code, two allowed for everyone")
	}
}

// A guest who joins the project keeps what they were given, as a member: by user id, with its end date, and the higher
// of a role they had already; the guest is gone.
func TestAGuestBecomesAMember(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	ends := now.Add(24 * time.Hour).Truncate(time.Millisecond)
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)
	anna, _, _ := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "editor", ends, now, "u-bence")
	if _, _, err := x.Invite(ctx, bence.ID, notes.ID, "anna@client.example", "viewer", time.Time{}, now, "u-bence"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Give(ctx, bence.ID, notes.ID, Grantee{Member: "u-anna"}, "editor", now, "u-bence", nil); err != nil {
		t.Fatal(err)
	}
	if err := x.StartSession(ctx, anna.ID, code("s"), now, now.Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if err := x.BecomeMember(ctx, anna.ID, "u-anna", []Granted{{bence.ID, clients.ID}, {bence.ID, notes.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.GuestByAddress(ctx, anna.Address); !errors.Is(err, ErrNoGuest) {
		t.Fatalf("the guest after she became a member: %v", err)
	}
	if _, _, err := x.SessionOf(ctx, code("s"), now); !errors.Is(err, ErrNoSession) {
		t.Fatalf("her guest session: %v", err)
	}
	got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, notes.ID})
	if len(got) != 2 {
		t.Fatalf("grants after: %+v", got)
	}
	for _, g := range got {
		if g.Member != "u-anna" || g.Guest != 0 || g.Role != "editor" {
			t.Errorf("a grant after: %+v", g)
		}
		if (g.Item == clients.ID) != g.Ends.Equal(ends) || (g.Item == notes.ID && !g.Ends.IsZero()) {
			t.Errorf("the end of a grant after: %+v", g)
		}
	}
	// Given again as a member, it ends no more.
	if _, err := x.Give(ctx, bence.ID, clients.ID, Grantee{Member: "u-anna"}, "editor", now, "u-bence", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID}); len(got) != 1 || !got[0].Ends.IsZero() {
		t.Fatalf("given again: %+v", got)
	}
}

// Only the invitations the caller says still work are carried over, never one the new member gave themselves, and
// where the member was given something on the item already the two are never mixed: the invitation takes the grant's
// place only when it is no less on both counts.
func TestAGuestBecomingAMemberGetsNoMoreThanWasGiven(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	friday := now.Add(24 * time.Hour).Truncate(time.Millisecond)
	later := friday.Add(24 * time.Hour)
	for name, c := range map[string]struct {
		role     string // the invitation's
		ends     time.Time
		had      string // what the member was given there already
		hadEnds  time.Time
		want     string
		wantEnds time.Time
	}{
		"editor until Friday, and viewer for good":      {"editor", friday, "viewer", time.Time{}, "viewer", time.Time{}},
		"viewer for good, and editor until Friday":      {"viewer", time.Time{}, "editor", friday, "editor", friday},
		"editor for good, and viewer until Friday":      {"editor", time.Time{}, "viewer", friday, "editor", time.Time{}},
		"editor until later, and editor until Friday":   {"editor", later, "editor", friday, "editor", later},
		"viewer until Friday, and editor until later":   {"viewer", friday, "editor", later, "editor", later},
		"viewer until later, and editor until Friday":   {"viewer", later, "editor", friday, "editor", friday},
		"editor until Friday, and editor for good":      {"editor", friday, "editor", time.Time{}, "editor", time.Time{}},
		"viewer until Friday, and viewer until Friday":  {"viewer", friday, "viewer", friday, "viewer", friday},
		"editor until Friday, and viewer until Friday":  {"editor", friday, "viewer", friday, "editor", friday},
		"editor until Friday, and nothing given before": {"editor", friday, "", time.Time{}, "editor", friday},
	} {
		x := open(t)
		bence := place(t, x, "u-bence")
		clients := put(t, x, bence, 0, "Clients", true, 0)
		anna, _, err := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", c.role, c.ends, now, "u-bence")
		if err != nil {
			t.Fatal(err)
		}
		if c.had != "" {
			if _, err := x.Give(ctx, bence.ID, clients.ID, Grantee{Member: "u-anna"}, c.had, now, "u-bence", nil); err != nil {
				t.Fatal(err)
			}
			if !c.hadEnds.IsZero() { // a member's grant has an end only from an invitation they had as a guest before
				if _, err := x.db.ExecContext(ctx, `UPDATE grants SET ends_at = ? WHERE member = 'u-anna'`, c.hadEnds.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := x.BecomeMember(ctx, anna.ID, "u-anna", []Granted{{bence.ID, clients.ID}}); err != nil {
			t.Fatal(err)
		}
		got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID})
		if len(got) != 1 || got[0].Member != "u-anna" || got[0].Role != c.want || !got[0].Ends.Equal(c.wantEnds) {
			t.Errorf("%s: %+v, want %s until %v", name, got, c.want, c.wantEnds)
		}
	}

	x := open(t)
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	notes := put(t, x, bence, 0, "Notes.txt", false, 5)
	own := put(t, x, bence, 0, "Own.txt", false, 5)
	anna, _, _ := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "editor", time.Time{}, now, "u-bence")
	for item, by := range map[int64]string{notes.ID: "u-bence", own.ID: "u-anna"} {
		if _, _, err := x.Invite(ctx, bence.ID, item, "anna@client.example", "viewer", time.Time{}, now, by); err != nil {
			t.Fatal(err)
		}
	}
	// Clients no longer works, the caller says; Own.txt she gave her own address herself.
	if err := x.BecomeMember(ctx, anna.ID, "u-anna", []Granted{{bence.ID, notes.ID}, {bence.ID, own.ID}}); err != nil {
		t.Fatal(err)
	}
	got, _ := x.GrantsOn(ctx, bence.ID, []int64{clients.ID, notes.ID, own.ID})
	if len(got) != 1 || got[0].Item != notes.ID || got[0].Member != "u-anna" || got[0].Guest != 0 {
		t.Fatalf("grants after, with one invitation that works: %+v", got)
	}
}

// A guest's upload in progress is theirs alone: a member, or another guest, with the same id finds nothing.
func TestAGuestsUploadIsTheirs(t *testing.T) {
	ctx := context.Background()
	x := open(t)
	now := time.Now()
	bence := place(t, x, "u-bence")
	clients := put(t, x, bence, 0, "Clients", true, 0)
	anna, _, _ := x.Invite(ctx, bence.ID, clients.ID, "anna@client.example", "editor", time.Time{}, now, "u-bence")
	id := "0123456789abcdef0123456789abcdef"
	if err := x.AddUpload(ctx, Upload{ID: id, PlaceID: bence.ID, FolderID: clients.ID, Guest: anna.ID, Name: "a", Size: 1, Started: now, Touched: now}); err != nil {
		t.Fatal(err)
	}
	if u, err := x.UploadOf(ctx, bence.ID, id, "", anna.ID); err != nil || u.Guest != anna.ID || u.Member != "" {
		t.Fatalf("Anna's upload: %+v %v", u, err)
	}
	for _, who := range []struct {
		member string
		guest  int64
	}{{"", anna.ID + 1}, {"u-bence", 0}, {"", 0}, {"u-bence", anna.ID}} {
		if _, err := x.UploadOf(ctx, bence.ID, id, who.member, who.guest); !errors.Is(err, ErrNoUpload) {
			t.Errorf("Anna's upload for %+v: %v", who, err)
		}
	}
}
