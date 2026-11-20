package test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// Guests: people outside the project, invited by email address. The members' calls are the interface's; a guest's
// browser sends nothing but the address, the Drive's own address as Origin on a change (as a browser adds it), and
// the cookie the Drive set when they signed in. Their codes come by mail, to the throwaway mail server.

const (
	anna = "anna@client.example"
	bob  = "bob@other.example"
	cleo = "cleo@client.example"
)

func invitePath(placeID, itemID int64) string {
	return fmt.Sprintf("/api/places/%d/items/%d/access/guests", placeID, itemID)
}

func invitationPath(placeID, itemID, guest int64) string {
	return fmt.Sprintf("%s/%d", invitePath(placeID, itemID), guest)
}

// invite types an address in "Give access", with a role and an end date (nil: none).
func invite(t *testing.T, c *harness.Client, placeID, itemID int64, address, role string, ends *time.Time) harness.Response {
	t.Helper()
	body := map[string]any{"address": address, "role": role}
	if ends != nil {
		body["ends"] = ends.Format(time.RFC3339Nano)
	}
	return sendJSON(t, c, http.MethodPost, invitePath(placeID, itemID), body)
}

type invitedJSON struct {
	Access  []accessJSON `json:"access"`
	Guest   *guestJSON   `json:"guest"`
	Member  *personJSON  `json:"member"`
	Invited bool         `json:"invited"`
}

// invited invites, and fails the test unless the Drive gave access.
func invited(t *testing.T, c *harness.Client, placeID, itemID int64, address, role string, ends *time.Time) invitedJSON {
	t.Helper()
	res := invite(t, c, placeID, itemID, address, role, ends)
	if res.Status != http.StatusOK {
		t.Fatalf("inviting %q: got %d %s", address, res.Status, res.Body)
	}
	var got invitedJSON
	res.JSON(t, &got)
	return got
}

// changeInvitation gives an invitation a new role and end date (nil: none).
func changeInvitation(t *testing.T, c *harness.Client, placeID, itemID, guest int64, role string, ends *time.Time) harness.Response {
	t.Helper()
	body := map[string]any{"role": role, "ends": nil}
	if ends != nil {
		body["ends"] = ends.Format(time.RFC3339Nano)
	}
	return sendJSON(t, c, http.MethodPut, invitationPath(placeID, itemID, guest), body)
}

func uninvite(t *testing.T, c *harness.Client, placeID, itemID, guest int64) harness.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, invitationPath(placeID, itemID, guest), nil)
}

// guestLine is the line of "who can see this" about the guest at address, or nil.
func guestLine(lines []accessJSON, address string) *accessJSON {
	for i, a := range lines {
		if a.Guest != nil && a.Guest.Address == address {
			return &lines[i]
		}
	}
	return nil
}

// browser is anyone's browser on the guests' page.
func browser(d *harness.Drive) *harness.Client { return d.WithHeaders(http.Header{}).From(d.Address) }

const guestAPI = "/s/guest/api"

func guestItemPath(placeID, itemID int64, what string) string {
	return fmt.Sprintf("%s/places/%d/items/%d%s", guestAPI, placeID, itemID, what)
}

func askForCode(t *testing.T, c *harness.Client, address string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, guestAPI+"/code", map[string]string{"address": address})
}

func signIn(t *testing.T, c *harness.Client, address, code string) harness.Response {
	t.Helper()
	return sendJSON(t, c, http.MethodPost, guestAPI+"/sign-in", map[string]string{"address": address, "code": code})
}

var codeInMail = regexp.MustCompile(`Your code is (\d{6})\n`)

// codesTo is every code mailed to address so far, oldest first.
func codesTo(t *testing.T, smtp *mailtest.Server, address string) []string {
	t.Helper()
	var out []string
	for _, m := range smtp.Messages() {
		if !slices.Equal(m.To, []string{address}) {
			continue
		}
		if c := codeInMail.FindStringSubmatch(readMail(t, m).body); c != nil {
			out = append(out, c[1])
		}
	}
	return out
}

// nthCode waits for the n-th code mailed to address, and returns it.
func nthCode(t *testing.T, smtp *mailtest.Server, address string, n int) string {
	t.Helper()
	var codes []string
	harness.Eventually(t, fmt.Sprintf("code %d to %s arrives", n, address), func() bool {
		codes = codesTo(t, smtp, address)
		return len(codes) >= n
	})
	return codes[n-1]
}

// mailsTo is every mail to address so far.
func mailsTo(t *testing.T, smtp *mailtest.Server, address string) []mailed {
	t.Helper()
	var out []mailed
	for _, m := range smtp.Messages() {
		if slices.Equal(m.To, []string{address}) {
			out = append(out, readMail(t, m))
		}
	}
	return out
}

// sessionCookie is the guest's session cookie an answer sets, or nil.
func sessionCookie(res harness.Response) *http.Cookie {
	for _, c := range (&http.Response{Header: res.Header}).Cookies() {
		if strings.HasSuffix(c.Name, "drive-guest") {
			return c
		}
	}
	return nil
}

// withCookie is the browser with the guest's session cookie.
func withCookie(d *harness.Drive, c *http.Cookie) *harness.Client {
	return browser(d).With("Cookie", c.Name+"="+c.Value)
}

// signedIn has the guest at address ask for a code, read it from their mail (the n-th they got) and sign in with it,
// and returns their session's cookie.
func signedIn(t *testing.T, d *harness.Drive, smtp *mailtest.Server, address string) *http.Cookie {
	t.Helper()
	n := len(codesTo(t, smtp, address)) + 1
	if res := askForCode(t, browser(d), address); res.Status != http.StatusAccepted {
		t.Fatalf("%s asking for a code: got %d %s", address, res.Status, res.Body)
	}
	res := signIn(t, browser(d), address, nthCode(t, smtp, address, n))
	c := sessionCookie(res)
	if res.Status != http.StatusOK || c == nil {
		t.Fatalf("%s signing in: got %d %s", address, res.Status, res.Body)
	}
	return c
}

// signedInAs says whether the cookie is a guest's session that works now: who they are.
func signedInAs(t *testing.T, d *harness.Drive, c *http.Cookie) string {
	t.Helper()
	res := withCookie(d, c).Get(t, guestAPI+"/me")
	if res.Status == http.StatusUnauthorized {
		return ""
	}
	if res.Status != http.StatusOK {
		t.Fatalf("who the guest is: got %d %s", res.Status, res.Body)
	}
	var me struct {
		Address string `json:"address"`
	}
	res.JSON(t, &me)
	return me.Address
}

// What a guest is shown.
type (
	guestItemJSON struct {
		ID    int64    `json:"id"`
		Name  string   `json:"name"`
		Kind  string   `json:"kind"`
		Size  *int64   `json:"size"`
		Opens bool     `json:"opens"`
		Can   []string `json:"can"`
	}
	guestListJSON struct {
		Path     []folderJSON    `json:"path"`
		Role     string          `json:"role"`
		Can      []string        `json:"can"`
		Checking bool            `json:"checking"`
		Items    []guestItemJSON `json:"items"`
	}
	guestSharedJSON struct {
		guestItemJSON
		Place    int64      `json:"place"`
		Role     string     `json:"role"`
		SharedBy string     `json:"sharedBy"`
		Until    *time.Time `json:"until"`
	}
)

// guestShared is the guest's "Shared with me", and its raw body.
func guestShared(t *testing.T, c *harness.Client) ([]guestSharedJSON, string) {
	t.Helper()
	res := c.Get(t, guestAPI+"/shared-with-me")
	if res.Status != http.StatusOK {
		t.Fatalf("a guest's Shared with me: got %d %s", res.Status, res.Body)
	}
	var l struct {
		Items []guestSharedJSON `json:"items"`
	}
	res.JSON(t, &l)
	return l.Items, string(res.Body)
}

// guestOpened opens a folder as the guests' page does: the list, then, while the Drive looks at the folder on the
// storage, the list once it has.
func guestOpened(t *testing.T, c *harness.Client, placeID, folderID int64) harness.Response {
	t.Helper()
	res := c.Get(t, guestItemPath(placeID, folderID, "/items"))
	for range 3 {
		if res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"checking":true`) {
			return res
		}
		res = c.Get(t, guestItemPath(placeID, folderID, "/items?wait"))
	}
	return res
}

func guestList(t *testing.T, c *harness.Client, placeID, folderID int64) guestListJSON {
	t.Helper()
	res := guestOpened(t, c, placeID, folderID)
	if res.Status != http.StatusOK {
		t.Fatalf("a guest listing folder %d: got %d %s", folderID, res.Status, res.Body)
	}
	var l guestListJSON
	res.JSON(t, &l)
	return l
}

func guestNames(items []guestItemJSON) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

func guestUpload(t *testing.T, c *harness.Client, placeID, folderID int64, name string, content []byte, existing string) harness.Response {
	t.Helper()
	q := url.Values{"name": {name}}
	if existing != "" {
		q.Set("existing", existing)
	}
	return c.Do(t, http.MethodPost, guestItemPath(placeID, folderID, "/files?"+q.Encode()), bytes.NewReader(content))
}

// Typing an address in "Give access": one that is not a member's invites a guest, who is mailed about it, and the
// answer says so; one that is a member's, however its case is written, is an ordinary share with that member. Dots and
// "+" tags make other addresses. Only whoever may share the item invites, as viewer or editor, never with an end date
// that has passed; the invitation can be changed and taken away.
func TestTypingAnAddressInvitesAGuestOrSharesWithAMember(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)

	got := invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	if got.Guest == nil || got.Guest.Address != anna || !got.Invited || got.Member != nil {
		t.Fatalf("inviting Anna: %+v", got)
	}
	annaID := got.Guest.ID
	line := guestLine(got.Access, anna)
	if line == nil || line.Role != "editor" || !line.Given || line.Until != nil || line.Works == nil || !*line.Works ||
		line.Uses == nil || *line.Uses != 0 || line.Used != nil || line.UserID != "" || line.Email != "" {
		t.Fatalf("Anna's line in who can see Bakery: %+v", line)
	}
	m := smtp.WaitFor(t, 1)[0]
	if !slices.Equal(m.To, []string{anna}) {
		t.Fatalf("the invitation went to %q", m.To)
	}
	mailed := readMail(t, m)
	wantLink := fmt.Sprintf("Open it: http://drive.test/s/guest/?item=%d&place=%d#as=anna%%40client.example\n", x.bakery.ID, x.place)
	if mailed.subject != "Bence Tóth invited you to “Bakery”" ||
		!strings.HasPrefix(mailed.body, "Bence Tóth invited you to the folder “Bakery” in the Drive of Kovács Accounting.\n"+
			"You can open and download it. As an editor you can also upload, make folders, rename, move and delete inside it.\n\n") ||
		!strings.Contains(mailed.body, wantLink) || strings.Contains(mailed.body, "Clients") {
		t.Fatalf("the invitation: %q\n%s", mailed.subject, mailed.body)
	}
	// Everyone who can see Bakery sees that a guest can, from Clients' list down.
	if l := list(t, b, x.place, x.clients.ID); named(t, l.Items, "Bakery").SeenBy.Guests != 1 || named(t, l.Items, "Price list.pdf").SeenBy.Guests != 0 {
		t.Fatalf("who can see Bakery and Price list.pdf: %+v, %+v", named(t, l.Items, "Bakery").SeenBy, named(t, l.Items, "Price list.pdf").SeenBy)
	}
	ok(t, "sharing Clients with Dóra as viewer", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	lines := accessOf(t, d.As(dora), itemAccessPath(x.place, x.y2026.ID))
	if l := guestLine(lines, anna); l == nil || !l.Inherited || l.From == nil || l.From.Name != "Bakery" || l.Works != nil || l.Uses != nil {
		t.Fatalf("Anna's line in who can see 2026, as Dóra, a viewer, sees it: %+v", l)
	}

	// Dóra's address, written another way, is Dóra's: an ordinary member share, mailed as one; no guest, and no end date.
	tomorrow := time.Now().Add(24 * time.Hour)
	got = invited(t, b, x.place, x.notes.ID, " DORA@Example.COM ", "viewer", &tomorrow)
	if got.Member == nil || got.Member.UserID != dora.UserID || got.Guest != nil || got.Invited {
		t.Fatalf("inviting Dóra's address: %+v", got)
	}
	if lines := accessLines(got.Access); !slices.Contains(lines, dora.UserID+" viewer (here)") || slices.ContainsFunc(got.Access, func(a accessJSON) bool { return a.Until != nil }) {
		t.Fatalf("who can see Notes.txt: %q", lines)
	}
	if got := readMail(t, smtp.WaitFor(t, 3)[2]); !slices.Equal(got.to, []string{dora.Email}) || got.subject != "Bence Tóth shared “Notes.txt” with you" {
		t.Fatalf("the mail to Dóra: %q %q", got.to, got.subject)
	}
	// With a "+" tag it is another address: a guest.
	if got := invited(t, b, x.place, x.notes.ID, "dora+drive@example.com", "viewer", nil); got.Guest == nil || got.Guest.Address != "dora+drive@example.com" {
		t.Fatalf("inviting dora+drive@example.com: %+v", got)
	}
	smtp.WaitFor(t, 4)

	// Invited again, nothing changes and nobody is mailed; her role changes with a change of the invitation.
	again := invited(t, b, x.place, x.bakery.ID, "Anna@Client.Example", "editor", nil)
	if again.Guest == nil || again.Guest.ID != annaID || again.Guest.Address != anna || again.Invited {
		t.Fatalf("inviting Anna again: %+v", again)
	}
	quietly(t, smtp, 4)
	res := changeInvitation(t, b, x.place, x.bakery.ID, annaID, "viewer", nil)
	var changed invitedJSON
	res.JSON(t, &changed)
	if res.Status != http.StatusOK || guestLine(changed.Access, anna).Role != "viewer" {
		t.Fatalf("making Anna a viewer: %d %s", res.Status, res.Body)
	}
	if res := changeInvitation(t, b, x.place, x.clients.ID, annaID, "viewer", nil); res.Status != http.StatusNotFound {
		t.Fatalf("changing an invitation that is not there: %d %s", res.Status, res.Body)
	}

	// Refused: what is not an address mail can go to, a role a guest is not given, an end date that has passed, and
	// one's own address.
	for _, bad := range []string{"anna", "anna@client", "Anna <anna@client.example>", "anna@client.example, eve@evil.example",
		"anna@client.example\r\nBcc: eve@evil.example", "anna@client.example\nRCPT TO:<eve@evil.example>", "ánna@client.example", ""} {
		if res := invite(t, b, x.place, x.bakery.ID, bad, "viewer", nil); res.Status != http.StatusBadRequest {
			t.Errorf("inviting %q: got %d %s, want 400", bad, res.Status, res.Body)
		}
	}
	yesterday := time.Now().Add(-24 * time.Hour)
	for what, res := range map[string]harness.Response{
		"as manager":              invite(t, b, x.place, x.bakery.ID, cleo, "manager", nil),
		"as owner":                invite(t, b, x.place, x.bakery.ID, cleo, "owner", nil),
		"until yesterday":         invite(t, b, x.place, x.bakery.ID, cleo, "viewer", &yesterday),
		"Bence, himself":          invite(t, b, x.place, x.bakery.ID, bence.Email, "viewer", nil),
		"changed to manager":      changeInvitation(t, b, x.place, x.bakery.ID, annaID, "manager", nil),
		"changed until yesterday": changeInvitation(t, b, x.place, x.bakery.ID, annaID, "viewer", &yesterday),
	} {
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s: got %d %s, want 400", what, res.Status, res.Body)
		}
	}
	quietly(t, smtp, 4)

	// Dóra, a viewer of Bakery, may not share it: inviting, changing and taking away answer as for an item that is not
	// there.
	nowhere := x.notes.ID + 1000
	o := d.As(dora)
	for what, try := range map[string]func(int64) harness.Response{
		"inviting":    func(id int64) harness.Response { return invite(t, o, x.place, id, cleo, "viewer", nil) },
		"changing":    func(id int64) harness.Response { return changeInvitation(t, o, x.place, id, annaID, "editor", nil) },
		"taking away": func(id int64) harness.Response { return uninvite(t, o, x.place, id, annaID) },
	} {
		sameAs(t, "Dóra "+what+" on Bakery", try(x.bakery.ID), try(nowhere))
	}

	// Taken away: she is gone from who can see it.
	res = uninvite(t, b, x.place, x.bakery.ID, annaID)
	var after invitedJSON
	res.JSON(t, &after)
	if res.Status != http.StatusOK || guestLine(after.Access, anna) != nil {
		t.Fatalf("taking Anna's invitation away: %d %s", res.Status, res.Body)
	}
}

// Inviting a guest needs mail that works: the Drive mails the invitation and the codes. Without mail settings, with a
// mail server that is not there, or one that cannot be reached as the settings say, inviting is unavailable and says
// why; the members' page is told before anyone types. Sharing with a member's address still works.
func TestWithoutMailThatWorksInvitingGuestsIsUnavailable(t *testing.T) {
	t.Parallel()
	_, port, _ := strings.Cut(strings.TrimPrefix(closedAddress(t), "http://"), ":")
	noTLS := mailtest.Start(t, mailtest.Options{})
	for name, c := range map[string]struct {
		env map[string]string
		why string
	}{
		"no mail settings": {nil, "This Drive has no mail settings"},
		"a mail server that is not there": {map[string]string{"DRIVE_SMTP_HOST": "127.0.0.1", "DRIVE_SMTP_PORT": port, "DRIVE_SMTP_TLS": "none",
			"DRIVE_MAIL_FROM": "drive@drive.test"}, "could not reach its mail server"},
		"a mail server without the encryption asked for": {map[string]string{"DRIVE_SMTP_HOST": noTLS.Host, "DRIVE_SMTP_PORT": fmt.Sprint(noTLS.Port),
			"DRIVE_SMTP_TLS": "starttls", "DRIVE_MAIL_FROM": "drive@drive.test"}, "could not be reached safely"},
	} {
		d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence, dora}, Env: c.env})
		b := d.As(bence)
		x := bencesFiles(t, d)
		res := invite(t, b, x.place, x.bakery.ID, anna, "viewer", nil)
		if res.Status != http.StatusConflict || !strings.Contains(errorOf(t, res), "Inviting guests is unavailable") ||
			!strings.Contains(errorOf(t, res), c.why) || !strings.Contains(errorOf(t, res), "public link") || !strings.Contains(string(res.Body), `"mail":false`) {
			t.Errorf("%s: inviting Anna: got %d %s", name, res.Status, res.Body)
		}
		if lines := accessLines(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID))); len(lines) != 1 {
			t.Errorf("%s: who can see Bakery after: %q", name, lines)
		}
		var me struct {
			Guests struct {
				Invite bool   `json:"invite"`
				Why    string `json:"why"`
			} `json:"guests"`
		}
		b.Get(t, "/api/me").JSON(t, &me)
		if me.Guests.Invite || !strings.Contains(me.Guests.Why, c.why) {
			t.Errorf("%s: the members' page is told %+v", name, me.Guests)
		}
		if got := invited(t, b, x.place, x.bakery.ID, dora.Email, "viewer", nil); got.Member == nil {
			t.Errorf("%s: sharing with Dóra's address: %+v", name, got)
		}
		if res := askForCode(t, browser(d), anna); res.Status != http.StatusAccepted {
			t.Errorf("%s: asking for a code: %d", name, res.Status)
		}
	}
	// With mail that works, the members' page is told it can.
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: mailSettings(smtp)})
	if res := d.As(bence).Get(t, "/api/me"); !strings.Contains(string(res.Body), `"guests":{"invite":true,"why":""}`) {
		t.Fatalf("with mail: %s", res.Body)
	}
}

// A guest signs in on the public prefix with a six-digit code mailed to them. Asking for one is answered the same for
// every address; a code goes only to an address with something shared with it (never to a member's, nor to anyone
// else), at most five an hour per address. Only the newest code works, once, for about ten minutes, and not after five
// wrong tries; a wrong code is answered the same for every address. Neither the code nor the session is in the index.
func TestAGuestSignsInWithACodeMailedToThem(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	invited(t, b, x.place, x.notes.ID, bob, "viewer", nil)
	smtp.WaitFor(t, 2)
	deleted(t, b, x.place, x.notes.ID) // all that was given to Bob is in the trash: nothing lets him in

	// The same answer for every address: Anna's, in any case; Bob's, with nothing he may open; a member's; nobody's.
	var first harness.Response
	for i, address := range []string{anna, "ANNA@Client.Example", bob, dora.Email, "nobody@example.com"} {
		res := askForCode(t, browser(d), address)
		res.Header.Del("Date")
		if res.Status != http.StatusAccepted || !strings.Contains(string(res.Body), "If anything on this Drive is shared with this address") {
			t.Fatalf("asking for a code for %s: %d %s", address, res.Status, res.Body)
		}
		if i == 0 {
			first = res
		} else if !bytes.Equal(res.Body, first.Body) || !headersEqual(res.Header, first.Header) {
			t.Errorf("asking for a code for %s answered otherwise than for Anna: %v %s", address, res.Header, res.Body)
		}
		if sessionCookie(res) != nil {
			t.Errorf("asking for a code for %s set a cookie", address)
		}
	}
	older, code := nthCode(t, smtp, anna, 1), nthCode(t, smtp, anna, 2)
	quietly(t, smtp, 4)
	if mails := mailsTo(t, smtp, anna); len(mails) != 3 || mails[1].subject != "Your code for the Drive of Kovács Accounting" ||
		!strings.Contains(mails[1].body, "It works once, for 10 minutes.") {
		t.Fatalf("the mails to Anna: %+v", mails)
	}
	if res := askForCode(t, browser(d), "not an address"); res.Status != http.StatusBadRequest {
		t.Fatalf("asking for a code for something that is not an address: %d", res.Status)
	}

	// A wrong code, the older one, or the right one for another address: the same answer as a code for nobody.
	wrong := signIn(t, browser(d), "nobody@example.com", code)
	if wrong.Status != http.StatusBadRequest || !strings.Contains(errorOf(t, wrong), "This code is not right") {
		t.Fatalf("a code for nobody: %d %s", wrong.Status, wrong.Body)
	}
	for what, res := range map[string]harness.Response{
		"the older code":       signIn(t, browser(d), anna, older),
		"Anna's code for Bob":  signIn(t, browser(d), bob, code),
		"Anna's code for Dóra": signIn(t, browser(d), dora.Email, code),
	} {
		if older == code && what == "the older code" {
			continue // one in a million
		}
		if res.Status != wrong.Status || !bytes.Equal(res.Body, wrong.Body) || sessionCookie(res) != nil {
			t.Errorf("%s: got %d %s", what, res.Status, res.Body)
		}
	}
	// The right one signs her in: a cookie for the guests' prefix only, that no script reads and no other site's page
	// sends with a change, for 30 days. It works once.
	res := signIn(t, browser(d), "Anna@Client.Example", code)
	c := sessionCookie(res)
	if res.Status != http.StatusOK || c == nil || c.Name != "drive-guest" || c.Path != "/s/guest" || !c.HttpOnly ||
		c.SameSite != http.SameSiteLaxMode || c.MaxAge < 30*24*3600-60 || c.MaxAge > 30*24*3600 || len(res.Header.Values("Set-Cookie")) != 1 {
		t.Fatalf("signing in: %d %s %v", res.Status, res.Body, res.Header.Values("Set-Cookie"))
	}
	if got := signedInAs(t, d, c); got != anna {
		t.Fatalf("signed in as %q", got)
	}
	if res := signIn(t, browser(d), anna, code); res.Status != http.StatusBadRequest || sessionCookie(res) != nil {
		t.Fatalf("the code a second time: %d %s", res.Status, res.Body)
	}
	if index := indexFile(t, d); bytes.Contains(index, []byte(code)) || bytes.Contains(index, []byte(c.Value)) {
		t.Fatal("the index holds the code or the session's token")
	}
	if logs := d.Logs(); strings.Contains(logs, code) || strings.Contains(logs, c.Value) || strings.Contains(logs, anna) {
		t.Fatalf("the log holds the code, the token or the address:\n%s", logs)
	}

	// Five wrong tries kill a code.
	askForCode(t, browser(d), anna)
	third := nthCode(t, smtp, anna, 3)
	for i := range 5 {
		guess := fmt.Sprintf("%06d", i)
		if guess == third {
			guess = "999999"
		}
		signIn(t, browser(d), anna, guess)
	}
	if res := signIn(t, browser(d), anna, third); res.Status != http.StatusBadRequest {
		t.Fatalf("a code after five wrong tries: %d %s", res.Status, res.Body)
	}

	// At most five codes an hour to one address: Anna has had three; two more come, and no other, however often she asks.
	for range 4 {
		askForCode(t, browser(d), anna)
	}
	nthCode(t, smtp, anna, 5)
	quietly(t, smtp, 2+5) // the invitations to Anna and Bob, and Anna's five codes
	// An hour later she is sent one again; it works for about ten minutes, then no more.
	d.Restart(t, map[string]string{harness.TestClockShift: "61m"})
	askForCode(t, browser(d), anna)
	sixth := nthCode(t, smtp, anna, 6)
	d.Restart(t, map[string]string{harness.TestClockShift: "72m"})
	if res := signIn(t, browser(d), anna, sixth); res.Status != http.StatusBadRequest {
		t.Fatalf("a code eleven minutes later: %d %s", res.Status, res.Body)
	}
	askForCode(t, browser(d), anna)
	if res := signIn(t, browser(d), anna, nthCode(t, smtp, anna, 7)); res.Status != http.StatusOK {
		t.Fatalf("a new code: %d %s", res.Status, res.Body)
	}
	// Bob, with nothing that lets him in, and Dóra, a member, were never sent one.
	if got := len(codesTo(t, smtp, bob)) + len(codesTo(t, smtp, dora.Email)); got != 0 {
		t.Fatalf("%d codes went to Bob and Dóra", got)
	}
}

// Over HTTPS the session's cookie is sent only over HTTPS, under a name no page over plain HTTP can set.
func TestAGuestsCookieOverHTTPS(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	env := mailSettings(smtp)
	env["DRIVE_ADDRESS"] = "https://drive.test"
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: env})
	x := bencesFiles(t, d)
	invited(t, d.As(bence), x.place, x.bakery.ID, anna, "viewer", nil)
	c := signedIn(t, d, smtp, anna)
	if c.Name != "__Secure-drive-guest" || !c.Secure || c.Path != "/s/guest" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("the cookie over HTTPS: %+v", c)
	}
	if got := signedInAs(t, d, c); got != anna {
		t.Fatalf("signed in as %q", got)
	}
	// The same token under the plain name is no session.
	if res := browser(d).With("Cookie", "drive-guest="+c.Value).Get(t, guestAPI+"/me"); res.Status != http.StatusUnauthorized {
		t.Fatalf("the token under another name: %d", res.Status)
	}
	// The invitation is mailed after the answer, and may arrive after the code.
	harness.Eventually(t, "the invitation leads to the Drive's HTTPS address", func() bool {
		for _, m := range smtp.Messages() {
			if strings.Contains(readMail(t, m).body, "Open it: https://drive.test/s/guest/?item=") {
				return true
			}
		}
		return false
	})
}

// A guest stays signed in on their browser for 30 days, kept by the Drive; it ends at once, for good, when the last of
// what was given to them is taken away, or lets them in no more: in the trash, gone from the storage, or in the My
// files of someone who left.
func TestAGuestStaysSignedInForThirtyDaysAndNoLongerThanTheirAccess(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	a := invited(t, b, x.place, x.bakery.ID, anna, "editor", nil).Guest.ID
	invited(t, b, x.place, x.notes.ID, anna, "viewer", nil)
	bobs := invited(t, b, x.place, x.prices.ID, bob, "viewer", nil).Guest.ID
	invited(t, b, x.place, x.secret.ID, cleo, "viewer", nil)
	annas, bobsCookie, cleos := signedIn(t, d, smtp, anna), signedIn(t, d, smtp, bob), signedIn(t, d, smtp, cleo)

	// 29 days on, all three are signed in; 31 days on, the sessions have ended.
	d.Restart(t, map[string]string{harness.TestClockShift: fmt.Sprint(29*24, "h")})
	for _, c := range []*http.Cookie{annas, bobsCookie, cleos} {
		if signedInAs(t, d, c) == "" {
			t.Fatalf("a guest 29 days on: signed out")
		}
	}
	d.Restart(t, map[string]string{harness.TestClockShift: fmt.Sprint(31*24, "h")})
	for _, c := range []*http.Cookie{annas, bobsCookie, cleos} {
		res := withCookie(d, c).Get(t, guestAPI+"/me")
		if res.Status != http.StatusUnauthorized || !strings.Contains(string(res.Body), `"signIn":true`) ||
			sessionCookie(res) == nil || sessionCookie(res).MaxAge >= 0 {
			t.Fatalf("a guest 31 days on: %d %s %v", res.Status, res.Body, res.Header.Values("Set-Cookie"))
		}
	}
	annas, bobsCookie, cleos = signedIn(t, d, smtp, anna), signedIn(t, d, smtp, bob), signedIn(t, d, smtp, cleo)
	b = d.As(bence)

	// Taking one of two invitations away leaves Anna signed in; taking the last signs her out at once, for good.
	ok(t, "taking Notes.txt away from Anna", uninvite(t, b, x.place, x.notes.ID, a))
	if signedInAs(t, d, annas) != anna {
		t.Fatal("Anna signed out when one of two invitations went")
	}
	ok(t, "taking Bakery away from Anna", uninvite(t, b, x.place, x.bakery.ID, a))
	if signedInAs(t, d, annas) != "" {
		t.Fatal("Anna still signed in once her last invitation went")
	}
	invited(t, b, x.place, x.bakery.ID, anna, "viewer", nil)
	if signedInAs(t, d, annas) != "" {
		t.Fatal("Anna's old session came back with a new invitation")
	}

	// In the trash, what was given to Bob lets him in no more: signed out, for good, also once it is back.
	deleted(t, b, x.place, x.prices.ID)
	if signedInAs(t, d, bobsCookie) != "" {
		t.Fatal("Bob still signed in with all he was given in the trash")
	}
	restored(t, b, x.place, x.prices.ID)
	if signedInAs(t, d, bobsCookie) != "" {
		t.Fatal("Bob's session came back with what was given to him")
	}
	if items, _ := guestShared(t, withCookie(d, signedIn(t, d, smtp, bob))); len(items) != 1 || items[0].ID != x.prices.ID {
		t.Fatalf("Bob's Shared with me once he signed in again: %+v", items)
	}
	_ = bobs

	// Gone from the storage, Secret.txt lets Cleo in no more, from the first look that misses it.
	d.Storage.Delete(t, "My files/bence@example.com/Private/Secret.txt", false)
	list(t, b, x.place, x.private.ID)
	if signedInAs(t, d, cleos) != "" {
		t.Fatal("Cleo still signed in with what was given to her gone from the storage")
	}

	// Bence leaves the project: his My files is held, and nothing shared from it lets anyone in.
	annas = signedIn(t, d, smtp, anna)
	d.SetMembers(t, reka, dora)
	harness.Eventually(t, "Anna is signed out once Bence has left", func() bool { return signedInAs(t, d, annas) == "" })
	n := len(codesTo(t, smtp, anna))
	askForCode(t, browser(d), anna)
	quietly(t, smtp, len(smtp.Messages()))
	if len(codesTo(t, smtp, anna)) != n {
		t.Fatal("Anna was sent a code with nothing that lets her in")
	}
}

// Whoever shares with a guest may give their access an end date; by default it has none. Before it, the guest works
// as before; from it on, they are signed out and get no code, and the sharer sees that the invitation no longer works.
// It can be moved, or taken off.
func TestASharerCanGiveAGuestsAccessAnEndDate(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	ends := time.Now().Add(2 * time.Hour).Truncate(time.Millisecond)
	a := invited(t, b, x.place, x.clients.ID, anna, "viewer", &ends)
	if l := guestLine(a.Access, anna); l == nil || l.Until == nil || !l.Until.Equal(ends) || !*l.Works {
		t.Fatalf("Anna's line, with an end date: %+v", l)
	}
	if l := guestLine(invited(t, b, x.place, x.clients.ID, bob, "viewer", nil).Access, bob); l == nil || l.Until != nil {
		t.Fatalf("Bob's line, without one: %+v", l)
	}
	// The invitation is mailed after the answer.
	harness.Eventually(t, "Anna's invitation arrives", func() bool { return len(mailsTo(t, smtp, anna)) > 0 })
	if !strings.Contains(mailsTo(t, smtp, anna)[0].body, "You have access until "+ends.UTC().Format("2 January 2006, 15:04 UTC")+".") {
		t.Fatalf("Anna's invitation does not say until when: %s", mailsTo(t, smtp, anna)[0].body)
	}
	annas, bobs := signedIn(t, d, smtp, anna), signedIn(t, d, smtp, bob)
	if items, _ := guestShared(t, withCookie(d, annas)); len(items) != 1 || items[0].Until == nil || !items[0].Until.Equal(ends) {
		t.Fatalf("Anna's Shared with me: %+v", items)
	}
	// Moved an hour later, then three hours on: before the new end she is in, after it out.
	later := ends.Add(time.Hour)
	ok(t, "moving Anna's end date", changeInvitation(t, b, x.place, x.clients.ID, a.Guest.ID, "viewer", &later))
	d.Restart(t, map[string]string{harness.TestClockShift: "150m"})
	if signedInAs(t, d, annas) != anna {
		t.Fatal("Anna signed out before her end date")
	}
	d.Restart(t, map[string]string{harness.TestClockShift: "4h"})
	if signedInAs(t, d, annas) != "" {
		t.Fatal("Anna still signed in after her end date")
	}
	if res := withCookie(d, bobs).Get(t, guestItemPath(x.place, x.clients.ID, "/items")); res.Status != http.StatusOK {
		t.Fatalf("Bob, without an end date: %d", res.Status)
	}
	n := len(smtp.Messages())
	askForCode(t, browser(d), anna)
	quietly(t, smtp, n)
	if l := guestLine(accessOf(t, d.As(bence), itemAccessPath(x.place, x.clients.ID)), anna); l == nil || *l.Works || !l.Until.Equal(later) {
		t.Fatalf("Anna's line after her end date: %+v", l)
	}
	// Taken off, she is in again (signing in anew).
	ok(t, "taking Anna's end date off", changeInvitation(t, d.As(bence), x.place, x.clients.ID, a.Guest.ID, "viewer", nil))
	if signedInAs(t, d, signedIn(t, d, smtp, anna)) != anna {
		t.Fatal("Anna could not sign in once her end date was taken off")
	}
}

// A guest whose address appears in the member list keeps their access as a member, tied to their user id from then on:
// their guest's session ends, and they work as a member, also after their address changes. A member who leaves does not
// become a guest; invited again by address after they left, they are a new guest.
func TestAGuestWhoJoinsTheProjectKeepsTheirAccessAsAMember(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	ends := time.Now().Add(48 * time.Hour).Truncate(time.Millisecond)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	invited(t, b, x.place, x.notes.ID, anna, "viewer", &ends)
	annas := signedIn(t, d, smtp, anna)

	member := harness.Member{UserID: "u-anna", Email: "Anna@Client.Example", DisplayName: "Anna Kiss", Role: "member"}
	d.SetMembers(t, reka, bence, member)
	harness.Eventually(t, "Anna is a member", func() bool { return d.As(member).Get(t, "/api/me").Status == http.StatusOK })
	harness.Eventually(t, "her guest's session ends", func() bool { return signedInAs(t, d, annas) == "" })
	// What was given to her as a guest becomes hers as a member just after the new member list is read.
	harness.Eventually(t, "Anna, a member, opens Bakery", func() bool {
		return d.As(member).Get(t, listPath(x.place, x.bakery.ID)).Status == http.StatusOK
	})
	if l := list(t, d.As(member), x.place, x.bakery.ID); l.Role != "editor" || !slices.Contains(l.Can, "upload") {
		t.Fatalf("Anna, a member, in Bakery: %+v", l)
	}
	lines := accessOf(t, b, itemAccessPath(x.place, x.notes.ID))
	if got := accessLines(lines); !slices.Contains(got, "u-anna viewer (here)") || guestLine(lines, anna) != nil {
		t.Fatalf("who can see Notes.txt: %q", got)
	}
	if i := slices.IndexFunc(lines, func(a accessJSON) bool { return a.UserID == "u-anna" }); lines[i].Until == nil || !lines[i].Until.Equal(ends) {
		t.Fatalf("her end date on Notes.txt as a member: %+v", lines[i])
	}
	if names := sharedNames(t, d.As(member)); !slices.Equal(names, []string{entry("Notes.txt", x.place), entry("Bakery", x.place)}) {
		t.Fatalf("Anna's Shared with me as a member: %q", names)
	}
	// Asked for, a code goes to a member's address no more.
	n := len(smtp.Messages())
	askForCode(t, browser(d), anna)
	quietly(t, smtp, n)

	// Her address changes: what she was given is hers by her user id.
	moved := member
	moved.Email = "anna.kiss@client.example"
	d.SetMembers(t, reka, bence, moved)
	harness.Eventually(t, "her new address is in the list", func() bool {
		var me meJSON
		d.As(moved).Get(t, "/api/me").JSON(t, &me)
		return me.Email == moved.Email
	})
	if l := list(t, d.As(moved), x.place, x.bakery.ID); l.Role != "editor" {
		t.Fatalf("Anna in Bakery after her address changed: %+v", l)
	}

	// She leaves: she is not a guest, and gets no code at either address.
	d.SetMembers(t, reka, bence)
	harness.Eventually(t, "Anna has left", func() bool { return d.As(moved).Get(t, "/api/me").Status == http.StatusForbidden })
	askForCode(t, browser(d), anna)
	askForCode(t, browser(d), moved.Email)
	quietly(t, smtp, n)
	if l := accessLines(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID))); !slices.Contains(l, "u-anna editor (here) (former)") {
		t.Fatalf("who can see Bakery after Anna left: %q", l)
	}
	// Invited again by her old address, she is a new guest.
	if got := invited(t, b, x.place, x.private.ID, anna, "viewer", nil); got.Guest == nil || !got.Invited {
		t.Fatalf("inviting Anna's address once she has left: %+v", got)
	}
	if items, _ := guestShared(t, withCookie(d, signedIn(t, d, smtp, anna))); len(items) != 1 || items[0].Name != "Private" {
		t.Fatalf("Anna's Shared with me as a guest again: %+v", items)
	}
}

// A guest given Bakery, deep in Bence's My files, tries to get out of it: every guest's route with every item they
// were not given (above it, beside it, elsewhere in the place, in the trash, in another place, or none), uploads in
// progress that are not theirs, moves out of it, renaming or deleting Bakery itself, the routes members and visitors
// have, and routes guests do not have (search, the trash, who has access, the members). Every try gets the same answer
// as an item that does not exist, or a refusal, and nothing changes. What they see names nothing above what was given,
// nobody's address, and nothing about who else can see it or who added it. The routes are those of
// internal/server/routes.go under the guests' prefix; a test there keeps the two lists equal.
func TestAGuestCannotGetOutOfWhatWasGivenToThem(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	invited(t, b, x.place, x.bakery.ID, bob, "editor", nil)
	invited(t, b, x.place, x.private.ID, bob, "editor", nil)
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	old := uploadedInto(t, b, x.place, x.bakery.ID, "Old.pdf", []byte("old"))
	deleted(t, b, x.place, old.ID)
	deleted(t, b, x.place, x.notes.ID)
	rekas, _ := myFiles(t, d, reka)
	rekasFile := uploaded(t, d.As(reka), rekas, "Réka's.txt", []byte("hers"))
	payroll := madeSharedFolder(t, b, "Payroll")
	slip := uploadedInto(t, b, payroll.ID, 0, "Slip.pdf", []byte("slip"))
	nowhere := slip.ID + x.notes.ID + 1000
	a, o := withCookie(d, signedIn(t, d, smtp, anna)), withCookie(d, signedIn(t, d, smtp, bob))
	if res := guestUpload(t, o, x.place, x.y2026.ID, "From Bob.pdf", []byte("bob"), ""); res.Status != http.StatusCreated {
		t.Fatalf("Bob's upload into 2026: %d %s", res.Status, res.Body)
	}
	bobsUpload := func() string {
		res := o.Do(t, http.MethodPost, guestItemPath(x.place, x.y2026.ID, "/uploads?name=Bob.bin&size=10"), nil)
		var s sessionJSON
		res.JSON(t, &s)
		return s.ID
	}()
	notHers := map[string]string{
		"Bence's upload into Bakery": startedUpload(t, b, x.place, x.bakery.ID, "Invoice.pdf", 10).ID,
		"Bob's upload into 2026":     bobsUpload,
		"Réka's upload":              startedUpload(t, d.As(reka), rekas, 0, "Mine.pdf", 10).ID,
	}

	type try struct{ method, path, body string }
	tries := func(place int64, item string) map[string]try {
		at := func(what string) string { return fmt.Sprintf("%s/places/%d/items/%s%s", guestAPI, place, item, what) }
		return map[string]try{
			"GET /s/guest/api/places/{place}/items/{item}":          {"GET", at(""), ""},
			"GET /s/guest/api/places/{place}/items/{item}/items":    {"GET", at("/items"), ""},
			"GET /s/guest/api/places/{place}/items/{item}/download": {"GET", at("/download"), ""},
			"GET /s/guest/api/places/{place}/items/{item}/open":     {"GET", at("/open"), ""},
			"GET /s/guest/api/places/{place}/items/{item}/zip":      {"GET", at("/zip"), ""},
			"POST /s/guest/api/places/{place}/items/{item}/files":   {"POST", at("/files?name=Planted.txt&existing=replace"), "x"},
			"POST /s/guest/api/places/{place}/items/{item}/uploads": {"POST", at("/uploads?name=Planted.txt&size=1&existing=replace"), ""},
			"POST /s/guest/api/places/{place}/items/{item}/folders": {"POST", at("/folders"), `{"name":"Planted"}`},
			"POST /s/guest/api/places/{place}/items/{item}/rename":  {"POST", at("/rename"), `{"name":"Renamed"}`},
			"POST /s/guest/api/places/{place}/items/{item}/move":    {"POST", at("/move"), fmt.Sprintf(`{"to":%d}`, x.y2026.ID)},
			"DELETE /s/guest/api/places/{place}/items/{item}":       {"DELETE", at(""), ""},
		}
	}
	uploadTries := func(id string) map[string]try {
		at := fmt.Sprintf("%s/places/%d/uploads/%s", guestAPI, x.place, id)
		return map[string]try{
			"GET /s/guest/api/places/{place}/uploads/{upload}":         {"GET", at, ""},
			"PUT /s/guest/api/places/{place}/uploads/{upload}":         {"PUT", at + "?offset=0", "x"},
			"POST /s/guest/api/places/{place}/uploads/{upload}/finish": {"POST", at + "/finish?existing=replace", ""},
			"DELETE /s/guest/api/places/{place}/uploads/{upload}":      {"DELETE", at, ""},
		}
	}
	send := func(c *harness.Client, t2 try) harness.Response {
		var body *strings.Reader
		if t2.body != "" {
			body = strings.NewReader(t2.body)
			return c.Do(t, t2.method, t2.path, body)
		}
		return c.Do(t, t2.method, t2.path, nil)
	}
	if n := len(tries(0, "")) + len(uploadTries("")); n != 15 {
		t.Fatalf("the test tries %d guests' routes that take an item or an upload; internal/server/server_test.go lists 15", n)
	}
	none := tries(x.place, fmt.Sprint(nowhere))
	notGiven := map[string][2]any{
		"Clients, the folder above Bakery":   {x.place, fmt.Sprint(x.clients.ID)},
		"Price list.pdf, beside Bakery":      {x.place, fmt.Sprint(x.prices.ID)},
		"Secret.txt, given to Bob only":      {x.place, fmt.Sprint(x.secret.ID)},
		"Notes.txt, in the trash":            {x.place, fmt.Sprint(x.notes.ID)},
		"Old.pdf, deleted out of Bakery":     {x.place, fmt.Sprint(old.ID)},
		"a file of Réka's My files":          {rekas, fmt.Sprint(rekasFile.ID)},
		"Réka's file, through Bence's place": {x.place, fmt.Sprint(rekasFile.ID)},
		"a file of a shared folder":          {payroll.ID, fmt.Sprint(slip.ID)},
		"Bakery, through another place":      {payroll.ID, fmt.Sprint(x.bakery.ID)},
		"no item (0)":                        {x.place, "0"},
		"a negative id":                      {x.place, "-1"},
		"not a number":                       {x.place, "x"},
		"Clients, written with a plus":       {x.place, "+" + fmt.Sprint(x.clients.ID)},
		"a very long number":                 {x.place, strings.Repeat("9", 40)},
	}
	for route := range none {
		missing := send(a, none[route])
		for what, at := range notGiven {
			sameAs(t, fmt.Sprintf("Anna: %s with %s", route, what), send(a, tries(at[0].(int64), at[1].(string))[route]), missing)
		}
	}
	noUpload := uploadTries("0123456789abcdef0123456789abcdef")
	for route := range noUpload {
		missing := send(a, noUpload[route])
		for what, id := range notHers {
			sameAs(t, fmt.Sprintf("Anna: %s with %s", route, what), send(a, uploadTries(id)[route]), missing)
		}
	}
	// A zip of what she was given, naming items outside it: the same answer as an item that is not there.
	zipNaming := func(id int64) harness.Response {
		return a.Get(t, guestItemPath(x.place, x.bakery.ID, fmt.Sprintf("/zip?item=%d", id)))
	}
	for what, id := range map[string]int64{"Price list.pdf": x.prices.ID, "Secret.txt": x.secret.ID, "Old.pdf, in the trash": old.ID,
		"Slip.pdf": slip.ID, "Offer.pdf, below": x.offer.ID} {
		sameAs(t, "a zip of Bakery naming "+what, zipNaming(id), zipNaming(nowhere))
	}
	if got := zipEntries(t, zipNaming(x.y2026.ID).Body); len(got) != 4 || got["2026/Offer.pdf"] != "offer" {
		t.Fatalf("a zip of Bakery naming 2026 holds %q", keys(got))
	}
	// What she was given itself stays where its giver put it: renaming it (to a name taken in Clients, or free) and
	// deleting it answer as for an item that is not there.
	for _, name := range []string{"Price list.pdf", "Bakery 2026"} {
		sameAs(t, "Anna renaming Bakery to "+name, sendJSON(t, a, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/rename"),
			map[string]string{"name": name}), send(a, none["POST /s/guest/api/places/{place}/items/{item}/rename"]))
	}
	sameAs(t, "Anna deleting Bakery", a.Do(t, http.MethodDelete, guestItemPath(x.place, x.bakery.ID, ""), nil),
		send(a, none["DELETE /s/guest/api/places/{place}/items/{item}"]))
	// Moving what is in it out of it: the same answer as a folder that does not exist.
	moveTo := func(to any) harness.Response {
		return sendJSON(t, a, http.MethodPost, guestItemPath(x.place, x.y2026.ID, "/move"), map[string]any{"to": to})
	}
	noFolder := moveTo(nowhere)
	for what, to := range map[string]any{"Clients": x.clients.ID, "Private": x.private.ID, "the top folder": nil, "Réka's file": rekasFile.ID,
		"Payroll's file": slip.ID, "Old.pdf, in the trash": old.ID} {
		if res := moveTo(to); res.Status != noFolder.Status || !bytes.Equal(res.Body, noFolder.Body) || res.Status != http.StatusNotFound {
			t.Errorf("Anna moving 2026 into %s: got %d %s", what, res.Status, res.Body)
		}
	}
	// Nor into another place, naming it: a guest's move has no place.
	if res := sendJSON(t, a, http.MethodPost, guestItemPath(x.place, x.y2026.ID, "/move"), map[string]any{"to": nil, "place": payroll.ID}); res.Status != http.StatusNotFound {
		t.Errorf("Anna moving 2026 into Payroll: got %d %s", res.Status, res.Body)
	}

	// Routes a guest does not have: no search, no trash, no top folder, no sharing, no links, no members.
	for _, path := range []string{"/search?q=Secret", fmt.Sprintf("/places/%d/trash", x.place), fmt.Sprintf("/places/%d/items", x.place),
		fmt.Sprintf("/places/%d/items/%d/access", x.place, x.bakery.ID), fmt.Sprintf("/places/%d/items/%d/link", x.place, x.bakery.ID),
		fmt.Sprintf("/places/%d/trash/%d/restore", x.place, old.ID), "/members", "/places", "/usage",
		fmt.Sprintf("/places/%d/zip", x.place)} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
			if res := a.Do(t, method, guestAPI+path, strings.NewReader(`{"role":"editor"}`)); res.Status != http.StatusNotFound && res.Status != http.StatusMethodNotAllowed {
				t.Errorf("Anna: %s %s: got %d %s", method, path, res.Status, res.Body)
			}
		}
	}
	// The members' routes refuse her, with her cookie or with a member's id; and the visitors' see only a link.
	for _, path := range []string{"/api/places", "/api/me", "/api/members", "/api/shared-with-me", "/api/search?q=Secret",
		fmt.Sprintf("/api/places/%d/items/%d/items", x.place, x.bakery.ID), fmt.Sprintf("/api/places/%d/items/%d/access", x.place, x.bakery.ID),
		"/s/guest/../../api/places", "/S/guest/api/me"} {
		for what, c := range map[string]*harness.Client{"Anna": a, "Anna naming Bence": a.With("Remote-User-Id", bence.UserID)} {
			if res := c.Get(t, path); res.Status != http.StatusForbidden {
				t.Errorf("%s asking for %s: got %d %.200s, want 403", what, path, res.Status, res.Body)
			}
		}
	}
	// Path tricks: the clean path decides.
	for _, trick := range []string{
		guestItemPath(x.place, x.bakery.ID, fmt.Sprintf("/../%d/download", x.prices.ID)),
		guestItemPath(x.place, x.bakery.ID, fmt.Sprintf("/items/../../%d/items", x.clients.ID)),
		fmt.Sprintf("%s/places/%d/items/%d%%2F..%%2F%d/items", guestAPI, x.place, x.bakery.ID, x.clients.ID),
		fmt.Sprintf("%s/places/%d/items//%d/items", guestAPI, x.place, x.clients.ID),
	} {
		if res := a.Get(t, trick); res.Status != http.StatusNotFound || strings.Contains(string(res.Body), "Price list") || strings.Contains(string(res.Body), "prices") {
			t.Errorf("Anna asking for %s: got %d %.200s", trick, res.Status, res.Body)
		}
	}

	// What she sees: Bakery and what is in it, from Bakery down, and nothing about anyone but who shared it with her.
	shared, body := guestShared(t, a)
	if len(shared) != 1 || shared[0].ID != x.bakery.ID || shared[0].SharedBy != "Bence Tóth" || shared[0].Role != "editor" || shared[0].Until != nil {
		t.Fatalf("Anna's Shared with me: %s", body)
	}
	l := guestList(t, a, x.place, x.y2026.ID)
	if !slices.Equal(pathNames(l.Path), []string{"Bakery", "2026"}) || l.Role != "editor" ||
		!slices.Equal(guestNames(l.Items), []string{"From Bob.pdf", "Ledger.xlsx", "Offer.pdf"}) {
		t.Fatalf("2026, as Anna sees it: %+v", l)
	}
	item := a.Get(t, guestItemPath(x.place, x.offer.ID, ""))
	for what, res := range map[string]harness.Response{"Shared with me": {Body: []byte(body)}, "2026": guestOpened(t, a, x.place, x.y2026.ID),
		"Bakery": guestOpened(t, a, x.place, x.bakery.ID), "Offer.pdf": item} {
		for _, secret := range []string{"Clients", "Price list", "Private", "My files", bence.Email, bence.UserID, bob, dora.Email,
			dora.UserID, "Dóra", "seenBy", "addedBy", "guests", "members"} {
			if strings.Contains(string(res.Body), secret) {
				t.Errorf("%s, as Anna sees it, says %q: %s", what, secret, res.Body)
			}
		}
	}
	if !strings.Contains(string(item.Body), `"path":[{"id":`+fmt.Sprint(x.bakery.ID)+`,"name":"Bakery"},{"id":`+fmt.Sprint(x.y2026.ID)+`,"name":"2026"}]`) {
		t.Fatalf("Offer.pdf, as Anna sees it: %s", item.Body)
	}

	// Nothing changed: on the storage, Clients and Bakery are as they were.
	if got := onStorage(t, d, "My files/bence@example.com/Clients"); !slices.Equal(got, []string{"Bakery/", "Price list.pdf"}) {
		t.Errorf("Clients on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery/2026"); !slices.Equal(got, []string{"From Bob.pdf", "Ledger.xlsx", "Offer.pdf"}) {
		t.Errorf("2026 on the storage: %q", got)
	}
}

// A guest editor works inside what was shared with them as a member editor does: uploads (in one request, and resumable),
// makes folders, renames, moves inside it and deletes into its owner's trash; a guest viewer opens and downloads, and
// nothing more. What a guest added is labelled with their address for members (an upload over it by a member takes the
// label off), and how often each invitation was used, and when last, is kept for whoever may share the item.
func TestAGuestWorksInsideWhatWasSharedAsTheirRoleAllows(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	invited(t, b, x.place, x.bakery.ID, anna, "editor", nil)
	invited(t, b, x.place, x.bakery.ID, cleo, "viewer", nil)
	ok(t, "sharing Bakery with Dóra as viewer", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	a, c := withCookie(d, signedIn(t, d, smtp, anna)), withCookie(d, signedIn(t, d, smtp, cleo))
	before := time.Now()

	// Anna uploads a file into Bakery, in one request, and a larger one in parts into 2026.
	res := guestUpload(t, a, x.place, x.bakery.ID, "Brief.pdf", []byte("brief"), "")
	var brief guestItemJSON
	res.JSON(t, &brief)
	if res.Status != http.StatusCreated || brief.Name != "Brief.pdf" {
		t.Fatalf("Anna uploading Brief.pdf: %d %s", res.Status, res.Body)
	}
	content := randomBytes(t, 3000)
	res = a.Do(t, http.MethodPost, guestItemPath(x.place, x.y2026.ID, "/uploads?name=Scan.bin&size=3000"), nil)
	var s sessionJSON
	res.JSON(t, &s)
	if res.Status != http.StatusCreated {
		t.Fatalf("Anna starting an upload: %d %s", res.Status, res.Body)
	}
	part := fmt.Sprintf("%s/places/%d/uploads/%s", guestAPI, x.place, s.ID)
	if res := a.Do(t, http.MethodPut, part+"?offset=0", bytes.NewReader(content)); res.Status != http.StatusOK {
		t.Fatalf("Anna sending a part: %d %s", res.Status, res.Body)
	}
	if res := a.Get(t, part); res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"offset":3000`) {
		t.Fatalf("where Anna's upload stands: %d %s", res.Status, res.Body)
	}
	if res := a.Do(t, http.MethodPost, part+"/finish", nil); res.Status != http.StatusCreated {
		t.Fatalf("Anna finishing her upload: %d %s", res.Status, res.Body)
	}
	// A folder, a rename, a move inside Bakery, a delete.
	res = sendJSON(t, a, http.MethodPost, guestItemPath(x.place, x.bakery.ID, "/folders"), map[string]string{"name": "From Anna"})
	var folder guestItemJSON
	res.JSON(t, &folder)
	if res.Status != http.StatusCreated {
		t.Fatalf("Anna making a folder: %d %s", res.Status, res.Body)
	}
	ok200 := func(what string, res harness.Response, want int) {
		t.Helper()
		if res.Status != want {
			t.Fatalf("%s: got %d %s, want %d", what, res.Status, res.Body, want)
		}
	}
	ok200("Anna renaming Brief.pdf", sendJSON(t, a, http.MethodPost, guestItemPath(x.place, brief.ID, "/rename"), map[string]string{"name": "Brief 2026.pdf"}), http.StatusOK)
	ok200("Anna moving it into her folder", sendJSON(t, a, http.MethodPost, guestItemPath(x.place, brief.ID, "/move"), map[string]any{"to": folder.ID}), http.StatusOK)
	ok200("Anna deleting Ledger.xlsx", a.Do(t, http.MethodDelete, guestItemPath(x.place, x.ledger.ID, ""), nil), http.StatusNoContent)
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery"); !slices.Equal(got, []string{"2026/", "From Anna/"}) {
		t.Fatalf("Bakery on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery/From Anna"); !slices.Equal(got, []string{"Brief 2026.pdf"}) {
		t.Fatalf("Anna's folder on the storage: %q", got)
	}
	if got := onStorage(t, d, "My files/bence@example.com/Clients/Bakery/2026"); !slices.Equal(got, []string{"Offer.pdf", "Scan.bin"}) {
		t.Fatalf("2026 on the storage: %q", got)
	}
	// What she deleted is in Bence's trash, his to restore; she has no trash.
	if trash := trashOf(t, b, x.place); len(trash) != 1 || trash[0].Name != "Ledger.xlsx" {
		t.Fatalf("Bence's trash: %+v", trash)
	}
	if res := a.Do(t, http.MethodPost, fmt.Sprintf("%s/places/%d/trash/%d/restore", guestAPI, x.place, x.ledger.ID), nil); res.Status != http.StatusNotFound {
		t.Fatalf("Anna restoring Ledger.xlsx: %d %s", res.Status, res.Body)
	}
	restored(t, b, x.place, x.ledger.ID)

	// Members see what she added as hers; she, and Cleo, do not see that.
	labels := func(c *harness.Client, folderID int64) map[string]string {
		out := map[string]string{}
		for _, it := range list(t, c, x.place, folderID).Items {
			if it.AddedBy != nil {
				out[it.Name] = it.AddedBy.Guest
			}
		}
		return out
	}
	if got := labels(b, x.bakery.ID); !mapsEqual(got, map[string]string{"From Anna": anna}) {
		t.Fatalf("labels in Bakery, as Bence sees them: %v", got)
	}
	if got := labels(d.As(dora), x.y2026.ID); !mapsEqual(got, map[string]string{"Scan.bin": anna}) {
		t.Fatalf("labels in 2026, as Dóra sees them: %v", got)
	}
	if got := labels(b, folder.ID); !mapsEqual(got, map[string]string{"Brief 2026.pdf": anna}) {
		t.Fatalf("labels in Anna's folder: %v", got)
	}
	for _, guest := range []*harness.Client{a, c} {
		if res := guestOpened(t, guest, x.place, x.y2026.ID); strings.Contains(string(res.Body), anna) || strings.Contains(string(res.Body), "addedBy") {
			t.Fatalf("a guest's list of 2026: %s", res.Body)
		}
	}
	// An upload over her file by Bence takes the label off: the content is his now.
	if res := uploadInto(t, b, x.place, folder.ID, "Brief 2026.pdf", []byte("bence's brief"), "replace"); res.Status != http.StatusCreated {
		t.Fatalf("Bence replacing Anna's brief: %d %s", res.Status, res.Body)
	}
	if got := labels(b, folder.ID); len(got) != 0 {
		t.Fatalf("labels in Anna's folder after Bence's upload: %v", got)
	}

	// She downloads, opens and zips as anyone with access does.
	if res := a.Get(t, guestItemPath(x.place, x.offer.ID, "/download")); res.Status != http.StatusOK || string(res.Body) != "offer" || savedAs(res) != "Offer.pdf" {
		t.Fatalf("Anna downloading Offer.pdf: %d %s", res.Status, res.Body)
	}
	if res := a.Get(t, guestItemPath(x.place, x.offer.ID, "/open")); res.Header.Get("Content-Type") != "application/pdf" ||
		res.Header.Get("Content-Security-Policy") != sandboxedPolicy {
		t.Fatalf("Anna opening Offer.pdf: %d %v", res.Status, res.Header)
	}
	if got := zipEntries(t, a.Get(t, guestItemPath(x.place, x.bakery.ID, "/zip")).Body); got["Bakery/2026/Offer.pdf"] != "offer" || len(got) != 7 {
		t.Fatalf("Bakery.zip, as Anna gets it: %q", keys(got))
	}

	// Cleo, a viewer, opens and downloads, and does nothing else: the same answer as for an item that is not there.
	if l := guestList(t, c, x.place, x.bakery.ID); l.Role != "viewer" || !slices.Equal(l.Can, []string{"list-as-guest", "download-as-guest"}) {
		t.Fatalf("Bakery, as Cleo sees it: %+v", l)
	}
	if res := c.Get(t, guestItemPath(x.place, x.offer.ID, "/download")); res.Status != http.StatusOK {
		t.Fatalf("Cleo downloading Offer.pdf: %d", res.Status)
	}
	nowhere := x.notes.ID + 1000
	for what, try := range map[string]func(int64) harness.Response{
		"uploading": func(id int64) harness.Response { return guestUpload(t, c, x.place, id, "Cleo.txt", []byte("c"), "") },
		"starting one": func(id int64) harness.Response {
			return c.Do(t, http.MethodPost, guestItemPath(x.place, id, "/uploads?name=C&size=1"), nil)
		},
		"making a folder": func(id int64) harness.Response {
			return sendJSON(t, c, http.MethodPost, guestItemPath(x.place, id, "/folders"), map[string]string{"name": "C"})
		},
		"renaming": func(id int64) harness.Response {
			return sendJSON(t, c, http.MethodPost, guestItemPath(x.place, id, "/rename"), map[string]string{"name": "C"})
		},
		"moving": func(id int64) harness.Response {
			return sendJSON(t, c, http.MethodPost, guestItemPath(x.place, id, "/move"), map[string]any{"to": x.bakery.ID})
		},
		"deleting": func(id int64) harness.Response {
			return c.Do(t, http.MethodDelete, guestItemPath(x.place, id, ""), nil)
		},
	} {
		target := x.y2026.ID
		sameAs(t, "Cleo "+what, try(target), try(nowhere))
	}

	// Each invitation's uses, and when it was last used, for whoever may share the item; and they are kept.
	line := guestLine(accessOf(t, b, itemAccessPath(x.place, x.bakery.ID)), anna)
	if line == nil || line.Uses == nil || *line.Uses < 10 || line.Used == nil || line.Used.Before(before.Add(-time.Second)) || time.Since(*line.Used) > time.Minute {
		t.Fatalf("Anna's invitation after her work: %+v", line)
	}
	if l := guestLine(accessOf(t, d.As(dora), itemAccessPath(x.place, x.bakery.ID)), anna); l == nil || l.Uses != nil || l.Used != nil {
		t.Fatalf("Anna's invitation, as Dóra, a viewer, sees it: %+v", l)
	}
	uses := *line.Uses
	d.Restart(t, nil)
	if after := guestLine(accessOf(t, d.As(bence), itemAccessPath(x.place, x.bakery.ID)), anna); after == nil || *after.Uses != uses || !after.Used.Equal(*line.Used) {
		t.Fatalf("Anna's invitation after a restart: %+v, want %d uses", after, uses)
	}
}

// Guests are outsiders: their transfers share the eight slots visitors may hold, in the outsiders' own line, and
// members keep the other 24.
func TestAGuestsTransfersAreAnOutsidersTurns(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka}, Env: mailSettings(smtp)})
	placeID, _ := myFiles(t, d, reka)
	r := d.As(reka)
	films := madeFolder(t, r, placeID, 0, "Films")
	big := uploadedInto(t, r, placeID, films.ID, "Big.mp4", randomBytes(t, 16<<20))
	_, page := madeLink(t, d, r, placeID, films.ID, false)
	invited(t, r, placeID, films.ID, anna, "viewer", nil)
	cookie := signedIn(t, d, smtp, anna)
	asGuest := fmt.Sprintf("Cookie: %s=%s\r\n", cookie.Name, cookie.Value)

	// Four visitors and four of Anna's downloads take the outsiders' eight; a ninth of hers waits, though 24 are free.
	for i := range 8 {
		var status int
		if i%2 == 0 {
			_, status = heldVisitorDownload(t, d, inLink(page, big.ID, "/download"), 5*time.Second)
		} else {
			_, status = heldRequest(t, d, asGuest, guestItemPath(placeID, big.ID, "/download"), 5*time.Second)
		}
		if status != http.StatusOK {
			t.Fatalf("outsider's transfer %d: got %d", i+1, status)
		}
	}
	ninth, _ := waitingFor(func() (net.Conn, int) {
		return heldRequest(t, d, asGuest, guestItemPath(placeID, big.ID, "/download"), 20*time.Second)
	})
	time.Sleep(time.Second)
	stillWaiting(t, "a ninth outsider's download, Anna's", ninth)
	// Her upload is told to wait its turn, and keeps its place.
	invited(t, r, placeID, films.ID, anna, "editor", nil)
	res := guestUpload(t, withCookie(d, cookie), placeID, films.ID, "Mine.txt", []byte("x"), "")
	if res.Status != http.StatusServiceUnavailable || !strings.Contains(string(res.Body), `"busy":true`) {
		t.Fatalf("Anna's upload with the outsiders' slots taken: %d %s", res.Status, res.Body)
	}
	// A member's download goes at once.
	if _, status := heldDownload(t, d, reka, downloadPath(placeID, big.ID), 5*time.Second); status != http.StatusOK {
		t.Fatalf("a member's download with the outsiders' slots taken: %d", status)
	}
}
