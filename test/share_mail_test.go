package test

import (
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	netmail "net/mail"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// mailSettings point a Drive at a throwaway mail server, which needs no TLS and no login.
func mailSettings(s *mailtest.Server) map[string]string {
	return map[string]string{
		"DRIVE_SMTP_HOST": s.Host, "DRIVE_SMTP_PORT": strconv.Itoa(s.Port), "DRIVE_SMTP_TLS": "none",
		"DRIVE_MAIL_FROM": "Kovács Accounting Drive <drive@drive.test>",
	}
}

// mailed is a message as a mail program shows it: to whom it went, its headers, its subject and its body, decoded.
type mailed struct {
	to      []string
	header  netmail.Header
	subject string
	body    string
}

func readMail(t *testing.T, m mailtest.Message) mailed {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(m.Data))
	if err != nil {
		t.Fatalf("a mail that cannot be read (%v):\n%s", err, m.Data)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("a subject that cannot be read (%v): %q", err, msg.Header.Get("Subject"))
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	return mailed{to: m.To, header: msg.Header, subject: subject, body: strings.ReplaceAll(string(body), "\r\n", "\n")}
}

// quietly waits a moment and fails the test if the mail server took more than n messages: what must not be sent.
func quietly(t *testing.T, s *mailtest.Server, n int) []mailtest.Message {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	got := s.Messages()
	if len(got) != n {
		t.Fatalf("the mail server took %d messages, want %d", len(got), n)
	}
	return got
}

// A member gets a mail when something is shared with them by name: plain and short, with who shared what and a link to
// it at the Drive's address. Sharing with everyone in the project, changing a role, and giving oneself access send
// none.
func TestASharedWithYouMailGoesToTheMemberSharedWithByName(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence)
	x := bencesFiles(t, d)
	if !strings.Contains(d.Logs(), net.JoinHostPort(smtp.Host, strconv.Itoa(smtp.Port))+" (none)") {
		t.Fatalf("the start-up line does not say where mail goes:\n%s", d.Logs())
	}

	// A folder: the link opens it.
	ok(t, "sharing Clients with Dóra", share(t, b, x.place, x.clients.ID, dora, "viewer"))
	got := readMail(t, smtp.WaitFor(t, 1)[0])
	if !slices.Equal(got.to, []string{"dora@example.com"}) || got.header.Get("To") != "<dora@example.com>" {
		t.Fatalf("the mail went to %q (To: %q)", got.to, got.header.Get("To"))
	}
	if from, err := netmail.ParseAddress(got.header.Get("From")); err != nil || from.Address != "drive@drive.test" || from.Name != "Kovács Accounting Drive" {
		t.Fatalf("From: %q (%v)", got.header.Get("From"), err)
	}
	if got.subject != "Bence Tóth shared “Clients” with you" {
		t.Fatalf("subject %q", got.subject)
	}
	wantBody := fmt.Sprintf("Bence Tóth shared the folder “Clients” with you in the Drive of Kovács Accounting.\n"+
		"You can open and download it.\n\nOpen it: http://drive.test/?folder=%d&place=%d\n", x.clients.ID, x.place)
	if got.body != wantBody {
		t.Fatalf("the body:\n%s\nwant:\n%s", got.body, wantBody)
	}

	// A file she can open only on its own: the link is "Shared with me", which lists it.
	ok(t, "sharing Notes.txt with Dóra", share(t, b, x.place, x.notes.ID, dora, "editor"))
	got = readMail(t, smtp.WaitFor(t, 2)[1])
	if got.subject != "Bence Tóth shared “Notes.txt” with you" || !strings.Contains(got.body, "shared the file “Notes.txt”") ||
		!strings.Contains(got.body, "As an editor you can also") || !strings.HasSuffix(got.body, "Open it: http://drive.test/?shared\n") {
		t.Fatalf("the mail about Notes.txt: %q\n%s", got.subject, got.body)
	}
	// A file in a folder she can open: the link is that folder.
	ok(t, "sharing Offer.pdf with Dóra", share(t, b, x.place, x.offer.ID, dora, "editor"))
	got = readMail(t, smtp.WaitFor(t, 3)[2])
	if want := fmt.Sprintf("Open it: http://drive.test/?folder=%d&place=%d\n", x.y2026.ID, x.place); !strings.HasSuffix(got.body, want) {
		t.Fatalf("the mail about Offer.pdf:\n%s\nwant it to end %q", got.body, want)
	}
	// A shared folder as a whole: its name, and the link opens it.
	team := madeSharedFolder(t, b, "Team")
	ok(t, "making Dóra a manager of Team", makeManager(t, b, team.ID, dora))
	got = readMail(t, smtp.WaitFor(t, 4)[3])
	if got.subject != "Bence Tóth shared “Team” with you" || !strings.Contains(got.body, "shared the shared folder “Team”") ||
		!strings.Contains(got.body, "As a manager") || !strings.HasSuffix(got.body, fmt.Sprintf("Open it: http://drive.test/?place=%d\n", team.ID)) {
		t.Fatalf("the mail about Team: %q\n%s", got.subject, got.body)
	}

	// No mail: a role changed, given again, given to everyone, given to oneself (Réka, a project admin, takes a role on
	// Team), or taken away.
	ok(t, "making Dóra an editor of Clients", share(t, b, x.place, x.clients.ID, dora, "editor"))
	ok(t, "making Dóra an editor of Clients again", share(t, b, x.place, x.clients.ID, dora, "editor"))
	ok(t, "sharing Private with everyone", shareWithEveryone(t, b, x.place, x.private.ID, "viewer"))
	ok(t, "Réka giving herself manager on Team", makeManager(t, d.As(reka), team.ID, reka))
	ok(t, "taking Notes.txt away from Dóra", unshare(t, b, x.place, x.notes.ID, dora))
	quietly(t, smtp, 4)
	// Given again after it was taken away, it is a new share.
	ok(t, "sharing Notes.txt with Dóra again", share(t, b, x.place, x.notes.ID, dora, "viewer"))
	got = readMail(t, smtp.WaitFor(t, 5)[4])
	if got.subject != "Bence Tóth shared “Notes.txt” with you" {
		t.Fatalf("subject %q", got.subject)
	}
	// A mail never names a folder above what was shared, nor a shared folder it is in.
	payroll := madeSharedFolder(t, b, "Payroll")
	slips := madeFolder(t, b, payroll.ID, 0, "Slips")
	ok(t, "sharing Slips with Dóra", share(t, b, payroll.ID, slips.ID, dora, "viewer"))
	got = readMail(t, smtp.WaitFor(t, 6)[5])
	if strings.Contains(got.subject+got.body, "Payroll") || !strings.Contains(got.body, "“Slips”") {
		t.Fatalf("the mail about Slips names more than it may: %q\n%s", got.subject, got.body)
	}
	// Nothing went to anyone else.
	for _, m := range smtp.Messages() {
		if !slices.Equal(m.To, []string{"dora@example.com"}) || m.From != "drive@drive.test" {
			t.Fatalf("a mail from %q to %q", m.From, m.To)
		}
	}
}

// Names come from people: from the member list, and file names from whoever uploads. None of it can add a header or a
// recipient to the mail; it reaches the reader as it was written, line breaks aside.
func TestNamesCannotChangeWhereTheMailGoes(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	sly := harness.Member{UserID: "u-sly", Email: "sly@example.com", DisplayName: "Sly\r\nBcc: evil@example.com\r\n\r\nInjected body", Role: "member"}
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, dora, sly}, Env: mailSettings(smtp)})
	// The gateway's own identity headers cannot carry a line break; the Drive reads the name from the member list.
	s := d.WithHeaders(http.Header{"Drive-Secret": {d.Secret}, "Remote-User-Id": {sly.UserID}}).From(d.Address)
	res := s.Get(t, "/api/places")
	var places placesJSON
	res.JSON(t, &places)
	place := places.Places[0].ID
	file := uploaded(t, s, place, "=?utf-8?q?Fake?= “quoted” Árvíztűrő.pdf", []byte("x"))
	ok(t, "Sly sharing the file with Dóra", share(t, s, place, file.ID, dora, "viewer"))
	m := smtp.WaitFor(t, 1)[0]
	got := readMail(t, m)
	if !slices.Equal(got.to, []string{"dora@example.com"}) || got.header.Get("Bcc") != "" || len(got.header["To"]) != 1 {
		t.Fatalf("the mail went to %q, with headers %v", got.to, got.header)
	}
	if want := "Sly  Bcc: evil@example.com    Injected body shared “=?utf-8?q?Fake?= “quoted” Árvíztűrő.pdf” with you"; got.subject != want {
		t.Fatalf("subject %q, want %q", got.subject, want)
	}
	if strings.Contains(got.body, "\nInjected body") || strings.Contains(got.body, "\nBcc") || !strings.HasPrefix(got.body, "Sly  Bcc: evil@example.com    Injected body shared the file") {
		t.Fatalf("the name broke a line:\n%s", got.body)
	}
	// Every header line is one header or a folded part of one.
	head, _, _ := strings.Cut(m.Data, "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		name, _, isHeader := strings.Cut(line, ":")
		if !strings.HasPrefix(line, " ") && (!isHeader || !slices.Contains([]string{"From", "To", "Subject", "Date", "Message-ID",
			"MIME-Version", "Content-Type", "Content-Transfer-Encoding", "Auto-Submitted"}, name)) {
			t.Fatalf("a header line %q:\n%s", line, head)
		}
	}
}

// Without mail settings the Drive sends no mail and shares as always; with a mail server that has hung, or is not
// there, sharing is not held up, and the failure is logged without the mail's text.
func TestSharingNeverWaitsForMail(t *testing.T) {
	t.Parallel()
	// No mail settings: nothing is attempted.
	off := harness.Start(t, reka, bence, dora)
	x := bencesFiles(t, off)
	ok(t, "sharing Clients with Dóra, without mail", share(t, off.As(bence), x.place, x.clients.ID, dora, "viewer"))
	if l := list(t, off.As(dora), x.place, x.clients.ID); l.Role != "viewer" {
		t.Fatalf("Dóra's Clients: %+v", l)
	}
	time.Sleep(300 * time.Millisecond)
	if logs := off.Logs(); !strings.Contains(logs, "mail=off") || strings.Contains(logs, "mail could not") || strings.Contains(logs, "mail was not") {
		t.Fatalf("the log of a Drive without mail:\n%s", logs)
	}

	// A mail server that takes the connection and never answers.
	hung := mailtest.Start(t, mailtest.Options{})
	hung.Silent(true)
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(hung)})
	y := bencesFiles(t, d)
	start := time.Now()
	ok(t, "sharing Clients with Dóra, the mail server hung", share(t, d.As(bence), y.place, y.clients.ID, dora, "viewer"))
	ok(t, "sharing Notes.txt with Dóra, the mail server hung", share(t, d.As(bence), y.place, y.notes.ID, dora, "viewer"))
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("two shares took %v with the mail server hung", took)
	}
	if l := list(t, d.As(dora), y.place, y.clients.ID); l.Role != "viewer" {
		t.Fatalf("Dóra's Clients: %+v", l)
	}

	// A mail server that is not there: the share stands, and the log says the mail failed, about whom, and nothing of it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	gone := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: map[string]string{
		"DRIVE_SMTP_HOST": "127.0.0.1", "DRIVE_SMTP_PORT": strconv.Itoa(port), "DRIVE_SMTP_TLS": "none", "DRIVE_MAIL_FROM": "drive@drive.test"}})
	z := bencesFiles(t, gone)
	ok(t, "sharing Clients with Dóra, no mail server there", share(t, gone.As(bence), z.place, z.clients.ID, dora, "viewer"))
	harness.Eventually(t, "the failed mail is logged", func() bool { return strings.Contains(gone.Logs(), "a mail could not be sent") })
	logs := gone.Logs()
	if !strings.Contains(logs, "recipient="+dora.UserID) {
		t.Fatalf("the log does not say whom the mail was for:\n%s", logs)
	}
	for _, text := range []string{"dora@example.com", "shared the folder", "Open it", "?place="} {
		if strings.Contains(logs, text) {
			t.Fatalf("the log holds %q of the mail:\n%s", text, logs)
		}
	}
	if l := list(t, gone.As(dora), z.place, z.clients.ID); l.Role != "viewer" {
		t.Fatalf("Dóra's Clients: %+v", l)
	}
}

// Mail settings with a mistake in them keep the Drive from starting, and say which.
func TestTheDriveDoesNotStartWithBrokenMailSettings(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		env  map[string]string
		name string
	}{
		{map[string]string{"DRIVE_SMTP_HOST": "smtp.example.com"}, "DRIVE_MAIL_FROM"},
		{map[string]string{"DRIVE_MAIL_FROM": "drive@drive.test"}, "DRIVE_SMTP_HOST"},
		{map[string]string{"DRIVE_SMTP_HOST": "smtp.example.com", "DRIVE_MAIL_FROM": "drive@drive.test", "DRIVE_SMTP_PORT": "five"}, "DRIVE_SMTP_PORT"},
	} {
		out := harness.StartFailing(t, harness.Options{Members: []harness.Member{reka}, Env: c.env})
		if !strings.Contains(out, c.name) {
			t.Errorf("with %q the Drive did not name %s:\n%s", c.env, c.name, out)
		}
	}
}
