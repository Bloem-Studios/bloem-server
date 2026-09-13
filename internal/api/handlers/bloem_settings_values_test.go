package handlers

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/settingskeys"
)

// No definition uses the account scope today, but the scope arrives from the
// query string. The refusal for direct-profile sessions runs before any
// contract logic, so the day an account-scoped definition exists, this
// boundary is already in force rather than silently absent.
func TestKeyedSettingsRefuseAccountScopeForDirectProfileSessions(t *testing.T) {
	handler, _ := newValuesTestHandler(t)
	key := settingskeys.NavShortcuts

	direct := func(method, requestKey string, body []byte) *httptest.ResponseRecorder {
		target := "/settings/values/" + requestKey + "?scope=account"
		var req *http.Request
		if body == nil {
			req = httptest.NewRequest(method, target, nil)
		} else {
			req = httptest.NewRequest(method, target, bytes.NewReader(body))
		}
		ctx := apimw.SetClaims(req.Context(), &auth.Claims{
			UserID: 1, ProfileID: "profile-1", AuthMethod: auth.AuthMethodDirectProfile,
		})
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("key", requestKey)
		req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, routeCtx))
		rec := httptest.NewRecorder()
		switch method {
		case http.MethodGet:
			handler.HandleGetValue(rec, req)
		case http.MethodPut:
			handler.HandleSetValue(rec, req)
		case http.MethodDelete:
			handler.HandleDeleteValue(rec, req)
		}
		return rec
	}

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := direct(method, key, []byte(`{"value":{}}`))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "account-scoped") {
			t.Fatalf("%s account scope via direct session = %d %s, want the explicit account-scope refusal",
				method, rec.Code, rec.Body.String())
		}
	}

	// The refusal precedes the definition lookup, so an unknown key answers
	// the same way — the boundary must not depend on the contract's contents.
	unknown := direct(http.MethodGet, "no.such.key", nil)
	if unknown.Code != http.StatusForbidden || !strings.Contains(unknown.Body.String(), "account-scoped") {
		t.Fatalf("unknown key account scope = %d %s, want the refusal ahead of definition lookup",
			unknown.Code, unknown.Body.String())
	}

	// An account session reaching the same URL is not caught by this refusal:
	// whatever else the contract says about the scope, the answer is not the
	// direct-session message.
	rec := routeValues(t, handler, http.MethodGet, key, "scope=account", nil)
	if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "account-scoped") {
		t.Fatalf("account session hit the direct-session refusal: %d %s", rec.Code, rec.Body.String())
	}
}
