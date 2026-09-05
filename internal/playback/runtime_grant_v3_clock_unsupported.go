//go:build !linux && !darwin

package playback

import (
	"errors"
	"time"
)

type runtimeGrantSystemClockV3 struct{}

func (runtimeGrantSystemClockV3) Now() (time.Duration, error) {
	return 0, errors.New("suspend-inclusive runtime grant clock unsupported")
}
