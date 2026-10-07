package middleware

import (
	"context"
	"errors"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

type nativeTenantSelectionResolver struct {
	defaultTenant, boundTenant tenancy.Context
	selected                   *uuid.UUID
	legacy                     bool
}

func (r *nativeTenantSelectionResolver) Resolve(_ context.Context, _ int, id *uuid.UUID, legacy bool) (tenancy.Context, error) {
	r.selected = id
	r.legacy = legacy
	if id == nil {
		return r.defaultTenant, nil
	}
	return r.boundTenant, nil
}

// Compare the companion directly with the existing owning surface middleware.
// The resolver is a focused selection double, not fabricated request authority.
func TestNativeMutationTenantMatchesOwningSurfaceUnit(t *testing.T) {
	a, b, membership := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name                  string
		native, direct, stale bool
		bound                 bool
	}{
		{"v1-account-bound-claims-ignored", false, false, false, false},
		{"v1-account-stale-bound-claims-ignored", false, false, true, false},
		{"v1-direct-profile", false, true, false, true},
		{"v1-direct-profile-stale", false, true, true, true},
		{"v2-account-bound", true, false, false, true},
		{"v2-account-stale", true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &nativeTenantSelectionResolver{defaultTenant: tenancy.Context{AccountID: 1, OrganizationID: a}, boundTenant: tenancy.Context{AccountID: 1, OrganizationID: b, MembershipID: membership, PolicyRevision: 7, SecurityRevision: 8}}
			claims := &auth.Claims{UserID: 1, OrganizationID: b.String(), MembershipID: membership.String(), PolicyRevision: 7, SecurityRevision: 8}
			if tc.direct {
				claims.AuthMethod = auth.AuthMethodDirectProfile
			}
			if tc.stale {
				claims.PolicyRevision = 6
			}
			middleware := NewTenantMiddleware(resolver)
			got, err := middleware.nativeMutationTenant(context.Background(), claims, tc.native)
			if tc.stale && tc.bound {
				var refusal *catalog.NativePhaseRefusal
				if !errors.As(err, &refusal) || refusal.Code != "unauthenticated" {
					t.Fatalf("stale result %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if (resolver.selected != nil) != tc.bound || resolver.legacy == tc.bound {
				t.Fatalf("selection id=%v legacy=%v", resolver.selected, resolver.legacy)
			}
			rec := httptest.NewRecorder()
			called := false
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				called = true
				own, _ := tenancy.FromContext(r.Context())
				if own.OrganizationID != got.OrganizationID {
					t.Fatal("surface tenant differs")
				}
			})
			r := httptest.NewRequest("POST", "/surface", nil).WithContext(SetClaims(context.Background(), claims))
			if tc.native {
				middleware.ResolveNative(next).ServeHTTP(rec, r)
			} else {
				middleware.ResolveLegacy(next).ServeHTTP(rec, r)
			}
			if tc.stale && tc.bound {
				if called || rec.Code != 401 {
					t.Fatal("owning surface failed to deny")
				}
			} else if !called {
				t.Fatal("owning surface did not delegate")
			}
		})
	}
}
