package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/models"
)

// AuthHandler handles authentication-related HTTP endpoints.
type AuthHandler struct {
	service              *auth.Service
	profileLogin         profileLoginService
	passwords            accountPasswordService
	jwt                  *auth.JWTService
	device               *auth.DeviceLoginService
	oauthRoutesAvailable bool
	apiKeyValidator      apimw.APIKeyValidator  // nil if API keys not configured
	apiKeyUserLoader     apimw.APIKeyUserLoader // nil if API keys not configured
	accessGroups         access.GroupPolicyProvider
	lifecycle            lifecycleidempotency.Coordinator
	lifecycleDigest      lifecycleidempotency.RequestDigester
	preauthDigest        lifecycleidempotency.PreauthActorDigester
	serverIdentity       interface {
		Resolve(context.Context) (string, error)
	}
	checkPrimaryProfile apimw.PrimaryProfileChecker
}

func (h *AuthHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, requestDigest lifecycleidempotency.RequestDigester, preauthDigest lifecycleidempotency.PreauthActorDigester, identity interface {
	Resolve(context.Context) (string, error)
}) {
	h.lifecycle = coordinator
	h.lifecycleDigest = requestDigest
	h.preauthDigest = preauthDigest
	h.serverIdentity = identity
}

type accountPasswordService interface {
	PasswordChangeAvailable(ctx context.Context, userID int) (bool, error)
	ChangePassword(ctx context.Context, userID int, currentPassword, newPassword string) error
}

type profileLoginService interface {
	LoginProfile(context.Context, string, string, auth.DeviceClaim) (*auth.TokenPair, auth.SessionSubject, error)
}

// NewAuthHandler creates a new AuthHandler backed by the given auth, JWT,
// and device login services.
func NewAuthHandler(service *auth.Service, jwt *auth.JWTService, device *auth.DeviceLoginService) *AuthHandler {
	handler := &AuthHandler{
		service: service,
		jwt:     jwt,
		device:  device,
	}
	if service != nil {
		handler.profileLogin = service
		handler.passwords = service
	}
	return handler
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

// SetAccessGroupProvider wires the access-group policy source used to resolve
// the effective (inherit/override) policy reported on login and /auth/me.
func (h *AuthHandler) SetAccessGroupProvider(provider access.GroupPolicyProvider) {
	h.accessGroups = provider
}

// SetPrimaryProfileChecker wires the account/profile ownership lookup used by
// account credential endpoints. A declared secondary profile must never be
// able to replace the shared account password, including on an admin account.
func (h *AuthHandler) SetPrimaryProfileChecker(check apimw.PrimaryProfileChecker) {
	h.checkPrimaryProfile = check
}

// SetOAuthRoutesAvailable controls whether OAuth login providers are
// advertised by /auth/providers. The router only mounts OAuth routes when the
// server has enough configuration to complete the flow.
func (h *AuthHandler) SetOAuthRoutesAvailable(available bool) {
	h.oauthRoutesAvailable = available
}

// --- Request/Response types ---

// loginRequest represents the JSON body of a POST /auth/login request.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Provider string `json:"provider,omitempty"`
}

// loginResponse represents the JSON body of a successful login response.
type loginResponse struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token"`
	ExpiresIn    int      `json:"expires_in"`
	User         UserView `json:"user"`
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

// setupRequest represents the JSON body of a POST /auth/setup request.
type setupRequest struct {
	Username             string `json:"username"`
	Email                string `json:"email"`
	Password             string `json:"password"`
	CreateDefaultProfile bool   `json:"create_default_profile"`
	DefaultProfileName   string `json:"default_profile_name,omitempty"`
}

// setupStatusResponse represents the JSON body of a GET /auth/setup request.
type setupStatusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

// signupRequest represents the JSON body of a POST /auth/signup request.
type signupRequest struct {
	Username             string `json:"username"`
	Email                string `json:"email"`
	Password             string `json:"password"`
	InviteCode           string `json:"invite_code"`
	CreateDefaultProfile bool   `json:"create_default_profile"`
	DefaultProfileName   string `json:"default_profile_name,omitempty"`
}

// signupStatusResponse represents the JSON body of a GET /auth/signup request.
type signupStatusResponse struct {
	Enabled bool `json:"enabled"`
}

// refreshRequest represents the JSON body of a POST /auth/refresh request.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshResponse represents the JSON body of a successful refresh response.
type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type pluginLaunchResponse struct {
	ExpiresIn int `json:"expires_in"`
}

type ImpersonationView struct {
	Active               bool   `json:"active"`
	ImpersonatorUserID   int    `json:"impersonator_user_id"`
	ImpersonatorUsername string `json:"impersonator_username"`
}

// UserView represents a user in JSON responses.
type UserView struct {
	ID              int                `json:"id"`
	Username        string             `json:"username"`
	Email           string             `json:"email"`
	Role            string             `json:"role"`
	Permissions     []string           `json:"permissions"`
	DownloadAllowed bool               `json:"download_allowed"`
	Impersonation   *ImpersonationView `json:"impersonation,omitempty"`
}

// sessionResponse represents a session in JSON responses.
type sessionResponse struct {
	ID         string     `json:"id"`
	DeviceName string     `json:"device_name"`
	IPAddress  string     `json:"ip_address"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// sessionsListResponse represents the JSON body of a GET /auth/sessions response.
type sessionsListResponse struct {
	Sessions []sessionResponse `json:"sessions"`
}

// errorResponse represents an error in JSON responses.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type authProviderResponse struct {
	ID             string `json:"id"`
	DisplayName    string `json:"display_name"`
	Mode           string `json:"mode"`
	Default        bool   `json:"default"`
	IconURL        string `json:"icon_url,omitempty"`
	InstallationID int    `json:"installation_id,omitempty"`
}

// --- Handler methods ---

// HandleLogin handles POST /auth/login.
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	// Extract device name from User-Agent header and IP from request.
	view, err := h.Login(r.Context(), LoginInput{
		Provider:   req.Provider,
		Username:   req.Username,
		Password:   req.Password,
		DeviceName: r.UserAgent(),
		IP:         clientip.FromContext(r.Context()),
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, loginResponseOf(view))
}

// LoginInput is a password login as the transport received it.
type LoginInput struct {
	Provider   string
	Username   string
	Password   string
	DeviceName string
	IP         string
}

// loginResponseOf renders the shared credential view in the v1 shape.
func loginResponseOf(v TokenPairView) loginResponse {
	return loginResponse(v)
}

// Login authenticates a username and password and opens a login session. v1
// POST /auth/login and v2 login both call it; a failure is an *APIError.
func (h *AuthHandler) Login(ctx context.Context, in LoginInput) (TokenPairView, error) {
	if in.Username == "" || in.Password == "" {
		return TokenPairView{}, apiError(http.StatusBadRequest, "bad_request", "Username and password are required")
	}
	pair, user, err := h.service.LoginWithProvider(ctx, in.Provider, in.Username, in.Password, in.DeviceName, in.IP)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return TokenPairView{}, apiError(http.StatusUnauthorized, "invalid_credentials", "Invalid username or password")
		}
		if errors.Is(err, auth.ErrUserDisabled) {
			return TokenPairView{}, apiError(http.StatusForbidden, "user_disabled", "User account is disabled")
		}
		return TokenPairView{}, apiError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
	return TokenPairView{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User:         buildUserResponse(user, effectiveDownloadAllowed(ctx, user, h.accessGroups), nil, nil),
	}, nil
}

// Logout revokes the caller's login session. v1 POST /auth/logout and v2
// logout both call it.
func (h *AuthHandler) Logout(ctx context.Context, claims *auth.Claims) error {
	if err := h.service.Logout(ctx, claims.SessionID); err != nil {
		return apiError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
	return nil
}

// EndImpersonation returns an impersonating session to the administrator.
// v1 POST /auth/impersonation/end and v2 endImpersonation both call it.
func (h *AuthHandler) EndImpersonation(ctx context.Context, claims *auth.Claims) error {
	if claims.ImpersonatorUserID == nil {
		return apiError(http.StatusBadRequest, "not_impersonating", "No active impersonation session")
	}
	if err := h.service.EndImpersonation(ctx, claims.SessionID, *claims.ImpersonatorUserID); err != nil {
		if errors.Is(err, auth.ErrNotImpersonating) {
			return apiError(http.StatusBadRequest, "not_impersonating", "No active impersonation session")
		}
		if errors.Is(err, auth.ErrImpersonationNotAllowed) {
			return apiError(http.StatusForbidden, "impersonation_not_allowed", "Impersonation is not allowed")
		}
		return apiError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}
	return nil
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

func (h *AuthHandler) HandleProviders(w http.ResponseWriter, r *http.Request) {
	providers := h.ListProviders()
	response := make([]authProviderResponse, 0, len(providers))
	for _, provider := range providers {
		response = append(response, authProviderResponse{
			ID:             provider.ID,
			DisplayName:    provider.DisplayName,
			Mode:           provider.Mode,
			Default:        provider.Default,
			IconURL:        provider.IconURL,
			InstallationID: provider.InstallationID,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleSetupStatus handles GET /auth/setup.
func (h *AuthHandler) HandleSetupStatus(w http.ResponseWriter, r *http.Request) {
	needsSetup, err := h.service.NeedsSetup(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
		return
	}

	writeJSON(w, http.StatusOK, setupStatusResponse{
		NeedsSetup: needsSetup,
	})
}

// HandleSetup handles POST /auth/setup.
func (h *AuthHandler) HandleSetup(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var req setupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	req.Username = auth.NormalizeUsername(req.Username)
	req.Email = auth.NormalizeEmail(req.Email)

	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Username, email, and password are required")
		return
	}

	deviceName := r.UserAgent()
	ip := clientip.FromContext(r.Context())
	if h.lifecycle != nil && h.lifecycleDigest != nil && h.preauthDigest != nil && h.serverIdentity != nil {
		h.handleLifecycleSetup(w, r, body, req, deviceName, ip)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}

	view, err := h.SetupInitialUser(r.Context(), RegistrationInput{
		Username:             req.Username,
		Email:                req.Email,
		Password:             req.Password,
		CreateDefaultProfile: req.CreateDefaultProfile,
		DefaultProfileName:   req.DefaultProfileName,
		DeviceName:           r.UserAgent(),
		IP:                   clientip.FromContext(r.Context()),
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, loginResponseOf(view))
}

// HandleLogout handles POST /auth/logout. Requires authentication.
func (h *AuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}

	if err := h.Logout(r.Context(), claims); err != nil {
		writeAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleEndImpersonation handles POST /auth/impersonation/end. Requires authentication.
func (h *AuthHandler) HandleEndImpersonation(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}
	if err := h.EndImpersonation(r.Context(), claims); err != nil {
		writeAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRefresh handles POST /auth/refresh.
func (h *AuthHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	pair, err := h.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, refreshResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
	})
}

func (h *AuthHandler) HandlePluginLaunch(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil || claims.SessionID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}

	const ttl = PluginLaunchTTL
	token, err := h.PluginLaunchToken(claims, apimw.GetProfileID(r.Context()))
	if err != nil {
		writeAPIError(w, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.PluginAccessCookieName,
		Value:    token,
		Path:     "/api/v1",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureRequest(r),
	})
	writeJSON(w, http.StatusOK, pluginLaunchResponse{ExpiresIn: int(ttl.Seconds())})
}

// HandleMe handles GET /auth/me. Requires authentication.
func (h *AuthHandler) HandleMe(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}

	resp, err := h.CurrentUser(r.Context(), claims)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// NeedsSetup reports whether the first administrator still has to be created.
// v1 GET /auth/setup and v2 getSetupStatus both answer from it.
func (h *AuthHandler) NeedsSetup(ctx context.Context) (bool, error) {
	return h.service.NeedsSetup(ctx)
}

// CurrentUser builds the account view of the authenticated caller. v1 GET
// /auth/me and v2 getCurrentUser both call it; a failure is an *APIError.
func (h *AuthHandler) CurrentUser(ctx context.Context, claims *auth.Claims) (UserView, error) {
	user, err := h.service.GetCurrentUser(ctx, claims)
	if err != nil {
		return UserView{}, apiError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}

	impersonator, err := h.loadImpersonator(ctx, claims)
	if err != nil && !auth.IsNotFound(err) {
		return UserView{}, apiError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
	}

	return buildUserResponse(user, effectiveDownloadAllowed(ctx, user, h.accessGroups), claims.ImpersonatorUserID, impersonator), nil
}

// HandleListSessions handles GET /auth/sessions. Requires authentication.
func (h *AuthHandler) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}

	sessions, err := h.ListSessions(r.Context(), claims.UserID)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	resp := sessionsListResponse{
		Sessions: make([]sessionResponse, 0, len(sessions)),
	}
	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, sessionResponse{
			ID:         s.ID,
			DeviceName: s.DeviceName,
			IPAddress:  s.IPAddress,
			CreatedAt:  s.CreatedAt,
			ExpiresAt:  s.ExpiresAt,
			RevokedAt:  s.RevokedAt,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleDeleteSession handles DELETE /auth/sessions/{id}. Requires authentication.
func (h *AuthHandler) HandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	claims, err := h.extractClaims(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid or missing authentication token")
		return
	}

	if err := h.RevokeSession(r.Context(), chi.URLParam(r, "id"), claims.UserID); err != nil {
		writeAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleSignupStatus handles GET /auth/signup.
func (h *AuthHandler) HandleSignupStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := h.SignupEnabled(r.Context())
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, signupStatusResponse{Enabled: enabled})
}

// HandleSignup handles POST /auth/signup.
func (h *AuthHandler) HandleSignup(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	var req signupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	req.Username = auth.NormalizeUsername(req.Username)
	req.Email = auth.NormalizeEmail(req.Email)

	if req.Username == "" || req.Email == "" || req.Password == "" || req.InviteCode == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Username, email, password, and invite code are required")
		return
	}

	deviceName := r.UserAgent()
	ip := clientip.FromContext(r.Context())
	if h.lifecycle != nil && h.lifecycleDigest != nil && h.preauthDigest != nil && h.serverIdentity != nil {
		h.handleLifecycleSignup(w, r, body, req, deviceName, ip)
		return
	}
	if r.Header.Get("Idempotency-Key") != "" {
		writeError(w, http.StatusServiceUnavailable, "lifecycle_idempotency_unavailable", "Lifecycle request safety is temporarily unavailable")
		return
	}

	view, err := h.Signup(r.Context(), RegistrationInput{
		Username:             req.Username,
		Email:                req.Email,
		Password:             req.Password,
		InviteCode:           req.InviteCode,
		CreateDefaultProfile: req.CreateDefaultProfile,
		DefaultProfileName:   req.DefaultProfileName,
		DeviceName:           r.UserAgent(),
		IP:                   clientip.FromContext(r.Context()),
	})
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, loginResponseOf(view))
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
	result, err := h.lifecycle.ExecuteCreate(r.Context(), request, func(ctx context.Context, tx pgx.Tx) ([]lifecycleidempotency.TargetBinding, lifecycleidempotency.Result, error) {
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

// --- Helper functions ---

func buildLoginResponse(pair *auth.TokenPair, user *models.User, downloadAllowed bool, impersonator *models.User) loginResponse {
	return loginResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		User:         buildUserResponse(user, downloadAllowed, impersonatorUserID(impersonator), impersonator),
	}
}

// effectiveDownloadAllowed resolves the account's download gate through the
// inherit/override policy (user override, else access group, else permissive
// default). A failed group lookup reports downloads as unavailable rather than
// falling back to the raw account value, which is not meaningful on its own.
func effectiveDownloadAllowed(ctx context.Context, user *models.User, groups access.GroupPolicyProvider) bool {
	if user == nil {
		return false
	}
	effective, err := access.EffectivePolicyForUser(ctx, user, groups)
	if err != nil {
		slog.WarnContext(ctx, "failed to resolve effective download policy", "component", "api", "user_id", user.ID, "error", err)
		return false
	}
	return effective.DownloadAllowed
}

func buildUserResponse(user *models.User, downloadAllowed bool, impersonatorUserID *int, impersonator *models.User) UserView {
	resp := UserView{
		ID:              user.ID,
		Username:        user.Username,
		Email:           user.Email,
		Role:            user.Role,
		Permissions:     auth.EffectivePermissions(user),
		DownloadAllowed: downloadAllowed,
	}
	if impersonatorUserID != nil {
		resp.Impersonation = &ImpersonationView{
			Active:             true,
			ImpersonatorUserID: *impersonatorUserID,
		}
		if impersonator != nil {
			resp.Impersonation.ImpersonatorUsername = impersonator.Username
		}
	}
	return resp
}

func impersonatorUserID(impersonator *models.User) *int {
	if impersonator == nil {
		return nil
	}
	return &impersonator.ID
}

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// IsSecureRequest reports whether the request arrived over TLS, directly or
// behind a proxy that forwards the scheme. It is the seam v2 shares with the
// v1 cookie handlers so both surfaces mark cookies Secure the same way.
func IsSecureRequest(r *http.Request) bool { return isSecureRequest(r) }

func (h *AuthHandler) loadImpersonator(ctx context.Context, claims *auth.Claims) (*models.User, error) {
	if claims == nil || claims.ImpersonatorUserID == nil {
		return nil, nil
	}
	return h.service.GetCurrentUser(ctx, &auth.Claims{UserID: *claims.ImpersonatorUserID})
}

// extractClaims extracts claims from the Authorization header: a JWT
// access token, or — when SetAPIKeyAuth has wired one in — a long-lived
// "sa_"-prefixed API key, validated the same way AuthMiddleware.RequireAuth
// validates one for the rest of the API. Without that parity, an API key
// works everywhere except here.
func (h *AuthHandler) extractClaims(r *http.Request) (*auth.Claims, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, auth.ErrInvalidToken
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return nil, auth.ErrInvalidToken
	}
	token := parts[1]

	if strings.HasPrefix(token, "sa_") {
		if h.apiKeyValidator == nil || h.apiKeyUserLoader == nil {
			return nil, auth.ErrInvalidToken
		}
		apiKey, err := h.apiKeyValidator.GetByKey(r.Context(), token)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		user, err := h.apiKeyUserLoader.GetByID(r.Context(), apiKey.UserID)
		if err != nil {
			return nil, auth.ErrInvalidToken
		}
		if !user.Enabled {
			return nil, auth.ErrInvalidToken
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
		}, nil
	}

	return h.jwt.ValidateToken(token)
}

// writeJSON marshals the given value as JSON and writes it to the response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response with the given status code,
// error code, and message.
// writeAPIError renders an *APIError in the v1 {error, message} shape; any
// other error is the generic internal error.
func writeAPIError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(apiErr.RetryAfter))
		}
		writeError(w, apiErr.Status, apiErr.Code, apiErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Error:   code,
		Message: message,
	})
}
