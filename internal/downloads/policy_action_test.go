package downloads

import (
	"context"
	"errors"
	"reflect"
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

func TestPolicyActionDeciderMatchesLegacyCapability(t *testing.T) {
	ctx := downloadResolvedTenantContext()
	pdp := newDownloadPolicyPDP(t)

	for _, downloadsEnabled := range []bool{false, true} {
		for _, transcodeEnabled := range []bool{false, true} {
			for _, downloadAllowed := range []bool{false, true} {
				for _, downloadTranscodeAllowed := range []bool{false, true} {
					for _, artifactsAvailable := range []bool{false, true} {
						cfg := config.DownloadConfig{Enabled: downloadsEnabled, TranscodeEnabled: transcodeEnabled}
						user := &models.User{ID: 9, DownloadAllowed: downloadAllowed, DownloadTranscodeAllowed: downloadTranscodeAllowed}
						legacy := newPolicyActionTestService(user, cfg, artifactsAvailable, nil)
						withPolicy := newPolicyActionTestService(user, cfg, artifactsAvailable, pdp)

						legacyCap, err := legacy.Capability(ctx, user.ID, "")
						if err != nil {
							t.Fatalf("legacy Capability error: %v", err)
						}
						policyCap, err := withPolicy.Capability(ctx, user.ID, "")
						if err != nil {
							t.Fatalf("policy Capability error: %v", err)
						}
						if !reflect.DeepEqual(policyCap, legacyCap) {
							t.Fatalf("policy capability = %+v, want legacy %+v for cfg=%+v user=%+v artifacts=%v",
								policyCap, legacyCap, cfg, user, artifactsAvailable)
						}
					}
				}
			}
		}
	}
}

func TestPolicyActionDeciderMatchesLegacyCreateGate(t *testing.T) {
	ctx := downloadResolvedTenantContext()
	pdp := newDownloadPolicyPDP(t)

	for _, downloadsEnabled := range []bool{false, true} {
		for _, transcodeEnabled := range []bool{false, true} {
			for _, downloadAllowed := range []bool{false, true} {
				for _, downloadTranscodeAllowed := range []bool{false, true} {
					for _, artifactsAvailable := range []bool{false, true} {
						cfg := config.DownloadConfig{Enabled: downloadsEnabled, TranscodeEnabled: transcodeEnabled}
						user := &models.User{ID: 9, DownloadAllowed: downloadAllowed, DownloadTranscodeAllowed: downloadTranscodeAllowed}
						legacy := newPolicyActionTestService(user, cfg, artifactsAvailable, nil)
						withPolicy := newPolicyActionTestService(user, cfg, artifactsAvailable, pdp)

						_, _, legacyErr := legacy.downloadConfigForUser(ctx, user.ID, "", "device-1")
						_, _, policyErr := withPolicy.downloadConfigForUser(ctx, user.ID, "", "device-1")
						if !sameDownloadGateError(policyErr, legacyErr) {
							t.Fatalf("policy create gate error = %v, want legacy %v for cfg=%+v user=%+v artifacts=%v",
								policyErr, legacyErr, cfg, user, artifactsAvailable)
						}
					}
				}
			}
		}
	}
}

func TestPolicyActionDeciderUsesGroupDownloadFlags(t *testing.T) {
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		AccountID:      9,
		Legacy:         true,
	})
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
	svc := newPolicyActionTestService(
		user,
		config.DownloadConfig{Enabled: true, TranscodeEnabled: true},
		true,
		newDownloadPolicyPDP(t),
	)
	svc.SetGroupPolicyProvider(downloadGroupProvider{group: &access.GroupPolicy{
		DownloadAllowed:          false,
		DownloadTranscodeAllowed: true,
		RequestsAllowed:          true,
	}})

	_, _, err := svc.downloadConfigForUser(ctx, user.ID, "", "device-1")
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("downloadConfigForUser() error = %v, want ErrDownloadNotAllowed", err)
	}
}

func TestDownloadCapabilityResolvesV2ProfileGroup(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	ctx := tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      9,
	})
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
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
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
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
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
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
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
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

func TestResolveTranscodePassesDeviceQualityFactsAndAppliesCeiling(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true, QualityCeiling: "1080p"}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: true}
	file := &models.MediaFile{ID: 3, Resolution: "2160p"}

	got, err := resolver.Resolve(
		downloadResolvedTenantContext(), Quality10Mbps, user, cfg, file,
		playback.ClientCapabilities{}, true, "device-9",
	)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if len(decider.inputs) != 1 {
		t.Fatalf("decider calls = %d, want 1", len(decider.inputs))
	}
	in := decider.inputs[0]
	if in.Action != policyengine.ActionDownloadTranscode ||
		in.DeviceID != "device-9" ||
		in.RequestedQuality != Quality10Mbps {
		t.Fatalf("action input = %+v, want download_transcode facts with device and requested quality", in)
	}
	if in.Tenant.OrganizationID != "10000000-0000-0000-0000-000000000001" ||
		in.Tenant.MembershipSecurityRevision != 11 {
		t.Fatalf("tenant facts = %+v, want resolved request tenant", in.Tenant)
	}
	if got.PrepareTarget.Resolution != "1080p" {
		t.Fatalf("PrepareTarget.Resolution = %q, want %q (policy ceiling applied)",
			got.PrepareTarget.Resolution, "1080p")
	}
}

func TestResolveTranscodeCeilingKeepsCompliantTarget(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true, QualityCeiling: "2160p"}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: true}
	file := &models.MediaFile{ID: 3, Resolution: "1080p"}

	got, err := resolver.Resolve(
		downloadResolvedTenantContext(), Quality10Mbps, user, cfg, file,
		playback.ClientCapabilities{}, true, "",
	)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if got.PrepareTarget.Resolution != "" {
		t.Fatalf("PrepareTarget.Resolution = %q, want no downscale for a compliant source",
			got.PrepareTarget.Resolution)
	}
}

type capturingActionDecider struct {
	inputs   []policyengine.ActionInput
	decision policyengine.ActionDecision
}

func (d *capturingActionDecider) CheckAction(_ context.Context, in policyengine.ActionInput) (policyengine.ActionDecision, policyengine.Meta, error) {
	d.inputs = append(d.inputs, in)
	return d.decision, policyengine.Meta{}, nil
}

func newPolicyActionTestService(
	user *models.User,
	cfg config.DownloadConfig,
	artifactsAvailable bool,
	decider ActionDecider,
) *Service {
	svc := NewService(nil, nil, nil, nil, nil, nil, fakeUserRepo{user}, nil, nil, &cfg)
	if artifactsAvailable {
		svc.SetArtifactManager(&ArtifactManager{})
	}
	if decider != nil {
		svc.SetActionDecider(decider)
	}
	return svc
}

func sameDownloadGateError(got, want error) bool {
	switch {
	case want == nil:
		return got == nil
	case errors.Is(want, ErrFeatureDisabled):
		return errors.Is(got, ErrFeatureDisabled)
	case errors.Is(want, ErrDownloadNotAllowed):
		return errors.Is(got, ErrDownloadNotAllowed)
	default:
		return errors.Is(got, want)
	}
}

func newDownloadPolicyPDP(t *testing.T) *policyengine.PDP {
	t.Helper()
	engine, err := policyengine.NewEngine(context.Background())
	if err != nil {
		t.Fatalf("NewEngine() error: %v", err)
	}
	return policyengine.NewPDP(engine)
}

type downloadGroupProvider struct {
	group *access.GroupPolicy
	err   error
}

func (p downloadGroupProvider) ResolvePolicy(context.Context, access.GroupSubject) (*access.GroupPolicy, error) {
	return p.group, p.err
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

// TestResolveOriginalAssertsServedQuality is the C6 regression: a direct
// original download of an over-ceiling source must be denied at create time —
// serve-time authorization (serveDownloadBytes) could never satisfy the row —
// while a capped transcode of the same source stays allowed, and a compliant
// source passes with the served quality asserted to the policy.
func TestResolveOriginalAssertsServedQuality(t *testing.T) {
	ctx := downloadResolvedTenantContext()
	pdp := newDownloadPolicyPDP(t)
	resolver := DownloadQualityResolver{actionDecider: pdp}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: true}
	user := &models.User{ID: 9, DownloadAllowed: true, DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p"}
	overCeiling := &models.MediaFile{ID: 3, Resolution: "2160p"}

	_, err := resolver.Resolve(ctx, QualityOriginal, user, cfg, overCeiling, playback.ClientCapabilities{}, true, "")
	if !errors.Is(err, ErrQualityUnavailable) {
		t.Fatalf("Resolve(over-ceiling original) error = %v, want ErrQualityUnavailable", err)
	}

	// The same over-ceiling source stays downloadable as a capped transcode:
	// the ceiling applies to the prepared artifact, not the source.
	transcode, err := resolver.Resolve(ctx, Quality5Mbps, user, cfg, overCeiling, playback.ClientCapabilities{}, true, "")
	if err != nil {
		t.Fatalf("Resolve(capped transcode of over-ceiling source) error: %v", err)
	}
	if !transcode.RequiresArtifact || transcode.DeliveryFormat != FormatTranscode {
		t.Fatalf("transcode decision = %+v, want artifact-backed transcode", transcode)
	}

	compliant := &models.MediaFile{ID: 4, Resolution: "1080p"}
	if _, err := resolver.Resolve(ctx, QualityOriginal, user, cfg, compliant, playback.ClientCapabilities{}, true, ""); err != nil {
		t.Fatalf("Resolve(compliant original) error: %v", err)
	}
}

// TestResolveOriginalPopulatesFileQualityFact pins the input contract: the
// final download check on original-resolution paths carries file_quality.
func TestResolveOriginalPopulatesFileQualityFact(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &models.User{ID: 9, DownloadAllowed: true, MaxPlaybackQuality: "2160p"}
	cfg := config.DownloadConfig{Enabled: true}
	file := &models.MediaFile{ID: 3, Resolution: "1080p"}

	if _, err := resolver.Resolve(
		downloadResolvedTenantContext(), QualityOriginal, user, cfg, file,
		playback.ClientCapabilities{}, true, "device-9",
	); err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if len(decider.inputs) != 1 {
		t.Fatalf("decider calls = %d, want 1", len(decider.inputs))
	}
	in := decider.inputs[0]
	if in.Action != policyengine.ActionDownload ||
		in.FileQuality != "1080p" ||
		in.RequestedQuality != QualityOriginal ||
		in.DeviceID != "device-9" {
		t.Fatalf("action input = %+v, want download action with file_quality asserted", in)
	}
}

func TestDownloadPolicyRejectsMissingTenantFactsBeforeEvaluation(t *testing.T) {
	decider := &capturingActionDecider{decision: policyengine.ActionDecision{Allowed: true}}
	resolver := DownloadQualityResolver{actionDecider: decider}
	user := &models.User{ID: 9, DownloadAllowed: true}
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

func downloadResolvedTenantContext() context.Context {
	return tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID:     uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:       uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:          9,
		OrganizationStatus: tenancy.OrganizationInitializing,
		MembershipStatus:   tenancy.MembershipActive,
		PolicyRevision:     7,
		SecurityRevision:   11,
		Legacy:             true,
	})
}
