package playback_test

// Bloem tenant/profile-aware session admission coverage moved out of Silo's
// session_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestSessionManagerContextProviderFeedsLimitsAndAdmission(t *testing.T) {
	type tenantKey struct{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetContextProvider(func(ctx context.Context, userID int, profileID string) (context.Context, error) {
		if userID != 7 || profileID != "profile-v2" {
			t.Fatalf("context subject = (%d, %q)", userID, profileID)
		}
		return context.WithValue(ctx, tenantKey{}, "validated"), nil
	})
	mgr.SetLimitProvider(func(ctx context.Context, _ int, _ string) (playback.SessionLimits, error) {
		if got := ctx.Value(tenantKey{}); got != "validated" {
			t.Fatalf("limit context tenant = %v", got)
		}
		return playback.SessionLimits{}, nil
	})
	mgr.SetAdmissionDecider(func(ctx context.Context, _ playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		if got := ctx.Value(tenantKey{}); got != "validated" {
			t.Fatalf("admission context tenant = %v", got)
		}
		return playback.AdmissionDecision{Allowed: true}, nil
	})

	if _, err := mgr.StartSessionWithFilesContext(context.Background(), 7, "profile-v2", 1, 1, playback.PlayDirect, false); err != nil {
		t.Fatalf("start session: %v", err)
	}
}

func TestSessionManagerLimitProviderReceivesPlaybackProfile(t *testing.T) {
	profileLimits := func(limits playback.SessionLimits) playback.SessionLimitProvider {
		return func(_ context.Context, userID int, profileID string) (playback.SessionLimits, error) {
			if userID != 1 || profileID != "profile-strict" {
				return playback.SessionLimits{}, nil
			}
			return limits, nil
		}
	}
	limitsForStreams := profileLimits(playback.SessionLimits{MaxStreams: 1})
	limitsForTranscodes := profileLimits(playback.SessionLimits{MaxTranscodes: 1})

	streams := playback.NewSessionManager(0, 0)
	streams.SetLimitProvider(limitsForStreams)
	if _, err := streams.StartSession(1, "profile-strict", 100, playback.PlayDirect, false); err != nil {
		t.Fatalf("StartSession(first stream) error: %v", err)
	}
	if _, err := streams.StartSession(1, "profile-strict", 101, playback.PlayDirect, false); !errors.Is(err, playback.ErrTooManyStreams) {
		t.Fatalf("StartSession(second stream) error = %v, want ErrTooManyStreams", err)
	}

	transcodes := playback.NewSessionManager(0, 0)
	transcodes.SetLimitProvider(limitsForTranscodes)
	if _, err := transcodes.StartSession(1, "profile-strict", 200, playback.PlayTranscode, false); err != nil {
		t.Fatalf("StartSession(first transcode) error: %v", err)
	}
	if _, err := transcodes.StartSession(1, "profile-strict", 201, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("StartSession(second transcode) error = %v, want ErrTooManyTranscodes", err)
	}
}

func TestSessionManager_PlaybackDisabledRejectsEveryPlayMethodBeforeDecider(t *testing.T) {
	for _, method := range []playback.PlayMethod{playback.PlayDirect, playback.PlayRemux, playback.PlayTranscode} {
		t.Run(string(method), func(t *testing.T) {
			sm := playback.NewSessionManager(0, 0)
			sm.SetLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
				return playback.SessionLimits{PlaybackDisabled: true}, nil
			})
			deciderCalled := false
			sm.SetAdmissionDecider(func(context.Context, playback.AdmissionRequest) (playback.AdmissionDecision, error) {
				deciderCalled = true
				return playback.AdmissionDecision{Allowed: true}, nil
			})

			_, err := sm.StartSession(1, "browse-only", 100, method, false)
			if !errors.Is(err, playback.ErrPlaybackNotAllowed) {
				t.Fatalf("StartSession() error = %v, want ErrPlaybackNotAllowed", err)
			}
			if deciderCalled {
				t.Fatal("admission decider called for an entitlement-level playback denial")
			}
		})
	}
}

func TestSessionManager_PlaybackDisabledRejectsRecipeReconstruction(t *testing.T) {
	sm := playback.NewSessionManager(0, 0)
	sm.SetLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{PlaybackDisabled: true}, nil
	})
	_, err := sm.RegisterReconstructedWithLimits(context.Background(), &playback.Session{
		ID: "old-recipe", UserID: 1, ProfileID: "browse-only", PlayMethod: playback.PlayDirect,
	})
	if !errors.Is(err, playback.ErrPlaybackNotAllowed) {
		t.Fatalf("RegisterReconstructedWithLimits() error = %v, want ErrPlaybackNotAllowed", err)
	}
	if _, err := sm.GetSession("old-recipe"); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("reconstructed denied session lookup error = %v, want ErrSessionNotFound", err)
	}
}

func playbackResolvedTenantContext() context.Context {
	return tenancy.WithContext(context.Background(), tenancy.Context{
		OrganizationID:      uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:        uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:           1,
		OrganizationStatus:  tenancy.OrganizationInitializing,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      7,
		SecurityRevision:    11,
		Legacy:              true,
		OrganizationDefault: true,
	})
}
