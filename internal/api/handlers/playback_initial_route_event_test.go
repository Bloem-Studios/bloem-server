package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

// TestInitialPlaybackRouteEventDedup proves a v2 route event is attributed to
// the owning profile, sanitized, rate limited, and recorded once per event id
// however many times the report is retried.
func TestInitialPlaybackRouteEventDedup(t *testing.T) {
	f := newInitialHTTPFixture(t)
	ctx := initialContextV3(t, f)
	status, data := f.call(t, http.MethodPost, "/start", f.request)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %s", status, data)
	}
	var started playback.DecisionResponseV3
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatal(err)
	}
	eventID := uuid.NewString()
	event := playback.RouteEventV3{ProtocolVersion: playback.ProtocolV3, PlaybackAttemptID: f.request.PlaybackAttemptID, SessionID: started.SessionID, PlanID: started.PlaybackPlan.PlanID, Event: playback.RouteEventFirstFrameV3, Diagnostics: map[string]string{"decoder_name": "synthetic", "unlisted": "must not persist"}}
	caller := initialCallerV3(f)
	for range 3 {
		if err := f.handler.ReportInitialRouteEvent(ctx, caller, PlaybackRouteEventCommand{EventID: eventID, Event: event}); err != nil {
			t.Fatalf("report: %v", err)
		}
	}
	second := uuid.NewString()
	if err := f.handler.ReportInitialRouteEvent(ctx, caller, PlaybackRouteEventCommand{EventID: second, Event: event}); err != nil {
		t.Fatalf("second report: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM playback_route_events WHERE playback_attempt_id = $1`, f.request.PlaybackAttemptID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count != 2 {
		t.Fatalf("expected exactly two recorded events, got %d", count)
	}
	var diagnostics map[string]string
	var storedID string
	if err := f.pool.QueryRow(ctx, `SELECT event_id::text, diagnostics FROM playback_route_events WHERE playback_attempt_id = $1 AND event_id = $2::uuid`, f.request.PlaybackAttemptID, eventID).Scan(&storedID, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if storedID != eventID || diagnostics["decoder_name"] != "synthetic" || diagnostics["unlisted"] != "" {
		t.Fatalf("stored event: %s %+v", storedID, diagnostics)
	}
	err := f.handler.ReportInitialRouteEvent(ctx, PlaybackCaller{UserID: f.userID, ProfileID: uuid.NewString(), InstallationID: f.flow.InstallationID}, PlaybackRouteEventCommand{EventID: uuid.NewString(), Event: event})
	requirePlaybackOperationError(t, err, http.StatusForbidden, "forbidden")
	err = f.handler.ReportInitialRouteEvent(ctx, caller, PlaybackRouteEventCommand{EventID: "not-a-uuid", Event: event})
	requirePlaybackOperationError(t, err, http.StatusBadRequest, "bad_request")
	foreign := event
	foreign.PlaybackAttemptID = uuid.NewString()
	err = f.handler.ReportInitialRouteEvent(ctx, caller, PlaybackRouteEventCommand{EventID: uuid.NewString(), Event: foreign})
	requirePlaybackOperationError(t, err, http.StatusForbidden, "forbidden")
	for i := 0; i < 130; i++ {
		err = f.handler.ReportInitialRouteEvent(ctx, caller, PlaybackRouteEventCommand{EventID: uuid.NewString(), Event: event})
		if err != nil {
			break
		}
	}
	requirePlaybackOperationError(t, err, http.StatusTooManyRequests, "event_rate_limited")
}

// initialCallerV3 is the caller the v2 adapter would pass. The HTTP fixture
// configures the flow without an installation id (the v1 handlers never need
// one); the v2 seams require the persisted identity, so the test installs one.
func initialCallerV3(f *initialHTTPFixture) PlaybackCaller {
	if f.flow.InstallationID == "" {
		f.flow.InstallationID = uuid.NewString()
	}
	return PlaybackCaller{UserID: f.userID, ProfileID: f.request.ProfileID, InstallationID: f.flow.InstallationID, ClientName: "synthetic", ClientVersion: "1"}
}

func initialContextV3(t *testing.T, f *initialHTTPFixture) context.Context {
	t.Helper()
	return apimwTestContext(t.Context(), f.userID, f.request.ProfileID)
}

func requirePlaybackOperationError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var operation *PlaybackOperationError
	if err == nil {
		t.Fatalf("expected %d %s, got success", status, code)
	}
	ok := errors.As(err, &operation)
	if !ok || operation.Status != status || operation.Code != code {
		t.Fatalf("expected %d %s, got %v", status, code, err)
	}
}

// apimwTestContext injects the authenticated account and profile the gate
// chain would attach in front of the v2 adapter.
func apimwTestContext(ctx context.Context, userID int, profileID string) context.Context {
	return apimw.SetProfileID(apimw.SetClaims(ctx, &auth.Claims{UserID: userID, Role: "user", TokenType: auth.TokenTypeAccess}), profileID)
}
