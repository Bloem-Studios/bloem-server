package downloads

// Bloem tenant/profile-group download policy coverage moved out of Silo's
// policy_action_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	policyengine "github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestDownloadCapabilityResolvesBloemProfileGroup(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      9,
	})
	user := &models.User{ID: 9}
	svc := newPolicyActionTestService(user, config.DownloadConfig{Enabled: true, TranscodeEnabled: true}, true, nil)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID: "profile-v2",
		profilePolicy: &access.GroupPolicy{
			DownloadAllowed:          false,
			DownloadTranscodeAllowed: false,
			RequestsAllowed:          true,
		},
		legacyPolicy: &access.GroupPolicy{
			DownloadAllowed:          true,
			DownloadTranscodeAllowed: true,
			RequestsAllowed:          true,
		},
	})

	capability, err := svc.Capability(ctx, user.ID, "profile-v2")
	if err != nil {
		t.Fatalf("Capability() error: %v", err)
	}
	if capability.DownloadAllowed || capability.TranscodeUserAllowed || len(capability.QualityPresets) != 0 {
		t.Fatalf("Capability() = %+v, want profile group to deny downloads and transcodes", capability)
	}
}

func TestDownloadCreateUsesProfileGroupTranscodeDenial(t *testing.T) {
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		AccountID:      9,
		Legacy:         true,
	})
	user := &models.User{ID: 9}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: true}
	svc := NewService(
		nil, nil, nil,
		fakeFileResolver{file: &models.MediaFile{ID: 3, ContentID: "movie-1", Resolution: "1080p"}},
		nil, nil, fakeUserRepo{user}, allowDownloadItemAccess{}, nil, &cfg,
	)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID: "profile-strict",
		profilePolicy: &access.GroupPolicy{
			DownloadAllowed:          true,
			DownloadTranscodeAllowed: false,
			RequestsAllowed:          true,
		},
		legacyPolicy: &access.GroupPolicy{
			DownloadAllowed:          true,
			DownloadTranscodeAllowed: true,
			RequestsAllowed:          true,
		},
	})

	_, err := svc.Create(ctx, user.ID, CreateRequest{
		FileID:    3,
		Quality:   Quality10Mbps,
		ProfileID: "profile-strict",
		DeviceID:  "device-1",
	}, catalog.AccessFilter{})
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("Create(transcoded download) error = %v, want ErrDownloadNotAllowed", err)
	}
}

func TestResolveDirectFileUsesProfileGroupDownloadDenial(t *testing.T) {
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		AccountID:      9,
		Legacy:         true,
	})
	user := &models.User{ID: 9}
	cfg := config.DownloadConfig{Enabled: true}
	svc := NewService(nil, nil, nil, nil, nil, nil, fakeUserRepo{user}, nil, nil, &cfg)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID: "profile-strict",
		profilePolicy: &access.GroupPolicy{
			DownloadAllowed: false,
			RequestsAllowed: true,
		},
		legacyPolicy: &access.GroupPolicy{
			DownloadAllowed: true,
			RequestsAllowed: true,
		},
	})

	_, err := svc.ResolveDirectFile(ctx, user.ID, "profile-strict", 3, FormatOriginal, catalog.AccessFilter{})
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("ResolveDirectFile() error = %v, want ErrDownloadNotAllowed", err)
	}
}

func TestDownloadServePathsUseProfileGroupDownloadDenial(t *testing.T) {
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		AccountID:      9,
		Legacy:         true,
	})
	user := &models.User{ID: 9}
	cfg := config.DownloadConfig{Enabled: true}
	svc := NewService(nil, nil, nil, nil, nil, nil, fakeUserRepo{user}, nil, nil, &cfg)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID: "profile-strict",
		profilePolicy: &access.GroupPolicy{
			DownloadAllowed: false,
			RequestsAllowed: true,
		},
		legacyPolicy: &access.GroupPolicy{
			DownloadAllowed: true,
			RequestsAllowed: true,
		},
	})

	t.Run("ephemeral", func(t *testing.T) {
		err := svc.ServeFile(ctx, nil, nil, user.ID, "profile-strict", "", "download-1", catalog.AccessFilter{})
		if !errors.Is(err, ErrDownloadNotAllowed) {
			t.Fatalf("ServeFile() error = %v, want ErrDownloadNotAllowed", err)
		}
	})
	t.Run("managed", func(t *testing.T) {
		_, err := svc.ResolveManagedFile(ctx, user.ID, "profile-strict", "device-1", "download-1", catalog.AccessFilter{})
		if !errors.Is(err, ErrDownloadNotAllowed) {
			t.Fatalf("ResolveManagedFile() error = %v, want ErrDownloadNotAllowed", err)
		}
	})
}

type profileDownloadGroupProvider struct {
	profileID     string
	profilePolicy *access.GroupPolicy
	legacyPolicy  *access.GroupPolicy
}

func (p profileDownloadGroupProvider) ResolvePolicy(_ context.Context, subject access.GroupSubject) (*access.GroupPolicy, error) {
	switch subject.ProfileID {
	case p.profileID:
		return p.profilePolicy, nil
	case "":
		return p.legacyPolicy, nil
	default:
		return nil, access.ErrGroupNotFound
	}
}

type allowDownloadItemAccess struct{}

func (allowDownloadItemAccess) EnsureAccessible(context.Context, string, catalog.AccessFilter) error {
	return nil
}

func TestDownloadPolicyRejectsMissingTenantFactsBeforeEvaluation(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &PolicyUser{ID: 9, Policy: access.EffectiveUserPolicy{DownloadAllowed: true}}
	cfg := config.DownloadConfig{Enabled: true}
	file := &models.MediaFile{ID: 3, Resolution: "1080p"}

	_, err := resolver.Resolve(context.Background(), QualityOriginal, user, cfg, file, playback.ClientCapabilities{}, true, "")
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("Resolve() error = %v, want ErrDownloadNotAllowed", err)
	}
	if len(decider.inputs) != 0 {
		t.Fatalf("decider calls = %d, want 0", len(decider.inputs))
	}
}

func TestDownloadPolicyRejectsTenantForDifferentAccount(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &PolicyUser{ID: 9, Policy: access.EffectiveUserPolicy{DownloadAllowed: true}}
	cfg := config.DownloadConfig{Enabled: true}
	file := &models.MediaFile{ID: 3, Resolution: "1080p"}

	_, err := resolver.Resolve(downloadResolvedTenantContextForAccount(8), QualityOriginal, user, cfg, file, playback.ClientCapabilities{}, true, "")
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("Resolve() error = %v, want ErrDownloadNotAllowed", err)
	}
	if len(decider.inputs) != 0 {
		t.Fatalf("decider calls = %d, want 0", len(decider.inputs))
	}
}

func downloadResolvedTenantContext() context.Context {
	return downloadResolvedTenantContextForAccount(9)
}

func downloadResolvedTenantContextForAccount(accountID int) context.Context {
	return tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:        uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:           accountID,
		OrganizationStatus:  tenancy.OrganizationInitializing,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      7,
		SecurityRevision:    11,
		Legacy:              true,
		OrganizationDefault: true,
	})
}
