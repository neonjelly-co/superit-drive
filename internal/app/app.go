// Package app starts the Drive: it reads the settings, opens the member list, the index and the storage, and serves
// HTTP until ctx ends. cmd/superit-drive only calls Run.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
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
	x, err := index.Open(ctx, s.DataDir)
	if err != nil {
		return err
	}
	defer x.Close()
	store := storage.New(s.Storage.URL, s.Storage.User, s.Storage.Password, s.Storage.Folder)
	if shift := clock.Shift(); shift != 0 {
		log.Warn("this is a test build: its clock is moved forward", "by", shift.String())
	}
	d := drive.New(x, store, drive.Options{Now: clock.Now, Log: log})
	defer d.Stop() // the looks at folders under way in the background end, before the index closes

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

	// Removes what has been in the trash for 30 days, and uploads abandoned for a day: now, and every hour while the
	// Drive runs.
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

	// Writes how often each public link was used, counted in memory as visitors use them: every half minute, and once
	// more when the Drive stops (after the last request, before the index closes).
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
