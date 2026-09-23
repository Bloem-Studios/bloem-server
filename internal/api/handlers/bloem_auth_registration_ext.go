package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// RegistrationLifecycleInput binds a registration to its original transport
// bytes. Each API surface uses a distinct route ID to isolate receipt formats.
type RegistrationLifecycleInput struct {
	Key, Method, RouteID string
	Body                 []byte
	Query                url.Values
}

func (h *AuthHandler) registrationLifecycleView(ctx context.Context, in RegistrationInput, setup bool) (TokenPairView, error) {
	if h.lifecycle == nil || h.lifecycleDigest == nil || h.preauthDigest == nil || h.serverIdentity == nil {
		return TokenPairView{}, apiError(503, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
	}
	serverID, err := h.serverIdentity.Resolve(ctx)
	if err != nil {
		return TokenPairView{}, apiError(503, "server_identity_unavailable", "Server identity is temporarily unavailable")
	}
	wire := in.Lifecycle
	actor := []string{serverID}
	if !setup {
		actor = append(actor, in.InviteCode)
	}
	request := lifecycleidempotency.Request{IdempotencyKey: wire.Key, Binding: lifecycleidempotency.Binding{
		ActorKind:          lifecycleidempotency.ActorPreauthIntent,
		ActorSubjectDigest: h.preauthDigest(wire.RouteID, actor...), Method: wire.Method, RouteID: wire.RouteID,
		RequestHash: h.lifecycleDigest(wire.Method, wire.RouteID, nil, wire.Query, wire.Body), TargetSource: lifecycleidempotency.TargetBodyAccount,
	}}
	if setup {
		ctx = lifecycleidempotency.WithInitialSetupAdmission(ctx, auth.InitialSetupAdvisoryLock)
	}
	result, err := h.lifecycle.ExecuteCreate(ctx, request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		var pair *auth.TokenPair
		var created auth.CreatedAccount
		var err error
		if setup {
			pair, created, err = h.service.SetupInitialUserInTransaction(ctx, tx, in.Username, in.Email, in.Password, in.CreateDefaultProfile, in.DefaultProfileName, in.DeviceName, in.IP)
		} else {
			pair, created, err = h.service.SignupInTransaction(ctx, tx, in.Username, in.Email, in.Password, in.InviteCode, in.CreateDefaultProfile, in.DefaultProfileName, in.DeviceName, in.IP)
		}
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		receipt, err := h.lifecycleLoginResult(ctx, tx, pair, created)
		return createdAccountLifecycleTargets(created), receipt, err
	})
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrSetupAlreadyComplete):
			return TokenPairView{}, apiError(409, "setup_complete", "Initial setup has already been completed")
		case errors.Is(err, auth.ErrSignupDisabled):
			return TokenPairView{}, apiError(403, "signup_disabled", "Public signups are not currently enabled")
		case errors.Is(err, auth.ErrInviteCodeNotFound):
			return TokenPairView{}, &APIError{Status: 400, Code: "invalid_code", Message: "Invalid invite code", Field: fieldInviteCode}
		case errors.Is(err, auth.ErrInviteCodeExhausted):
			return TokenPairView{}, &APIError{Status: 400, Code: "code_exhausted", Message: "This invite code has reached its maximum uses", Field: fieldInviteCode}
		case errors.Is(err, auth.ErrInviteCodeDisabled):
			return TokenPairView{}, &APIError{Status: 400, Code: "code_disabled", Message: "This invite code is no longer active", Field: fieldInviteCode}
		case auth.IsDuplicate(err):
			return TokenPairView{}, apiError(409, "duplicate", "Username or email already taken")
		default:
			return TokenPairView{}, profileLifecycleError(err)
		}
	}
	var receipt loginResponse
	if err := json.Unmarshal(result.Body, &receipt); err != nil {
		return TokenPairView{}, apiError(500, "internal_error", "Stored registration response is invalid")
	}
	return TokenPairView{AccessToken: receipt.AccessToken, RefreshToken: receipt.RefreshToken, ExpiresIn: receipt.ExpiresIn, User: receipt.User}, nil //nolint:staticcheck // S1016: explicit mapping keeps the two shapes decoupled.
}
