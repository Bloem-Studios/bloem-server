package handlers

import (
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// A Silo app reads a playback start refusal by Silo's reasons. Bloem's
// organization refusals must arrive as the Silo refusal they are, not as
// internal_error: a frozen organization is playback denied by server policy,
// and an organization's spent transcode pool is a transcode limit (retryable,
// and videos that need no converting still play), as a household's pool is.
func TestSessionStartErrorV3GivesOrganizationRefusalsSiloReasons(t *testing.T) {
	for _, tc := range []struct {
		err       error
		reason    string
		retryable bool
	}{
		{err: playback.ErrTenantFrozen, reason: "policy_denied"},
		{err: playback.ErrTenantTranscodesExceeded, reason: "transcode_limit_reached", retryable: true},
		{err: playback.ErrHouseholdStreamsExceeded, reason: "stream_limit_reached", retryable: true},
		{err: playback.ErrHouseholdTranscodesExceeded, reason: "transcode_limit_reached", retryable: true},
	} {
		got := sessionStartErrorV3(fmt.Errorf("wrapped: %w", tc.err))
		if got == nil || got.reason != tc.reason || got.retryable != tc.retryable {
			t.Errorf("sessionStartErrorV3(%v) = %#v, want reason %q retryable %v", tc.err, got, tc.reason, tc.retryable)
		}
	}
}
