package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

func TestLifecycleIdempotencyPreflightRejectsRequiredMissingKeyBeforeNext(t *testing.T) {
	nextCalls := 0
	preflight := NewLifecycleIdempotencyPreflight(
		func(context.Context) (lifecycleidempotency.Phase, error) {
			return lifecycleidempotency.PhaseRequired, nil
		},
		func(method, path string) bool { return method == http.MethodDelete && path == "/api/v1/admin/users/7" },
	)
	handler := preflight.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalls++ }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/7", nil))
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428", recorder.Code)
	}
	if nextCalls != 0 {
		t.Fatalf("next calls = %d, want 0", nextCalls)
	}
}

func TestLifecycleIdempotencyPreflightAllowsOptionalUnkeyedAndValidKeyed(t *testing.T) {
	for _, test := range []struct {
		name  string
		phase lifecycleidempotency.Phase
		key   string
	}{
		{name: "optional unkeyed", phase: lifecycleidempotency.PhaseOptional},
		{name: "optional keyed", phase: lifecycleidempotency.PhaseOptional, key: "valid-key-123456789"},
		{name: "required keyed", phase: lifecycleidempotency.PhaseRequired, key: "valid-key-123456789"},
	} {
		t.Run(test.name, func(t *testing.T) {
			nextCalls := 0
			preflight := NewLifecycleIdempotencyPreflight(
				func(context.Context) (lifecycleidempotency.Phase, error) { return test.phase, nil },
				func(string, string) bool { return true },
			)
			handler := preflight.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalls++
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", nil)
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNoContent || nextCalls != 1 {
				t.Fatalf("status/calls = %d/%d, want 204/1", recorder.Code, nextCalls)
			}
		})
	}
}

func TestLifecycleIdempotencyPreflightRejectsMalformedKeyInOptionalPhase(t *testing.T) {
	preflight := NewLifecycleIdempotencyPreflight(
		func(context.Context) (lifecycleidempotency.Phase, error) {
			return lifecycleidempotency.PhaseOptional, nil
		},
		func(string, string) bool { return true },
	)
	handler := preflight.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("malformed key reached next handler")
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", nil)
	request.Header.Set("Idempotency-Key", "short")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
