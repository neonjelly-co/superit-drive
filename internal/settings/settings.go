// Package settings reads every setting of the Drive from environment variables. There is no settings page and no
// settings file: whoever runs the Drive (Superit's daemon, or a self-hoster) hands everything over at start.
//
// Every variable is listed in Variables, which the README's settings table mirrors. Add a new setting there first.
package settings

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
}

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
}

// MinSecretLength is the shortest header secret the Drive accepts.
const MinSecretLength = 32

// FromEnv reads the settings. getenv is os.Getenv outside tests. Every problem is reported at once, so a person
// fixing their setup sees the whole list and not one complaint per restart.
func FromEnv(getenv func(string) string) (Settings, error) {
	var problems []string
	value := func(name string) string {
		for _, v := range Variables {
			if v.Name != name {
				continue
			}
			s := strings.TrimSpace(getenv(name))
			if s == "" && v.Required {
				problems = append(problems, name+" is not set")
			}
			if s == "" {
				return v.Default
			}
			return s
		}
		panic("settings: " + name + " is missing from Variables")
	}

	s := Settings{
		Listen:       value("DRIVE_LISTEN"),
		DataDir:      value("DRIVE_DATA_DIR"),
		ProjectName:  value("DRIVE_PROJECT_NAME"),
		MembersFile:  value("DRIVE_MEMBERS_FILE"),
		HeaderSecret: value("DRIVE_HEADER_SECRET"),
		Storage: Storage{
			User:     value("DRIVE_STORAGE_USER"),
			Password: value("DRIVE_STORAGE_PASSWORD"),
			Folder:   value("DRIVE_STORAGE_FOLDER"),
		},
	}

	if raw := value("DRIVE_STORAGE_URL"); raw != "" {
		u, err := parseAddress(raw, true)
		if err != nil {
			problems = append(problems, "DRIVE_STORAGE_URL "+err.Error())
		}
		s.Storage.URL = u
	}
	if raw := value("DRIVE_ADDRESS"); raw != "" {
		u, err := parseAddress(raw, false)
		if err != nil {
			problems = append(problems, "DRIVE_ADDRESS "+err.Error())
		}
		s.Address = u
	}
	if raw := value("DRIVE_CAPACITY"); raw != "" {
		c, err := ParseSize(raw)
		if err != nil {
			problems = append(problems, "DRIVE_CAPACITY "+err.Error())
		}
		s.Capacity = c
	}
	if s.HeaderSecret != "" && len(s.HeaderSecret) < MinSecretLength {
		problems = append(problems, fmt.Sprintf("DRIVE_HEADER_SECRET must be at least %d characters", MinSecretLength))
	}
	if strings.ContainsFunc(s.HeaderSecret, func(r rune) bool { return r > unicode.MaxASCII || !unicode.IsPrint(r) }) {
		problems = append(problems, "DRIVE_HEADER_SECRET must be printable ASCII, so it can travel in a header")
	}
	if strings.Contains(s.Storage.Folder, "\\") || strings.Contains("/"+s.Storage.Folder+"/", "/../") {
		problems = append(problems, "DRIVE_STORAGE_FOLDER must be a plain folder path such as /drive")
	}

	if len(problems) > 0 {
		return Settings{}, errors.New("the Drive cannot start: " + strings.Join(problems, "; "))
	}
	return s, nil
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
