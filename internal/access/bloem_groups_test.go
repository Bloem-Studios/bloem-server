package access

// Bloem subject-scoped group policy coverage moved out of Silo's groups_test.go.

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestEffectivePolicyForSubjectResolvesExactSubject(t *testing.T) {
	organizationID := uuid.New()
	subject := GroupSubject{
		OrganizationID: organizationID,
		AccountID:      7,
		ProfileID:      "profile-7",
	}
	provider := &recordingGroupPolicyProvider{policy: &GroupPolicy{
		MaxPlaybackQuality:       PlaybackQualityStandard,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: true,
		RequestsAllowed:          true,
	}}
	user := &models.User{ID: 7}

	effective, err := EffectivePolicyForSubject(context.Background(), user, subject, provider)
	if err != nil {
		t.Fatalf("EffectivePolicyForSubject() error: %v", err)
	}
	if provider.subject != subject {
		t.Fatalf("ResolvePolicy subject = %#v, want %#v", provider.subject, subject)
	}
	if effective.MaxPlaybackQuality != PlaybackQualityStandard {
		t.Fatalf("MaxPlaybackQuality = %q, want %q", effective.MaxPlaybackQuality, PlaybackQualityStandard)
	}
}

func TestEffectivePolicyForSubjectAdminBypassesGroupsWithPlaybackAllowed(t *testing.T) {
	provider := &recordingGroupPolicyProvider{policy: &GroupPolicy{
		PlaybackAllowed: false,
	}}
	user := &models.User{ID: 7, Role: models.RoleAdmin}

	effective, err := EffectivePolicyForSubject(context.Background(), user, GroupSubject{AccountID: user.ID}, provider)
	if err != nil {
		t.Fatalf("EffectivePolicyForSubject() error: %v", err)
	}
	if !effective.PlaybackAllowed {
		t.Fatal("PlaybackAllowed = false, want ungrouped admin playback allowed")
	}
	if provider.subject != (GroupSubject{}) {
		t.Fatalf("ResolvePolicy called for admin subject %#v", provider.subject)
	}
}

func TestApplyGroupPolicyRestrictsEntitlementPlaybackTranscodeAndProfiles(t *testing.T) {
	user := &models.User{
		MaxProfiles: 8,
	}

	effective := ApplyGroupPolicy(user, &GroupPolicy{
		PlaybackAllowed:  false,
		TranscodeAllowed: false,
		MaxProfiles:      3,
		RequestsAllowed:  true,
	})

	if effective.PlaybackAllowed {
		t.Fatal("PlaybackAllowed = true, want browse-only group to deny playback")
	}
	if effective.TranscodeAllowed || effective.AudioTranscodeAllowed {
		t.Fatalf("transcode flags = (%t, %t), want group restriction on video and audio", effective.TranscodeAllowed, effective.AudioTranscodeAllowed)
	}
	if effective.MaxProfiles != 3 {
		t.Fatalf("MaxProfiles = %d, want strictest positive limit 3", effective.MaxProfiles)
	}
}

func TestApplyGroupPolicyMaxProfilesUsesStrictestPositiveLayer(t *testing.T) {
	tests := []struct {
		name       string
		accountCap int
		groupCap   int
		want       int
	}{
		{name: "account is stricter", accountCap: 2, groupCap: 5, want: 2},
		{name: "group is stricter", accountCap: 5, groupCap: 2, want: 2},
		{name: "unlimited group inherits account", accountCap: 5, groupCap: 0, want: 5},
		{name: "unlimited account inherits group", accountCap: 0, groupCap: 4, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effective := ApplyGroupPolicy(&models.User{MaxProfiles: tt.accountCap}, &GroupPolicy{
				PlaybackAllowed:  true,
				TranscodeAllowed: true,
				MaxProfiles:      tt.groupCap,
				RequestsAllowed:  true,
			})
			if effective.MaxProfiles != tt.want {
				t.Fatalf("MaxProfiles = %d, want %d", effective.MaxProfiles, tt.want)
			}
		})
	}
}

type recordingGroupPolicyProvider struct {
	subject GroupSubject
	policy  *GroupPolicy
	err     error
}

func (p *recordingGroupPolicyProvider) ResolvePolicy(_ context.Context, subject GroupSubject) (*GroupPolicy, error) {
	p.subject = subject
	return p.policy, p.err
}

// groupedTenancyContext returns a context carrying the tenancy info
// EffectivePolicyForUser needs to resolve a GroupSubject for accountID, so
// tests exercise the admin short-circuit itself rather than the separate
// "no tenancy context" fallback.
func groupedTenancyContext(accountID int) context.Context {
	return tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID: uuid.New(),
		AccountID:      accountID,
		Legacy:         true,
	})
}
