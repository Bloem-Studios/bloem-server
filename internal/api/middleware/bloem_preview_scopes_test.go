package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestPreviewAdminScopeIsAnExactAllowlist(t *testing.T) {
	scopes, err := auth.NormalizeAPIKeyScopes([]string{"admin:preview"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path string
		allow        bool
	}{
		{"POST", "/api/bloem/v1/admin/platform/preview/account", true},
		{"GET", "/api/bloem/v1/admin/platform/preview/settings", true},
		{"PUT", "/api/bloem/v1/admin/platform/preview/settings", true},
		{"PUT", "/api/bloem/v1/admin/platform/preview/libraries", true},
		{"PUT", "/api/bloem/v1/admin/platform/preview/keys", true},
		{"DELETE", "/api/bloem/v1/admin/platform/preview/sessions", true},
		{"POST", "/api/bloem/v1/admin/platform/preview/verify", true},
		{"GET", "/api/bloem/v1/admin/platform/preview/capacity", true},
		{"GET", "/api/bloem/v1/admin/platform/preview/catalog?query=movie", true},
		{"DELETE", "/api/bloem/v1/admin/platform/preview/account", false},
		{"PUT", "/api/v2/admin/settings", false},
		{"POST", "/api/v2/admin/users", false},
		{"POST", "/api/bloem/v1/preview/sessions", false},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		if apiKeyScopesAllow(scopes, r) != test.allow {
			t.Errorf("%s %s", test.method, test.path)
		}
	}
}
