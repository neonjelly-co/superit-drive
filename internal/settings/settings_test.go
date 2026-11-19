package settings

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func complete() map[string]string {
	return map[string]string{
		"DRIVE_STORAGE_URL":      "https://u1.your-storagebox.de",
		"DRIVE_STORAGE_USER":     "u1",
		"DRIVE_STORAGE_PASSWORD": "pw",
		"DRIVE_ADDRESS":          "https://files.example.com",
		"DRIVE_PROJECT_NAME":     "Kovács Accounting",
		"DRIVE_HEADER_SECRET":    strings.Repeat("s", MinSecretLength),
		"DRIVE_MEMBERS_FILE":     "/members/members.json",
		"DRIVE_DATA_DIR":         "/data",
	}
}

func TestReadsEverySetting(t *testing.T) {
	env := complete()
	env["DRIVE_CAPACITY"] = "1TB"
	env["DRIVE_STORAGE_FOLDER"] = "/drive"
	s, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.Storage.URL.Host != "u1.your-storagebox.de" || s.Storage.Folder != "/drive" || s.Capacity != 1e12 ||
		s.Address.Host != "files.example.com" || s.ProjectName != "Kovács Accounting" || s.Listen != ":8080" {
		t.Fatalf("unexpected settings: %+v", s)
	}
}

func TestDefaults(t *testing.T) {
	env := complete()
	s, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.Storage.Folder != "/" || s.Capacity != 0 || s.Listen != ":8080" {
		t.Fatalf("unexpected defaults: %+v", s)
	}
}

func TestNamesEveryMissingSetting(t *testing.T) {
	_, err := FromEnv(func(string) string { return "" })
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, v := range Variables {
		if v.Required && !strings.Contains(err.Error(), v.Name) {
			t.Errorf("the error does not name %s: %v", v.Name, err)
		}
	}
}

func TestRefusesBadValues(t *testing.T) {
	cases := map[string]string{
		"DRIVE_HEADER_SECRET":  "short",
		"DRIVE_ADDRESS":        "https://example.com/drive",
		"DRIVE_STORAGE_URL":    "ftp://example.com",
		"DRIVE_CAPACITY":       "lots",
		"DRIVE_STORAGE_FOLDER": "/drive/../other",
	}
	for name, bad := range cases {
		env := complete()
		env[name] = bad
		if _, err := FromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q: expected an error naming it, got %v", name, bad, err)
		}
	}
}

// The storage check needs the storage settings and nothing else.
func TestTheStorageSettingsAloneAreEnoughToCheckAServer(t *testing.T) {
	env := map[string]string{
		"DRIVE_STORAGE_URL":      "https://dav.example.com/remote",
		"DRIVE_STORAGE_USER":     "u1",
		"DRIVE_STORAGE_PASSWORD": "pw",
	}
	s, err := StorageFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.URL.String() != "https://dav.example.com/remote" || s.User != "u1" || s.Password != "pw" || s.Folder != "/" {
		t.Fatalf("unexpected storage settings: %+v", s)
	}

	_, err = StorageFromEnv(func(string) string { return "" })
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, v := range Variables {
		storage := strings.HasPrefix(v.Name, "DRIVE_STORAGE_")
		if named := strings.Contains(err.Error(), v.Name); named != (storage && v.Required) {
			t.Errorf("named %s: %v, want %v: %v", v.Name, named, storage && v.Required, err)
		}
	}
}

// The README's settings table is the settings reference for people running the Drive themselves: it lists every
// variable, and nothing else.
func TestTheREADMEListsEverySetting(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Z_]+)` \\| (yes|no)")
	documented := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(string(readme), -1) {
		documented[m[1]] = m[2] == "yes"
	}
	for _, v := range Variables {
		required, ok := documented[v.Name]
		switch {
		case !ok:
			t.Errorf("the README's settings table does not list %s", v.Name)
		case required != v.Required:
			t.Errorf("the README's settings table says %s is required: %v, want %v", v.Name, required, v.Required)
		}
		delete(documented, v.Name)
	}
	for name := range documented {
		t.Errorf("the README's settings table lists %s, which the Drive does not read", name)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"1000":   1000,
		"500GB":  500e9,
		"1 TB":   1e12,
		"1TiB":   1 << 40,
		"10gib":  10 << 30,
		"2048KB": 2048e3,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-1", "1.5TB", "1PB", "TB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) should fail", in)
		}
	}
}
