package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

// A missing issuer is a bad request, not a conflict with an existing setup.
func TestBloemPreviewEnsureAccountRejectsMissingIssuerAsInvalid(t *testing.T) {
	h := NewBloemPreviewAdminHandler(preview.NewAdmin(nil, nil))
	for _, body := range []string{`{}`, `{"issuer":""}`} {
		req := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/admin/preview/account", strings.NewReader(body))
		req = req.WithContext(middleware.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}))
		rec := httptest.NewRecorder()
		h.HandleEnsureAccount(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error":"invalid_preview_settings"`) {
			t.Fatalf("%s: response=%d %s", body, rec.Code, rec.Body.String())
		}
	}
}
