package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// manyFolders makes n folders in a folder, a few at a time.
func manyFolders(t *testing.T, c *harness.Client, placeID, in int64, n int, name func(int) string) {
	t.Helper()
	var wg sync.WaitGroup
	names := make(chan string)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for nm := range names {
				if res := newFolder(t, c, placeID, in, nm); res.Status != http.StatusCreated {
					t.Errorf("making %q: got %d %s", nm, res.Status, res.Body)
				}
			}
		}()
	}
	for i := range n {
		names <- name(i)
	}
	close(names)
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
}

// A guest's list of a folder of thousands is too long to keep, and is sent as it is made. Its turn is given back when
// it turns out that long: guests who do not take such a list keep nobody else out, not even from signing in.
func TestALongListThatIsNotTakenKeepsNoGuestsTurn(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	manyFolders(t, b, x.place, x.bakery.ID, 5000, func(i int) string { return fmt.Sprintf("%04d-%s", i, strings.Repeat("n", 230)) })
	invited(t, b, x.place, x.bakery.ID, anna, "viewer", nil)
	ck := signedIn(t, d, smtp, anna)
	listing := guestItemPath(x.place, x.bakery.ID, "/items")
	if whole := withCookie(d, ck).Get(t, listing); whole.Status != http.StatusOK || len(whole.Body) < 1<<20 {
		t.Fatalf("the guest's long list: got %d with %d bytes, want 200 and more than a connection's buffers hold", whole.Status, len(whole.Body))
	}

	stalledConnections(t, d, 16, "GET "+listing+" HTTP/1.1\r\nHost: drive\r\nCookie: "+ck.Name+"="+ck.Value+"\r\n\r\n")
	start := time.Now()
	if res := browser(d).Get(t, guestAPI+"/me"); res.Status != http.StatusUnauthorized {
		t.Fatalf("asking who is signed in, with 16 long lists not taken: got %d %s, want 401", res.Status, res.Body)
	}
	if res := askForCode(t, browser(d), "someone@example.org"); res.Status != http.StatusAccepted {
		t.Fatalf("asking for a sign-in link, with 16 long lists not taken: got %d %s, want 202", res.Status, res.Body)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("answered after %v: the lists not taken kept it waiting", took)
	}
}

// A member who has had as many invitations mailed as they may in an hour gets no more by sending the removal of an
// invitation together with the same invitation again: whether the guest has one is read as the invitation is made,
// in turn with removals.
func TestTheInvitationsMailedInAnHourAreNotPassedByRemovingAndInvitingAtOnce(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	type slot struct {
		address string
		guest   int64
	}
	var slots []slot
	for i := range 30 {
		address := fmt.Sprintf("g%02d@client.example", i)
		got := invited(t, b, x.place, x.bakery.ID, address, "viewer", nil)
		slots = append(slots, slot{address, got.Guest.ID})
	}
	smtp.WaitFor(t, 30)
	for i, s := range slots {
		for try := 0; try < 5; try++ {
			var wg sync.WaitGroup
			var res harness.Response
			wg.Add(2)
			delay := time.Duration(i%6) * 300 * time.Microsecond
			go func() { defer wg.Done(); res = invite(t, d.As(bence), x.place, x.bakery.ID, s.address, "viewer", nil) }()
			go func() { defer wg.Done(); time.Sleep(delay); uninvite(t, d.As(bence), x.place, x.bakery.ID, s.guest) }()
			wg.Wait()
			if res.Status != http.StatusOK {
				break // refused: past what may be mailed, with no invitation there any more
			}
			var got invitedJSON
			_ = json.Unmarshal(res.Body, &got)
			if got.Invited {
				t.Fatalf("an invitation was made and mailed past the %d an hour: %s", 30, res.Body)
			}
		}
	}
	time.Sleep(time.Second)
	if n := len(smtp.Messages()); n != 30 {
		t.Fatalf("%d invitations were mailed, want the 30 an hour and no more", n)
	}
}
