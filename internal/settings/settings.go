// Package settings reads every setting of the Drive from environment variables. There is no settings page and no
// settings file: whoever runs the Drive (Superit's daemon, or a self-hoster) hands everything over at start.
//
// Every variable is listed in Variables, which the README's settings table mirrors. Add a new setting there first.
package settings

import (
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Settings is everything the Drive is told at start.
type Settings struct {
	// Listen is the address the Drive's web server listens on, such as ":8080".
	Listen string
	// DataDir is the Drive's own folder on the server. It holds the index and nothing else.
	DataDir string
	// Address is the Drive's own address as people reach it, such as https://files.example.com.
	Address *url.URL
	// ProjectName is the name of the project (the organisation) the Drive belongs to.
	ProjectName string
	// MembersFile is the member list file, written by whoever runs the Drive and only read by it.
	MembersFile string
	// HeaderSecret is the value the reverse proxy adds to every signed-in request. Requests without it are refused.
	HeaderSecret string
	// Storage is where the files live.
	Storage Storage
	// Capacity is the size of the Drive storage in bytes, or 0 when it is not known.
	Capacity int64
	// Mail is the mail server the Drive sends its own mail through, such as "shared with you". Its Host is empty when
	// the Drive sends no mail.
	Mail Mail
	// Rollback is the last time the storage was put back to a snapshot, as whoever runs the Drive declares it; nil when
	// none is declared.
	Rollback *Rollback
}

// A Rollback is the storage put back to a snapshot: by a project admin, through Superit's dashboard, on Superit's Drive
// storage. The Drive is stopped meanwhile and started again with these settings, which say what happened; it keeps being
// started with the same ones until the next rollback.
type Rollback struct {
	// At is when the storage was put back. It tells one rollback from another: a Drive started again with the same At
	// has taken that rollback in already.
	At time.Time
	// To is when the snapshot it was put back to was taken: the files are as they were then.
	To time.Time
	// By is who put it back, as members read it: their name, with control characters taken out and cut to
	// MaxRollbackByLength characters. It is shown as text, never as markup.
	By string
}

// MaxRollbackByLength is how many characters of DRIVE_ROLLBACK_BY the Drive keeps; a longer name is cut, ending in "…".
const MaxRollbackByLength = 100

// Mail is the mail server the Drive sends mail through, over SMTP.
type Mail struct {
	// Host is the mail server's name or address, such as smtp.example.com; empty when the Drive sends no mail.
	Host string
	Port int
	// TLS is how the connection is encrypted.
	TLS MailTLS
	// User and Password are the login, both empty when the server needs none.
	User     string
	Password string
	// From is the address the Drive's mail comes from.
	From *netmail.Address
}

// MailTLS is how the connection to the mail server is encrypted.
type MailTLS string

const (
	// StartTLS starts in plain text and asks the server to switch to TLS (STARTTLS) before anything else is sent. The
	// mail is not sent when the server does not offer it.
	StartTLS MailTLS = "starttls"
	// ImplicitTLS is TLS from the first byte, as on port 465.
	ImplicitTLS MailTLS = "implicit"
	// NoTLS sends everything in plain text: only for a mail server on the same machine or network that needs no login.
	NoTLS MailTLS = "none"
)

// Storage is the WebDAV server that holds the files: a Storage Box bought from Superit, or the customer's own.
type Storage struct {
	// URL is the WebDAV server's address, such as https://u12345.your-storagebox.de.
	URL *url.URL
	// User and Password are the login for the WebDAV server.
	User     string
	Password string
	// Folder is the folder on the WebDAV server the Drive keeps everything in. "/" is the server's top.
	Folder string
}

// A Variable is one environment variable the Drive reads.
type Variable struct {
	Name     string
	Required bool
	Default  string
	Meaning  string
}

// Variables lists every environment variable, in the order the README documents them.
var Variables = []Variable{
	{Name: "DRIVE_STORAGE_URL", Required: true, Meaning: "Address of the WebDAV server that holds the files (http or https)."},
	{Name: "DRIVE_STORAGE_USER", Required: true, Meaning: "Login name for the WebDAV server."},
	{Name: "DRIVE_STORAGE_PASSWORD", Required: true, Meaning: "Password for the WebDAV server."},
	{Name: "DRIVE_STORAGE_FOLDER", Default: "/", Meaning: "Folder on the WebDAV server that the Drive keeps everything in. Made if missing."},
	{Name: "DRIVE_CAPACITY", Meaning: "Size of the storage, in bytes or with a unit (500GB, 1TB, 1TiB). Empty when not known."},
	{Name: "DRIVE_ADDRESS", Required: true, Meaning: "The Drive's own address as people reach it, such as https://files.example.com. Changes are accepted only from pages at this address."},
	{Name: "DRIVE_PROJECT_NAME", Required: true, Meaning: "Name of the project (the organisation) the Drive belongs to."},
	{Name: "DRIVE_HEADER_SECRET", Required: true, Meaning: "Secret the reverse proxy adds as the Drive-Secret header on signed-in requests. At least 32 characters."},
	{Name: "DRIVE_MEMBERS_FILE", Required: true, Meaning: "Path of the member list file. Re-read when it changes."},
	{Name: "DRIVE_DATA_DIR", Required: true, Meaning: "Folder for the index. Set to /data in the container image."},
	{Name: "DRIVE_LISTEN", Default: ":8080", Meaning: "Address the web server listens on."},
	{Name: "DRIVE_SMTP_HOST", Meaning: "Mail server the Drive sends its mail through (\"shared with you\"), such as smtp.example.com. Empty: the Drive sends no mail, and sharing works without it."},
	{Name: "DRIVE_SMTP_PORT", Default: "587", Meaning: "Port of the mail server."},
	{Name: "DRIVE_SMTP_TLS", Meaning: "How the connection to the mail server is encrypted: starttls, implicit (TLS from the first byte) or none. Empty: implicit on port 465, starttls on any other. none only without a login."},
	{Name: "DRIVE_SMTP_USER", Meaning: "Login name for the mail server. Empty when it needs none."},
	{Name: "DRIVE_SMTP_PASSWORD", Meaning: "Password for the mail server, with DRIVE_SMTP_USER."},
	{Name: "DRIVE_MAIL_FROM", Meaning: "Address the Drive's mail comes from, such as drive@files.example.com or Drive <drive@files.example.com>. Needed with DRIVE_SMTP_HOST."},
	{Name: "DRIVE_ROLLBACK_AT", Meaning: "When the storage was last put back to a snapshot, as a time with its offset (RFC 3339), such as 2026-11-20T09:41:00Z. With DRIVE_ROLLBACK_TO and DRIVE_ROLLBACK_BY; empty when it never was."},
	{Name: "DRIVE_ROLLBACK_TO", Meaning: "When the snapshot it was put back to was taken (RFC 3339), before DRIVE_ROLLBACK_AT."},
	{Name: "DRIVE_ROLLBACK_BY", Meaning: "Who put it back, as members should read it: their name. Control characters are taken out, and it is cut to 100 characters."},
}

// MinSecretLength is the shortest header secret the Drive accepts.
const MinSecretLength = 32

// PublishedExampleSecret is the header secret the worked example (examples/compose) once came with. Whoever knows the
// secret and reaches the Drive's port is whoever they say they are, and everyone can read this one: the Drive does not
// start with it.
const PublishedExampleSecret = "example-only-header-secret-0123456789abcdef"

// DevelopmentSecret is the header secret of the repository's own development settings (dev/drive.env), as public as
// the example's. The Drive runs with it, for development on one's own computer, and says so loudly as it starts; only
// when it listens on that computer alone (DRIVE_LISTEN at 127.0.0.1, ::1 or localhost): anywhere else, whoever reaches
// the port would be whoever they say they are.
const DevelopmentSecret = "dev-secret-dev-secret-dev-secret-0000"

// FromEnv reads the settings. getenv is os.Getenv outside tests. Every problem is reported at once, so a person
// fixing their setup sees the whole list and not one complaint per restart.
func FromEnv(getenv func(string) string) (Settings, error) {
	r := &reader{getenv: getenv}
	s := Settings{
		Listen:       r.value("DRIVE_LISTEN"),
		DataDir:      r.value("DRIVE_DATA_DIR"),
		ProjectName:  r.value("DRIVE_PROJECT_NAME"),
		MembersFile:  r.value("DRIVE_MEMBERS_FILE"),
		HeaderSecret: r.value("DRIVE_HEADER_SECRET"),
		Storage:      r.storage(),
	}

	if raw := r.value("DRIVE_ADDRESS"); raw != "" {
		u, err := parseAddress(raw, false)
		if err != nil {
			r.problem("DRIVE_ADDRESS " + err.Error())
		}
		s.Address = u
	}
	if raw := r.value("DRIVE_CAPACITY"); raw != "" {
		c, err := ParseSize(raw)
		if err != nil {
			r.problem("DRIVE_CAPACITY " + err.Error())
		}
		s.Capacity = c
	}
	if s.HeaderSecret != "" && len(s.HeaderSecret) < MinSecretLength {
		r.problem(fmt.Sprintf("DRIVE_HEADER_SECRET must be at least %d characters", MinSecretLength))
	}
	if s.HeaderSecret == PublishedExampleSecret {
		r.problem("DRIVE_HEADER_SECRET is the example's, which everyone can read: make one of your own (openssl rand -hex 32)")
	}
	if s.HeaderSecret == DevelopmentSecret && !onThisComputerOnly(s.Listen) {
		r.problem("DRIVE_HEADER_SECRET is the repository's development secret, which everyone can read: it is taken only " +
			"when DRIVE_LISTEN is on this computer alone (such as 127.0.0.1:8080); make one of your own (openssl rand -hex 32)")
	}
	if strings.ContainsFunc(s.HeaderSecret, func(r rune) bool { return r > unicode.MaxASCII || !unicode.IsPrint(r) }) {
		r.problem("DRIVE_HEADER_SECRET must be printable ASCII, so it can travel in a header")
	}
	s.Mail = r.mail()
	s.Rollback = r.rollback()

	if err := r.err("the Drive cannot start: "); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// StorageFromEnv reads only the storage settings (DRIVE_STORAGE_URL, DRIVE_STORAGE_USER, DRIVE_STORAGE_PASSWORD and
// DRIVE_STORAGE_FOLDER), for checking a WebDAV server before a Drive is set up on it.
func StorageFromEnv(getenv func(string) string) (Storage, error) {
	r := &reader{getenv: getenv}
	s := r.storage()
	if err := r.err("the storage cannot be checked: "); err != nil {
		return Storage{}, err
	}
	return s, nil
}

// A reader reads variables and collects every problem with them.
type reader struct {
	getenv   func(string) string
	problems []string
}

func (r *reader) problem(p string) { r.problems = append(r.problems, p) }

func (r *reader) err(prefix string) error {
	if len(r.problems) == 0 {
		return nil
	}
	return errors.New(prefix + strings.Join(r.problems, "; "))
}

// value is the variable's value, its default when it is not set, and a problem when it is required and not set.
func (r *reader) value(name string) string {
	for _, v := range Variables {
		if v.Name != name {
			continue
		}
		s := strings.TrimSpace(r.getenv(name))
		if s == "" && v.Required {
			r.problem(name + " is not set")
		}
		if s == "" {
			return v.Default
		}
		return s
	}
	panic("settings: " + name + " is missing from Variables")
}

// mail reads the mail settings. With none of them set, the Drive sends no mail; a mail server named without
// everything it needs, or settings without a mail server, are refused, so a mistake never passes for "no mail".
func (r *reader) mail() Mail {
	m := Mail{
		Host:     r.value("DRIVE_SMTP_HOST"),
		User:     r.value("DRIVE_SMTP_USER"),
		Password: r.value("DRIVE_SMTP_PASSWORD"),
	}
	from, tls := r.value("DRIVE_MAIL_FROM"), r.value("DRIVE_SMTP_TLS")
	if m.Host == "" {
		for _, name := range []string{"DRIVE_SMTP_PORT", "DRIVE_SMTP_TLS", "DRIVE_SMTP_USER", "DRIVE_SMTP_PASSWORD", "DRIVE_MAIL_FROM"} {
			if strings.TrimSpace(r.getenv(name)) != "" {
				r.problem(name + " is set but DRIVE_SMTP_HOST is not: set the mail server too, or leave every mail setting empty")
				break
			}
		}
		return Mail{}
	}
	if strings.ContainsFunc(m.Host, func(c rune) bool { return c <= ' ' || c > unicode.MaxASCII || strings.ContainsRune("/\\@:[]?#", c) }) &&
		net.ParseIP(m.Host) == nil {
		r.problem("DRIVE_SMTP_HOST must be a host name or an IP address, without a scheme, port or path")
	}
	port, err := strconv.Atoi(r.value("DRIVE_SMTP_PORT"))
	if err != nil || port < 1 || port > 65535 {
		r.problem("DRIVE_SMTP_PORT must be a port number from 1 to 65535")
	}
	m.Port = port
	switch MailTLS(tls) {
	case "":
		m.TLS = StartTLS
		if port == 465 {
			m.TLS = ImplicitTLS
		}
	case StartTLS, ImplicitTLS, NoTLS:
		m.TLS = MailTLS(tls)
	default:
		r.problem("DRIVE_SMTP_TLS must be starttls, implicit or none")
	}
	if (m.User == "") != (m.Password == "") {
		r.problem("DRIVE_SMTP_USER and DRIVE_SMTP_PASSWORD go together: set both, or neither")
	}
	if m.TLS == NoTLS && m.User != "" {
		r.problem("DRIVE_SMTP_TLS=none would send the mail server's login unencrypted: it is allowed only without DRIVE_SMTP_USER")
	}
	if from == "" {
		r.problem("DRIVE_MAIL_FROM is not set: the Drive's mail needs an address to come from")
	} else if a, err := netmail.ParseAddress(from); err != nil || !strings.Contains(a.Address, "@") {
		r.problem("DRIVE_MAIL_FROM must be one email address, such as drive@files.example.com or Drive <drive@files.example.com>")
	} else {
		m.From = a
	}
	return m
}

// rollback reads the last rollback of the storage. All three settings go together: some without the others are refused,
// so a mistake never passes for "no rollback".
func (r *reader) rollback() *Rollback {
	at, to, by := r.value("DRIVE_ROLLBACK_AT"), r.value("DRIVE_ROLLBACK_TO"), r.value("DRIVE_ROLLBACK_BY")
	if at == "" && to == "" && by == "" {
		return nil
	}
	if at == "" || to == "" || by == "" {
		r.problem("DRIVE_ROLLBACK_AT, DRIVE_ROLLBACK_TO and DRIVE_ROLLBACK_BY go together: set all three, or none")
		return nil
	}
	rb := &Rollback{By: CleanName(by, MaxRollbackByLength)}
	var err error
	if rb.At, err = time.Parse(time.RFC3339, at); err != nil {
		r.problem("DRIVE_ROLLBACK_AT must be a time with its offset, such as 2026-11-20T09:41:00Z (RFC 3339)")
	}
	if rb.To, err = time.Parse(time.RFC3339, to); err != nil {
		r.problem("DRIVE_ROLLBACK_TO must be a time with its offset, such as 2026-11-19T03:00:00Z (RFC 3339)")
	}
	if !rb.At.IsZero() && !rb.To.IsZero() && !rb.To.Before(rb.At) {
		r.problem("DRIVE_ROLLBACK_TO must be before DRIVE_ROLLBACK_AT: the storage goes back to a snapshot taken before it was put back")
	}
	if rb.By == "" {
		r.problem("DRIVE_ROLLBACK_BY must say who put the storage back")
	}
	return rb
}

// CleanName makes a name from outside (such as who put the storage back) fit to show as one line of text: what is not
// UTF-8 becomes U+FFFD, control characters become spaces, the characters that change the direction of text are taken
// out, runs of spaces become one, and it is cut to at most max characters, the last of them "…".
func CleanName(s string, max int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.Map(func(c rune) rune {
		switch {
		case unicode.IsControl(c), c == '\u2028', c == '\u2029':
			return ' '
		case c == '\u061c', c == '\u200e', c == '\u200f', c >= '\u202a' && c <= '\u202e', c >= '\u2066' && c <= '\u2069':
			return -1
		}
		return c
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > max {
		s = strings.TrimSpace(string(runes[:max-1])) + "…"
	}
	return s
}

func (r *reader) storage() Storage {
	s := Storage{
		User:     r.value("DRIVE_STORAGE_USER"),
		Password: r.value("DRIVE_STORAGE_PASSWORD"),
		Folder:   r.value("DRIVE_STORAGE_FOLDER"),
	}
	if raw := r.value("DRIVE_STORAGE_URL"); raw != "" {
		u, err := parseAddress(raw, true)
		if err != nil {
			r.problem("DRIVE_STORAGE_URL " + err.Error())
		}
		s.URL = u
	}
	if strings.Contains(s.Folder, "\\") || strings.Contains("/"+s.Folder+"/", "/../") {
		r.problem("DRIVE_STORAGE_FOLDER must be a plain folder path such as /drive")
	}
	return s
}

// parseAddress accepts an absolute http or https address without a login, query or fragment. Only the storage's
// address may have a path; the Drive's own address must be the top of its host, because the interface's files are
// served from there.
func parseAddress(raw string, pathAllowed bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("must be an http or https address")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must not contain a login, query or fragment")
	}
	if !pathAllowed && u.Path != "" && u.Path != "/" {
		return nil, errors.New("must not have a path: the Drive needs an address of its own")
	}
	return u, nil
}

var sizeUnits = map[string]int64{
	"":    1,
	"B":   1,
	"KB":  1000,
	"MB":  1000 * 1000,
	"GB":  1000 * 1000 * 1000,
	"TB":  1000 * 1000 * 1000 * 1000,
	"KIB": 1 << 10,
	"MIB": 1 << 20,
	"GIB": 1 << 30,
	"TIB": 1 << 40,
}

// ParseSize reads a size such as "1000000", "500GB", "1 TB" or "1TiB" into bytes. KB, MB, GB and TB count in
// thousands, as storage is sold; KiB, MiB, GiB and TiB in 1024s.
func ParseSize(raw string) (int64, error) {
	s := strings.ToUpper(strings.ReplaceAll(raw, " ", ""))
	i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if i == -1 {
		i = len(s)
	}
	number, unit := s[:i], s[i:]
	multiplier, ok := sizeUnits[unit]
	n, err := strconv.ParseInt(number, 10, 64)
	if !ok || err != nil || n <= 0 || n > (1<<62)/multiplier {
		return 0, fmt.Errorf("must be a whole number of bytes, or one with a unit such as 500GB or 1TiB, not %q", raw)
	}
	return n * multiplier, nil
}

// onThisComputerOnly says whether a listen address takes connections from this computer alone.
func onThisComputerOnly(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
