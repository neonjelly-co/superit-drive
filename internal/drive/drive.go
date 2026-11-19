// Package drive holds the Drive's operations, joining the index and the storage. It does not decide who may do
// what: by the time a call gets here, the access package has already said yes.
//
// Layout on the storage, under the Drive's folder (DRIVE_STORAGE_FOLDER), with real names so anyone with the storage
// login can read it:
//
//	My files/
//	    reka@example.com/        one top folder per member's "My files", named after their email address
//	    bence@example.com/
//
// Shared folders and the hidden folders for the trash, uploads in progress and the index copy are added by the
// tickets that bring them.
package drive

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// MyFilesFolder is the folder on the storage that holds every member's "My files".
const MyFilesFolder = "My files"

// storageTimeout bounds one listing or folder change on the storage.
const storageTimeout = 30 * time.Second

// ErrStorage marks an error that came from the storage rather than from the Drive itself. The storage package's own
// errors (such as storage.ErrUnauthorized) are kept inside it.
var ErrStorage = errors.New("the storage could not do it")

func storageError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrStorage, err)
}

// Drive is one Drive: its index and its storage.
type Drive struct {
	index   *index.Index
	storage *storage.Client
}

// New makes a Drive.
func New(x *index.Index, s *storage.Client) *Drive {
	return &Drive{index: x, storage: s}
}

// Place finds a place by its id.
func (d *Drive) Place(ctx context.Context, id int64) (index.Place, error) {
	return d.index.PlaceByID(ctx, id)
}

// MyFiles returns the member's "My files", making its top folder on the storage the first time.
//
// The folder is named after the member's email address at that moment. The index ties it to the member's user id,
// so a later change of address does not move or orphan anything.
func (d *Drive) MyFiles(ctx context.Context, m members.Member) (index.Place, error) {
	p, err := d.index.MyFiles(ctx, m.UserID)
	if err == nil || !errors.Is(err, index.ErrNoPlace) {
		return p, err
	}
	folder, err := d.freeFolder(ctx, path.Join(MyFilesFolder, folderName(m)))
	if err != nil {
		return index.Place{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	if err := d.storage.MakeFolder(ctx, folder); err != nil {
		return index.Place{}, storageError(err)
	}
	return d.index.AddMyFiles(ctx, m.UserID, folder)
}

// freeFolder returns want, or want with " (2)", " (3)" and so on added when another place already uses it (a removed
// member's held files can keep the name of an address that now belongs to someone else).
func (d *Drive) freeFolder(ctx context.Context, want string) (string, error) {
	for n := 1; n < 1000; n++ {
		candidate := want
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)", want, n)
		}
		taken, err := d.index.FolderTaken(ctx, candidate)
		if err != nil || !taken {
			return candidate, err
		}
	}
	return "", fmt.Errorf("no free folder name for %q", want)
}

// folderName is the name of a member's top folder: their email address in lower case, made safe as one folder name.
func folderName(m members.Member) string {
	name := strings.ToLower(strings.TrimSpace(m.Email))
	name = strings.NewReplacer("/", "_", "\\", "_").Replace(name)
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return m.UserID
	}
	return name
}

// List reads the place's top folder from the storage, brings the index in step, and returns what the index holds.
// If the top folder has gone from the storage, it is made again, empty.
func (d *Drive) List(ctx context.Context, p index.Place) ([]index.Item, error) {
	sctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	entries, err := d.storage.List(sctx, p.Folder)
	if errors.Is(err, storage.ErrNotFound) {
		entries, err = nil, d.storage.MakeFolder(sctx, p.Folder)
	}
	if err != nil {
		return nil, storageError(err)
	}
	seen := make([]index.Seen, len(entries))
	for i, e := range entries {
		seen[i] = index.Seen{Name: e.Name, Folder: e.Folder, Size: e.Size, Changed: e.Changed}
	}
	if err := d.index.SetTopItems(ctx, p.ID, seen); err != nil {
		return nil, err
	}
	return d.index.TopItems(ctx, p.ID)
}
