// Package web holds the Drive's interface. `bun run build` compiles the TypeScript and React sources in this folder
// into dist/, and the Go build embeds dist/ into the binary. Without a build, dist/ holds only .gitkeep and the Drive
// says the interface is missing.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Interface is the built interface: index.html, favicon.svg and assets/.
func Interface() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is always embedded
	}
	return sub
}
