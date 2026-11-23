package handlers

import (
	"errors"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// Refusals for a household's shared pool (Bloem household logins). The pool
// is the household's: its main account, its profiles and its logins draw on
// the same streams and conversions, so a member refused because the pool is
// full is told that the household's limit is reached, and that someone else
// in the household may be the one playing. The reason codes are Silo's, so a
// client that handles them needs nothing new. sessionStartErrorV3 asks here
// first; an account playing on its own keeps Silo's wording.

const (
	householdStreamLimitMessageV3 = "Your household is already playing as many videos at once as its plan allows. " +
		"Paused videos count too. Stop one of yours, or ask someone else in your household to stop theirs, then try again."
	householdTranscodeLimitMessageV3 = "This video needs to be converted to play on this device, but your household is already " +
		"converting as many videos as its plan allows. Paused videos count too. Stop one of yours, or ask someone else " +
		"in your household to stop theirs, then try again. Videos that don't need converting will still play."
)

// bloemHouseholdStartErrorV3 is the terminal for a start the household's
// shared pool refused, nil for any other error.
func bloemHouseholdStartErrorV3(err error) *transportErrorV3 {
	switch {
	case errors.Is(err, playback.ErrHouseholdStreamsExceeded):
		return &transportErrorV3{reason: "stream_limit_reached", message: householdStreamLimitMessageV3, retryable: true}
	case errors.Is(err, playback.ErrHouseholdTranscodesExceeded):
		return &transportErrorV3{reason: transcodeLimitReachedReasonV3, message: householdTranscodeLimitMessageV3, retryable: true}
	default:
		return nil
	}
}
