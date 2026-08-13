package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type tenantViewerResolverStub struct {
	tenant tenancy.Context
	err    error
	input  access.ResolveInput
	seen   tenancy.Context
}

func (s *tenantViewerResolverStub) ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error) {
	return s.tenant, s.err
}

func (s *tenantViewerResolverStub) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	s.input = input
	s.seen, _ = tenancy.FromContext(ctx)
	if s.seen.AccountID != input.UserID {
		return access.Scope{}, errors.New("wrong tenant subject")
	}
	return access.Scope{UserID: input.UserID, ProfileID: input.ProfileID}, nil
}

func TestTenantViewerResolverAttachesAuthoritativeSubjectTenant(t *testing.T) {
	want := tenancy.Context{
		OrganizationID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:        uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		AccountID:           17,
		OrganizationStatus:  tenancy.OrganizationActive,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      4,
		SecurityRevision:    5,
		OrganizationDefault: true,
	}
	stub := &tenantViewerResolverStub{tenant: want}
	resolver := NewTenantViewerResolver(stub, stub)
	forged := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"),
		AccountID:      999,
	})
	input := access.ResolveInput{UserID: 17, ProfileID: "profile-17", SkipPINVerification: true}

	scope, err := resolver.Resolve(forged, input)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if scope.UserID != 17 || scope.ProfileID != "profile-17" || stub.seen != want || stub.input != input {
		t.Fatalf("scope/tenant/input = %#v / %#v / %#v, want authoritative %#v / %#v", scope, stub.seen, stub.input, want, input)
	}
}

func TestTenantViewerResolverFailsClosedWithoutResolvedSubject(t *testing.T) {
	wantErr := errors.New("tenant lookup failed")
	stub := &tenantViewerResolverStub{err: wantErr}
	resolver := NewTenantViewerResolver(stub, stub)

	if _, err := resolver.Resolve(context.Background(), access.ResolveInput{UserID: 17, ProfileID: "forged"}); !errors.Is(err, wantErr) {
		t.Fatalf("Resolve() error = %v, want %v", err, wantErr)
	}
	if stub.input.UserID != 0 {
		t.Fatalf("viewer resolver was called after tenant failure: %#v", stub.input)
	}
}
