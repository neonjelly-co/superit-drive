// Command superit-drive runs Superit Drive: a web server that keeps a project's files on WebDAV storage.
//
// Every setting comes from environment variables; see internal/settings or the README.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/neonjelly-co/superit-drive/internal/app"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := app.Run(ctx, os.Getenv, version, log)
	stop()
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}
