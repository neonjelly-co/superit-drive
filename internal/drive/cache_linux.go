package drive

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCached asks the kernel to let go of what it caches of a file the Drive wrote and synced, and will not read again
// soon (a copy of the index): the page cache counts against the container's memory too, and need not hold it.
func dropCached(file string) {
	f, err := os.Open(file)
	if err != nil {
		return
	}
	defer f.Close()
	dropRange(f, 0, 0)
}

// dropRange asks the kernel to let go of what it caches of the bytes of f from offset, length of them (0: to its end).
// Only what is on disk already goes: dirty pages are written first by a sync.
func dropRange(f *os.File, offset, length int64) {
	_ = unix.Fadvise(int(f.Fd()), offset, length, unix.FADV_DONTNEED)
}
