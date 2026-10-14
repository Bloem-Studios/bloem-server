package middleware

import (
	"net/http"
	"regexp"
)

// Exact admin routes only: this scope grants neither a guest session nor
// ordinary user, entitlement, library or server-settings administration.
func bloemPreviewScopeRoutes() []scopeRoute {
	return []scopeRoute{
		{http.MethodPost, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/account$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/settings$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/settings$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/libraries$`)},
		{http.MethodPut, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/keys$`)},
		{http.MethodDelete, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/sessions$`)},
		{http.MethodPost, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/verify$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/capacity$`)},
		{http.MethodGet, regexp.MustCompile(`^/api/bloem/v1/admin/platform/preview/catalog$`)},
	}
}
