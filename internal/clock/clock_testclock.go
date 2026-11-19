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
