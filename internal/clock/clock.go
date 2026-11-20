//go:build !testclock

// Package clock is the Drive's clock: what it uses to date the trash and to know when 30 days have passed.
//
// In a Drive built with the build tag testclock, and only there, the environment variable
// SUPERIT_DRIVE_TEST_CLOCK_SHIFT (a Go duration, such as "744h") moves the clock forward, so the tests through HTTP
// (test/harness builds the Drive that way) can see what happens 30 days later without waiting for them; and
// SUPERIT_DRIVE_TEST_WALK_EVERY and SUPERIT_DRIVE_TEST_WALK_PAUSE (Go durations) make the walk through the storage start
// that soon after the Drive starts, run that often and rest that long after each folder; and
// SUPERIT_DRIVE_TEST_STOP_AT names a point in the Drive's work (StopAt) where it stops at once, as if it were killed, so a
// test can see what the Drive does when it starts again; and SUPERIT_DRIVE_TEST_COPY_QUIET, SUPERIT_DRIVE_TEST_COPY_LATEST
// and SUPERIT_DRIVE_TEST_COPY_RETRY (Go durations) make the copy of the index follow access changes that soon. A Drive
// built any other way, such as the released image, reads no such variable.
package clock

import "time"

// Now is the time.
func Now() time.Time { return time.Now() }

// Shift is how far the clock is moved: always 0 here.
func Shift() time.Duration { return 0 }

// Walk is the timing of the walk through the storage that a test build sets (SUPERIT_DRIVE_TEST_WALK_EVERY and
// SUPERIT_DRIVE_TEST_WALK_PAUSE): ok is always false here, and the Drive walks at its own pace.
func Walk() (every, pause time.Duration, ok bool) { return 0, 0, false }

// Copying is the timing of the copies of the index that a test build sets (SUPERIT_DRIVE_TEST_COPY_QUIET,
// SUPERIT_DRIVE_TEST_COPY_LATEST and SUPERIT_DRIVE_TEST_COPY_RETRY): ok is always false here, and the Drive copies at
// its own pace.
func Copying() (quiet, latest, retry time.Duration, ok bool) { return 0, 0, 0, false }

// StopAt is a point in the Drive's work where a test build may stop (SUPERIT_DRIVE_TEST_STOP_AT): here it does nothing.
func StopAt(point string) {}
