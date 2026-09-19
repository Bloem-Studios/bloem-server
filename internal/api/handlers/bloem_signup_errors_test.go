package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
)

type bloemSignupErrorCoordinator struct {
	authLifecycleReplayCoordinator
	err error
}

func (c *bloemSignupErrorCoordinator) ExecuteCreate(_ context.Context, request lifecycleidempotency.Request, _ lifecycleidempotency.CreateMutator) (lifecycleidempotency.Result, error) {
	c.request = request
	return lifecycleidempotency.Result{}, fmt.Errorf("signup rejected: %w", c.err)
}

// Lifecycle safety must not replace the established public signup refusals.
// This unit test checks translation; frozen transport packets exercise the real
// transaction, field-level v2 problem projection, and absence of side effects.
func TestBloemLifecycleSignupPreservesInviteCodeErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		code, detail string
	}{
		{"unknown", auth.ErrInviteCodeNotFound, "invalid_code", "Invalid invite code"},
		{"exhausted", auth.ErrInviteCodeExhausted, "code_exhausted", "This invite code has reached its maximum uses"},
		{"disabled", auth.ErrInviteCodeDisabled, "code_disabled", "This invite code is no longer active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinator := &bloemSignupErrorCoordinator{err: tc.err}
			h := NewAuthHandler(nil, nil, nil)
			h.SetLifecycleIdempotency(coordinator, lifecycleidempotency.NewRequestDigester([]byte("synthetic-signup-test-digest")), lifecycleidempotency.NewPreauthActorDigester([]byte("synthetic-signup-test-actor")), authLifecycleIdentity{id: "fixture-server"})
			view, err := h.Signup(t.Context(), RegistrationInput{
				Username: "fixture", Email: "fixture@example.test", Password: "synthetic-fixture-password", InviteCode: "FIXTURE",
				Lifecycle: &RegistrationLifecycleInput{Method: http.MethodPost, RouteID: "apiv2.auth.signup", Body: []byte(`{}`)},
			})
			var refusal *APIError
			if !errors.As(err, &refusal) {
				t.Fatalf("error type=%T, want APIError", err)
			}
			if refusal.Status != http.StatusBadRequest || refusal.Code != tc.code || refusal.Message != tc.detail || refusal.Field != fieldInviteCode {
				t.Fatalf("unexpected signup refusal: %+v", refusal)
			}
			if view.AccessToken != "" || view.RefreshToken != "" {
				t.Fatal("refused signup returned credentials")
			}
			if coordinator.request.Binding.RouteID != "apiv2.auth.signup" || coordinator.request.Binding.ActorKind != lifecycleidempotency.ActorPreauthIntent {
				t.Fatal("signup bypassed preauthentication lifecycle binding")
			}
		})
	}
}
