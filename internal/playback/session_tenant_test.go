package playback_test

// Park tenant organization enforcement at playback admission (bloem-park
// growth G2): the tenant's transcode pool is SHARED across its accounts, a
// frozen tenant plays nothing, and an account with no tenant is untouched.
// These are sold entitlements, so the gate holds on the inline path and the
// policy-decider path alike.

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func tenantLimitProvider(perUser map[int]playback.SessionLimits) playback.SessionLimitProvider {
	return func(_ context.Context, userID int, _ string) (playback.SessionLimits, error) {
		return perUser[userID], nil
	}
}

func TestTenantTranscodePoolIsSharedAcrossUsers(t *testing.T) {
	sm := playback.NewSessionManager(0, 0) // no server-wide caps in the way
	tenant := playback.SessionLimits{TenantID: "7", TenantMaxTranscodes: 2}
	sm.SetLimitProvider(tenantLimitProvider(map[int]playback.SessionLimits{
		1: tenant, 2: tenant, 3: tenant,
		9: {}, // no tenant
	}))

	if _, err := sm.StartSession(1, "p", 100, playback.PlayTranscode, false); err != nil {
		t.Fatalf("first tenant transcode: %v", err)
	}
	if _, err := sm.StartSession(2, "p", 101, playback.PlayTranscode, false); err != nil {
		t.Fatalf("second tenant transcode: %v", err)
	}
	// A THIRD account of the same tenant is refused: the pool is tenant-wide.
	if _, err := sm.StartSession(3, "p", 102, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTenantTranscodesExceeded) {
		t.Fatalf("third tenant transcode = %v, want ErrTenantTranscodesExceeded", err)
	}
	// Direct play is not a transcode and stays admitted.
	if _, err := sm.StartSession(3, "p", 103, playback.PlayDirect, false); err != nil {
		t.Fatalf("tenant direct play: %v", err)
	}
	// An account with no tenant never draws from the pool.
	if _, err := sm.StartSession(9, "p", 104, playback.PlayTranscode, false); err != nil {
		t.Fatalf("no-tenant transcode: %v", err)
	}
}

func TestFrozenTenantPlaysNothing(t *testing.T) {
	sm := playback.NewSessionManager(0, 0)
	sm.SetLimitProvider(tenantLimitProvider(map[int]playback.SessionLimits{
		1: {TenantID: "7", TenantMaxTranscodes: 4, TenantFrozen: true},
	}))
	if _, err := sm.StartSession(1, "p", 100, playback.PlayDirect, false); !errors.Is(err, playback.ErrTenantFrozen) {
		t.Fatalf("frozen direct = %v, want ErrTenantFrozen", err)
	}
	if _, err := sm.StartSession(1, "p", 100, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTenantFrozen) {
		t.Fatalf("frozen transcode = %v, want ErrTenantFrozen", err)
	}
}

// The gate holds even when a policy decider is wired and ALLOWS the session:
// the tenant pool is the operator's sold entitlement, not per-account policy.
func TestTenantGateHoldsOnDeciderPath(t *testing.T) {
	sm := playback.NewSessionManager(0, 0)
	tenant := playback.SessionLimits{TenantID: "7", TenantMaxTranscodes: 1}
	sm.SetLimitProvider(tenantLimitProvider(map[int]playback.SessionLimits{1: tenant, 2: tenant}))
	sm.SetAdmissionDecider(func(context.Context, playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		return playback.AdmissionDecision{Allowed: true}, nil
	})

	if _, err := sm.StartSession(1, "p", 100, playback.PlayTranscode, false); err != nil {
		t.Fatalf("first transcode: %v", err)
	}
	if _, err := sm.StartSession(2, "p", 101, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTenantTranscodesExceeded) {
		t.Fatalf("second transcode = %v, want ErrTenantTranscodesExceeded despite the decider allowing", err)
	}
}
