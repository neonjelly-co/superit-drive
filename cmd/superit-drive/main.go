// Command superit-drive runs Superit Drive: a web server that keeps a project's files on WebDAV storage.
//
//	superit-drive                 run the Drive
//	superit-drive check-storage   check whether the WebDAV server in the storage settings can hold the Drive's files
//	superit-drive licenses        print the Drive's licence and the notices of the software built into it
//
// Every setting comes from environment variables; see internal/settings or docs/settings.md.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	superitdrive "github.com/neonjelly-co/superit-drive"
	"github.com/neonjelly-co/superit-drive/internal/app"
	"github.com/neonjelly-co/superit-drive/internal/storagecheck"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

const usage = `Usage:
  superit-drive                 Run the Drive. Every setting is an environment variable (see docs/settings.md).
  superit-drive check-storage   Check whether the WebDAV server in DRIVE_STORAGE_URL, DRIVE_STORAGE_USER,
                                DRIVE_STORAGE_PASSWORD and DRIVE_STORAGE_FOLDER can hold the Drive's files.
                                Exits 0 when it can, 1 when a step failed, 2 when a setting is missing.
  superit-drive licenses        Print the licence the Drive is published under (AGPL-3.0), then the notices and
                                licences of the software that is not its own and is built into it.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args := os.Args[1:]; {
	case len(args) == 0:
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		slog.SetDefault(log) // what has no logger of its own (opening the index) writes its lines the same way
		if err := app.Run(ctx, os.Getenv, version, log); err != nil {
			log.Error(err.Error())
			stop()
			os.Exit(1)
		}
	case len(args) == 1 && args[0] == "check-storage":
		code := storagecheck.Run(ctx, os.Getenv, version, os.Stdout, os.Stderr)
		stop()
		os.Exit(code)
	case len(args) == 1 && args[0] == "licenses":
		fmt.Print(superitdrive.License, "\n", superitdrive.ThirdPartyNotices)
	default:
		fmt.Fprintln(os.Stderr, usage)
		stop()
		os.Exit(2)
	}
}
