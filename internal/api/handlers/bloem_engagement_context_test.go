package handlers

import (
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBloemEngagementRequiresNativePlatformContextAndPreservesActor(t *testing.T) {
	for _, scope := range []auth.AdminScope{"", auth.AdminScopeOrganization, auth.AdminScopePlatform} {
		t.Run(string(scope), func(t *testing.T) {
			registry := &fakePromotionRegistry{}
			req := httptest.NewRequest(http.MethodPost, "/platform/promotions", strings.NewReader(adminPromotionCreateBody))
			if scope != "" {
				req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 77, Scope: scope}))
			}
			rec := httptest.NewRecorder()
			RequireBloemPlatformContext(http.HandlerFunc((&AdminPromotionsHandler{registry: registry}).HandleCreate)).ServeHTTP(rec, req)
			if scope == auth.AdminScopePlatform {
				if rec.Code != 201 || registry.createdBy != 77 {
					t.Fatalf("status=%d actor=%d body=%s", rec.Code, registry.createdBy, rec.Body.String())
				}
			} else if rec.Code != 403 || registry.createdBy != 0 {
				t.Fatalf("scope=%s status=%d actor=%d", scope, rec.Code, registry.createdBy)
			}
		})
	}
}
