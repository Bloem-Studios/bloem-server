package policy

import "errors"

// ErrTenantFactsUnavailable marks a missing, incomplete, or inactive
// server-resolved tenant context at a policy adapter boundary.
var ErrTenantFactsUnavailable = errors.New("tenant facts unavailable")
