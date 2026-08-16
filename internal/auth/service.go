package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

// Sentinel errors for service operations.
var (
	ErrSessionRevoked          = errors.New("session has been revoked")
	ErrSetupAlreadyComplete    = errors.New("initial setup already complete")
	ErrSignupDisabled          = errors.New("public signups are not enabled")
	ErrImpersonationNotAllowed = errors.New("impersonation not allowed")
	ErrAlreadyImpersonating    = errors.New("already impersonating")
	ErrNotImpersonating        = errors.New("not impersonating")
	// ErrDeviceRequired refuses a direct profile login that names no device:
	// the session binds to exactly one, and an empty binding would make every
	// later device check vacuous.
	ErrDeviceRequired = errors.New("a device id is required for direct profile login")
)

// TokenPair holds the access and refresh tokens returned after login or refresh.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds until access token expires
}

// SettingsGetter retrieves server settings by key.
// Implemented by catalog.ServerSettingsRepo.
type SettingsGetter interface {
	Get(ctx context.Context, key string) (string, error)
}

// OwnershipBootstrapper activates the protected platform and organization
// ownership state for the first account created during setup.
type OwnershipBootstrapper interface {
	ActivateInitialOwnership(ctx context.Context, accountID int) error
}

type serviceUserRepository interface {
	AccountUserRepository
	Count(ctx context.Context) (int, error)
	GetByID(ctx context.Context, id int) (*models.User, error)
}

type serviceSessionRepository interface {
	Create(ctx context.Context, session models.AuthSession) error
	// CreateProfileSessionIfCurrent inserts a direct-profile session only if
	// the subject verified at authentication is still the current one,
	// serialized against credential rotation.
	CreateProfileSessionIfCurrent(ctx context.Context, session models.AuthSession, subject SessionSubject) error
	GetByID(ctx context.Context, id string) (*models.AuthSession, error)
	ListByUser(ctx context.Context, userID int) ([]*models.AuthSession, error)
	Revoke(ctx context.Context, id string) error
	ExtendExpiresAt(ctx context.Context, id string, expiresAt time.Time) error
}

// directProfileCredentials is the direct-profile half of the auth service's
// dependencies. It is an interface so refresh's failure handling — which must
// tell "this binding is gone" apart from "the database was briefly
// unreachable" — can be exercised without a database.
type directProfileCredentials interface {
	Authenticate(ctx context.Context, email, password string, device DeviceClaim) (SessionSubject, error)
	CurrentSessionSubject(
		ctx context.Context, accountID int, profileID string, revision int64, device DeviceClaim,
	) (SessionSubject, error)
}

type claimsContextKey struct{}

// WithClaims stores auth claims on the context for auth-owned flows.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// ClaimsFromContext retrieves auth claims previously stored with WithClaims.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*Claims)
	return claims
}

// Service orchestrates authentication operations using an AuthProvider,
// JWTService, and session/user repositories.
type Service struct {
	provider           AuthProvider
	jwt                *JWTService
	sessions           serviceSessionRepository
	users              serviceUserRepository
	inviteCodes        *InviteCodeRepository
	settings           SettingsGetter
	providers          map[string]AuthProvider
	metadata           map[string]LoginProviderInfo
	defaultID          string
	accounts           *AccountProvisioner
	ownership          OwnershipBootstrapper
	memberships        MembershipProvisioner
	profileCredentials directProfileCredentials
}

// SetProfileCredentialService installs the optional direct-profile credential
// service. Leaving it nil preserves legacy account authentication unchanged.
func (s *Service) SetProfileCredentialService(credentials *ProfileCredentialService) {
	if credentials == nil {
		// Guard the typed-nil trap: assigning a nil *ProfileCredentialService
		// straight into the interface would leave every nil check false.
		s.profileCredentials = nil
		return
	}
	s.profileCredentials = credentials
}

// LoginProfile exchanges a direct profile credential for a profile-bound
// session. It does not use or alter the legacy account-login path.
func (s *Service) LoginProfile(ctx context.Context, email, password string, device DeviceClaim) (*TokenPair, SessionSubject, error) {
	if s.profileCredentials == nil {
		return nil, SessionSubject{}, ErrInvalidCredentials
	}
	// The device binding is enforced on every later request, so a session may
	// not be minted without one. The HTTP handler checks this too, but any
	// compatibility adapter calling this service directly must hit the same
	// wall.
	device.ID = strings.TrimSpace(device.ID)
	if device.ID == "" {
		return nil, SessionSubject{}, ErrDeviceRequired
	}
	subject, err := s.profileCredentials.Authenticate(ctx, email, password, device)
	if err != nil {
		return nil, SessionSubject{}, err
	}
	sessionID := uuid.New().String()
	profileID := subject.ProfileID
	credentialRevision := subject.CredentialRevision
	session := models.AuthSession{
		ID:                        sessionID,
		UserID:                    subject.AccountID,
		DeviceName:                device.Name,
		DeviceID:                  device.ID,
		IPAddress:                 device.IPAddress,
		ExpiresAt:                 time.Now().Add(s.jwt.RefreshExpiry()),
		ProfileID:                 &profileID,
		ProfileCredentialRevision: &credentialRevision,
		AuthMethod:                AuthMethodDirectProfile,
	}
	if err := s.sessions.CreateProfileSessionIfCurrent(ctx, session, subject); err != nil {
		return nil, SessionSubject{}, fmt.Errorf("creating direct profile session: %w", err)
	}
	pair, err := s.generateTokenPair(Claims{
		UserID:             subject.AccountID,
		Role:               legacyRoleUser,
		SessionID:          sessionID,
		ProfileID:          subject.ProfileID,
		OrganizationID:     subject.OrganizationID,
		MembershipID:       subject.MembershipID,
		PolicyRevision:     subject.PolicyRevision,
		SecurityRevision:   subject.SecurityRevision,
		AuthMethod:         AuthMethodDirectProfile,
		DeviceID:           device.ID,
		CredentialRevision: subject.CredentialRevision,
	})
	if err != nil {
		return nil, SessionSubject{}, err
	}
	return pair, subject, nil
}

// SetOwnershipBootstrapper installs the protected ownership activation used by
// initial setup. A nil bootstrapper is retained for isolated compatibility
// fixtures that do not provide tenant state.
func (s *Service) SetOwnershipBootstrapper(bootstrapper OwnershipBootstrapper) {
	s.ownership = bootstrapper
}

// SetMembershipProvisioner installs default-organization provisioning for all
// accounts created through this service and its registered plugin providers.
func (s *Service) SetMembershipProvisioner(provisioner MembershipProvisioner) {
	s.memberships = provisioner
	s.accounts.SetMembershipProvisioner(provisioner)
	for _, provider := range s.providers {
		if pluginProvider, ok := provider.(*PluginProvider); ok {
			pluginProvider.SetMembershipProvisioner(provisioner)
		}
	}
}

type LoginProviderInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Mode        string `json:"mode"`
	Default     bool   `json:"default"`
	// IconURL is rendered next to the "Sign in with X" button. Set for
	// auth_provider.v1 plugins that ship an icon (icon_url manifest field).
	IconURL string `json:"icon_url,omitempty"`
	// InstallationID is non-zero when the provider is backed by a plugin.
	// The login UI uses it to build /api/v1/auth/oauth/{install_id}/init URLs.
	InstallationID int `json:"installation_id,omitempty"`
}

type RegisteredProvider struct {
	Info     LoginProviderInfo
	Provider AuthProvider
}

// NewService creates a new auth Service with the given dependencies.
func NewService(
	provider AuthProvider,
	jwt *JWTService,
	sessions *SessionRepository,
	users *UserRepository,
	inviteCodes *InviteCodeRepository,
	settings SettingsGetter,
	storeProvider userstore.UserStoreProvider,
) *Service {
	service := &Service{
		provider:    provider,
		jwt:         jwt,
		sessions:    sessions,
		users:       users,
		inviteCodes: inviteCodes,
		settings:    settings,
		providers:   map[string]AuthProvider{},
		metadata:    map[string]LoginProviderInfo{},
		accounts:    NewAccountProvisioner(users, storeProvider),
	}
	if provider != nil {
		service.RegisterProvider(LoginProviderInfo{
			ID:          "local",
			DisplayName: "Local",
			Mode:        "credentials",
			Default:     true,
		}, provider)
	}
	return service
}

// Login authenticates the user with the given credentials and creates a new
// session. Returns a TokenPair containing the access and refresh tokens.
func (s *Service) Login(ctx context.Context, username, password, deviceName, ip string) (*TokenPair, *models.User, error) {
	return s.loginWithProvider(ctx, "local", username, password, deviceName, ip)
}

func (s *Service) LoginWithProvider(
	ctx context.Context,
	providerID string,
	username string,
	password string,
	deviceName string,
	ip string,
) (*TokenPair, *models.User, error) {
	if providerID == "" {
		providerID = s.defaultID
	}
	return s.loginWithProvider(ctx, providerID, username, password, deviceName, ip)
}

func (s *Service) RegisterProvider(info LoginProviderInfo, provider AuthProvider) {
	if provider == nil || info.ID == "" {
		return
	}
	if info.DisplayName == "" {
		info.DisplayName = info.ID
	}
	if info.Mode == "" {
		info.Mode = "credentials"
	}

	s.providers[info.ID] = provider
	s.metadata[info.ID] = info
	if pluginProvider, ok := provider.(*PluginProvider); ok && s.memberships != nil {
		pluginProvider.SetMembershipProvisioner(s.memberships)
	}
	if s.defaultID == "" || info.Default {
		s.defaultID = info.ID
	}
}

// FindOAuthInstallation returns the PluginProvider registered for the given
// plugin installation, if it is an OAuth-capable provider. nil if no match.
func (s *Service) FindOAuthInstallation(installationID int) *PluginProvider {
	if installationID <= 0 {
		return nil
	}
	for _, p := range s.providers {
		pp, ok := p.(*PluginProvider)
		if !ok || pp == nil {
			continue
		}
		if pp.InstallationID() != installationID {
			continue
		}
		// Only OAuth-capable installs participate in /oauth/... routes. The
		// caller (OAuthHandler.ResolveClient) checks Mode metadata; here we
		// simply scope to PluginProvider instances bound to this install.
		return pp
	}
	return nil
}

// CompleteOAuthLogin runs the post-ExchangeCode half of login: the handler
// has already called the plugin's ExchangeCode RPC and is passing the
// AuthenticateResponse back. Service finds the matching PluginProvider,
// looks up or auto-provisions the user, creates a session, and mints
// access/refresh tokens.
func (s *Service) CompleteOAuthLogin(ctx context.Context, in OAuthLoginInput) (*TokenPair, *models.User, error) {
	provider := s.FindOAuthInstallation(in.InstallationID)
	if provider == nil {
		return nil, nil, ErrInvalidCredentials
	}
	user, err := provider.CompleteOAuth(ctx, in.Response)
	if err != nil {
		return nil, nil, err
	}
	// Linking flow (sess.LinkingUserID > 0): we already provisioned/identified
	// `user` via the plugin identity. If the caller asked to link onto a
	// different existing user, future work will need to:
	//   - reject if the identity is already linked elsewhere (409)
	//   - otherwise upsert plugin_auth_identities to point at LinkingUserID
	// For v1 the OAuth handler always passes 0; the v1 PR doesn't add the
	// /me/account "Link account" SPA UI. Leaving as a TODO.
	_ = in.LinkingUserID

	sessionID := uuid.New().String()
	session := models.AuthSession{
		ID:         sessionID,
		UserID:     user.ID,
		DeviceName: in.DeviceName,
		IPAddress:  in.IP,
		ExpiresAt:  time.Now().Add(s.jwt.RefreshExpiry()),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, nil, fmt.Errorf("creating session: %w", err)
	}
	pair, err := s.generateTokenPair(Claims{
		UserID:    user.ID,
		Role:      user.Role,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, nil, err
	}
	return pair, user, nil
}

func (s *Service) ListProviders() []LoginProviderInfo {
	providers := make([]LoginProviderInfo, 0, len(s.metadata))
	for _, info := range s.metadata {
		info.Default = info.ID == s.defaultID
		providers = append(providers, info)
	}
	sort.Slice(providers, func(i, j int) bool {
		if providers[i].Default != providers[j].Default {
			return providers[i].Default
		}
		return providers[i].DisplayName < providers[j].DisplayName
	})
	return providers
}

func (s *Service) loginWithProvider(
	ctx context.Context,
	providerID string,
	username string,
	password string,
	deviceName string,
	ip string,
) (*TokenPair, *models.User, error) {
	provider := s.providers[providerID]
	if provider == nil {
		return nil, nil, ErrInvalidCredentials
	}

	user, err := provider.Authenticate(ctx, Credentials{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, nil, err
	}

	// Create a new session with a pre-generated ID to avoid the race condition
	// of looking up the session after creation.
	sessionID := uuid.New().String()
	session := models.AuthSession{
		ID:         sessionID,
		UserID:     user.ID,
		DeviceName: deviceName,
		IPAddress:  ip,
		ExpiresAt:  time.Now().Add(s.jwt.RefreshExpiry()),
	}

	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, nil, fmt.Errorf("creating session: %w", err)
	}

	pair, err := s.generateTokenPair(Claims{
		UserID:    user.ID,
		Role:      user.Role,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, nil, err
	}

	return pair, user, nil
}

// NeedsSetup reports whether the system still needs its initial user account.
// The rule lives in needsSetup so the standalone SetupState reporter answers
// identically.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	return needsSetup(ctx, s.users)
}

// SetupInitialUser creates the first admin account and signs it in.
func (s *Service) SetupInitialUser(
	ctx context.Context,
	username, email, password string,
	createDefaultProfile bool,
	defaultProfileName string,
	deviceName, ip string,
) (*TokenPair, *models.User, error) {
	needsSetup, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !needsSetup {
		return nil, nil, ErrSetupAlreadyComplete
	}

	createdUser, err := s.accounts.CreateAccount(ctx, CreateAccountInput{
		User: models.CreateUserInput{
			Username: username,
			Email:    email,
			Password: password,
			Role:     "admin",
		},
		DefaultProfile: DefaultProfileOptions{
			Enabled: createDefaultProfile,
			Name:    defaultProfileName,
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("creating initial user: %w", err)
	}

	if s.ownership != nil {
		if err := s.ownership.ActivateInitialOwnership(ctx, createdUser.ID); err != nil {
			if deleteErr := s.accounts.users.Delete(ctx, createdUser.ID); deleteErr != nil {
				return nil, nil, fmt.Errorf("activating initial ownership: %w (cleanup user: %w)", err, deleteErr)
			}
			return nil, nil, fmt.Errorf("activating initial ownership: %w", err)
		}
	}

	// Reuse the standard login flow so setup creates a normal session pair.
	return s.Login(ctx, username, password, deviceName, ip)
}

// Signup creates a new user account using an invite code. Requires that
// public signups are enabled via the "signup.enabled" server setting.
func (s *Service) Signup(
	ctx context.Context,
	username, email, password, code string,
	createDefaultProfile bool,
	defaultProfileName string,
	deviceName, ip string,
) (*TokenPair, *models.User, error) {
	// Check global signup toggle.
	if s.settings != nil {
		enabled, err := s.settings.Get(ctx, "signup.enabled")
		if err != nil {
			return nil, nil, fmt.Errorf("checking signup setting: %w", err)
		}
		if enabled != "true" {
			return nil, nil, ErrSignupDisabled
		}
	} else {
		return nil, nil, ErrSignupDisabled
	}

	// Redeem the invite code (atomic increment).
	if err := s.inviteCodes.RedeemCode(ctx, code); err != nil {
		return nil, nil, err
	}

	// Create the user with standard role and access to all libraries.
	if _, err := s.accounts.CreateAccount(ctx, CreateAccountInput{
		User: models.CreateUserInput{
			Username: username,
			Email:    email,
			Password: password,
			Role:     "user",
		},
		DefaultProfile: DefaultProfileOptions{
			Enabled: createDefaultProfile,
			Name:    defaultProfileName,
		},
	}); err != nil {
		return nil, nil, fmt.Errorf("creating user: %w", err)
	}

	// Log them in to create a session and return tokens.
	return s.Login(ctx, username, password, deviceName, ip)
}

// IsSignupEnabled reports whether public signups are enabled.
func (s *Service) IsSignupEnabled(ctx context.Context) (bool, error) {
	if s.settings == nil {
		return false, nil
	}
	enabled, err := s.settings.Get(ctx, "signup.enabled")
	if err != nil {
		return false, fmt.Errorf("checking signup setting: %w", err)
	}
	return enabled == "true", nil
}

// Logout revokes the session identified by sessionID.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.Revoke(ctx, sessionID)
}

// StartImpersonation creates a new target-user session with admin provenance.
func (s *Service) StartImpersonation(ctx context.Context, adminUserID, targetUserID int, deviceName, ip string) (*TokenPair, *models.User, *models.User, error) {
	if claims := ClaimsFromContext(ctx); claims != nil {
		if claims.TokenType == TokenTypeAPIKey || claims.SessionID == "" {
			return nil, nil, nil, ErrImpersonationNotAllowed
		}
		currentSession, err := s.sessions.GetByID(ctx, claims.SessionID)
		if err != nil {
			if !IsSessionNotFound(err) {
				return nil, nil, nil, fmt.Errorf("getting current session: %w", err)
			}
		} else if currentSession.ImpersonatorUserID != nil {
			return nil, nil, nil, ErrAlreadyImpersonating
		}
	}

	admin, err := s.users.GetByID(ctx, adminUserID)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil, nil, ErrImpersonationNotAllowed
		}
		return nil, nil, nil, fmt.Errorf("getting admin user: %w", err)
	}
	if admin.Role != "admin" || !admin.Enabled {
		return nil, nil, nil, ErrImpersonationNotAllowed
	}
	if adminUserID == targetUserID {
		return nil, nil, nil, ErrImpersonationNotAllowed
	}

	target, err := s.users.GetByID(ctx, targetUserID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting target user: %w", err)
	}
	if !target.Enabled || target.Role == "admin" {
		return nil, nil, nil, ErrImpersonationNotAllowed
	}

	sessionID := uuid.New().String()
	impersonatorUserID := admin.ID
	startedAt := time.Now()
	session := models.AuthSession{
		ID:                     sessionID,
		UserID:                 target.ID,
		DeviceName:             deviceName,
		IPAddress:              ip,
		ExpiresAt:              startedAt.Add(s.jwt.RefreshExpiry()),
		ImpersonatorUserID:     &impersonatorUserID,
		ImpersonationStartedAt: &startedAt,
	}

	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, nil, nil, fmt.Errorf("creating session: %w", err)
	}

	pair, err := s.generateTokenPair(Claims{
		UserID:             target.ID,
		Role:               target.Role,
		SessionID:          sessionID,
		ImpersonatorUserID: &impersonatorUserID,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	return pair, admin, target, nil
}

// EndImpersonation revokes an impersonated session without affecting the original admin session.
func (s *Service) EndImpersonation(ctx context.Context, sessionID string, impersonatorUserID int) error {
	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.ImpersonatorUserID == nil {
		return ErrNotImpersonating
	}
	if *session.ImpersonatorUserID != impersonatorUserID {
		return ErrImpersonationNotAllowed
	}

	return s.sessions.Revoke(ctx, sessionID)
}

// Refresh validates the refresh token, checks that the associated session is
// still valid, and issues a new token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.jwt.ValidateToken(refreshToken)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh token: %w", err)
	}
	if claims.TokenType != TokenTypeRefresh {
		return nil, fmt.Errorf("invalid refresh token: %w", ErrInvalidToken)
	}

	session, err := s.sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		if IsSessionNotFound(err) {
			return nil, ErrSessionRevoked
		}
		return nil, fmt.Errorf("getting session: %w", err)
	}
	if session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) {
		return nil, ErrSessionRevoked
	}
	if session.AuthMethod == AuthMethodDirectProfile {
		return s.refreshDirectProfile(ctx, claims, session)
	}

	user, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrSessionRevoked
		}
		return nil, fmt.Errorf("getting user: %w", err)
	}
	if !user.Enabled {
		return nil, ErrSessionRevoked
	}
	if err := s.validateImpersonator(ctx, session.ImpersonatorUserID); err != nil {
		return nil, err
	}

	// Slide the session window forward so an active client never hits the
	// hard expires_at set at login. A failure here is non-fatal: the refresh
	// still returns fresh tokens; the session just keeps its prior expiry.
	newExpiry := time.Now().Add(s.jwt.RefreshExpiry())
	if err := s.sessions.ExtendExpiresAt(ctx, session.ID, newExpiry); err != nil && !IsSessionNotFound(err) {
		return nil, fmt.Errorf("extending session: %w", err)
	}

	return s.generateTokenPair(Claims{
		UserID:             user.ID,
		Role:               user.Role,
		SessionID:          session.ID,
		ImpersonatorUserID: session.ImpersonatorUserID,
		ProfileID:          claims.ProfileID,
		OrganizationID:     claims.OrganizationID,
		MembershipID:       claims.MembershipID,
		PolicyRevision:     claims.PolicyRevision,
		SecurityRevision:   claims.SecurityRevision,
		AuthMethod:         claims.AuthMethod,
		DeviceID:           claims.DeviceID,
		CredentialRevision: claims.CredentialRevision,
	})
}

// refreshDirectProfile re-issues a direct-profile token pair. A direct token
// carries its own tenancy and credential facts, so refresh trusts none of
// them: the presented claims must match the persisted session binding, and the
// new pair is minted from the subject as the database currently has it. A
// session whose binding no longer holds is revoked rather than refreshed.
func (s *Service) refreshDirectProfile(
	ctx context.Context,
	claims *Claims,
	session *models.AuthSession,
) (*TokenPair, error) {
	if s.profileCredentials == nil {
		// A server that lost its credential service cannot revalidate the
		// subject, but that is a wiring fault rather than grounds to destroy
		// the session.
		return nil, fmt.Errorf("direct profile sessions are unavailable")
	}
	bindingHolds := claims.UserID == session.UserID &&
		claims.AuthMethod == AuthMethodDirectProfile &&
		session.ProfileID != nil &&
		session.ProfileCredentialRevision != nil &&
		claims.ProfileID == *session.ProfileID &&
		claims.DeviceID == session.DeviceID &&
		claims.CredentialRevision == *session.ProfileCredentialRevision
	if !bindingHolds {
		_ = s.sessions.Revoke(ctx, session.ID)
		return nil, ErrSessionRevoked
	}

	subject, err := s.profileCredentials.CurrentSessionSubject(
		ctx,
		session.UserID,
		*session.ProfileID,
		*session.ProfileCredentialRevision,
		DeviceClaim{ID: session.DeviceID, Name: session.DeviceName, IPAddress: session.IPAddress},
	)
	if err != nil {
		// Only a subject that is genuinely no longer valid ends the session.
		// A connection failure, cancellation, or timeout says nothing about
		// the binding and must not destroy a working session.
		if !errors.Is(err, ErrSessionRevoked) {
			return nil, fmt.Errorf("revalidating direct profile subject: %w", err)
		}
		_ = s.sessions.Revoke(ctx, session.ID)
		return nil, ErrSessionRevoked
	}

	// Slide the session window forward exactly as account refresh does, so an
	// active direct-profile client never hits the hard expiry set at login.
	newExpiry := time.Now().Add(s.jwt.RefreshExpiry())
	if err := s.sessions.ExtendExpiresAt(ctx, session.ID, newExpiry); err != nil && !IsSessionNotFound(err) {
		return nil, fmt.Errorf("extending session: %w", err)
	}

	return s.generateTokenPair(Claims{
		UserID:             subject.AccountID,
		Role:               legacyRoleUser,
		SessionID:          session.ID,
		ProfileID:          subject.ProfileID,
		DeviceID:           subject.Device.ID,
		OrganizationID:     subject.OrganizationID,
		MembershipID:       subject.MembershipID,
		PolicyRevision:     subject.PolicyRevision,
		SecurityRevision:   subject.SecurityRevision,
		AuthMethod:         AuthMethodDirectProfile,
		CredentialRevision: subject.CredentialRevision,
	})
}

func (s *Service) validateImpersonator(ctx context.Context, impersonatorUserID *int) error {
	if impersonatorUserID == nil {
		return nil
	}

	impersonator, err := s.users.GetByID(ctx, *impersonatorUserID)
	if err != nil {
		if IsNotFound(err) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("getting impersonator user: %w", err)
	}
	if !impersonator.Enabled || impersonator.Role != "admin" {
		return ErrSessionRevoked
	}
	return nil
}

// GetCurrentUser retrieves the user associated with the given JWT claims.
func (s *Service) GetCurrentUser(ctx context.Context, claims *Claims) (*models.User, error) {
	user, err := s.users.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	return user, nil
}

// GetSessions returns all sessions for the given user ID.
func (s *Service) GetSessions(ctx context.Context, userID int) ([]*models.AuthSession, error) {
	return s.sessions.ListByUser(ctx, userID)
}

// RevokeSession revokes a specific session. It verifies the session belongs
// to the given user before revoking.
func (s *Service) RevokeSession(ctx context.Context, sessionID string, userID int) error {
	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}

	if session.UserID != userID {
		return ErrSessionNotFound
	}

	return s.sessions.Revoke(ctx, sessionID)
}

// generateTokenPair creates a new access/refresh token pair for the given
// claims.
func (s *Service) generateTokenPair(claims Claims) (*TokenPair, error) {
	accessToken, err := s.jwt.generateAccessToken(claims)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	refreshToken, err := s.jwt.generateRefreshToken(claims)
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.jwt.AccessExpiry().Seconds()),
	}, nil
}
