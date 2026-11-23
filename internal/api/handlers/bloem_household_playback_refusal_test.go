package handlers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// A household member refused because the household's shared pool is full
// hears that the household's limit is reached, under the same reason code
// clients already handle; an account on its own keeps Silo's wording.
func TestHouseholdPoolRefusalSaysTheHouseholdsLimitIsReached(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
	}{
		{playback.ErrHouseholdStreamsExceeded, "stream_limit_reached"},
		{playback.ErrHouseholdTranscodesExceeded, transcodeLimitReachedReasonV3},
		{fmt.Errorf("reserve playback capacity: %w", playback.ErrHouseholdTranscodesExceeded), transcodeLimitReachedReasonV3},
	} {
		got := sessionStartErrorV3(tc.err)
		if got.reason != tc.reason || !got.retryable {
			t.Fatalf("%v: reason %q retryable %v; want %q, retryable", tc.err, got.reason, got.retryable, tc.reason)
		}
		if !strings.Contains(got.message, "household") || strings.Contains(got.message, "your account") {
			t.Fatalf("%v: message %q; want the household's limit, not the account's", tc.err, got.message)
		}
	}
}

func TestAccountLimitRefusalKeepsSilosWording(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{playback.ErrTooManyStreams, "Your account is already playing as many videos at once as it's allowed to."},
		{playback.ErrTooManyTranscodes, "your account is already converting as many videos as it's allowed to."},
	} {
		if got := sessionStartErrorV3(tc.err); !strings.Contains(got.message, tc.want) {
			t.Fatalf("%v: message %q; want Silo's %q", tc.err, got.message, tc.want)
		}
	}
}
