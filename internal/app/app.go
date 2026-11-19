// Package app starts the Drive: it reads the settings, opens the member list, the index and the storage, and serves
// HTTP until ctx ends. cmd/superit-drive only calls Run.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/server"
	"github.com/neonjelly-co/superit-drive/internal/settings"
	"github.com/neonjelly-co/superit-drive/internal/storage"
	"github.com/neonjelly-co/superit-drive/web"
)

// Run runs the Drive until ctx ends. getenv is os.Getenv; version is logged at start.
func Run(ctx context.Context, getenv func(string) string, version string, log *slog.Logger) error {
	s, err := settings.FromEnv(getenv)
	if err != nil {
		return err
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

	srv := server.New(server.Config{
		ProjectName:  s.ProjectName,
		HeaderSecret: s.HeaderSecret,
		Members:      list,
		Drive:        drive.New(x, store),
		Interface:    web.Interface(),
		Log:          log,
	})

	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()

	log.Info("listening", "address", ln.Addr().String(), "version", version, "storage", store.Address(),
		"members", list.Current().Len(), "data", s.DataDir)
	if err := httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}
