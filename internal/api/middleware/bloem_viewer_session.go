package middleware

// Bloem-owned. How the viewer-access gate answers a session that its
// profile's organization revoked.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
)

// ErrSessionRevokedByOrganization is the viewer resolver's refusal of an
// account-login session that started before the profile's organization
// revoked the account (internal/api/bloem_org_session_revocation.go). The
// resolver has ended the session by then. It wraps access.ErrProfileNotFound,
// so a caller that knows only that error keeps refusing the profile.
var ErrSessionRevokedByOrganization = fmt.Errorf("%w: the session started before the organization revoked the account", access.ErrProfileNotFound)

// refuseSessionRevokedByOrganization writes the gate's answer to
// ErrSessionRevokedByOrganization and reports whether err was one. The answer
// is the 401 session_invalid of any ended session, so the client signs in
// again instead of treating the profile as missing.
func refuseSessionRevokedByOrganization(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, ErrSessionRevokedByOrganization) {
		return false
	}
	writeUnauthorized(w, "Session is no longer valid", ReasonSessionInvalid)
	return true
}
