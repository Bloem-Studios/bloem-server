//go:build linux

package playback

import (
	"time"

	"golang.org/x/sys/unix"
)

type runtimeGrantSystemClockV3 struct{}

// CLOCK_BOOTTIME includes suspend; Linux CLOCK_MONOTONIC_RAW does not.
// https://man7.org/linux/man-pages/man2/clock_gettime.2.html
func (runtimeGrantSystemClockV3) Now() (time.Duration, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return 0, err
	}
	return time.Duration(ts.Nano()), nil
}
