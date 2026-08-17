package api

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestPlaybackSessionLimitProviderResolvesCanonicalProfileGroup(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{OrganizationID: uuid.New(), AccountID: 999})
	groups := &profileSessionLimitGroupProvider{
		want: access.GroupSubject{
			OrganizationID: organizationID,
			AccountID:      7,
			ProfileID:      "profile-v2",
		},
		policy: &access.GroupPolicy{
			MaxStreams:      1,
			MaxTranscodes:   1,
			RequestsAllowed: true,
		},
	}
	provider := playbackSessionLimitProvider(
		sessionLimitUserRepositoryStub{user: &models.User{
			ID:                    7,
			MaxStreams:            8,
			MaxTranscodes:         4,
			TranscodeAllowed:      true,
			AudioTranscodeAllowed: true,
		}},
		groups,
		sessionLimitTenantResolverStub{tenant: tenancy.Context{OrganizationID: organizationID, AccountID: 7}},
		nil,
	)

	limits, err := provider(ctx, 7, "profile-v2")
	if err != nil {
		t.Fatalf("session limit provider error: %v", err)
	}
	if limits.MaxStreams != 1 || limits.MaxTranscodes != 1 {
		t.Fatalf("session limits = %+v, want profile group stream/transcode limits of 1", limits)
	}
}

func TestPlaybackSessionLimitProviderFailsClosedWithoutTenantResolver(t *testing.T) {
	provider := playbackSessionLimitProvider(
		sessionLimitUserRepositoryStub{user: &models.User{ID: 7}},
		&profileSessionLimitGroupProvider{},
		nil,
		nil,
	)
	if _, err := provider(context.Background(), 7, "profile-v2"); !errors.Is(err, tenancy.ErrTenantUnavailable) {
		t.Fatalf("session limit provider error = %v, want tenant unavailable", err)
	}
}

func TestPlaybackSessionContextProviderCarriesAuthoritativeTenant(t *testing.T) {
	want := tenancy.Context{
		OrganizationID: uuid.MustParse("10000000-0000-0000-0000-000000000002"),
		AccountID:      7,
	}
	provider := playbackSessionContextProvider(sessionLimitTenantResolverStub{tenant: want})

	ctx, err := provider(context.Background(), 7, "profile-v2")
	if err != nil {
		t.Fatalf("context provider error: %v", err)
	}
	got, ok := tenancy.FromContext(ctx)
	if !ok || got != want {
		t.Fatalf("tenant context = (%+v, %t), want %+v", got, ok, want)
	}
}

type sessionLimitTenantResolverStub struct {
	tenant tenancy.Context
	err    error
}

func (s sessionLimitTenantResolverStub) ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error) {
	return s.tenant, s.err
}

type sessionLimitUserRepositoryStub struct {
	user *models.User
}

func (s sessionLimitUserRepositoryStub) GetByID(context.Context, int) (*models.User, error) {
	return s.user, nil
}

type profileSessionLimitGroupProvider struct {
	want   access.GroupSubject
	policy *access.GroupPolicy
}

func (p *profileSessionLimitGroupProvider) ResolvePolicy(_ context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	if subject != p.want {
		return nil, access.ErrGroupNotFound
	}
	return p.policy, nil
}
