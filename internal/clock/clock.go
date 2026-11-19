//go:build !testclock

// Package clock is the Drive's clock: what it uses to date the trash and to know when 30 days have passed.
//
// In a Drive built with the build tag testclock, and only there, the environment variable
// SUPERIT_DRIVE_TEST_CLOCK_SHIFT (a Go duration, such as "744h") moves the clock forward, so the tests through HTTP
// (test/harness builds the Drive that way) can see what happens 30 days later without waiting for them. A Drive built
// any other way, such as the released image, reads no such variable.
package clock

import "time"

// Now is the time.
func Now() time.Time { return time.Now() }

// Shift is how far the clock is moved: always 0 here.
func Shift() time.Duration { return 0 }
