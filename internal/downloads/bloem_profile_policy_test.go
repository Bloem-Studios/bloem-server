package downloads

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
)

// An ordinary Silo group provider has no tenant/profile contract. Restoring
// the account helper must preserve its group and explicit user overrides.
func TestDownloadAccountPolicyWithoutBloemProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed *bool
		wantErr error
	}{
		{name: "group denies", wantErr: ErrDownloadNotAllowed},
		{name: "account override allows", allowed: ptrBool(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(4)
			user := &models.User{ID: 9, AccessGroupID: &groupID, DownloadAllowed: tc.allowed}
			svc := newPolicyActionTestService(user, config.DownloadConfig{Enabled: true}, false, nil)
			provider := &accountDownloadGroupProvider{policy: &access.GroupPolicy{DownloadAllowed: false}}
			svc.SetGroupPolicyProvider(provider)
			_, _, err := svc.downloadConfigForUser(t.Context(), user.ID, "device-1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("account download gate error = %v, want %v", err, tc.wantErr)
			}
			if provider.calls != 1 {
				t.Fatalf("account group lookups = %d, want 1", provider.calls)
			}
		})
	}
}

func TestDownloadCreatePreservesProfileDownloadDenial(t *testing.T) {
	svc := deniedProfileDownloadService(false)
	_, err := svc.Create(downloadResolvedTenantContext(), 9, CreateRequest{
		ProfileID: "profile-strict", DeviceID: "device-1", FileID: 3, Quality: QualityOriginal,
	}, catalog.AccessFilter{})
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("Create(original) error = %v, want ErrDownloadNotAllowed", err)
	}
}

func TestDownloadProfilePolicyScopeFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing tenant", ctx: context.Background()},
		{name: "different tenant account", ctx: downloadResolvedTenantContextForAccount(8)},
		{name: "different profile account", ctx: bloemWithDownloadProfile(downloadResolvedTenantContext(), 8, "profile-strict")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := deniedProfileDownloadService(true)
			_, _, err := svc.downloadConfigForUser(tc.ctx, 9, "device-1")
			if !errors.Is(err, ErrDownloadNotAllowed) {
				t.Fatalf("download gate error = %v, want ErrDownloadNotAllowed", err)
			}
		})
	}
	// An unknown opted-in profile must not fall back to an allowed account.
	svc := deniedProfileDownloadService(true)
	_, _, err := svc.bloemDownloadConfigForProfile(downloadResolvedTenantContext(), 9, "missing-profile", "device-1")
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("unknown profile error = %v, want ErrDownloadNotAllowed", err)
	}
}

func newProfileRetryFixture(t *testing.T, format string, downloadsAllowed, transcodesAllowed, serverTranscode bool) (managedFixture, *Service, string, *ArtifactRepository, string) {
	t.Helper()
	f := seedManagedFixture(t)
	ctx := downloadResolvedTenantContextForAccount(f.userID)
	id := f.createManagedEntry(t)
	artifacts := NewArtifactRepository(f.pool)
	a := newArtifact(t, f.fileID, fmt.Sprintf("profile-retry-%d", time.Now().UnixNano()))
	a, _, err := artifacts.EnsureQueued(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM downloads WHERE id = $1`, id)
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM download_artifacts WHERE id = $1`, a.ID)
	})
	if _, err := f.pool.Exec(ctx, `UPDATE download_artifacts SET status = 'expired' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	artifactID := a.ID
	if format == FormatOriginal {
		artifactID = ""
	}
	if _, err := f.pool.Exec(ctx, `UPDATE downloads SET status = 'completed', format = $2, artifact_id = NULLIF($3, '') WHERE id = $1`, id, format, artifactID); err != nil {
		t.Fatal(err)
	}
	cfg := config.DownloadConfig{Enabled: true, TranscodeEnabled: serverTranscode}
	svc := NewService(f.repo, nil, NewQuantityLimiter(f.repo, 0, 0, 0), nil, nil, nil,
		fakeUserRepo{&models.User{ID: f.userID}}, allowDownloadItemAccess{}, nil, &cfg)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID:     f.profileA,
		profilePolicy: &access.GroupPolicy{DownloadAllowed: downloadsAllowed, DownloadTranscodeAllowed: transcodesAllowed},
		legacyPolicy:  &access.GroupPolicy{DownloadAllowed: true, DownloadTranscodeAllowed: true},
	})
	svc.SetArtifactManager(NewArtifactManager(artifacts, f.repo, nil, nil, "profile-retry-test", nil, nil))
	return f, svc, id, artifacts, a.ID
}

func TestProfilePrepareAgainPolicyDB(t *testing.T) {
	for _, tc := range []struct {
		name             string
		format           string
		downloadsAllowed bool
		transcodeAllowed bool
		serverTranscode  bool
		wantErr          error
		wantStatus       string
	}{
		{name: "original download denied", format: FormatOriginal, transcodeAllowed: true, serverTranscode: true, wantErr: ErrDownloadNotAllowed, wantStatus: StatusCompleted},
		{name: "transcode download denied", format: FormatTranscode, transcodeAllowed: true, serverTranscode: true, wantErr: ErrDownloadNotAllowed, wantStatus: StatusCompleted},
		{name: "revoked transcode denied", format: FormatTranscode, downloadsAllowed: true, serverTranscode: true, wantErr: ErrDownloadNotAllowed, wantStatus: StatusCompleted},
		{name: "original with transcode denied", format: FormatOriginal, downloadsAllowed: true, serverTranscode: true, wantStatus: StatusCompleted},
		{name: "original with server transcode disabled", format: FormatOriginal, downloadsAllowed: true, wantStatus: StatusCompleted},
		{name: "remux with transcode denied", format: FormatRemux, downloadsAllowed: true, serverTranscode: true, wantStatus: StatusPreparing},
		{name: "server transcode disabled", format: FormatTranscode, downloadsAllowed: true, transcodeAllowed: true, wantErr: ErrFormatUnavailable, wantStatus: StatusCompleted},
		{name: "authorized transcode", format: FormatTranscode, downloadsAllowed: true, transcodeAllowed: true, serverTranscode: true, wantStatus: StatusPreparing},
	} {
		for _, withPolicy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/policy=%t", tc.name, withPolicy), func(t *testing.T) {
				f, svc, id, artifacts, artifactID := newProfileRetryFixture(t, tc.format, tc.downloadsAllowed, tc.transcodeAllowed, tc.serverTranscode)
				if withPolicy {
					svc.SetActionDecider(newDownloadPolicyPDP(t))
				}
				ctx := downloadResolvedTenantContextForAccount(f.userID)
				got, err := NewProfilePrepareAgainService(svc).PrepareAgain(ctx, f.userID, f.profileA, f.deviceA, id, catalog.AccessFilter{})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PrepareAgain() error = %v, want %v", err, tc.wantErr)
				}
				stored, err := f.repo.GetManagedByID(ctx, id, f.userID, f.profileA, f.deviceA)
				if err != nil || stored.Status != tc.wantStatus {
					t.Fatalf("stored download = %+v, error = %v; want status %s", stored, err, tc.wantStatus)
				}
				if tc.wantErr == nil && (got == nil || got.Status != tc.wantStatus || got.Revision != stored.Revision) {
					t.Fatalf("PrepareAgain() = %+v, want status %s and unchanged revision", got, tc.wantStatus)
				}
				wantArtifact := ArtifactExpired
				if tc.wantStatus == StatusPreparing {
					wantArtifact = ArtifactQueued
				}
				if got := artifactStatus(t, artifacts, artifactID); got != wantArtifact {
					t.Fatalf("artifact status = %s, want %s", got, wantArtifact)
				}
				if got := svc.Config(ctx).TranscodeEnabled; got != tc.serverTranscode {
					t.Fatalf("cached server TranscodeEnabled = %t, want %t", got, tc.serverTranscode)
				}
			})
		}
	}
}

func TestPrepareAgainRetainsSiloAccountPolicyDB(t *testing.T) {
	f, svc, id, _, _ := newProfileRetryFixture(t, FormatOriginal, false, false, true)
	ctx := downloadResolvedTenantContextForAccount(f.userID)
	got, err := svc.PrepareAgain(ctx, f.userID, f.profileA, f.deviceA, id, catalog.AccessFilter{})
	if err != nil || got.Status != StatusCompleted {
		t.Fatalf("account PrepareAgain = (%+v, %v), want completed original", got, err)
	}
}

func TestProfilePrepareAgainRejectsInvalidScopeDB(t *testing.T) {
	f, svc, id, _, _ := newProfileRetryFixture(t, FormatOriginal, true, false, true)
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		device string
		want   error
	}{
		{name: "missing tenant", ctx: t.Context(), device: f.deviceA, want: ErrDownloadNotAllowed},
		{name: "different tenant account", ctx: downloadResolvedTenantContextForAccount(f.userID + 1), device: f.deviceA, want: ErrDownloadNotAllowed},
		{name: "different device", ctx: downloadResolvedTenantContextForAccount(f.userID), device: f.deviceB, want: ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProfilePrepareAgainService(svc).PrepareAgain(tc.ctx, f.userID, f.profileA, tc.device, id, catalog.AccessFilter{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("PrepareAgain() error = %v, want %v", err, tc.want)
			}
		})
	}
}

// Replace the original after the adapter's row check, before Silo reloads it.
// The retry-bound configuration must still forbid a transcode for this profile.
func TestProfilePrepareAgainReplacementCannotBypassTranscodeDenialDB(t *testing.T) {
	f, svc, id, artifacts, artifactID := newProfileRetryFixture(t, FormatOriginal, true, false, true)
	svc.userRepo = &reloadDownloadUserResolver{
		user: &models.User{ID: f.userID},
		onReload: func(ctx context.Context) error {
			_, err := f.pool.Exec(ctx, `UPDATE downloads SET format = 'transcode', artifact_id = $2 WHERE id = $1`, id, artifactID)
			return err
		},
	}
	ctx := downloadResolvedTenantContextForAccount(f.userID)
	_, err := NewProfilePrepareAgainService(svc).PrepareAgain(ctx, f.userID, f.profileA, f.deviceA, id, catalog.AccessFilter{})
	if !errors.Is(err, ErrFormatUnavailable) {
		t.Fatalf("PrepareAgain() error = %v, want ErrFormatUnavailable", err)
	}
	if got := artifactStatus(t, artifacts, artifactID); got != ArtifactExpired {
		t.Fatalf("artifact status = %s, want expired", got)
	}
}

func TestProfilePrepareAgainRechecksTranscodePolicyDB(t *testing.T) {
	f, svc, id, artifacts, artifactID := newProfileRetryFixture(t, FormatTranscode, true, true, true)
	svc.userRepo = &reloadDownloadUserResolver{
		user: &models.User{ID: f.userID},
		onReload: func(context.Context) error {
			svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
				profileID:     f.profileA,
				profilePolicy: &access.GroupPolicy{DownloadAllowed: true, DownloadTranscodeAllowed: false},
				legacyPolicy:  &access.GroupPolicy{DownloadAllowed: true, DownloadTranscodeAllowed: true},
			})
			return nil
		},
	}
	ctx := downloadResolvedTenantContextForAccount(f.userID)
	_, err := NewProfilePrepareAgainService(svc).PrepareAgain(ctx, f.userID, f.profileA, f.deviceA, id, catalog.AccessFilter{})
	if !errors.Is(err, ErrFormatUnavailable) {
		t.Fatalf("PrepareAgain() error = %v, want ErrFormatUnavailable", err)
	}
	if got := artifactStatus(t, artifacts, artifactID); got != ArtifactExpired {
		t.Fatalf("artifact status = %s, want expired", got)
	}
}

type reloadDownloadUserResolver struct {
	user     *models.User
	calls    int
	onReload func(context.Context) error
}

func (r *reloadDownloadUserResolver) GetByID(ctx context.Context, _ int) (*models.User, error) {
	r.calls++
	if r.calls == 2 {
		if err := r.onReload(ctx); err != nil {
			return nil, err
		}
	}
	return r.user, nil
}

func TestProfilePrepareAgainRequiresIdentity(t *testing.T) {
	for _, tc := range []struct{ profile, device string }{{"", "device-1"}, {"profile-strict", ""}} {
		_, err := NewProfilePrepareAgainService(nil).PrepareAgain(t.Context(), 9, tc.profile, tc.device, "download-1", catalog.AccessFilter{})
		if !errors.Is(err, ErrProfileRequired) {
			t.Fatalf("PrepareAgain(%q, %q) error = %v, want ErrProfileRequired", tc.profile, tc.device, err)
		}
	}
	_, err := NewProfilePrepareAgainService(deniedProfileDownloadService(false)).PrepareAgain(
		downloadResolvedTenantContext(), 9, "profile-strict", "device-1", "download-1", catalog.AccessFilter{})
	if !errors.Is(err, ErrDownloadNotAllowed) {
		t.Fatalf("profile denied before storage error = %v, want ErrDownloadNotAllowed", err)
	}
}

type accountDownloadGroupProvider struct {
	policy *access.GroupPolicy
	calls  int
}

func (p *accountDownloadGroupProvider) GetPolicyForUser(context.Context, int) (*access.GroupPolicy, error) {
	p.calls++
	return p.policy, nil
}

func deniedProfileDownloadService(downloadAllowed bool) *Service {
	svc := newPolicyActionTestService(&models.User{ID: 9}, config.DownloadConfig{Enabled: true, TranscodeEnabled: true}, true, nil)
	svc.SetGroupPolicyProvider(profileDownloadGroupProvider{
		profileID: "profile-strict",
		profilePolicy: &access.GroupPolicy{
			DownloadAllowed:          downloadAllowed,
			DownloadTranscodeAllowed: false,
		},
		legacyPolicy: &access.GroupPolicy{DownloadAllowed: true, DownloadTranscodeAllowed: true},
	})
	return svc
}
