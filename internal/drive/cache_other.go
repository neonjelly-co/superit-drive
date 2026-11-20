//go:build !linux

package drive

// dropCached does nothing outside Linux, where the Drive's container runs (cache_linux.go).
func dropCached(string) {}
