package auth_test

// Bloem-owned. Covers bloem_operator_authority.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

func TestResolveOperatorAuthorityRequiresEnabledMatchingIncarnation(t *testing.T) {
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	replaced := uuid.MustParse("66666666-7777-4888-8999-aaaaaaaaaaaa")

	tests := []struct {
		name    string
		store   platformAdminAccountStoreStub
		wantErr error
	}{
		{name: "missing", store: platformAdminAccountStoreStub{err: auth.ErrNotFound}, wantErr: auth.ErrOperatorIneligible},
		{name: "nil_account", store: platformAdminAccountStoreStub{}, wantErr: auth.ErrOperatorIneligible},
		{name: "disabled", store: platformAdminAccountStoreStub{account: &models.User{ID: 41, AccountIncarnationID: incarnation, Role: "admin", Enabled: false}}, wantErr: auth.ErrOperatorIneligible},
		{name: "replaced_incarnation", store: platformAdminAccountStoreStub{account: &models.User{ID: 41, AccountIncarnationID: replaced, Role: "admin", Enabled: true}}, wantErr: auth.ErrOperatorIneligible},
		{name: "store_failure", store: platformAdminAccountStoreStub{err: errors.New("database down")}, wantErr: auth.ErrOperatorAuthorityUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := auth.ResolveOperatorAuthority(context.Background(), auth.NewPlatformAdminAuthorizer(tt.store), 41, incarnation)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ResolveOperatorAuthority() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	for _, role := range []string{"admin", "user"} {
		t.Run("enabled_"+role, func(t *testing.T) {
			store := platformAdminAccountStoreStub{account: &models.User{ID: 41, AccountIncarnationID: incarnation, Role: role, Enabled: true}}
			got, err := auth.ResolveOperatorAuthority(context.Background(), auth.NewPlatformAdminAuthorizer(store), 41, incarnation)
			if err != nil {
				t.Fatalf("ResolveOperatorAuthority() error = %v", err)
			}
			want := auth.OperatorAuthority{AccountID: 41, AccountIncarnationID: incarnation, PlatformAdmin: role == "admin"}
			if got != want {
				t.Fatalf("ResolveOperatorAuthority() = %#v, want %#v", got, want)
			}
		})
	}
}

type operatorlessAuthorizer struct{}

func (operatorlessAuthorizer) IsPlatformAdmin(context.Context, int) (bool, error) { return true, nil }

type mismatchedOperatorAuthorizer struct{ operatorlessAuthorizer }

func (mismatchedOperatorAuthorizer) ResolveOperator(context.Context, int, uuid.UUID) (auth.OperatorAuthority, error) {
	return auth.OperatorAuthority{AccountID: 42, AccountIncarnationID: uuid.New(), PlatformAdmin: true}, nil
}

// An authorizer without the operator capability, or one that answers for a
// different account, must deny rather than skip the check.
func TestResolveOperatorAuthorityFailsClosed(t *testing.T) {
	incarnation := uuid.New()
	if _, err := auth.ResolveOperatorAuthority(context.Background(), operatorlessAuthorizer{}, 41, incarnation); !errors.Is(err, auth.ErrOperatorAuthorityUnavailable) {
		t.Fatalf("without capability: error = %v, want ErrOperatorAuthorityUnavailable", err)
	}
	if _, err := auth.ResolveOperatorAuthority(context.Background(), nil, 41, incarnation); !errors.Is(err, auth.ErrOperatorAuthorityUnavailable) {
		t.Fatalf("nil authorizer: error = %v, want ErrOperatorAuthorityUnavailable", err)
	}
	if _, err := auth.ResolveOperatorAuthority(context.Background(), mismatchedOperatorAuthorizer{}, 41, incarnation); !errors.Is(err, auth.ErrOperatorIneligible) {
		t.Fatalf("mismatched answer: error = %v, want ErrOperatorIneligible", err)
	}
}
