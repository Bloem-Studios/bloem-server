package playback

import "errors"

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
