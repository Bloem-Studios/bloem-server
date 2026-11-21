package playback

import (
	"errors"
	"fmt"
)

// Bloem sentinel errors for playback admission.
var (
	// ErrTenantTranscodesExceeded is the TENANT organization's shared
	// transcode pool running dry (bloem-park growth G2) — distinct from
	// the per-user cap so a member can tell "you hit your limit" from
	// "your server is busy".
	ErrTenantTranscodesExceeded = errors.New("tenant transcode capacity exhausted")
	// ErrTenantFrozen blocks every playback start for a frozen tenant
	// organization (dunning, or a plan downgrade below the accounts in use).
	ErrTenantFrozen = errors.New("tenant is frozen")
)

// A household's shared pool running dry (bloem_household_reservation.go). They
// wrap Silo's own limits, so every refusal path treats them as the stream or
// transcode limit they are.
var (
	ErrHouseholdStreamsExceeded    = fmt.Errorf("household stream limit reached: %w", ErrTooManyStreams)
	ErrHouseholdTranscodesExceeded = fmt.Errorf("household transcode limit reached: %w", ErrTooManyTranscodes)
)
