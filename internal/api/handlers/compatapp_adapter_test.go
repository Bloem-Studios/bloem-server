package handlers

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/compatapp"
)

// The adapter is the only place the lifecycle service's vocabulary meets this
// package's. Every mapping is pinned here: a sentinel that stops being
// translated would otherwise reach the handler's default branch and turn a
// precise 404/409/422 into an opaque 503, which is exactly the kind of
// silently broken seam an untested adapter has hidden here before.

func TestCompatApplicationErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		assert func(t *testing.T, mapped error)
	}{
		{
			name:   "no error",
			err:    nil,
			assert: func(t *testing.T, mapped error) { requireNilError(t, mapped) },
		},
		{
			name: "unknown application",
			err:  compatapp.ErrApplicationNotFound,
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityApplicationNotFound)
			},
		},
		{
			name: "wrapped unknown application",
			err:  fmt.Errorf("lock application: %w", compatapp.ErrApplicationNotFound),
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityApplicationNotFound)
			},
		},
		{
			name: "unknown kind",
			err:  compatapp.ErrUnknownKind,
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityKindUnknown)
			},
		},
		{
			name: "unknown capability",
			err:  compatapp.ErrUnknownCapability,
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityCapabilityUnknown)
			},
		},
		{
			name: "empty capability grant",
			err:  compatapp.ErrNoCapabilities,
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityCapabilityUnknown)
			},
		},
		{
			name: "capability outside the grant",
			err:  compatapp.ErrCapabilityNotGranted,
			assert: func(t *testing.T, mapped error) {
				requireSentinel(t, mapped, ErrCompatibilityCapabilityUnknown)
			},
		},
		{
			name: "stale revision",
			err:  &compatapp.RevisionMismatchError{InstanceID: "inst-1", Expected: 3, Current: 9},
			assert: func(t *testing.T, mapped error) {
				requireCurrentRevision(t, mapped, 9)
			},
		},
		{
			name: "wrapped stale revision",
			err:  fmt.Errorf("disable: %w", &compatapp.RevisionMismatchError{InstanceID: "inst-1", Expected: 3, Current: 9}),
			assert: func(t *testing.T, mapped error) {
				requireCurrentRevision(t, mapped, 9)
			},
		},
		{
			// Revocation is terminal state the caller has not seen. The admin
			// surface's only conflict channel is the revision conflict, and
			// the instruction it carries — reload and retry — is the correct
			// one: the reloaded row shows the application revoked.
			name: "revoked application",
			err:  &compatapp.ApplicationRevokedError{InstanceID: "inst-1", Current: 4},
			assert: func(t *testing.T, mapped error) {
				requireCurrentRevision(t, mapped, 4)
			},
		},
		{
			// A bare revoked sentinel carries no revision, so it must not
			// masquerade as a conflict with a made-up one.
			name: "revoked without a revision",
			err:  compatapp.ErrApplicationRevoked,
			assert: func(t *testing.T, mapped error) {
				var revision *CompatibilityRevisionError
				if errors.As(mapped, &revision) {
					t.Fatalf("mapped = %#v, want no fabricated revision conflict", revision)
				}
			},
		},
		{
			name: "unrecognized failure travels unchanged",
			err:  errors.New("connection refused"),
			assert: func(t *testing.T, mapped error) {
				if mapped == nil || mapped.Error() != "connection refused" {
					t.Fatalf("mapped = %v, want the original error", mapped)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.assert(t, mapCompatApplicationError(testCase.err))
		})
	}
}

func requireNilError(t *testing.T, mapped error) {
	t.Helper()
	if mapped != nil {
		t.Fatalf("mapped = %v, want nil", mapped)
	}
}

func requireSentinel(t *testing.T, mapped, want error) {
	t.Helper()
	if !errors.Is(mapped, want) {
		t.Fatalf("mapped = %v, want it to match %v", mapped, want)
	}
}

func requireCurrentRevision(t *testing.T, mapped error, want int64) {
	t.Helper()
	var revision *CompatibilityRevisionError
	if !errors.As(mapped, &revision) {
		t.Fatalf("mapped = %v, want a *CompatibilityRevisionError", mapped)
	}
	if revision.CurrentRevision != want {
		t.Fatalf("current revision = %d, want %d", revision.CurrentRevision, want)
	}
}

func TestCompatApplicationViewCarriesLifecycleStateOnly(t *testing.T) {
	contact := time.Date(2026, 8, 14, 9, 30, 0, 0, time.UTC)
	revoked := time.Date(2026, 8, 14, 9, 45, 0, 0, time.UTC)

	cases := []struct {
		name        string
		application compatapp.Application
		wantState   string
		wantRevoked bool
		wantHealthy bool
	}{
		{
			name: "enabled and healthy",
			application: compatapp.Application{
				InstanceID: "inst-1", Kind: compatapp.KindJellyfin, Enabled: true,
				Health: compatapp.HealthHealthy, LastContactAt: &contact,
			},
			wantState: "enabled", wantHealthy: true,
		},
		{
			name: "enabled but degraded",
			application: compatapp.Application{
				InstanceID: "inst-1", Kind: compatapp.KindJellyfin, Enabled: true,
				Health: compatapp.HealthDegraded,
			},
			wantState: "enabled",
		},
		{
			name: "disabled",
			application: compatapp.Application{
				InstanceID: "inst-1", Kind: compatapp.KindJellyfin,
				Health: compatapp.HealthHealthy,
			},
			wantState: "disabled", wantHealthy: true,
		},
		{
			// Revocation outranks the reversible off switch: a revoked
			// application that is still flagged enabled must never read as
			// enabled.
			name: "revoked while still flagged enabled",
			application: compatapp.Application{
				InstanceID: "inst-1", Kind: compatapp.KindJellyfin, Enabled: true,
				Health: compatapp.HealthHealthy, RevokedAt: &revoked,
			},
			wantState: "revoked", wantRevoked: true, wantHealthy: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			view := adaptCompatibilityApplication(testCase.application)
			if view.State != testCase.wantState {
				t.Fatalf("state = %q, want %q", view.State, testCase.wantState)
			}
			if view.Revoked != testCase.wantRevoked || view.Healthy != testCase.wantHealthy {
				t.Fatalf("view = %#v, want revoked %v healthy %v", view, testCase.wantRevoked, testCase.wantHealthy)
			}
		})
	}

	full := adaptCompatibilityApplication(compatapp.Application{
		ID:             "b0a1f0de-0000-0000-0000-000000000001",
		Kind:           compatapp.KindAudiobookshelf,
		InstanceID:     "inst-abs",
		Version:        "2.1.0",
		ImageDigest:    "sha256:beef",
		APIRangeMin:    1,
		APIRangeMax:    3,
		Capabilities:   []compatapp.Capability{compatapp.CapabilityCatalog, compatapp.CapabilityState},
		TLSFingerprint: "aa11",
		Enabled:        true,
		Health:         compatapp.HealthHealthy,
		LastContactAt:  &contact,
		Revision:       12,
	})
	switch {
	case full.InstanceID != "inst-abs" || full.Kind != "audiobookshelf":
		t.Fatalf("view = %#v, want the enrolled identity", full)
	case full.Version != "2.1.0" || full.ImageDigest != "sha256:beef":
		t.Fatalf("view = %#v, want the reported build", full)
	case full.APIRange.Min != "1" || full.APIRange.Max != "3":
		t.Fatalf("api range = %#v, want 1..3", full.APIRange)
	case len(full.Capabilities) != 2 || full.Capabilities[0] != "catalog":
		t.Fatalf("capabilities = %v, want the granted set", full.Capabilities)
	case full.Revision != 12:
		t.Fatalf("revision = %d, want 12", full.Revision)
	case full.LastContactAt == nil || !full.LastContactAt.Equal(contact):
		t.Fatalf("last contact = %v, want %v", full.LastContactAt, contact)
	}

	// An application with no granted capabilities must serialize as an empty
	// list, not null: the admin UI iterates it.
	empty := adaptCompatibilityApplication(compatapp.Application{InstanceID: "inst-2", Kind: compatapp.KindJellyfin})
	if empty.Capabilities == nil {
		t.Fatal("capabilities = nil, want an empty list")
	}
}

func TestCompatApplicationServiceIsNilWithoutALifecycleService(t *testing.T) {
	// A typed nil behind the interface would look wired to the router and
	// mount an admin surface that panics on first use.
	if adapter := NewCompatApplicationService(nil); adapter != nil {
		t.Fatalf("adapter = %#v, want an untyped nil so the surface stays unmounted", adapter)
	}
}
