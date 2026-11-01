package handlers

import (
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// organizationDomainAPIError answers a sign-in refused on another
// organization's domain, or nil for any other error. It is a 409 so /api/v2
// renders it as conflict with this detail, which the sign-in page shows; a
// 403 would render as permission_denied, which it reports as a disabled
// account. Bloem-owned.
func organizationDomainAPIError(err error) *APIError {
	var domainErr *auth.OrganizationDomainError
	if !errors.As(err, &domainErr) {
		return nil
	}
	message := "This account signs in at its organization's own address."
	if domainErr.SignInURL != "" {
		message = "This account signs in at " + domainErr.SignInURL + "."
	}
	return apiError(http.StatusConflict, "wrong_organization_domain", message)
}
