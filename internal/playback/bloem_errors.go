package playback

import "fmt"

// Bloem sentinel errors for playback admission. Each wraps the Silo refusal
// it is, so Silo's error mapping (the v3 terminal reasons, Jellyfin, ABS)
// answers a Silo client with that refusal rather than an internal error.
var (
	// ErrTenantTranscodesExceeded is the TENANT organization's shared
	// transcode pool running dry (bloem-park growth G2) — distinct from
	// the per-user cap in its own message, and a transcode limit to Silo's
	// mapping: retryable, and videos that need no converting still play.
	ErrTenantTranscodesExceeded = fmt.Errorf("tenant transcode capacity exhausted: %w", ErrTooManyTranscodes)
	// ErrTenantFrozen blocks every playback start for a frozen tenant
	// organization (dunning, or a plan downgrade below the accounts in use):
	// playback denied by server policy.
	ErrTenantFrozen = fmt.Errorf("tenant is frozen: %w", ErrPlaybackNotAllowed)
)

// A household's shared pool running dry (bloem_household_reservation.go). They
// wrap Silo's own limits, so every refusal path treats them as the stream or
// transcode limit they are.
var (
	ErrHouseholdStreamsExceeded    = fmt.Errorf("household stream limit reached: %w", ErrTooManyStreams)
	ErrHouseholdTranscodesExceeded = fmt.Errorf("household transcode limit reached: %w", ErrTooManyTranscodes)
)
