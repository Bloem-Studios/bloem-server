package middleware

// Bloem tenant-fact coverage for the policy middleware, moved out of Silo's
// policy_gates_test.go.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestPolicyActingAdminMiddlewareRejectsMissingTenantFacts(t *testing.T) {
	next := NewPolicyActingAdminMiddleware(newMiddlewarePolicyPDP(t), nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/sessions", nil)
	req = req.WithContext(SetClaims(req.Context(), adminClaims()))
	rec := httptest.NewRecorder()

	next.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func TestPolicyMiddlewarePopulatesResolvedTenantFacts(t *testing.T) {
	want := policy.TenantFacts{
		Present:                    true,
		Legacy:                     true,
		OrganizationID:             "10000000-0000-0000-0000-000000000001",
		MembershipID:               "20000000-0000-0000-0000-000000000001",
		OrganizationStatus:         "initializing",
		MembershipStatus:           "active",
		OrganizationPolicyRevision: 7,
		MembershipSecurityRevision: 11,
	}

	t.Run("acting admin", func(t *testing.T) {
		decider := &capturingPermissionDecider{decision: policy.PermissionDecision{Allowed: true}}
		response := captureActingAdminResponse(NewPolicyActingAdminMiddleware(decider, nil), adminClaims(), "")
		if response.code != http.StatusNoContent {
			t.Fatalf("status = %d body = %s, want no content", response.code, response.body)
		}
		if len(decider.inputs) != 1 || decider.inputs[0].Tenant != want {
			t.Fatalf("permission inputs = %+v, want tenant %+v", decider.inputs, want)
		}
	})

	t.Run("permission gate", func(t *testing.T) {
		decider := &capturingPermissionDecider{decision: policy.PermissionDecision{Allowed: true}}
		response := captureMarkerEditResponse(NewPolicyPermissionMiddleware(
			fakePermissionUserLoader{user: &models.User{ID: 7, Role: "user", Enabled: true, Permissions: []string{policy.PermissionMarkerEdit}}},
			nil,
			nil,
			decider,
		), userClaims())
		if response.code != http.StatusNoContent {
			t.Fatalf("status = %d body = %s, want no content", response.code, response.body)
		}
		if len(decider.inputs) != 1 || decider.inputs[0].Tenant != want {
			t.Fatalf("permission inputs = %+v, want tenant %+v", decider.inputs, want)
		}
	})
}

func TestPolicyMiddlewareRejectsTenantForDifferentAccount(t *testing.T) {
	t.Run("acting admin", func(t *testing.T) {
		decider := &capturingPermissionDecider{decision: policy.PermissionDecision{Allowed: true}}
		next := NewPolicyActingAdminMiddleware(decider, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodGet, "/admin/sessions", nil)
		ctx := SetClaims(req.Context(), adminClaims())
		ctx = tenancy.WithContext(ctx, middlewareResolvedTenant(8))
		rec := httptest.NewRecorder()

		next.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d body = %s, want internal error", rec.Code, rec.Body.String())
		}
		if len(decider.inputs) != 0 {
			t.Fatalf("decider calls = %d, want 0", len(decider.inputs))
		}
	})

	t.Run("permission gate", func(t *testing.T) {
		decider := &capturingPermissionDecider{decision: policy.PermissionDecision{Allowed: true}}
		gate := NewPolicyPermissionMiddleware(
			fakePermissionUserLoader{user: &models.User{ID: 7, Role: "user", Enabled: true, Permissions: []string{policy.PermissionMarkerEdit}}},
			nil,
			nil,
			decider,
		)
		next := gate.RequireMarkerEdit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodPut, "/markers/files/5", nil)
		ctx := SetClaims(req.Context(), userClaims())
		ctx = tenancy.WithContext(ctx, middlewareResolvedTenant(8))
		rec := httptest.NewRecorder()

		next.ServeHTTP(rec, req.WithContext(ctx))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d body = %s, want internal error", rec.Code, rec.Body.String())
		}
		if len(decider.inputs) != 0 {
			t.Fatalf("decider calls = %d, want 0", len(decider.inputs))
		}
	})
}

func TestPolicyPermissionMiddlewareDoesNotTrustPreexistingTenantFacts(t *testing.T) {
	decider := &capturingPermissionDecider{decision: policy.PermissionDecision{Allowed: true}}
	middleware := &PolicyPermissionMiddleware{pdp: decider}
	req := httptest.NewRequest(http.MethodPut, "/markers/files/5", nil)
	req = req.WithContext(tenancy.WithContext(req.Context(), middlewareResolvedTenant(8)))

	_, err := middleware.checkPermission(req, policy.PermissionInput{
		UserID: 7,
		Tenant: policy.TenantFacts{
			Present:                    true,
			Legacy:                     true,
			OrganizationID:             "forged-organization",
			MembershipID:               "forged-membership",
			OrganizationStatus:         "initializing",
			MembershipStatus:           "active",
			OrganizationPolicyRevision: 99,
			MembershipSecurityRevision: 99,
		},
	})
	if !errors.Is(err, policy.ErrTenantFactsUnavailable) {
		t.Fatalf("checkPermission() error = %v, want ErrTenantFactsUnavailable", err)
	}
	if len(decider.inputs) != 0 {
		t.Fatalf("decider calls = %d, want 0", len(decider.inputs))
	}
}

func middlewareResolvedTenant(accountID int) tenancy.Context {
	return tenancy.Context{
		OrganizationID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:        uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:           accountID,
		OrganizationStatus:  tenancy.OrganizationInitializing,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      7,
		SecurityRevision:    11,
		Legacy:              true,
		OrganizationDefault: true,
	}
}

type capturingPermissionDecider struct {
	inputs   []policy.PermissionInput
	decision policy.PermissionDecision
}

func (d *capturingPermissionDecider) CheckPermission(_ context.Context, input policy.PermissionInput) (policy.PermissionDecision, policy.Meta, error) {
	d.inputs = append(d.inputs, input)
	return d.decision, policy.Meta{}, nil
}
