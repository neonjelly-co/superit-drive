// Package drive holds the Drive's operations, joining the index and the storage. It does not decide who may do
// what: by the time a call gets here, the access package has already said yes.
//
// Layout on the storage, under the Drive's folder (DRIVE_STORAGE_FOLDER), with real names so anyone with the storage
// login can read it:
//
//	My files/
//	    reka@example.com/          one top folder per member's "My files", named after their email address
//	        Tax notes 2026.pdf     files and folders with their real names, at any depth
//	        Drafts/
//	    bence@example.com/
//	Shared folders/
//	    Clients/                   one top folder per shared folder, named after it ("Clients (2)" when another
//	        Bakery Ltd/            shared folder, one in the trash included, holds that name)
//	.superit-drive/                the Drive's own hidden folders
//	    trash/
//	        My files/reka@example.com/          one trash per place, beside the place's own path
//	            2026-11-19 7f3a9c1e/Offer.docx  one folder per deleted item: the day it was deleted, a random part,
//	                                            and in it the item under its real name
//	        Shared folders/Payroll/
//	            2026-11-19 0c41d2aa/Payroll     a deleted shared folder: its whole top folder, beside what was deleted
//	                                            from it, until it is restored or removed for good with its trash
//	    uploads/
//	        3f9c0b…e1                           one file per upload while its bytes arrive, named after a random id;
//	                                            moved into its folder when whole
//	    index/<storage id>/copy-…42.sqlite     the latest copy of the index (copies.go)
//	    storage-id                              the Drive's mark: says the storage is the one its index belongs to
//
// Every change is two steps, the storage and then the index. files.go has uploads in one request, downloads and the
// name rules, uploads.go resumable uploads and moving any upload into place, zip.go folders downloaded as one zip,
// folders.go new folders, renames and moves, trash.go the trash, shared.go shared folders, sharing.go the access given
// on places and items, links.go public links, sendin.go files sent in through an upload-only link, reread.go keeping the index in step with changes made on the storage
// directly (a folder re-read when it is opened), walk.go the slow walk through the whole storage, rollback.go the
// storage put back to a snapshot, held.go handing over or deleting the My files of a member who left, copies.go the
// copies of the index in the data folder and on the storage, adopt.go making the index from one, and this file the
// places, paths and listings.
package drive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/storage"
)

// MyFilesFolder is the folder on the storage that holds every member's "My files".
const MyFilesFolder = "My files"

// HiddenFolder is the folder on the storage that holds the Drive's own folders, beside the places. Its name starts
// with a dot, so WebDAV programs that hide such names do not show it among the files.
const HiddenFolder = ".superit-drive"

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

// Options are what a Drive needs besides its index and storage.
type Options struct {
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Log receives what the Drive does on its own, such as emptying the trash. Nothing is logged when nil.
	Log *slog.Logger
	// StopAt is called at named points of the Drive's work where a test build may stop it, as if it were killed
	// (clock.StopAt); nothing when nil.
	StopAt func(point string)
	// LinkLimit is how much an upload-only link takes in, in bytes, until it is reset: DefaultLinkLimit when 0. Only a
	// test build sets another (clock.LinkLimit).
	LinkLimit int64
	// LinkFiles is how many files an upload-only link takes in until it is reset: DefaultLinkFiles when 0. Only a test
	// build sets another (clock.LinkFiles).
	LinkFiles int64
}

// Drive is one Drive: its index and its storage.
type Drive struct {
	index   *index.Index
	storage *storage.Client
	now     func() time.Time
	log     *slog.Logger

	// writing holds the storage paths being changed (an upload in flight, a rename or move on its way), so two
	// changes of one name cannot both pass the check that it is free, and a listing leaves the name alone.
	mu      sync.Mutex
	writing map[string]bool

	// generations counts the Drive's own changes to each folder; a listing that began before one is not applied.
	generations generations

	// locks holds one lock per place: see inPlace and wholePlace.
	locks placeLocks

	// resume is whether the storage can resume an upload (CanResume), once it is known.
	resumeMu sync.Mutex
	resume   resumeState

	// uses counts the uses of public links between writes to the index (links.go).
	uses uses

	// rereading holds one look at a folder on the storage at a time per folder (reread.go); rechecks are the looks
	// made in the background when a folder is opened; mark is whether the storage is the Drive's own.
	rereading stripes
	rechecks  rechecks
	mark      mark
	// expiring lets one run at a time remove what the trash has kept for 30 days: the run at start and a person opening
	// the trash would otherwise delete the same entry on the storage at once, and one of them fail.
	expiring sync.Mutex
	// background is the context of the Drive's own work in the background, which Stop ends and waits for.
	background context.Context
	stop       context.CancelFunc
	working    sync.WaitGroup
	// logged is when each kind of warning was last logged, so a recurring one is logged now and then.
	logged logged
	// acts holds the storage paths claimed for the acts on held My files under way (held.go).
	acts acts
	// stopAt is Options.StopAt.
	stopAt func(point string)
	// linkLimit is how much an upload-only link takes in until it is reset; intake keeps what is on its way through each
	// (sendin.go).
	linkLimit int64
	linkFiles int64
	intake    intake
}

// By is who does something that is recorded: a member, by user id, a guest (Member is then ""), or a visitor through
// the public link with the id Link (both are then empty).
type By struct {
	Member string
	Guest  index.Guest
	Link   int64
}

// added is who added an item from outside the project, as the index labels it: a guest, or a visitor through a link.
func (b By) added() index.AddedBy {
	return index.AddedBy{Guest: b.Guest.Address, Link: b.Link != 0}
}

// Now is the Drive's clock: what it dates things by, and decides end dates on.
func (d *Drive) Now() time.Time { return d.now() }

// New makes a Drive.
func New(x *index.Index, s *storage.Client, o Options) *Drive {
	d := &Drive{index: x, storage: s, now: o.Now, log: o.Log, writing: map[string]bool{}, stopAt: o.StopAt, linkLimit: o.LinkLimit, linkFiles: o.LinkFiles}
	if d.linkFiles <= 0 {
		d.linkFiles = DefaultLinkFiles
	}
	if d.linkLimit <= 0 {
		d.linkLimit = DefaultLinkLimit
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.stopAt == nil {
		d.stopAt = func(string) {}
	}
	if d.log == nil {
		d.log = slog.New(slog.DiscardHandler)
	}
	d.background, d.stop = context.WithCancel(context.Background())
	return d
}

// Stop ends the Drive's work in the background (the looks at folders opened, see Recheck) and waits for it, before the
// index is closed.
func (d *Drive) Stop() {
	d.stop()
	d.working.Wait()
}

// Place finds a place by its id. A shared folder in the trash is not found: see DeletedPlace.
func (d *Drive) Place(ctx context.Context, id int64) (index.Place, error) {
	return d.index.PlaceByID(ctx, id)
}

// DeletedPlace finds a shared folder in the trash by its id.
func (d *Drive) DeletedPlace(ctx context.Context, id int64) (index.Place, error) {
	return d.index.DeletedPlace(ctx, id)
}

// ErrPlaceGone: the place went to the trash (or was removed for good) after the request found it.
var ErrPlaceGone = errors.New("the place is no longer there")

// placeLocks holds one lock per place. A change inside a place holds it shared (inPlace); moving a whole shared
// folder into the trash, out of it, or away for good holds it alone (wholePlace).
type placeLocks struct {
	mu sync.Mutex
	m  map[int64]*sync.RWMutex
}

func (l *placeLocks) of(id int64) *sync.RWMutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[int64]*sync.RWMutex{}
	}
	if l.m[id] == nil {
		l.m[id] = &sync.RWMutex{}
	}
	return l.m[id]
}

// inPlace holds the place's lock for a change inside it (an upload, a new folder, a rename or move, deleting or
// restoring an item, and a listing, which makes a missing top folder again), and checks that the place is still there
// and not in the trash. Without it, a request that found a shared folder just before it went to the trash could make
// its top folder again afterwards, and write into it.
func (d *Drive) inPlace(ctx context.Context, p index.Place) (release func(), err error) {
	l := d.locks.of(p.ID)
	l.RLock()
	if _, err := d.index.PlaceByID(ctx, p.ID); err != nil {
		l.RUnlock()
		if errors.Is(err, index.ErrNoPlace) {
			return nil, ErrPlaceGone
		}
		return nil, err
	}
	return l.RUnlock, nil
}

// inPlaces is inPlace for a change in two places at once, such as a move from one into the other: it holds both
// places' locks, the one with the lower id first, so that two such changes in opposite directions never each hold one
// and wait for the other.
func (d *Drive) inPlaces(ctx context.Context, a, b index.Place) (release func(), err error) {
	if a.ID > b.ID {
		a, b = b, a
	}
	first, err := d.inPlace(ctx, a)
	if err != nil {
		return nil, err
	}
	second, err := d.inPlace(ctx, b)
	if err != nil {
		first()
		return nil, err
	}
	return func() { second(); first() }, nil
}

// wholePlaceWait is how long moving a whole shared folder waits for the changes inside it to end.
const wholePlaceWait = 10 * time.Second

// wholePlace holds the place's lock alone, once the changes inside it have ended; ErrBusy when they have not within
// wholePlaceWait. Changes inside the place that start meanwhile go first: a long upload is never held up by this.
func (d *Drive) wholePlace(ctx context.Context, id int64) (release func(), err error) {
	l := d.locks.of(id)
	deadline := time.Now().Add(wholePlaceWait)
	for !l.TryLock() {
		if time.Now().After(deadline) {
			return nil, ErrBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return l.Unlock, nil
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
	d.marked(ctx)
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

// pathIn is the storage path reached from top (a place's top folder, or its folder in the trash) through names, one
// folder or item name each. Every path the Drive makes from a name passes through here. ok is false when any name is
// not one plain segment ("", ".", "..", or one holding a "/", which only a misbehaving server could list): such a
// name never becomes a path, so no name a person or the storage supplies can reach outside its place, at any depth.
func pathIn(top string, names ...string) (p string, ok bool) {
	if top == "" {
		return "", false
	}
	p = top
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			return "", false
		}
		p += "/" + name
	}
	return p, true
}

// itemPath is where a live item of the place is on the storage: under the place's top folder, through the folders
// above it.
func itemPath(p index.Place, it index.Item) (string, bool) {
	names := make([]string, 0, len(it.Above)+1)
	for _, f := range it.Above {
		names = append(names, f.Name)
	}
	return pathIn(p.Folder, append(names, it.Name)...)
}

// folderPath is where a folder of the place is on the storage; nil is the place's top folder.
func folderPath(p index.Place, folder *index.Item) (string, bool) {
	if folder == nil {
		return pathIn(p.Folder)
	}
	return itemPath(p, *folder)
}

// folderID is a folder's id as the index takes it: 0 for the place's top folder.
func folderID(folder *index.Item) int64 {
	if folder == nil {
		return 0
	}
	return folder.ID
}

// ErrGone: the item, or the folder it is in or was going to, is no longer on the storage.
var ErrGone = errors.New("no longer on the storage")

// Item finds the live item with id in the place, at any depth.
func (d *Drive) Item(ctx context.Context, p index.Place, id int64) (index.Item, error) {
	return d.index.Item(ctx, p.ID, id)
}

// ChildrenPage is what the index holds in a folder of the place (nil: its top folder), folders first, then by name: at
// most limit items, after the item after (nil: from the first), and whether there are more. It does not ask the storage,
// and takes no lock: a look at the folder on the storage (Recheck) brings the index in step.
func (d *Drive) ChildrenPage(ctx context.Context, p index.Place, folder *index.Item, after *index.Item, limit int) ([]index.Item, bool, error) {
	return d.index.ChildrenAfter(ctx, p.ID, folderID(folder), after, limit)
}

// ensureTop makes the place's top folder on the storage if it has gone (someone removed it directly), before something
// is put into it; folder is where it goes, nil for the top folder. One request when the folder is there.
func (d *Drive) ensureTop(ctx context.Context, p index.Place, folder *index.Item) error {
	if folder != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, storageTimeout)
	defer cancel()
	err := d.storage.CreateFolder(ctx, p.Folder)
	switch {
	case err == nil || errors.Is(err, storage.ErrExists):
		return nil
	case errors.Is(err, storage.ErrNotFound):
		return storageError(d.storage.MakeFolder(ctx, p.Folder))
	}
	return storageError(err)
}

// claim marks storage paths as being changed. ok is false when another change holds any of them; then none is held.
func (d *Drive) claim(paths ...string) (release func(), ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range paths {
		if d.writing[p] {
			return nil, false
		}
	}
	for _, p := range paths {
		d.writing[p] = true
	}
	return func() {
		d.mu.Lock()
		for _, p := range paths {
			delete(d.writing, p)
		}
		d.mu.Unlock()
	}, true
}

// claimed says whether a change holds the storage path p.
func (d *Drive) claimed(p string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writing[p]
}

// busyInside says whether a change holds a path inside the folder at dir, such as an upload into it.
func (d *Drive) busyInside(dir string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for p := range d.writing {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// busyAt says whether a change holds the path p, or a folder above it: what is at p is moving.
func (d *Drive) busyAt(p string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for w := range d.writing {
		if w == p || strings.HasPrefix(p, w+"/") {
			return true
		}
	}
	return false
}

// busyIn says whether a change holds the path p, or anything inside it.
func (d *Drive) busyIn(p string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for w := range d.writing {
		if w == p || strings.HasPrefix(w, p+"/") {
			return true
		}
	}
	return false
}

// A folderKey names one folder of one place for the generation counters: folder 0 is the place's top folder, and
// trashFolder the place's trash.
type folderKey struct{ place, folder int64 }

const trashFolder = -1

// generations counts the Drive's own changes per folder, in a fixed number of counters that folders share (a shared
// counter only makes a listing skip its update more often). They live in memory: a listing never outlasts the Drive.
type generations [256]atomic.Uint64

func (g *generations) at(k folderKey) *atomic.Uint64 {
	h := uint64(k.place)*0x9e3779b97f4a7c15 + uint64(k.folder)
	h ^= h >> 31
	return &g[h%uint64(len(g))]
}

// changed records that the Drive changed what is in these folders. Call it after the change is made on the storage
// and before it is written to the index: then a listing that read the storage before the change sees, when it comes
// to write, that it is out of date.
func (d *Drive) changed(keys ...folderKey) {
	for _, k := range keys {
		d.generations.at(k).Add(1)
	}
}

// since returns a check that says whether the Drive changed the folder after since was called.
func (d *Drive) since(k folderKey) func() bool {
	counter := d.generations.at(k)
	start := counter.Load()
	return func() bool { return counter.Load() != start }
}

// Used is about how much space the Drive's files take: the sum of the sizes of every file the index knows, in every
// place, the trash included. Files put on the storage directly count once a look at their folder has seen them, and
// files gone from it until they are dropped from the index.
func (d *Drive) Used(ctx context.Context) (int64, error) {
	return d.index.Used(ctx)
}
