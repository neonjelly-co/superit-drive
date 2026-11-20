package mail

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/internal/settings"
)

var from = &netmail.Address{Name: "Superit Drive · Kovács", Address: "drive@files.example.com"}

// read parses a message as a mail program would: its headers, the subject decoded, the body decoded.
func read(t *testing.T, data string) (netmail.Header, string, string) {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatalf("the message cannot be read: %v\n%s", err, data)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("the subject cannot be decoded: %v", err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("the body cannot be decoded: %v", err)
	}
	return msg.Header, subject, string(body)
}

// Names and file names come from people. Nothing in them can end a header, add one, or add a recipient; what they say
// still reaches the reader, in any alphabet, and no line is too long for a mail server.
func TestAMessageIsSafeWhateverTheNamesHoldIn(t *testing.T) {
	subject := "Réka\r\nBcc: evil@example.com\r\n\r\nshared “" + strings.Repeat("Árvíztűrő tükörfúrógép ", 12) + " .pdf” with you\x00"
	body := "Réka shared it with you.\n.\nOpen it: https://files.example.com/?place=3&folder=12\n" + strings.Repeat("é", 200) + "\n"
	data := string(Compose(from, "dora@example.com", subject, body, time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)))

	for _, line := range strings.SplitAfter(data, "\r\n") {
		if strings.Contains(strings.TrimSuffix(line, "\r\n"), "\n") || strings.Contains(strings.TrimSuffix(line, "\r\n"), "\r") {
			t.Fatalf("a line ends without CRLF: %q", line)
		}
		if len(line) > 200 {
			t.Fatalf("a line is %d bytes long: %q", len(line), line)
		}
	}
	h, gotSubject, gotBody := read(t, data)
	var names []string
	for name := range h {
		names = append(names, name)
	}
	slices.Sort(names)
	if want := []string{"Auto-Submitted", "Content-Transfer-Encoding", "Content-Type", "Date", "From", "Message-Id", "Mime-Version", "Subject", "To"}; !slices.Equal(names, want) {
		t.Fatalf("headers %q, want %q", names, want)
	}
	if want := Clean(subject); gotSubject != want || strings.ContainsAny(gotSubject, "\r\n\x00 ") || !strings.Contains(gotSubject, "Bcc: evil@example.com") {
		t.Fatalf("the subject reads %q, want %q", gotSubject, want)
	}
	if h.Get("To") != "<dora@example.com>" {
		t.Fatalf("To: %q", h.Get("To"))
	}
	if a, err := netmail.ParseAddress(h.Get("From")); err != nil || a.Name != from.Name || a.Address != from.Address {
		t.Fatalf("From: %q (%v)", h.Get("From"), err)
	}
	if want := strings.ReplaceAll(body, "\n", "\r\n"); gotBody != want {
		t.Fatalf("the body reads %q, want %q", gotBody, want)
	}
	if !strings.HasSuffix(h.Get("Message-Id"), "@files.example.com>") || h.Get("Date") != "Thu, 19 Nov 2026 10:00:00 +0000" {
		t.Fatalf("Message-ID %q, Date %q", h.Get("Message-Id"), h.Get("Date"))
	}
}

func TestRecipientIsOnePlainAddress(t *testing.T) {
	if got, err := Recipient("dora@example.com"); err != nil || got != "dora@example.com" {
		t.Fatalf("a plain address: %q, %v", got, err)
	}
	for _, bad := range []string{"", "dora", "Dóra <dora@example.com>", "dora@example.com, evil@example.com",
		"dora@example.com\r\nRCPT TO:<evil@example.com>", "<dora@example.com>", " dora@example.com"} {
		if _, err := Recipient(bad); err == nil {
			t.Errorf("%q passed as a recipient", bad)
		}
	}
}

// A certificate for 127.0.0.1, and a TLS configuration on each side.
func tlsPair(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		&tls.Config{RootCAs: roots, ServerName: "127.0.0.1"}
}

// logs collects what a Mailer logs.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.Write(p) }
func (l *logs) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

// running is a Mailer for the mail server s, sending until the test ends.
func running(t *testing.T, s settings.Mail, client *tls.Config, timeout time.Duration) (*Mailer, *logs) {
	t.Helper()
	l := &logs{}
	m := New(s, "files.example.com", slog.New(slog.NewTextHandler(l, nil)))
	m.tls, m.timeout = client, timeout
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return m, l
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var message = Message{To: "dora@example.com", Subject: "Réka shared “Offer.pdf” with you", Body: "Open it: https://files.example.com/?shared\n",
	About: []any{"mail", "shared with you", "recipient", "u-dora"}}

// Through STARTTLS and with a login, as on port 587; and with TLS from the first byte, as on port 465.
func TestSendsOverTLSAndSignsIn(t *testing.T) {
	server, client := tlsPair(t)
	for _, implicit := range []bool{false, true} {
		smtp := mailtest.Start(t, mailtest.Options{TLS: server, Implicit: implicit, User: "drive", Password: "pw"})
		s := settings.Mail{Host: smtp.Host, Port: smtp.Port, TLS: settings.StartTLS, User: "drive", Password: "pw", From: from}
		if implicit {
			s.TLS = settings.ImplicitTLS
		}
		m, l := running(t, s, client, 5*time.Second)
		m.Send(message)
		got := smtp.WaitFor(t, 1)
		if !got[0].TLS || !got[0].SignedIn || got[0].From != from.Address || !slices.Equal(got[0].To, []string{"dora@example.com"}) {
			t.Fatalf("implicit TLS %v: the message came as %+v; log: %s", implicit, got[0], l)
		}
		_, subject, body := read(t, got[0].Data)
		if subject != message.Subject || body != "Open it: https://files.example.com/?shared\r\n" {
			t.Fatalf("implicit TLS %v: subject %q, body %q", implicit, subject, body)
		}
	}
}

// A mail server that does not offer STARTTLS gets nothing, not even the login: the failure is logged, with what the
// message was about and nothing of its text.
func TestNothingIsSentWithoutTheEncryptionAskedFor(t *testing.T) {
	smtp := mailtest.Start(t, mailtest.Options{})
	m, l := running(t, settings.Mail{Host: smtp.Host, Port: smtp.Port, TLS: settings.StartTLS, User: "drive", Password: "pw", From: from}, nil, 5*time.Second)
	m.Send(message)
	eventually(t, "the failure is logged", func() bool { return strings.Contains(l.String(), "STARTTLS") })
	if got := smtp.Messages(); len(got) != 0 {
		t.Fatalf("the server took %+v", got)
	}
	for _, secret := range []string{"Offer.pdf", "files.example.com/?shared", "dora@example.com", "pw"} {
		if strings.Contains(l.String(), secret) {
			t.Fatalf("the log holds %q: %s", secret, l)
		}
	}
	if !strings.Contains(l.String(), "recipient=u-dora") {
		t.Fatalf("the log does not say what the mail was about: %s", l)
	}
}

// Sending never waits for the mail server: a hung one holds up only the sender in the background, and what does not
// fit in the queue meanwhile is dropped and logged.
func TestSendingNeverWaitsForTheMailServer(t *testing.T) {
	smtp := mailtest.Start(t, mailtest.Options{})
	smtp.Silent(true)
	m, l := running(t, settings.Mail{Host: smtp.Host, Port: smtp.Port, TLS: settings.NoTLS, From: from}, nil, 500*time.Millisecond)
	start := time.Now()
	for range QueueSize + 5 {
		m.Send(message)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("queuing took %v", took)
	}
	eventually(t, "the hung server is given up on", func() bool { return strings.Contains(l.String(), "could not be sent") })
	if !strings.Contains(l.String(), "too many are waiting") {
		t.Fatalf("nothing was dropped: %s", l)
	}
	// Once it answers again, what is queued goes.
	smtp.Silent(false)
	smtp.WaitFor(t, 1)
}

// Without a mail server, nothing is queued and nothing is attempted.
func TestWithoutAMailServerNothingIsSent(t *testing.T) {
	m := New(settings.Mail{}, "files.example.com", slog.New(slog.DiscardHandler))
	if m.On() || m.Describe() != "off" {
		t.Fatalf("on %v, %q", m.On(), m.Describe())
	}
	m.Send(message)
	m.Run(context.Background()) // returns at once
	var none *Mailer
	none.Send(message)
	if none.On() {
		t.Fatal("a nil Mailer is on")
	}
}

// Whether mail works: off without a mail server; otherwise checked (connect, encrypt, sign in, and nothing sent), and
// learnt from every message sent. A mail server that refuses the login, or is not there, does not work, and says why.
func TestWhetherMailWorks(t *testing.T) {
	off := New(settings.Mail{}, "files.example.com", slog.New(slog.DiscardHandler))
	if ok, why := off.Works(context.Background()); ok || why != whyOff {
		t.Fatalf("without a mail server: %v %q", ok, why)
	}
	if ok, why := off.Known(); ok || why != whyOff {
		t.Fatalf("without a mail server, known: %v %q", ok, why)
	}

	server, client := tlsPair(t)
	smtp := mailtest.Start(t, mailtest.Options{TLS: server, User: "drive", Password: "pw"})
	good := settings.Mail{Host: smtp.Host, Port: smtp.Port, TLS: settings.StartTLS, User: "drive", Password: "pw", From: from}
	m, _ := running(t, good, client, 5*time.Second)
	if ok, _ := m.Known(); !ok {
		t.Fatal("before anything is known, mail is taken to work")
	}
	if ok, why := m.Works(context.Background()); !ok || why != "" {
		t.Fatalf("a mail server that works: %v %q", ok, why)
	}
	if got := smtp.Messages(); len(got) != 0 {
		t.Fatalf("a check sent %d messages", len(got))
	}

	wrong := good
	wrong.Password = "wrong"
	w, l := running(t, wrong, client, 5*time.Second)
	if ok, why := w.Works(context.Background()); ok || why != whyLogin {
		t.Fatalf("a mail server that refuses the login: %v %q", ok, why)
	}
	if ok, why := w.Known(); ok || why != whyLogin {
		t.Fatalf("known after the check: %v %q", ok, why)
	}
	if strings.Contains(l.String(), "wrong") {
		t.Fatalf("the log holds the password: %s", l)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	gone := good
	gone.Port = port
	g, _ := running(t, gone, client, 5*time.Second)
	if ok, why := g.Works(context.Background()); ok || why != whyUnreachable {
		t.Fatalf("a mail server that is not there: %v %q", ok, why)
	}

	// A message that could not be sent says so too; one sent says it works again.
	m.Send(Message{To: "dora@example.com", Subject: "x", Body: "x"})
	smtp.WaitFor(t, 1)
	if ok, _ := m.Known(); !ok {
		t.Fatal("after a message was sent")
	}
	if err := w.Deliver(context.Background(), message); err == nil {
		t.Fatal("a message through a server that refuses the login was sent")
	}
}
