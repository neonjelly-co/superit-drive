// Package app starts the Drive: it reads the settings, opens the member list, the index and the storage, and serves
// HTTP until ctx ends. cmd/superit-drive only calls Run.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/clock"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/mail"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/server"
	"github.com/neonjelly-co/superit-drive/internal/settings"
	"github.com/neonjelly-co/superit-drive/internal/storage"
	"github.com/neonjelly-co/superit-drive/internal/transfers"
	"github.com/neonjelly-co/superit-drive/web"
)

// MemoryLimit is Go's memory limit for the Drive (debug.SetMemoryLimit), unless GOMEMLIMIT sets another. The Drive is
// counted at 64 MiB, and the kernel's network buffers for the transfers in flight are charged to it too (package
// transfers), so Go keeps well below: past the limit its garbage collector works harder instead of letting the heap
// grow.
const MemoryLimit = 24 << 20

// MaxHeaderBytes is the most a request's first line and headers may take together, well below the megabyte Go allows
// when nothing is set: a request with more is answered 431 before any of it is looked at, so that nobody makes the
// Drive read, keep or log a megabyte for each request. An address the Drive answers and the headers a browser and the
// reverse proxy send with it (identity, cookies, an Origin) take a few kilobytes.
const MaxHeaderBytes = 32 << 10

// Run runs the Drive until ctx ends. getenv is os.Getenv; version is logged at start.
func Run(ctx context.Context, getenv func(string) string, version string, log *slog.Logger) error {
	s, err := settings.FromEnv(getenv)
	if err != nil {
		return err
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(MemoryLimit)
	}

	list, err := members.Watch(ctx, s.MembersFile, log)
	if err != nil {
		return err
	}
	store := storage.New(s.Storage.URL, s.Storage.User, s.Storage.Password, s.Storage.Folder)
	if u := s.Storage.URL; u != nil && u.Scheme == "http" && !nearby(u.Hostname()) {
		log.Warn("DRIVE_STORAGE_URL is http, not https: the storage's login and every file travel unencrypted to "+
			"another machine. Use https unless the way there is private", "host", u.Hostname())
	}
	if shift := clock.Shift(); shift != 0 {
		log.Warn("this is a test build: its clock is moved forward", "by", shift.String())
	}
	// A data folder without an index (a new server) makes it from a copy, its own or the storage's, when there is one.
	if err := drive.Adopt(ctx, s.DataDir, store, log, clock.Now); err != nil {
		return err
	}
	if s.HeaderSecret == settings.DevelopmentSecret {
		log.Warn("DRIVE_HEADER_SECRET is the repository's development secret, which everyone can read: whoever reaches the " +
			"Drive's port is whoever they say they are. Only for development on your own computer; make one of your own otherwise")
	}
	x, err := index.Open(ctx, s.DataDir)
	if err != nil {
		return err
	}
	defer x.Close()
	if most, ok := clock.SearchMost(); ok {
		x.EndSearchesAfter(most)
		log.Warn("this is a test build: a search is ended after the time the test sets", "after", most.String())
	}
	linkLimit, _ := clock.LinkLimit()
	linkFiles, _ := clock.LinkFiles()
	d := drive.New(x, store, drive.Options{Now: clock.Now, Log: log, StopAt: clock.StopAt, LinkLimit: linkLimit, LinkFiles: linkFiles})
	defer d.Stop() // the looks at folders under way in the background end, before the index closes

	// A hand-over or deletion of a removed member's files that a stop cut short holds what it changes on the storage, before
	// anything looks there (Tidy, below, finishes it).
	if err := d.HoldActs(ctx); err != nil {
		return err
	}

	// A rollback of the storage the settings declare is taken in once (rollback.go): the walk below then starts at once.
	if rb := s.Rollback; rb != nil {
		if _, err := d.TakeIn(ctx, &drive.Rollback{At: rb.At, To: rb.To, By: rb.By}); err != nil {
			return err
		}
	}

	// Sends the Drive's own mail in the background, so a request never waits for the mail server; none without one.
	mailer := mail.New(s.Mail, s.Address.Hostname(), log)
	mailCtx, stopMail := context.WithCancel(ctx)
	mailDone := make(chan struct{})
	go func() {
		defer close(mailDone)
		mailer.Run(mailCtx)
	}()
	defer func() { stopMail(); <-mailDone }()

	srv := server.New(server.Config{
		ProjectName:  s.ProjectName,
		Address:      s.Address,
		HeaderSecret: s.HeaderSecret,
		Members:      list,
		Drive:        d,
		Capacity:     s.Capacity,
		Mail:         mailer,
		Interface:    web.Interface(),
		Log:          log,
	})

	// Sends guests the codes they ask for, one at a time, in the background: the page that asks answers at once, the same
	// way for every address.
	codesCtx, stopCodes := context.WithCancel(ctx)
	codesDone := make(chan struct{})
	go func() {
		defer close(codesDone)
		srv.SendCodes(codesCtx)
	}()
	defer func() { stopCodes(); <-codesDone }() // before the index closes

	// A guest whose address is in the member list is a member: what was given to them is theirs as a member, by user id.
	// Now, and whenever the list changes.
	admit := func(l *members.List) {
		if _, err := d.AdmitMembers(ctx, l, srv.WorkingInvitations); err != nil && ctx.Err() == nil {
			log.Error("guests who joined the project could not be made members; it is tried again when the member list changes", "error", err)
		}
	}
	admit(list.Current())
	admitCtx, stopAdmit := context.WithCancel(ctx)
	admitDone := make(chan struct{})
	go func() {
		defer close(admitDone)
		seen := list.Current()
		tick := time.NewTicker(members.PollInterval)
		defer tick.Stop()
		for {
			select {
			case <-admitCtx.Done():
				return
			case <-tick.C:
				if l := list.Current(); l != seen {
					seen = l
					admit(l)
				}
			}
		}
	}()
	defer func() { stopAdmit(); <-admitDone }() // before the index closes

	// Removes what has been in the trash for 30 days, uploads abandoned for a day, and guests' ended sessions and old
	// codes: now, and every hour while the Drive runs.
	tidyCtx, stopTidy := context.WithCancel(ctx)
	tidyDone := make(chan struct{})
	go func() {
		defer close(tidyDone)
		d.Tidy(tidyCtx, time.Hour)
	}()
	defer func() { stopTidy(); <-tidyDone }() // before the index closes

	// Walks through the storage now and then, and brings the index in step with what was changed there directly.
	walking := drive.DefaultWalking
	if every, pause, ok := clock.Walk(); ok {
		walking = drive.Walking{First: every, Every: every, Pause: pause}
		log.Warn("this is a test build: the walk through the storage runs at the pace the test sets", "every", every.String(), "pause", pause.String())
	}
	walkCtx, stopWalk := context.WithCancel(ctx)
	walkDone := make(chan struct{})
	go func() {
		defer close(walkDone)
		d.Walk(walkCtx, walking)
	}()
	defer func() { stopWalk(); <-walkDone }() // before the index closes

	// Writes a copy of the index into the data folder and onto the storage: now, after access changes, every night, and as
	// the Drive stops.
	copying := drive.DefaultCopying
	if quiet, latest, retry, ok := clock.Copying(); ok {
		copying.Quiet, copying.Latest, copying.Retry, copying.Poll = quiet, latest, retry, min(copying.Poll, quiet/4)
		copying.Apart = 0
		log.Warn("this is a test build: the copy of the index follows access changes at the pace the test sets",
			"quiet", quiet.String(), "latest", latest.String(), "retry", retry.String())
	}
	// It stops after the last request has been answered (as the uses below), and copies once more if access changed.
	copyCtx, stopCopies := context.WithCancel(context.WithoutCancel(ctx))
	copiesDone := make(chan struct{})
	go func() {
		defer close(copiesDone)
		d.KeepCopies(copyCtx, s.DataDir, copying)
	}()
	defer func() { stopCopies(); <-copiesDone }() // before the index closes

	// Writes how often each public link and each guest's invitation was used, counted in memory as visitors and guests
	// use them: every half minute, and once more when the Drive stops (after the last request, before the index closes).
	usesCtx, stopUses := context.WithCancel(context.WithoutCancel(ctx))
	usesDone := make(chan struct{})
	go func() {
		defer close(usesDone)
		d.KeepUses(usesCtx, 30*time.Second)
	}()
	defer func() { stopUses(); <-usesDone }()

	// Every connection's network buffers are capped (package transfers).
	ln, err := transfers.Listen(s.Listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    MaxHeaderBytes,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	log.Info("listening", "address", ln.Addr().String(), "version", version, "storage", store.Address(),
		"members", list.Current().Len(), "data", s.DataDir, "mail", mailer.Describe(),
		"memoryLimit", strconv.FormatInt(debug.SetMemoryLimit(-1)>>20, 10)+" MiB")
	if err := httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone // the requests under way have ended, or had ten seconds to
	log.Info("stopped")
	return nil
}

// nearby says whether a storage's host is this computer or a name without a dot (another container's on the same
// network, as in the worked example): where a storage is reached over http without its login crossing a network that
// others share.
func nearby(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost" || !strings.Contains(host, ".")
}
