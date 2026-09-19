package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type bloemAccountTenantFunc func(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)

func (f bloemAccountTenantFunc) Resolve(ctx context.Context, id int, org *uuid.UUID, legacy bool) (tenancy.Context, error) {
	return f(ctx, id, org, legacy)
}

type bloemAccountViewerFunc func(context.Context, access.ResolveInput) (access.Scope, error)

func (f bloemAccountViewerFunc) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	return f(ctx, input)
}

func TestBloemAccountProfileViewerAuthorityOrder(t *testing.T) {
	for _, tc := range []struct {
		name, profile     string
		direct, suspended bool
		status            int
		calls             string
	}{
		{"profileless", "", false, false, 204, ""},
		{"selected", "profile-1", false, false, 204, "tenant viewer "},
		{"suspended", "profile-1", false, true, 403, "tenant "},
		{"direct without header", "", true, false, 403, ""},
		{"direct with header", "profile-1", true, false, 403, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := ""
			org := uuid.New()
			tenant := apimw.NewTenantMiddleware(bloemAccountTenantFunc(func(_ context.Context, id int, requested *uuid.UUID, legacy bool) (tenancy.Context, error) {
				calls += "tenant "
				if id != 7 || requested != nil || !legacy {
					t.Fatal("caller selected tenant authority")
				}
				if tc.suspended {
					return tenancy.Context{}, tenancy.ErrTenantSuspended
				}
				return tenancy.Context{AccountID: id, OrganizationID: org}, nil
			}))
			viewer := apimw.NewViewerAccessMiddleware(bloemAccountViewerFunc(func(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
				calls += "viewer "
				resolved, ok := tenancy.FromContext(ctx)
				if !ok || resolved.AccountID != 7 || resolved.OrganizationID != org {
					t.Fatal("viewer ran without authoritative tenant")
				}
				return access.Scope{UserID: 7, ProfileID: tc.profile}, nil
			}))
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			handler := apimw.RejectDirectProfileSession(bloemAccountProfileViewer(tenant, viewer)(next))
			req := httptest.NewRequest("GET", "/api/v1/auth/account/capability", nil)
			req.Header.Set("X-Profile-Id", tc.profile)
			claims := &auth.Claims{UserID: 7, TokenType: auth.TokenTypeAccess}
			if tc.direct {
				claims.AuthMethod = auth.AuthMethodDirectProfile
				claims.ProfileID = "profile-1"
			}
			req = req.WithContext(apimw.SetClaims(req.Context(), claims))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status || calls != tc.calls {
				t.Fatalf("status/order=%d/%q, want %d/%q", rec.Code, calls, tc.status, tc.calls)
			}
		})
	}
}
