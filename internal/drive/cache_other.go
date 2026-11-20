//go:build !linux

package drive

import "os"

// dropCached does nothing outside Linux, where the Drive's container runs (cache_linux.go).
func dropCached(string) {}

// dropRange does nothing outside Linux (cache_linux.go).
func dropRange(*os.File, int64, int64) {}
