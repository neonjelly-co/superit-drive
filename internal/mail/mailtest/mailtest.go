// Package mailtest is a throwaway SMTP server for tests: it takes whatever mail it is sent and keeps it, so a test can
// see what the Drive sent. It speaks just enough SMTP for Go's own client (net/smtp): EHLO, STARTTLS and AUTH PLAIN
// when asked to, MAIL, RCPT, DATA, RSET, NOOP and QUIT. It can also say nothing at all, like a mail server that has
// stopped answering. Only tests use it.
package mailtest

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A Message is one mail the server took: who sent it and to whom, as the SMTP commands said, and its data as it came
// (headers and body, with CRLF line endings, and the dots SMTP doubles at the start of a line undoubled).
type Message struct {
	From string
	To   []string
	Data string
	// TLS and SignedIn say how it came: over TLS, and after signing in.
	TLS      bool
	SignedIn bool
}

// Options change what the server offers.
type Options struct {
	// TLS, when set, makes the server offer STARTTLS with it; with Implicit, it speaks TLS from the first byte instead.
	TLS      *tls.Config
	Implicit bool
	// User and Password, when set, make the server offer AUTH PLAIN and refuse mail from anyone who has not signed in
	// with them.
	User, Password string
}

// Server is a running throwaway SMTP server on 127.0.0.1.
type Server struct {
	// Host and Port are where it listens.
	Host string
	Port int

	o        Options
	ln       net.Listener
	mu       sync.Mutex
	messages []Message
	silent   atomic.Bool
}

// Start starts a server that stops when the test ends.
func Start(t testing.TB, o Options) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, o: o, ln: ln}
	if o.Implicit && o.TLS != nil {
		s.ln = tls.NewListener(ln, o.TLS)
	}
	go s.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// Silent makes the server take new connections and never answer them, like a mail server that has hung.
func (s *Server) Silent(on bool) { s.silent.Store(on) }

// Messages is every message the server has taken so far, oldest first.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// WaitFor waits up to five seconds for the server to have taken n messages, and returns them.
func (s *Server) WaitFor(t testing.TB, n int) []Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := s.Messages()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mail server took %d messages in five seconds, want %d", len(got), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		if s.silent.Load() {
			go func() { // holds it open, saying nothing, until the other side gives up
				_, _ = conn.Read(make([]byte, 1))
				time.Sleep(time.Minute)
				_ = conn.Close()
			}()
			continue
		}
		go s.serve(conn)
	}
}

// session is one connection's state.
type session struct {
	conn     net.Conn
	r        *bufio.Reader
	tls      bool
	signedIn bool
	from     string
	to       []string
}

func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	_, implicit := conn.(*tls.Conn)
	x := &session{conn: conn, r: bufio.NewReader(conn), tls: implicit}
	say := func(line string) bool {
		_, err := conn.Write([]byte(line + "\r\n"))
		return err == nil
	}
	if !say("220 mailtest ready") {
		return
	}
	for {
		line, err := x.r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			lines := []string{"250-mailtest"}
			if s.o.TLS != nil && !x.tls {
				lines = append(lines, "250-STARTTLS")
			}
			if s.o.User != "" {
				lines = append(lines, "250-AUTH PLAIN")
			}
			lines = append(lines, "250 8BITMIME")
			_, _ = conn.Write([]byte(strings.Join(lines, "\r\n") + "\r\n"))
		case "STARTTLS":
			if s.o.TLS == nil || x.tls {
				say("502 no")
				continue
			}
			say("220 go ahead")
			t := tls.Server(conn, s.o.TLS)
			if err := t.Handshake(); err != nil {
				return
			}
			conn, x.conn, x.r, x.tls = t, t, bufio.NewReader(t), true
			x.from, x.to, x.signedIn = "", nil, false
		case "AUTH":
			mech, initial, _ := strings.Cut(arg, " ")
			raw, err := base64.StdEncoding.DecodeString(initial)
			parts := strings.Split(string(raw), "\x00")
			if !strings.EqualFold(mech, "PLAIN") || err != nil || len(parts) != 3 || s.o.User == "" ||
				parts[1] != s.o.User || parts[2] != s.o.Password {
				say("535 authentication failed")
				continue
			}
			x.signedIn = true
			say("235 signed in")
		case "MAIL":
			if s.o.User != "" && !x.signedIn {
				say("530 sign in first")
				continue
			}
			x.from, x.to = address(arg), nil
			say("250 ok")
		case "RCPT":
			x.to = append(x.to, address(arg))
			say("250 ok")
		case "DATA":
			if x.from == "" || len(x.to) == 0 {
				say("503 MAIL and RCPT first")
				continue
			}
			say("354 go ahead")
			data, ok := readData(x.r)
			if !ok {
				return
			}
			s.mu.Lock()
			s.messages = append(s.messages, Message{From: x.from, To: x.to, Data: data, TLS: x.tls, SignedIn: x.signedIn})
			s.mu.Unlock()
			x.from, x.to = "", nil
			say("250 taken")
		case "RSET":
			x.from, x.to = "", nil
			say("250 ok")
		case "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("500 " + strconv.Quote(verb) + " is not known here")
		}
	}
}

// address is the address in "FROM:<a@example.com>" or "TO:<a@example.com>", with whatever follows it left out.
func address(arg string) string {
	_, rest, _ := strings.Cut(arg, ":")
	rest = strings.TrimSpace(rest)
	if i := strings.IndexByte(rest, '>'); strings.HasPrefix(rest, "<") && i > 0 {
		return rest[1:i]
	}
	return rest
}

// readData reads a message's data up to the line holding only ".", undoubling leading dots, and keeps its CRLFs.
func readData(r *bufio.Reader) (string, bool) {
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", false
		}
		if line == ".\r\n" {
			return b.String(), true
		}
		b.WriteString(strings.TrimPrefix(line, "."))
	}
}
