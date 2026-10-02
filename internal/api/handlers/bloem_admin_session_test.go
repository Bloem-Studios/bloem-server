package handlers

import (
	"context"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bloemRevocationUpdateRequest(t *testing.T, h *AdminHandler, claims *auth.Claims, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/42", strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", "42")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
	ctx = tenancy.WithContext(ctx, tenancy.Context{OrganizationID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), AccountID: claims.UserID})
	rec := httptest.NewRecorder()
	h.HandleUpdateUser(rec, req.WithContext(apimw.SetClaims(ctx, claims)))
	return rec
}
