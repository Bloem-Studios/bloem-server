//go:build darwin

package playback

import (
	"time"

	"golang.org/x/sys/unix"
)

type runtimeGrantSystemClockV3 struct{}

// Darwin MONOTONIC_RAW includes sleep; UPTIME_RAW explicitly excludes it.
// https://github.com/apple-oss-distributions/Libc/blob/main/gen/clock_gettime.3
func (runtimeGrantSystemClockV3) Now() (time.Duration, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); err != nil {
		return 0, err
	}
	return time.Duration(ts.Nano()), nil
}
