// Package mail sends the Drive's own mail, such as "shared with you", over SMTP with the settings it is given
// (DRIVE_SMTP_* and DRIVE_MAIL_FROM). It uses only the standard library.
//
// Sending never holds up or fails the request that asked for it: a message is put in a queue, and one sender in the
// background sends what is queued, one message at a time, each within a time limit. A message that cannot be sent is
// logged with what it was about, never with its text, and is not tried again. Without a mail server (DRIVE_SMTP_HOST
// empty) nothing is queued and nothing is attempted.
//
// Everything that comes from people (names, file names) is made safe here: it is put in the headers only encoded, with
// line breaks and other control characters taken out first, so no name can add a header or a recipient.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/neonjelly-co/superit-drive/internal/settings"
)

// A Message is one mail to one person. Subject and Body are plain text, as people should read them; Body's lines are
// separated by "\n".
type Message struct {
	// To is the recipient's email address, and nothing else: no name.
	To      string
	Subject string
	Body    string
	// About is logged, as slog key and value pairs, when the message cannot be sent: what it was about, never its text
	// or its recipient's address, such as "mail", "shared with you", "recipient", "<user id>".
	About []any
}

// QueueSize is how many messages can wait to be sent. A message that finds the queue full is dropped and logged.
const QueueSize = 64

// Timeout bounds the sending of one message, from connecting to the mail server to its last answer.
const Timeout = 30 * time.Second

// Mailer sends the Drive's mail. A nil Mailer, or one without a mail server, sends nothing.
type Mailer struct {
	s     settings.Mail
	hello string
	log   *slog.Logger
	queue chan Message
	// tls is the TLS configuration for the mail server, nil for the system's own (only tests set it).
	tls *tls.Config
	// timeout is Timeout, or shorter in tests.
	timeout time.Duration
}

// New makes a Mailer that sends through the mail server s; hello is the name the Drive gives itself to it (the host of
// DRIVE_ADDRESS). Run must run for anything to be sent.
func New(s settings.Mail, hello string, log *slog.Logger) *Mailer {
	switch ip := net.ParseIP(hello); {
	case hello == "" || strings.ContainsFunc(hello, func(r rune) bool { return r <= ' ' || r > unicode.MaxASCII }):
		hello = "localhost"
	case ip != nil && ip.To4() != nil:
		hello = "[" + ip.String() + "]" // an address literal, as SMTP writes one (RFC 5321)
	case ip != nil:
		hello = "[IPv6:" + ip.String() + "]"
	}
	m := &Mailer{s: s, hello: hello, log: log, timeout: Timeout}
	if s.Host != "" {
		m.queue = make(chan Message, QueueSize)
	}
	return m
}

// On says whether the Drive sends mail: whether it was given a mail server.
func (m *Mailer) On() bool { return m != nil && m.queue != nil }

// Describe says where mail goes, for the log line at start: the mail server and how it is reached, or "off".
func (m *Mailer) Describe() string {
	if !m.On() {
		return "off"
	}
	return net.JoinHostPort(m.s.Host, strconv.Itoa(m.s.Port)) + " (" + string(m.s.TLS) + ")"
}

// Send queues msg to be sent, and returns at once. Without a mail server it does nothing.
func (m *Mailer) Send(msg Message) {
	if !m.On() {
		return
	}
	select {
	case m.queue <- msg:
	default:
		m.log.Warn("a mail was not sent: too many are waiting to be sent", msg.About...)
	}
}

// Run sends what is queued, one message at a time, until ctx ends. What is still queued then is not sent.
func (m *Mailer) Run(ctx context.Context) {
	if !m.On() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.queue:
			if err := m.deliver(ctx, msg); err != nil {
				m.log.Warn("a mail could not be sent", append([]any{"error", err.Error()}, msg.About...)...)
			}
		}
	}
}

// deliver sends one message: connect, encrypt as the settings say, sign in when there is a login, send, and quit.
func (m *Mailer) deliver(ctx context.Context, msg Message) error {
	to, err := Recipient(msg.To)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	address := net.JoinHostPort(m.s.Host, strconv.Itoa(m.s.Port))
	dialer := &net.Dialer{}
	var conn net.Conn
	if m.s.TLS == settings.ImplicitTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: m.tlsConfig()}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("connecting to the mail server: %w", err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) // the Drive stopping ends it at once
	defer stop()

	c, err := smtp.NewClient(conn, m.s.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("the mail server's greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello(m.hello); err != nil {
		return fmt.Errorf("saying hello to the mail server: %w", err)
	}
	if m.s.TLS == settings.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS, and DRIVE_SMTP_TLS is starttls: nothing was sent")
		}
		if err := c.StartTLS(m.tlsConfig()); err != nil {
			return fmt.Errorf("switching to TLS (STARTTLS): %w", err)
		}
	}
	if m.s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.s.User, m.s.Password, m.s.Host)); err != nil {
			return fmt.Errorf("signing in to the mail server: %w", err)
		}
	}
	if err := c.Mail(m.s.From.Address); err != nil {
		return fmt.Errorf("the mail server refused the sender: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("the mail server refused the recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("starting to send: %w", err)
	}
	if _, err := w.Write(Compose(m.s.From, to, msg.Subject, msg.Body, time.Now())); err != nil {
		return fmt.Errorf("sending: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail server did not take the message: %w", err)
	}
	_ = c.Quit()
	return nil
}

func (m *Mailer) tlsConfig() *tls.Config {
	if m.tls != nil {
		return m.tls
	}
	return &tls.Config{ServerName: m.s.Host, MinVersion: tls.VersionTLS12}
}

// Recipient checks that address is one plain email address (no name, no list, no line break) and returns it.
func Recipient(address string) (string, error) {
	a, err := netmail.ParseAddress(address)
	if err != nil || a.Name != "" || a.Address != address || !strings.Contains(address, "@") ||
		strings.ContainsFunc(address, func(r rune) bool { return r <= ' ' || r == '<' || r == '>' || r == ',' }) {
		return "", errors.New("the recipient's address is not one plain email address")
	}
	return address, nil
}

// Clean makes text from people safe for one line of a mail: line breaks, tabs and every other control character
// (and the Unicode line and paragraph separators) become spaces, and spaces at either end are trimmed.
func Clean(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' || r == '\u0085' {
			return ' '
		}
		return r
	}, text))
}

// Compose writes the message as it goes to the mail server: headers, then the body as quoted-printable UTF-8 with
// CRLF line endings. to must already be checked (Recipient). The subject is cleaned, then encoded (RFC 2047) and
// folded, so nothing in it can end its header.
func Compose(from *netmail.Address, to, subject, body string, now time.Time) []byte {
	var b bytes.Buffer
	header := func(name, value string) {
		b.WriteString(name + ": " + value + "\r\n")
	}
	header("From", from.String())
	header("To", "<"+to+">")
	header("Subject", strings.ReplaceAll(mime.QEncoding.Encode("utf-8", Clean(subject)), "?= =?", "?=\r\n =?"))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+randomID()+"@"+domainOf(from.Address)+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	// Sent by a program, not a person: auto-responders do not answer it (RFC 3834).
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	// In text mode the writer ends every line ("\n", "\r\n" or "\r") with CRLF, and keeps lines short.
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	return b.Bytes()
}

func domainOf(address string) string {
	if i := strings.LastIndexByte(address, '@'); i >= 0 && i < len(address)-1 {
		return address[i+1:]
	}
	return "localhost"
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
