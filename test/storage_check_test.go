package test

import (
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// The steps of `superit-drive check-storage`, in order.
var checkSteps = []string{"sign in", "make a folder", "write a file", "read part of it", "move it", "delete it"}

// checkStorage runs the storage check against folder on the stand-in storage, with env added to the settings.
func checkStorage(t *testing.T, folder string, env map[string]string) (string, int, *harness.Storage) {
	t.Helper()
	settings, s := harness.StorageAt(t, folder)
	for k, v := range env {
		settings[k] = v
	}
	out, code := harness.Command(t, settings, "check-storage")
	return out, code, s
}

// passed says whether the output has every step in order, each ok, and that resuming uploads is (or is not)
// supported.
func passed(t *testing.T, out string, resume string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(checkSteps)+3 {
		t.Fatalf("want a header, %d steps, the resume line and the result; got:\n%s", len(checkSteps), out)
	}
	for i, step := range checkSteps {
		if !strings.HasPrefix(lines[i+1], step+": ok") {
			t.Errorf("line %d: got %q, want %q", i+2, lines[i+1], step+": ok")
		}
	}
	if !strings.HasPrefix(lines[len(lines)-2], "resume an upload: "+resume) {
		t.Errorf("got %q, want resuming uploads %s", lines[len(lines)-2], resume)
	}
	if want := "This server can hold the Drive's files. Resuming uploads: " + resume + "."; lines[len(lines)-1] != want {
		t.Errorf("got %q, want %q", lines[len(lines)-1], want)
	}
}

// noneLeft fails when the check left a folder of its own in the Drive's folder.
func noneLeft(t *testing.T, s *harness.Storage) {
	t.Helper()
	entries, _ := s.List(t, "")
	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".superit-drive-check-") {
			t.Errorf("the check left %s behind", e.Name)
		}
	}
}

func TestTheStorageCheckPassesOnAGoodServerAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	folder := "/" + harness.Unique("check")
	settings, s := harness.StorageAt(t, folder)
	s.Put(t, "My files/reka@example.com/Tax notes.pdf", "the customer's own file")

	out, code, _ := checkStorage(t, folder, nil)
	if code != 0 {
		t.Fatalf("exit code %d, want 0:\n%s", code, out)
	}
	if !strings.HasPrefix(out, "Checking the WebDAV server at http://") || !strings.Contains(out, folder+"/ for Superit Drive dev.") {
		t.Errorf("the first line does not say which server and folder it checks:\n%s", out)
	}
	passed(t, out, "supported") // Apache's mod_dav writes into part of a file, as a Storage Box does
	noneLeft(t, s)

	entries, _ := s.List(t, "")
	if len(entries) != 1 || entries[0].Name != "My files" {
		t.Errorf("the Drive's folder holds %+v, want only what was there", entries)
	}
	if got, ok := s.Get(t, "My files/reka@example.com/Tax notes.pdf"); !ok || string(got) != "the customer's own file" {
		t.Errorf("the file that was there is now %q, %v", got, ok)
	}
	if strings.Contains(out, settings["DRIVE_STORAGE_PASSWORD"]) {
		t.Error("the output shows the storage password")
	}
}

func TestTheStorageCheckMakesTheDrivesFolderWhenItIsMissing(t *testing.T) {
	t.Parallel()
	folder := "/" + harness.Unique("check") + "/drive"
	out, code, s := checkStorage(t, folder, nil)
	if code != 0 {
		t.Fatalf("exit code %d, want 0:\n%s", code, out)
	}
	passed(t, out, "supported")
	if !strings.Contains(out, "sign in: ok (the Drive's folder is not there yet; the check makes it, as the Drive would)") {
		t.Errorf("the check did not say the folder was missing:\n%s", out)
	}
	if entries, ok := s.List(t, ""); !ok || len(entries) != 0 {
		t.Errorf("want the Drive's folder made and empty; there: %v, holding %+v", ok, entries)
	}
}

// The stand-in storage has a folder for each thing a server could refuse or ignore (test/webdav/storage.conf).
func TestTheStorageCheckNamesTheStepThatFailed(t *testing.T) {
	t.Parallel()
	closed := closedAddress(t)
	cases := []struct {
		name   string
		step   string
		folder string            // the folder whose server configuration makes the step fail
		env    map[string]string // or settings that do
		reason string            // a regular expression for what follows "<step>: failed: "
	}{
		{"wrong password", "sign in", "", map[string]string{"DRIVE_STORAGE_PASSWORD": "not-the-password"},
			`the server refused the login; check DRIVE_STORAGE_USER and DRIVE_STORAGE_PASSWORD`},
		{"nothing listening", "sign in", "", map[string]string{"DRIVE_STORAGE_URL": closed},
			`could not reach the server: dial tcp ` + regexp.QuoteMeta(strings.TrimPrefix(closed, "http://")) + `: .*refused`},
		{"MKCOL refused", "make a folder", "/refuses-mkcol", nil,
			`making the folder "/refuses-mkcol/": the server answered 403 Forbidden`},
		{"PUT refused", "write a file", "/refuses-put", nil,
			`writing "\.superit-drive-check-[0-9a-f]{12}/check\.bin": the server answered 403 Forbidden`},
		{"Range ignored", "read part of it", "/ignores-range", nil,
			`asked for part of the file, the server sent all of it: it does not answer Range requests`},
		{"MOVE refused", "move it", "/refuses-move", nil,
			`moving "\.superit-drive-check-[0-9a-f]{12}/check\.bin": the server answered 403 Forbidden`},
		{"DELETE refused", "delete it", "/refuses-delete", nil,
			`deleting "\.superit-drive-check-[0-9a-f]{12}/check moved\.bin": the server answered 403 Forbidden`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			folder := c.folder + "/" + harness.Unique("check")
			out, code, s := checkStorage(t, folder, c.env)
			if code != 1 {
				t.Fatalf("exit code %d, want 1:\n%s", code, out)
			}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			failed := -1
			for i, step := range checkSteps {
				if step == c.step {
					failed = i
				}
			}
			for i := range failed {
				if lines[i+1] != checkSteps[i]+": ok" && !strings.HasPrefix(lines[i+1], checkSteps[i]+": ok (") {
					t.Errorf("line %d: got %q, want %q", i+2, lines[i+1], checkSteps[i]+": ok")
				}
			}
			want := regexp.MustCompile("^" + regexp.QuoteMeta(c.step+": failed: ") + c.reason + "$")
			if line := lines[failed+1]; !want.MatchString(line) {
				t.Errorf("got  %q\nwant %s", line, want)
			}
			if want := `This server cannot hold the Drive's files: "` + c.step + `" failed.`; lines[len(lines)-1] != want {
				t.Errorf("got %q, want %q", lines[len(lines)-1], want)
			}
			for _, later := range checkSteps[failed+1:] {
				if strings.Contains(out, "\n"+later+": ") {
					t.Errorf("the check went on to %q after %q failed:\n%s", later, c.step, out)
				}
			}
			if strings.Contains(out, "resume an upload") {
				t.Errorf("the check tried resuming after a step failed:\n%s", out)
			}

			switch {
			case c.step == "delete it":
				// The server deletes nothing, so the check's folder stays, and the check says where.
				if !strings.Contains(out, "clean up: the check's folder http://") || !strings.Contains(out, "remove it by hand") {
					t.Errorf("the check did not say it left its folder behind:\n%s", out)
				}
			case c.step != "sign in":
				noneLeft(t, s)
			}
		})
	}
}

func TestTheStorageCheckSaysWhetherUploadsCanResume(t *testing.T) {
	t.Parallel()
	cases := []struct {
		folder string
		reason string
	}{
		{"/refuses-content-range",
			`the server refused to write the second half into the file from its middle (PUT with Content-Range): writing part of ".superit-drive-check-`},
		{"/ignores-content-range",
			"the server ignored where the second half goes (Content-Range) and replaced the file with it"},
	}
	for _, c := range cases {
		t.Run(c.folder, func(t *testing.T) {
			t.Parallel()
			out, code, s := checkStorage(t, c.folder+"/"+harness.Unique("check"), nil)
			// Resuming is not a must: the server can still hold the Drive's files.
			if code != 0 {
				t.Fatalf("exit code %d, want 0:\n%s", code, out)
			}
			passed(t, out, "not supported")
			if !strings.Contains(out, "resume an upload: not supported: "+c.reason) {
				t.Errorf("want the reason %q:\n%s", c.reason, out)
			}
			noneLeft(t, s)
		})
	}
}

func TestTheStorageCheckNeedsOnlyTheStorageSettings(t *testing.T) {
	t.Parallel()
	out, code := harness.Command(t, map[string]string{"DRIVE_STORAGE_FOLDER": "/drive"}, "check-storage")
	if code != 2 {
		t.Fatalf("exit code %d, want 2:\n%s", code, out)
	}
	for _, name := range []string{"DRIVE_STORAGE_URL", "DRIVE_STORAGE_USER", "DRIVE_STORAGE_PASSWORD"} {
		if !strings.Contains(out, name+" is not set") {
			t.Errorf("the check did not say %s is missing:\n%s", name, out)
		}
	}
	for _, name := range []string{"DRIVE_ADDRESS", "DRIVE_HEADER_SECRET", "DRIVE_MEMBERS_FILE", "DRIVE_DATA_DIR"} {
		if strings.Contains(out, name) {
			t.Errorf("the check asked for %s, which it does not need:\n%s", name, out)
		}
	}
}

func TestAnUnknownCommandIsRefused(t *testing.T) {
	t.Parallel()
	out, code := harness.Command(t, nil, "check-everything")
	if code != 2 || !strings.Contains(out, "superit-drive check-storage") {
		t.Fatalf("exit code %d, want 2 and the usage:\n%s", code, out)
	}
}

// closedAddress is an http address on which nothing listens.
func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	return "http://" + address
}
