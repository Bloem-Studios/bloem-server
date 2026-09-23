package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
)

func (h *AuthHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, requestDigest lifecycleidempotency.RequestDigester, preauthDigest lifecycleidempotency.PreauthActorDigester, identity interface {
	Resolve(context.Context) (string, error)
}) {
	h.lifecycle = coordinator
	h.lifecycleDigest = requestDigest
	h.preauthDigest = preauthDigest
	h.serverIdentity = identity
}

type profileLoginService interface {
	LoginProfile(context.Context, string, string, auth.DeviceClaim) (*auth.TokenPair, auth.SessionSubject, error)
}

// SetAPIKeyAuth wires API-key authentication into the handlers whose own
// extractClaims previously only accepted a JWT — the same asymmetry
// AuthMiddleware.RequireAuth already closed for the rest of the API. Without
// this, a long-lived "sa_" API key authenticates against every other
// endpoint but is silently rejected by /auth/me, /auth/sessions, and
// friends, which is exactly backwards for a key whose whole purpose is to
// outlive a login session. Nil validator/loader (the zero value) preserves
// today's JWT-only behavior.
func (h *AuthHandler) SetAPIKeyAuth(validator apimw.APIKeyValidator, loader apimw.APIKeyUserLoader) {
	h.apiKeyValidator = validator
	h.apiKeyUserLoader = loader
}

type profileLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

// profileLoginResponse deliberately excludes the account record and sibling
// profiles. A successful direct profile login establishes exactly one subject.
type profileLoginResponse struct {
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token"`
	ExpiresIn          int    `json:"expires_in"`
	ProfileID          string `json:"profile_id"`
	OrganizationID     string `json:"organization_id"`
	MembershipID       string `json:"membership_id"`
	PolicyRevision     int64  `json:"policy_revision"`
	SecurityRevision   int64  `json:"security_revision"`
	CredentialRevision int64  `json:"credential_revision"`
}

// HandleProfileLogin exchanges an optional direct profile credential for a
// profile-bound session without changing the legacy account login flow.
func (h *AuthHandler) HandleProfileLogin(w http.ResponseWriter, r *http.Request) {
	if h.profileLogin == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Direct profile login is not configured")
		return
	}
	var req profileLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Email and password are required")
		return
	}
	// The session binds to exactly one device, and that binding is enforced on
	// every subsequent request. An empty device id would bind the session to
	// "no device" and make the enforcement vacuous.
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "A device_id is required for direct profile login")
		return
	}
	pair, subject, err := h.profileLogin.LoginProfile(r.Context(), req.Email, req.Password, auth.DeviceClaim{
		ID:        req.DeviceID,
		Name:      r.UserAgent(),
		IPAddress: clientip.FromContext(r.Context()),
	})
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		return
	}
	writeJSON(w, http.StatusOK, profileLoginResponse{
		AccessToken:        pair.AccessToken,
		RefreshToken:       pair.RefreshToken,
		ExpiresIn:          pair.ExpiresIn,
		ProfileID:          subject.ProfileID,
		OrganizationID:     subject.OrganizationID,
		MembershipID:       subject.MembershipID,
		PolicyRevision:     subject.PolicyRevision,
		SecurityRevision:   subject.SecurityRevision,
		CredentialRevision: subject.CredentialRevision,
	})
}

type transactionalAuthAccessGroups interface {
	GetInTransaction(context.Context, pgx.Tx, uuid.UUID, int64) (*access.Group, error)
}

func (h *AuthHandler) handleLifecycleSetup(w http.ResponseWriter, r *http.Request, body []byte, req setupRequest, deviceName, ip string) {
	serverID, err := h.serverIdentity.Resolve(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_identity_unavailable", "Server identity is temporarily unavailable")
		return
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind:          lifecycleidempotency.ActorPreauthIntent,
			ActorSubjectDigest: h.preauthDigest("auth.setup", serverID),
			Method:             r.Method,
			RouteID:            "auth.setup",
			RequestHash:        h.lifecycleDigest(r.Method, "auth.setup", nil, r.URL.Query(), body),
			TargetSource:       lifecycleidempotency.TargetBodyAccount,
		},
	}
	setupCtx := lifecycleidempotency.WithInitialSetupAdmission(r.Context(), auth.InitialSetupAdvisoryLock)
	result, err := h.lifecycle.ExecuteCreate(setupCtx, request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		pair, created, err := h.service.SetupInitialUserInTransaction(ctx, tx, req.Username, req.Email, req.Password, req.CreateDefaultProfile, req.DefaultProfileName, deviceName, ip)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		response, err := h.lifecycleLoginResult(ctx, tx, pair, created)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		return createdAccountLifecycleTargets(created), response, nil
	})
	if err != nil {
		if writeBloemLifecycleError(w, err) {
			return
		}
		if errors.Is(err, auth.ErrSetupAlreadyComplete) {
			writeError(w, http.StatusUnauthorized, "setup_complete", "Initial setup has already been completed")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		return
	}
	writeBloemLifecycleResult(w, result)
}

func (h *AuthHandler) handleLifecycleSignup(w http.ResponseWriter, r *http.Request, body []byte, req signupRequest, deviceName, ip string) {
	serverID, err := h.serverIdentity.Resolve(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_identity_unavailable", "Server identity is temporarily unavailable")
		return
	}
	request := lifecycleidempotency.Request{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Binding: lifecycleidempotency.Binding{
			ActorKind:          lifecycleidempotency.ActorPreauthIntent,
			ActorSubjectDigest: h.preauthDigest("auth.signup", serverID, req.InviteCode),
			Method:             r.Method,
			RouteID:            "auth.signup",
			RequestHash:        h.lifecycleDigest(r.Method, "auth.signup", nil, r.URL.Query(), body),
			TargetSource:       lifecycleidempotency.TargetBodyAccount,
		},
	}
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
		pair, created, err := h.service.SignupInTransaction(ctx, tx, req.Username, req.Email, req.Password, req.InviteCode, req.CreateDefaultProfile, req.DefaultProfileName, deviceName, ip)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		response, err := h.lifecycleLoginResult(ctx, tx, pair, created)
		if err != nil {
			return nil, lifecycleidempotency.Result{}, err
		}
		return createdAccountLifecycleTargets(created), response, nil
	})
	if err != nil {
		if writeBloemLifecycleError(w, err) {
			return
		}
		switch {
		case errors.Is(err, auth.ErrSignupDisabled):
			writeError(w, http.StatusForbidden, "signup_disabled", "Public signups are not currently enabled")
		case errors.Is(err, auth.ErrInviteCodeNotFound):
			writeError(w, http.StatusBadRequest, "invalid_code", "Invalid invite code")
		case errors.Is(err, auth.ErrInviteCodeExhausted):
			writeError(w, http.StatusBadRequest, "code_exhausted", "This invite code has reached its maximum uses")
		case errors.Is(err, auth.ErrInviteCodeDisabled):
			writeError(w, http.StatusBadRequest, "code_disabled", "This invite code is no longer active")
		case auth.IsDuplicate(err):
			writeError(w, http.StatusBadRequest, "duplicate", "Username or email already taken")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		}
		return
	}
	writeBloemLifecycleResult(w, result)
}

func createdAccountLifecycleTargets(created auth.CreatedAccount) []lifecycleidempotency.TargetBinding {
	return []lifecycleidempotency.TargetBinding{{
		OrganizationID: created.OrganizationID, MembershipID: created.MembershipID,
		AccountID: created.User.ID, AccountIncarnationID: created.User.AccountIncarnationID,
		ProfileID: created.ProfileID,
	}}
}

func (h *AuthHandler) lifecycleLoginResult(ctx context.Context, tx pgx.Tx, pair *auth.TokenPair, created auth.CreatedAccount) (lifecycleidempotency.Result, error) {
	var groupPolicy *access.GroupPolicy
	if created.User.Role != models.RoleAdmin && h.accessGroups != nil {
		var groupID *int64
		if err := tx.QueryRow(ctx, `SELECT access_group_id FROM organization_memberships WHERE id=$1`, created.MembershipID).Scan(&groupID); err != nil {
			return lifecycleidempotency.Result{}, err
		}
		if groupID != nil {
			groups, ok := h.accessGroups.(transactionalAuthAccessGroups)
			if !ok {
				return lifecycleidempotency.Result{}, errors.New("access groups do not support caller-owned transactions")
			}
			group, err := groups.GetInTransaction(ctx, tx, created.OrganizationID, *groupID)
			if err != nil {
				return lifecycleidempotency.Result{}, err
			}
			if group != nil {
				policy := group.Policy()
				groupPolicy = &policy
			}
		}
	}
	downloadAllowed := access.ApplyGroupPolicy(created.User, groupPolicy).DownloadAllowed
	payload, err := json.Marshal(buildLoginResponse(pair, created.User, downloadAllowed, nil))
	if err != nil {
		return lifecycleidempotency.Result{}, err
	}
	return lifecycleidempotency.Result{Status: http.StatusCreated, Body: payload, Headers: map[string][]string{"Content-Type": {"application/json"}}}, nil
}

// bloemAuthHandlerExt holds the Bloem-only AuthHandler dependencies so the
// Silo-owned struct carries a single embedded line.
type bloemAuthHandlerExt struct {
	loginTenants    apimw.TenantResolver
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
	preauthDigest   lifecycleidempotency.PreauthActorDigester
	serverIdentity  interface {
		Resolve(context.Context) (string, error)
	}
}

// bloemAPIKeyClaims validates a long-lived "sa_"-prefixed API key when
// SetAPIKeyAuth has wired one in, the same way AuthMiddleware.RequireAuth
// validates one for the rest of the API. Without that parity, an API key
// works everywhere except here. handled=false leaves the token to the JWT path.
func (h *AuthHandler) bloemAPIKeyClaims(r *http.Request, token string) (*auth.Claims, bool, error) {
	if !strings.HasPrefix(token, "sa_") {
		return nil, false, nil
	}
	if h.apiKeyValidator == nil || h.apiKeyUserLoader == nil {
		return nil, true, auth.ErrInvalidToken
	}
	apiKey, err := h.apiKeyValidator.GetByKey(r.Context(), token)
	if err != nil {
		return nil, true, auth.ErrInvalidToken
	}
	user, err := h.apiKeyUserLoader.GetByID(r.Context(), apiKey.UserID)
	if err != nil {
		return nil, true, auth.ErrInvalidToken
	}
	if !user.Enabled {
		return nil, true, auth.ErrInvalidToken
	}
	go func(id int64) {
		_ = h.apiKeyValidator.UpdateLastUsed(context.Background(), id)
	}(apiKey.ID)
	return &auth.Claims{
		UserID:               user.ID,
		AccountIncarnationID: user.AccountIncarnationID.String(),
		Role:                 user.Role,
		SessionID:            "",
		TokenType:            auth.TokenTypeAPIKey,
		APIKeyID:             apiKey.ID,
		RateTier:             apiKey.RateTier,
		APIKeyScopes:         apiKey.Scopes,
	}, true, nil
}

// bloemLifecycleSetup normalizes and validates the request, then dispatches it to the
// lifecycle receipt path when wired. It reports whether it wrote the response.
func (h *AuthHandler) bloemLifecycleSetup(w http.ResponseWriter, r *http.Request, body []byte, req *setupRequest) bool {
	req.Username = auth.NormalizeUsername(req.Username)
	req.Email = auth.NormalizeEmail(req.Email)

	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Username, email, and password are required")
		return true
	}

	deviceName := r.UserAgent()
	ip := clientip.FromContext(r.Context())
	if h.lifecycle != nil && h.lifecycleDigest != nil && h.preauthDigest != nil && h.serverIdentity != nil {
		h.handleLifecycleSetup(w, r, body, *req, deviceName, ip)
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
}

// bloemLifecycleSignup normalizes and validates the request, then dispatches it to the
// lifecycle receipt path when wired. It reports whether it wrote the response.
func (h *AuthHandler) bloemLifecycleSignup(w http.ResponseWriter, r *http.Request, body []byte, req *signupRequest) bool {
	req.Username = auth.NormalizeUsername(req.Username)
	req.Email = auth.NormalizeEmail(req.Email)

	if req.Username == "" || req.Email == "" || req.Password == "" || req.InviteCode == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Username, email, password, and invite code are required")
		return true
	}

	deviceName := r.UserAgent()
	ip := clientip.FromContext(r.Context())
	if h.lifecycle != nil && h.lifecycleDigest != nil && h.preauthDigest != nil && h.serverIdentity != nil {
		h.handleLifecycleSignup(w, r, body, *req, deviceName, ip)
		return true
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return true
	}
	return false
}
