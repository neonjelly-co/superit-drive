// Package storagecheck finds out whether a WebDAV server can hold the Drive's files, before a Drive is set up on it:
// `superit-drive check-storage`. It reads only the storage settings (DRIVE_STORAGE_URL, DRIVE_STORAGE_USER,
// DRIVE_STORAGE_PASSWORD and DRIVE_STORAGE_FOLDER) and uses the Drive's own WebDAV client, so it asks the server
// exactly what the Drive will.
//
// It takes these steps in order and stops at the first that fails:
//
//	sign in          list the Drive's folder with the login (a folder that is missing is fine: the Drive makes it)
//	make a folder    make a folder of its own inside the Drive's folder, and the Drive's folder if it is missing
//	write a file     write a file of 256 KiB into it
//	read part of it  read 1000 bytes from the middle of the file, and only those
//	move it          move the file to another name, on the server
//	delete it        delete the file
//
// Then it finds out whether resuming uploads is supported: it writes the first half of a file, then the second half
// into the same file from the middle (PUT with Content-Range), and reads the file back. A server that refuses this, or
// ignores where the part goes and replaces the file, does not support it. This never fails the check.
//
// Whatever happens, it then removes its own folder. If it made the Drive's folder, that folder stays.
package storagecheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/settings"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// The exit codes of `superit-drive check-storage`.
const (
	// Usable: every step passed. Resuming uploads may or may not be supported; the output says which.
	Usable = 0
	// Unusable: a step failed, and the output names it.
	Unusable = 1
	// BadSettings: a storage setting is missing or wrong, and nothing was asked of the server.
	BadSettings = 2
)

const (
	fileSize   = 256 << 10
	partOffset = 100_000
	partLength = 1000
	// timeout bounds the whole check; cleaning up gets cleanupTimeout more.
	timeout        = 2 * time.Minute
	cleanupTimeout = 30 * time.Second
)

// Run checks the WebDAV server in the storage settings and writes one line per step to out, then a line with the
// result. A problem with the settings goes to errOut. It returns the exit code.
func Run(ctx context.Context, getenv func(string) string, version string, out, errOut io.Writer) int {
	s, err := settings.StorageFromEnv(getenv)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return BadSettings
	}
	c := &check{
		store:   storage.New(s.URL, s.User, s.Password, s.Folder),
		dir:     ".superit-drive-check-" + randomHex(6),
		content: randomBytes(fileSize),
	}
	fmt.Fprintf(out, "Checking the WebDAV server at %s for Superit Drive %s.\n", c.store.Address(), version)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, st := range c.steps() {
		note, err := st.run(ctx)
		if err != nil {
			fmt.Fprintf(out, "%s: failed: %s\n", st.name, reason(err))
			c.cleanUp(ctx, out)
			fmt.Fprintf(out, "This server cannot hold the Drive's files: %q failed.\n", st.name)
			return Unusable
		}
		if note != "" {
			fmt.Fprintf(out, "%s: ok (%s)\n", st.name, note)
		} else {
			fmt.Fprintf(out, "%s: ok\n", st.name)
		}
	}

	resume := "supported"
	if why := c.resume(ctx); why != "" {
		fmt.Fprintf(out, "resume an upload: not supported: %s\n", why)
		resume = "not supported"
	} else {
		fmt.Fprintln(out, "resume an upload: supported")
	}
	c.cleanUp(ctx, out)
	fmt.Fprintf(out, "This server can hold the Drive's files. Resuming uploads: %s.\n", resume)
	return Usable
}

// check is one run of the check.
type check struct {
	store   *storage.Client
	dir     string // the check's own folder, inside the Drive's folder
	content []byte // what it writes
	made    bool   // whether it may have made its folder
	cleaned bool   // whether cleanUp has run; it runs once
}

type step struct {
	name string
	// run returns a note for the person, or why the step failed.
	run func(context.Context) (string, error)
}

func (c *check) steps() []step {
	return []step{
		{"sign in", c.signIn},
		{"make a folder", c.makeFolder},
		{"write a file", c.writeFile},
		{"read part of it", c.readPart},
		{"move it", c.move},
		{"delete it", c.delete},
	}
}

func (c *check) file() string  { return c.dir + "/check.bin" }
func (c *check) moved() string { return c.dir + "/check moved.bin" }

func (c *check) signIn(ctx context.Context) (string, error) {
	_, err := c.store.List(ctx, "")
	if errors.Is(err, storage.ErrNotFound) {
		return "the Drive's folder is not there yet; the check makes it, as the Drive would", nil
	}
	return "", err
}

func (c *check) makeFolder(ctx context.Context) (string, error) {
	c.made = true
	if err := c.store.MakeFolder(ctx, c.dir); err != nil {
		return "", err
	}
	if _, err := c.store.List(ctx, c.dir); err != nil {
		return "", fmt.Errorf("the server said it made the folder, but then could not list it: %s", reason(err))
	}
	return "", nil
}

func (c *check) writeFile(ctx context.Context) (string, error) {
	size := int64(len(c.content))
	if err := c.store.Put(ctx, c.file(), bytes.NewReader(c.content), size); err != nil {
		return "", err
	}
	e, err := c.store.Stat(ctx, c.file())
	if err != nil {
		return "", fmt.Errorf("the server took the file, but then could not find it: %s", reason(err))
	}
	if e.Folder || e.Size != size {
		return "", fmt.Errorf("the server took a file of %d bytes, but then listed it with %d", size, e.Size)
	}
	return "", nil
}

func (c *check) readPart(ctx context.Context) (string, error) {
	f, err := c.store.OpenPart(ctx, c.file(), partOffset, partLength)
	if errors.Is(err, storage.ErrNoRanges) {
		return "", errors.New("asked for part of the file, the server sent all of it: it does not answer Range requests")
	}
	if err != nil {
		return "", err
	}
	defer f.Body.Close()
	got, err := io.ReadAll(io.LimitReader(f.Body, partLength+1))
	if err != nil {
		return "", fmt.Errorf("the server stopped sending the part: %s", reason(err))
	}
	if !bytes.Equal(got, c.content[partOffset:partOffset+partLength]) {
		return "", fmt.Errorf("asked for bytes %d to %d, the server sent other bytes", partOffset, partOffset+partLength-1)
	}
	return "", nil
}

func (c *check) move(ctx context.Context) (string, error) {
	if err := c.store.Move(ctx, c.file(), c.moved()); err != nil {
		return "", err
	}
	if _, err := c.store.Stat(ctx, c.file()); !errors.Is(err, storage.ErrNotFound) {
		if err != nil {
			return "", fmt.Errorf("the server said it moved the file, but then could not say whether it left: %s", reason(err))
		}
		return "", errors.New("the server said it moved the file, but it is still at its old name")
	}
	e, err := c.store.Stat(ctx, c.moved())
	if err != nil {
		return "", fmt.Errorf("the server said it moved the file, but then could not find it at its new name: %s", reason(err))
	}
	if e.Folder || e.Size != int64(len(c.content)) {
		return "", fmt.Errorf("the server moved the file, but then listed it with %d bytes instead of %d", e.Size, len(c.content))
	}
	return "", nil
}

func (c *check) delete(ctx context.Context) (string, error) {
	if err := c.store.Delete(ctx, c.moved()); err != nil {
		return "", err
	}
	if _, err := c.store.Stat(ctx, c.moved()); !errors.Is(err, storage.ErrNotFound) {
		if err != nil {
			return "", fmt.Errorf("the server said it deleted the file, but then could not say whether it is gone: %s", reason(err))
		}
		return "", errors.New("the server said it deleted the file, but it is still there")
	}
	return "", nil
}

// resume says why an upload that stopped could not resume on this server, or "" when it could.
func (c *check) resume(ctx context.Context) string {
	p := c.dir + "/resume.bin"
	half := len(c.content) / 2
	total := int64(len(c.content))
	if err := c.store.Put(ctx, p, bytes.NewReader(c.content[:half]), int64(half)); err != nil {
		return "writing the first half of a file failed: " + reason(err)
	}
	if err := c.store.PutPart(ctx, p, int64(half), bytes.NewReader(c.content[half:]), total-int64(half), total); err != nil {
		return "the server refused to write the second half into the file from its middle (PUT with Content-Range): " + reason(err)
	}
	f, err := c.store.Open(ctx, p)
	if err != nil {
		return "reading the file back failed: " + reason(err)
	}
	defer f.Body.Close()
	got, err := io.ReadAll(io.LimitReader(f.Body, total+1))
	switch {
	case err != nil:
		return "reading the file back failed: " + reason(err)
	case bytes.Equal(got, c.content):
		return ""
	case bytes.Equal(got, c.content[half:]):
		return "the server ignored where the second half goes (Content-Range) and replaced the file with it"
	}
	return "after the second half was written into the file from its middle, the file held other bytes"
}

// cleanUp removes the check's folder, once, even when ctx has ended. It says so when it cannot.
func (c *check) cleanUp(ctx context.Context, out io.Writer) {
	if c.cleaned || !c.made {
		return
	}
	c.cleaned = true
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	err := c.store.DeleteFolder(ctx, c.dir)
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		return
	}
	fmt.Fprintf(out, "clean up: the check's folder %s%s/ is still on the server (%s); remove it by hand.\n",
		c.store.Address(), c.dir, reason(err))
}

// reason is why a step failed, in words for the person running the check.
func reason(err error) string {
	var status *storage.StatusError
	var request *url.Error
	switch {
	case errors.Is(err, storage.ErrUnauthorized):
		return "the server refused the login; check DRIVE_STORAGE_USER and DRIVE_STORAGE_PASSWORD"
	case errors.As(err, &status):
		what := strconv.Quote(status.Path)
		if status.Path == "" {
			what = "the Drive's folder"
		}
		r := fmt.Sprintf("%s %s: the server answered %s", status.Doing, what, status.Status)
		if status.Location != "" {
			r += ", pointing to " + status.Location
		}
		return r
	case errors.Is(err, context.DeadlineExceeded):
		return "the server did not answer in time"
	case errors.As(err, &request):
		return "could not reach the server: " + request.Err.Error()
	}
	return err.Error()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }
