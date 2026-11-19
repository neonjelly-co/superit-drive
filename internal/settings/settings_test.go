package settings

import (
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
