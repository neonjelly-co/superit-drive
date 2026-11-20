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
	_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}
