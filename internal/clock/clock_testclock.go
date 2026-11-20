//go:build testclock

package clock

import (
	"os"
	"time"
)

// shift is read once, when the Drive starts. A value that is not a duration is no shift.
var shift = func() time.Duration {
	d, err := time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_CLOCK_SHIFT"))
	if err != nil {
		return 0
	}
	return d
}()

// Now is the time, moved forward by SUPERIT_DRIVE_TEST_CLOCK_SHIFT.
func Now() time.Time { return time.Now().Add(shift) }

// Shift is how far the clock is moved.
func Shift() time.Duration { return shift }

// Walk is the timing of the walk through the storage set by SUPERIT_DRIVE_TEST_WALK_EVERY (when it starts after the
// Drive does, and how often) and SUPERIT_DRIVE_TEST_WALK_PAUSE (how long it rests after each folder, 0 when not set);
// ok is false without the first.
func Walk() (every, pause time.Duration, ok bool) {
	every, err := time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_WALK_EVERY"))
	if err != nil || every <= 0 {
		return 0, 0, false
	}
	pause, _ = time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_WALK_PAUSE"))
	return every, max(pause, 0), true
}

// Copying is the timing of the copies of the index set by SUPERIT_DRIVE_TEST_COPY_QUIET (how long after the last access
// change nothing more changes before a copy is written), SUPERIT_DRIVE_TEST_COPY_LATEST (the longest a change waits for
// its copy) and SUPERIT_DRIVE_TEST_COPY_RETRY (how soon a copy the storage did not take is tried again); ok is false
// without the first. The others are the first when not set.
func Copying() (quiet, latest, retry time.Duration, ok bool) {
	quiet, err := time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_COPY_QUIET"))
	if err != nil || quiet <= 0 {
		return 0, 0, 0, false
	}
	latest, retry = quiet, quiet
	if d, err := time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_COPY_LATEST")); err == nil && d > 0 {
		latest = d
	}
	if d, err := time.ParseDuration(os.Getenv("SUPERIT_DRIVE_TEST_COPY_RETRY")); err == nil && d > 0 {
		retry = d
	}
	return quiet, latest, retry, true
}

// StopAt stops the Drive at once, with exit code 3 and nothing written or closed, when SUPERIT_DRIVE_TEST_STOP_AT names
// point: as if it were killed there.
func StopAt(point string) {
	if os.Getenv("SUPERIT_DRIVE_TEST_STOP_AT") == point {
		os.Stdout.WriteString("stopped at " + point + " (SUPERIT_DRIVE_TEST_STOP_AT)\n")
		os.Exit(3)
	}
}
