package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// bloemServiceDeps holds Bloem's tenancy collaborators on Service.
type bloemServiceDeps struct {
	ownership          OwnershipBootstrapper
	profileCredentials directProfileCredentials
}

var (
	// ErrDeviceRequired refuses a direct profile login that names no device:
	// the session binds to exactly one, and an empty binding would make every
	// later device check vacuous.
	ErrDeviceRequired = errors.New("a device id is required for direct profile login")
)

// OwnershipBootstrapper activates the protected platform and organization
// ownership state for the first account created during setup.
type OwnershipBootstrapper interface {
	ActivateInitialOwnership(ctx context.Context, accountID int) error
}

type transactionalOwnershipBootstrapper interface {
	ActivateInitialOwnershipInTransaction(context.Context, pgx.Tx, int) error
}

type transactionalSetupUserRepository interface {
	ClaimInitialSetupInTransaction(context.Context, pgx.Tx) error
}

type transactionalSessionRepository interface {
	CreateInTransaction(context.Context, pgx.Tx, models.AuthSession) error
	GetByIDInTransaction(context.Context, pgx.Tx, string) (*models.AuthSession, error)
}

type serviceUserRepository interface {
	Begin(context.Context) (pgx.Tx, error)
	LoginDirectory
	LocalPasswordLoginAllowed(context.Context) (bool, error)
	AccountUserRepository
	Count(ctx context.Context) (int, error)
	GetByID(ctx context.Context, id int) (*models.User, error)
	CompareAndSwapPassword(ctx context.Context, id int, expectedHash, newPassword string) error
	ReplaceTemporaryPassword(ctx context.Context, id int, expectedHash, newPassword, keepSessionID string) error
}

type serviceSessionRepository interface {
	ActiveSession(context.Context, string, int) (SessionAuthority, bool, error)
	IsValid(context.Context, string) (bool, error)
	Begin(context.Context) (pgx.Tx, error)
	createWithQuerier(context.Context, sessionExecQuerier, models.AuthSession) error
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
		UserID:               subject.AccountID,
		AccountIncarnationID: subject.AccountIncarnationID,
		Role:                 legacyRoleUser,
		SessionID:            sessionID,
		ProfileID:            subject.ProfileID,
		OrganizationID:       subject.OrganizationID,
		MembershipID:         subject.MembershipID,
		PolicyRevision:       subject.PolicyRevision,
		SecurityRevision:     subject.SecurityRevision,
		AuthMethod:           AuthMethodDirectProfile,
		DeviceID:             device.ID,
		CredentialRevision:   subject.CredentialRevision,
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

// SetupInitialUserInTransaction creates the initial account (the server owner),
// its optional profile, ownership state and login session in the caller's
// transaction.
func (s *Service) SetupInitialUserInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	username, email, password string,
	createDefaultProfile bool,
	defaultProfileName string,
	deviceName, ip string,
) (*TokenPair, *models.User, error) {
	users, ok := s.users.(transactionalSetupUserRepository)
	if !ok {
		return nil, nil, errors.New("account repository does not support transactional setup")
	}
	if err := users.ClaimInitialSetupInTransaction(ctx, tx); err != nil {
		return nil, nil, err
	}
	input := CreateAccountInput{
		User: models.CreateUserInput{
			Username: username,
			Email:    email,
			Password: password,
			Role:     legacyRoleAdmin,
		},
		DefaultProfile: DefaultProfileOptions{Enabled: createDefaultProfile, Name: defaultProfileName},
	}
	user, conflict, err := s.accounts.CreateUserInTransaction(ctx, tx, input.User)
	if err == nil && conflict {
		err = ErrDuplicate
	}
	if err != nil {
		return nil, nil, fmt.Errorf("creating initial user: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET is_owner = true, break_glass = true WHERE id = $1`, user.ID); err != nil {
		return nil, nil, fmt.Errorf("claiming initial owner: %w", err)
	}
	user.IsOwner, user.BreakGlass = true, true
	if err := s.accounts.createProfileInTransactionOrBridge(ctx, tx, user.ID, input); err != nil {
		return nil, nil, err
	}
	ownership, ok := s.ownership.(transactionalOwnershipBootstrapper)
	if !ok {
		return nil, nil, errors.New("ownership bootstrapper does not support transactional setup")
	}
	if err := ownership.ActivateInitialOwnershipInTransaction(ctx, tx, user.ID); err != nil {
		return nil, nil, fmt.Errorf("activating initial ownership: %w", err)
	}
	sessions, ok := s.sessions.(transactionalSessionRepository)
	if !ok {
		return nil, nil, errors.New("session repository does not support transactional setup")
	}
	sessionID := uuid.NewString()
	if err := sessions.CreateInTransaction(ctx, tx, models.AuthSession{
		ID: sessionID, UserID: user.ID, DeviceName: deviceName, IPAddress: ip,
		ExpiresAt: time.Now().Add(s.jwt.RefreshExpiry()),
	}); err != nil {
		return nil, nil, fmt.Errorf("creating session: %w", err)
	}
	pair, err := s.generateTokenPair(Claims{
		UserID:               user.ID,
		AccountIncarnationID: user.AccountIncarnationID.String(),
		Role:                 user.Role,
		SessionID:            sessionID,
	})
	if err != nil {
		return nil, nil, err
	}
	return pair, user, nil
}

// StartImpersonationInTransaction creates the impersonated login session on a
// caller-owned transaction so a lifecycle receipt and its token response are
// committed atomically with that session.
func (s *Service) StartImpersonationInTransaction(ctx context.Context, tx pgx.Tx, adminUserID, targetUserID int, deviceName, ip string) (*TokenPair, *models.User, *models.User, error) {
	sessions, ok := any(s.sessions).(transactionalSessionRepository)
	if !ok {
		return nil, nil, nil, errors.New("session repository does not support caller-owned transactions")
	}
	users, ok := any(s.users).(interface {
		GetByIDInTransaction(context.Context, pgx.Tx, int) (*models.User, error)
	})
	if !ok {
		return nil, nil, nil, errors.New("account repository does not support caller-owned transactions")
	}
	if claims := ClaimsFromContext(ctx); claims != nil {
		if claims.TokenType == TokenTypeAPIKey || claims.SessionID == "" {
			return nil, nil, nil, ErrImpersonationNotAllowed
		}
		currentSession, err := sessions.GetByIDInTransaction(ctx, tx, claims.SessionID)
		if err != nil {
			if !IsSessionNotFound(err) {
				return nil, nil, nil, fmt.Errorf("getting current session: %w", err)
			}
		} else if currentSession.ImpersonatorUserID != nil {
			return nil, nil, nil, ErrAlreadyImpersonating
		}
	}
	admin, err := users.GetByIDInTransaction(ctx, tx, adminUserID)
	if err != nil {
		if IsNotFound(err) {
			return nil, nil, nil, ErrImpersonationNotAllowed
		}
		return nil, nil, nil, fmt.Errorf("getting admin user: %w", err)
	}
	if admin.Role != "admin" || !admin.Enabled || adminUserID == targetUserID {
		return nil, nil, nil, ErrImpersonationNotAllowed
	}
	target, err := users.GetByIDInTransaction(ctx, tx, targetUserID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getting target user: %w", err)
	}
	if !target.Enabled || target.Role == "admin" {
		return nil, nil, nil, ErrImpersonationNotAllowed
	}
	sessionID := uuid.NewString()
	impersonatorUserID := admin.ID
	startedAt := time.Now()
	if err := sessions.CreateInTransaction(ctx, tx, models.AuthSession{
		ID: sessionID, UserID: target.ID, DeviceName: deviceName, IPAddress: ip,
		ExpiresAt: startedAt.Add(s.jwt.RefreshExpiry()), ImpersonatorUserID: &impersonatorUserID,
		ImpersonationStartedAt: &startedAt,
	}); err != nil {
		return nil, nil, nil, fmt.Errorf("creating session: %w", err)
	}
	pair, err := s.generateTokenPair(Claims{
		UserID: target.ID, AccountIncarnationID: target.AccountIncarnationID.String(), Role: target.Role,
		SessionID: sessionID, ImpersonatorUserID: &impersonatorUserID,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return pair, admin, target, nil
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
		return nil, sessionCheckUnavailable("revalidating direct profile subject", errors.New("profile credential service is unavailable"))
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
			return nil, sessionCheckUnavailable("revalidating direct profile subject", err)
		}
		_ = s.sessions.Revoke(ctx, session.ID)
		return nil, ErrSessionRevoked
	}

	// Slide the session window forward exactly as account refresh does, so an
	// active direct-profile client never hits the hard expiry set at login.
	newExpiry := time.Now().Add(s.jwt.RefreshExpiry())
	if err := s.sessions.ExtendExpiresAt(ctx, session.ID, newExpiry); err != nil && !IsSessionNotFound(err) {
		return nil, sessionCheckUnavailable("extending session", err)
	}

	return s.generateTokenPair(Claims{
		UserID:               subject.AccountID,
		AccountIncarnationID: subject.AccountIncarnationID,
		Role:                 legacyRoleUser,
		SessionID:            session.ID,
		ProfileID:            subject.ProfileID,
		DeviceID:             subject.Device.ID,
		OrganizationID:       subject.OrganizationID,
		MembershipID:         subject.MembershipID,
		PolicyRevision:       subject.PolicyRevision,
		SecurityRevision:     subject.SecurityRevision,
		AuthMethod:           AuthMethodDirectProfile,
		CredentialRevision:   subject.CredentialRevision,
	})
}
