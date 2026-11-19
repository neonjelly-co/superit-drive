// Package memory proves the Drive stays inside its memory figure: it runs the Drive's container image the way it is
// installed, limited to 64 MiB of memory (as the daemon's catalogue counts it), against the stand-in storage, and moves
// files much larger than that through it, many at once: 40 downloads and 10 uploads. It checks that the container was
// not killed and that every byte arrived as it was sent, and reports the memory the container used.
//
// It is slow (it builds the image and moves about 13 GiB), so it runs only with the build tag memory:
//
//	make test-memory
package memory
