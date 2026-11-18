package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auditmutation"
	"github.com/Silo-Server/silo-server/internal/models"
)

// bloemServiceDeps holds Bloem's tenancy collaborators on Service.
type bloemServiceDeps struct {
	ownership OwnershipBootstrapper
	// bloemUsers and bloemSessions are Silo's users and sessions repositories
	// as Bloem's own lines use them: behind interfaces, so Bloem's tests can
	// fake them. Silo's lines keep Silo's concrete fields.
	bloemUsers    serviceUserRepository
	bloemSessions serviceSessionRepository
}

// bloemUseRepositories gives Bloem's lines the repositories NewService was
// given. A nil repository stays a nil interface, not a typed nil.
func (s *Service) bloemUseRepositories(sessions *SessionRepository, users *UserRepository) {
	if users != nil {
		s.bloemUsers = users
	}
	if sessions != nil {
		s.bloemSessions = sessions
	}
}

// OwnershipBootstrapper activates the protected platform and organization
// ownership state for the first account created during setup.
type OwnershipBootstrapper interface {
	ActivateInitialOwnership(ctx context.Context, accountID int) error
}

type transactionalOwnershipBootstrapper interface {
	ActivateInitialOwnershipInTransaction(context.Context, pgx.Tx, int) error
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
	ReplacePasswordSigningOutOthers(ctx context.Context, id int, expectedHash, newPassword, keepSessionID string) error
}

type serviceSessionRepository interface {
	ActiveSession(context.Context, string, int) (SessionAuthority, bool, error)
	IsValid(context.Context, string) (bool, error)
	Begin(context.Context) (pgx.Tx, error)
	createWithQuerier(context.Context, sessionExecQuerier, models.AuthSession) error
	Create(ctx context.Context, session models.AuthSession) error
	GetByID(ctx context.Context, id string) (*models.AuthSession, error)
	ListByUser(ctx context.Context, userID int) ([]*models.AuthSession, error)
	Revoke(ctx context.Context, id string) error
	revokeChecked(ctx context.Context, id string, check func(*models.AuthSession) error,
		audit func(pgx.Tx, *models.AuthSession) (*auditmutation.Entry, error)) error
	ExtendExpiresAt(ctx context.Context, id string, expiresAt time.Time) error
}

// SetOwnershipBootstrapper installs the protected ownership activation used by
// initial setup. A nil bootstrapper is retained for isolated compatibility
// fixtures that do not provide tenant state.
func (s *Service) SetOwnershipBootstrapper(bootstrapper OwnershipBootstrapper) {
	s.ownership = bootstrapper
}

// bloemRequireTransactionalProfile refuses setup's default profile on a store
// that cannot join the setup transaction (the SQLite bridge), before the
// account is inserted or the store's files are opened, rather than taking
// Silo's separate-store fallback. SetupInitialUser calls it.
func (s *Service) bloemRequireTransactionalProfile(createDefaultProfile bool) error {
	if createDefaultProfile && !s.accounts.SupportsTransactionalProfiles() {
		return ErrTransactionalProfileUnavailable
	}
	return nil
}

// bloemActivateInitialOwnership makes the first account the owner of the
// default organization inside the setup transaction. SetupInitialUser calls it
// after Silo creates the account.
func (s *Service) bloemActivateInitialOwnership(ctx context.Context, tx pgx.Tx, userID int) error {
	ownership, ok := s.ownership.(transactionalOwnershipBootstrapper)
	if !ok {
		return errors.New("ownership bootstrapper does not support transactional setup")
	}
	if err := ownership.ActivateInitialOwnershipInTransaction(ctx, tx, userID); err != nil {
		return fmt.Errorf("activating initial ownership: %w", err)
	}
	return nil
}

// StartImpersonationInTransaction creates the impersonated login session on a
// caller-owned transaction so a lifecycle receipt and its token response are
// committed atomically with that session.
func (s *Service) StartImpersonationInTransaction(ctx context.Context, tx pgx.Tx, adminUserID, targetUserID int, deviceName, ip string) (*TokenPair, *models.User, *models.User, error) {
	sessions, ok := any(s.bloemSessions).(transactionalSessionRepository)
	if !ok {
		return nil, nil, nil, errors.New("session repository does not support caller-owned transactions")
	}
	users, ok := any(s.bloemUsers).(interface {
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
