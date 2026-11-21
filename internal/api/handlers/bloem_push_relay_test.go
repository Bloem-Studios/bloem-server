package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeBloemRelayRegistrar struct {
	result notifications.RelayCredentialResult
	err    error
	calls  int
}

func (f *fakeBloemRelayRegistrar) RegisterBloemRelay(context.Context) (notifications.RelayCredentialResult, error) {
	f.calls++
	return f.result, f.err
}

func registerBloemRelay(t *testing.T, relay *fakeBloemRelayRegistrar) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewBloemPushRelayHandlerWith(relay).HandleRegister(rec, httptest.NewRequest(http.MethodPost, "/api/bloem/v2/admin/platform/notifications/push/relay/register", nil))
	return rec
}

// A registration answers Silo's registration shape with no credential in it.
func TestBloemPushRelayRegisterAnswersSilosShapeWithoutTheKey(t *testing.T) {
	expires := time.Date(2026, 12, 21, 10, 0, 0, 0, time.UTC)
	relay := &fakeBloemRelayRegistrar{result: notifications.RelayCredentialResult{
		Credential: notifications.PushRelayCredential{RelayURL: notifications.BloemPushRelayURL, DeploymentID: "dep-1",
			APIKey: "capability-secret", KeyPrefix: "cap_v1_x", ExpiresAt: expires},
		RequestID: "relay-request", APNsTopics: []string{"com.example.admin"},
	}}
	rec := registerBloemRelay(t, relay)
	if rec.Code != http.StatusOK || relay.calls != 1 {
		t.Fatalf("register = %d (%d calls) %s", rec.Code, relay.calls, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "capability-secret") {
		t.Fatalf("response carries the relay credential: %s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"relay_url": notifications.BloemPushRelayURL, "deployment_id": "dep-1", "key_prefix": "cap_v1_x",
		"api_key_configured": true, "relay_request_id": "relay-request", "expires_at": "2026-12-21T10:00:00Z",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %v, want %v", key, body[key], value)
		}
	}
	if topics, _ := body["apns_topics"].([]any); len(topics) != 1 || topics[0] != "com.example.admin" {
		t.Errorf("apns_topics = %v", body["apns_topics"])
	}
}

// Relay failures map as on Silo's register route, Retry-After included.
func TestBloemPushRelayRegisterMapsRelayErrorsAsSilo(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{"rate limited", notifications.RelayCredentialError{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "slow down", RetryAfter: 1500 * time.Millisecond}, http.StatusTooManyRequests, "relay_rate_limited", "2"},
		{"deployment refused", notifications.RelayCredentialError{Status: http.StatusForbidden, Code: "forbidden", Message: "no"}, http.StatusUnprocessableEntity, "relay_deployment_rejected", ""},
		{"relay down", notifications.RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_unreachable", Message: "Push relay could not be reached"}, http.StatusBadGateway, "relay_error", ""},
		{"settings write", errors.New("settings store failed"), http.StatusInternalServerError, "internal_error", ""},
		{"no sender", notifications.ErrBloemRelayUnavailable, http.StatusServiceUnavailable, "push_relay_unavailable", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := registerBloemRelay(t, &fakeBloemRelayRegistrar{err: tc.err})
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"error":"`+tc.code+`"`) {
				t.Fatalf("register = %d %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
		})
	}
}

// No push backing, no route.
func TestBloemPushRelayHandlerNeedsAService(t *testing.T) {
	if NewBloemPushRelayHandler(nil) != nil {
		t.Fatal("a server without admin push got a relay register handler")
	}
}
