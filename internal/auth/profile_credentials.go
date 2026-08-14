package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrIncompleteProfileCredentials = errors.New("profile login email and password must be set together")
	ErrCredentialEmailInUse         = errors.New("login email is already in use")
	ErrProfileCredentialNotFound    = errors.New("profile credential subject not found")
)

const AuthMethodDirectProfile = "direct_profile"

// statusActive is the organization and membership status a direct-profile
// subject must hold for its session to be valid.
const statusActive = "active"

// DeviceClaim identifies the device receiving a profile-bound session. The
// profile credential service preserves it on the subject; device authorization
// remains enforced by the existing request and policy layers.
type DeviceClaim struct {
	ID        string
	Name      string
	Platform  string
	IPAddress string
}

// SessionSubject is the verified identity used when minting a direct-profile
// session. It carries the tenant facts resolved at credential verification so
// callers do not need to infer an organization from a profile ID.
type SessionSubject struct {
	AccountID          int
	ProfileID          string
	OrganizationID     string
	MembershipID       string
	PolicyRevision     int64
	SecurityRevision   int64
	CredentialRevision int64
	Device             DeviceClaim
	AuthMethod         string
}

// ProfileCredentialService owns optional direct profile credentials. Account
// credentials continue to be managed by UserRepository and are registered by
// the database trigger installed with this service's migration.
type ProfileCredentialService struct {
	pool *pgxpool.Pool
}

func NewProfileCredentialService(pool *pgxpool.Pool) *ProfileCredentialService {
	if pool == nil {
		return nil
	}
	return &ProfileCredentialService{pool: pool}
}

// Set enables or replaces a profile's direct credential. Empty email and
// password together disable the credential; either value alone is invalid.
func (s *ProfileCredentialService) Set(ctx context.Context, accountID int, profileID, email, password string) (err error) {
	if s == nil || s.pool == nil {
		return fmt.Errorf("profile credential service is unavailable")
	}
	// Only trim here. Case folding belongs to the database, which owns the
	// registry key: Go and PostgreSQL do not agree on Unicode case for every
	// input, and two implementations of "the same email" is exactly the bug
	// that lets a registered address fail to authenticate.
	email = strings.TrimSpace(email)
	if (email == "") != (password == "") {
		return ErrIncompleteProfileCredentials
	}
	if email == "" {
		return s.Clear(ctx, accountID, profileID)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing profile password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile credential update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockProfileCredentialSubject(ctx, tx, accountID, profileID); err != nil {
		return err
	}
	// credential_revision is bumped and the profile's direct sessions are
	// revoked by database triggers, so a write that never reaches this service
	// rotates the credential exactly the same way.
	if _, err := tx.Exec(ctx, `
		UPDATE user_profiles
		SET login_email = $3,
			password_hash = $4,
			updated_at = now()
		WHERE user_id = $1 AND id = $2`, accountID, profileID, email, string(hash)); err != nil {
		return mapProfileCredentialWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile credential update: %w", err)
	}
	return nil
}

// Clear disables direct login for a profile and revokes its direct sessions.
func (s *ProfileCredentialService) Clear(ctx context.Context, accountID int, profileID string) (err error) {
	if s == nil || s.pool == nil {
		return fmt.Errorf("profile credential service is unavailable")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile credential clear: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockProfileCredentialSubject(ctx, tx, accountID, profileID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_profiles
		SET login_email = NULL,
			password_hash = NULL,
			updated_at = now()
		WHERE user_id = $1 AND id = $2`, accountID, profileID); err != nil {
		return mapProfileCredentialWriteError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile credential clear: %w", err)
	}
	return nil
}

// Authenticate verifies a direct profile email/password pair and returns only
// the profile-bound tenant subject. All failures intentionally use the same
// ErrInvalidCredentials result to avoid disclosing account or tenant state.
func (s *ProfileCredentialService) Authenticate(ctx context.Context, email, password string, device DeviceClaim) (SessionSubject, error) {
	if s == nil || s.pool == nil {
		return SessionSubject{}, fmt.Errorf("profile credential service is unavailable")
	}
	var (
		subject      SessionSubject
		passwordHash string
		enabled      bool
		orgStatus    string
		memberStatus string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT profiles.user_id,
		       profiles.id,
		       profiles.organization_id::text,
		       memberships.id::text,
		       organizations.policy_revision,
		       memberships.security_revision,
		       profiles.credential_revision,
		       profiles.password_hash,
		       users.enabled,
		       organizations.status,
		       memberships.status
		FROM login_email_registry registry
		JOIN user_profiles profiles
		  ON profiles.user_id = registry.profile_user_id
		 AND profiles.id = registry.profile_id
		JOIN users ON users.id = profiles.user_id
		JOIN organizations ON organizations.id = profiles.organization_id
		JOIN organization_memberships memberships
		  ON memberships.organization_id = profiles.organization_id
		 AND memberships.account_id = profiles.user_id
		WHERE registry.normalized_email = public.vondel_normalize_login_email($1)`, email).Scan(
		&subject.AccountID,
		&subject.ProfileID,
		&subject.OrganizationID,
		&subject.MembershipID,
		&subject.PolicyRevision,
		&subject.SecurityRevision,
		&subject.CredentialRevision,
		&passwordHash,
		&enabled,
		&orgStatus,
		&memberStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionSubject{}, ErrInvalidCredentials
		}
		return SessionSubject{}, fmt.Errorf("load profile login subject: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil ||
		!enabled || orgStatus != statusActive || memberStatus != statusActive {
		return SessionSubject{}, ErrInvalidCredentials
	}
	subject.Device = device
	subject.AuthMethod = AuthMethodDirectProfile
	return subject, nil
}

// CurrentSessionSubject re-resolves a direct-profile subject from the
// database, refusing it unless the credential revision the session was bound
// to is still current and the account, organization, and membership are all
// still active. Refresh uses this instead of trusting the presented token.
func (s *ProfileCredentialService) CurrentSessionSubject(
	ctx context.Context,
	accountID int,
	profileID string,
	revision int64,
	device DeviceClaim,
) (SessionSubject, error) {
	if s == nil || s.pool == nil {
		return SessionSubject{}, fmt.Errorf("profile credential service is unavailable")
	}
	var (
		subject      SessionSubject
		enabled      bool
		orgStatus    string
		memberStatus string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT profiles.user_id,
		       profiles.id,
		       profiles.organization_id::text,
		       memberships.id::text,
		       organizations.policy_revision,
		       memberships.security_revision,
		       profiles.credential_revision,
		       users.enabled,
		       organizations.status,
		       memberships.status
		FROM user_profiles profiles
		JOIN users ON users.id = profiles.user_id
		JOIN organizations ON organizations.id = profiles.organization_id
		JOIN organization_memberships memberships
		  ON memberships.organization_id = profiles.organization_id
		 AND memberships.account_id = profiles.user_id
		WHERE profiles.user_id = $1 AND profiles.id = $2`, accountID, profileID).Scan(
		&subject.AccountID,
		&subject.ProfileID,
		&subject.OrganizationID,
		&subject.MembershipID,
		&subject.PolicyRevision,
		&subject.SecurityRevision,
		&subject.CredentialRevision,
		&enabled,
		&orgStatus,
		&memberStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionSubject{}, ErrSessionRevoked
		}
		return SessionSubject{}, fmt.Errorf("load current profile subject: %w", err)
	}
	if subject.CredentialRevision != revision || !enabled || orgStatus != statusActive || memberStatus != statusActive {
		return SessionSubject{}, ErrSessionRevoked
	}
	subject.Device = device
	subject.AuthMethod = AuthMethodDirectProfile
	return subject, nil
}

func lockProfileCredentialSubject(ctx context.Context, tx pgx.Tx, accountID int, profileID string) error {
	var found bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_profiles WHERE user_id = $1 AND id = $2 FOR UPDATE
		)`, accountID, profileID).Scan(&found)
	if err != nil {
		return fmt.Errorf("lock profile credential subject: %w", err)
	}
	if !found {
		return ErrProfileCredentialNotFound
	}
	return nil
}

// mapProfileCredentialWriteError sanitizes credential write failures. The
// login email arrives in the request and must not travel back out through an
// error: a unique violation becomes a stable sentinel, and any other database
// error is reduced to its code and message so a driver detail carrying the
// submitted row cannot reach a caller or a log.
func mapProfileCredentialWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return ErrCredentialEmailInUse
		}
		return fmt.Errorf("profile credential write failed: %s (SQLSTATE %s)", pgErr.Message, pgErr.Code)
	}
	return fmt.Errorf("profile credential write failed: %w", err)
}
