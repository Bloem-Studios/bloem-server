package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// CreateProfileSessionIfCurrent serializes direct-profile session creation with
// credential rotation by locking the profile row and checking the exact subject
// facts authenticated before the session was minted.
func (r *SessionRepository) CreateProfileSessionIfCurrent(
	ctx context.Context,
	session models.AuthSession,
	subject SessionSubject,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin direct profile session: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locking is what closes the window between verifying a subject and
	// inserting the session it authorizes: this session asserts the account is
	// enabled and the organization and membership are active at the revisions
	// it carries, so none of that may change underneath it.
	//
	// The rows are taken one statement at a time, in the order organization,
	// account, membership, profile. That order is the one the tenancy writers
	// already use — administration locks a membership before the profile it
	// moves — and taking them in a single joined statement would acquire them
	// in scan order instead, which is profile-first and deadlocks against
	// exactly that writer.
	//
	// The tenancy rows are taken FOR SHARE rather than FOR UPDATE: this
	// session only asserts their state, so concurrent logins make no
	// conflicting claim and must not serialize behind one organization row.
	// The profile is taken FOR UPDATE because a credential reset contends for
	// it directly.
	var (
		organizationID   string
		policyRevision   int64
		organizationStat string
	)
	err = tx.QueryRow(ctx, `
		SELECT id::text, policy_revision, status
		FROM organizations WHERE id::text = $1
		FOR SHARE`, subject.OrganizationID).Scan(&organizationID, &policyRevision, &organizationStat)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("lock organization for direct profile session: %w", err)
	}

	var accountID int
	var accountIncarnationID string
	var enabled bool
	err = tx.QueryRow(ctx, `
		SELECT id, account_incarnation_id::text, enabled FROM users WHERE id = $1
		FOR SHARE`, subject.AccountID).Scan(&accountID, &accountIncarnationID, &enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("lock account for direct profile session: %w", err)
	}

	var (
		membershipID     string
		securityRevision int64
		membershipStat   string
	)
	err = tx.QueryRow(ctx, `
		SELECT id::text, security_revision, status
		FROM organization_memberships
		WHERE organization_id::text = $1 AND account_id = $2
		FOR SHARE`, subject.OrganizationID, subject.AccountID).
		Scan(&membershipID, &securityRevision, &membershipStat)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("lock membership for direct profile session: %w", err)
	}

	var profileID, profileOrganizationID string
	var credentialRevision int64
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id::text, credential_revision
		FROM user_profiles WHERE user_id = $1 AND id = $2
		FOR UPDATE`, subject.AccountID, subject.ProfileID).
		Scan(&profileID, &profileOrganizationID, &credentialRevision)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("lock profile for direct profile session: %w", err)
	}

	current := SessionSubject{
		AccountID:            accountID,
		AccountIncarnationID: accountIncarnationID,
		ProfileID:            profileID,
		OrganizationID:       profileOrganizationID,
		MembershipID:         membershipID,
		PolicyRevision:       policyRevision,
		SecurityRevision:     securityRevision,
		CredentialRevision:   credentialRevision,
		Device:               subject.Device,
		AuthMethod:           subject.AuthMethod,
	}
	if current != subject || !enabled ||
		organizationID != subject.OrganizationID ||
		organizationStat != statusActive || membershipStat != statusActive {
		return ErrSessionRevoked
	}

	if err := r.createWithQuerier(ctx, tx, session); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit direct profile session: %w", err)
	}
	return nil
}

// CreateInTransaction inserts a login session on a caller-owned transaction.
// Public lifecycle creates use it so the encrypted token response can never be
// committed separately from its account, membership and receipt.
func (r *SessionRepository) CreateInTransaction(ctx context.Context, tx pgx.Tx, session models.AuthSession) error {
	return r.createWithQuerier(ctx, tx, session)
}

// GetByIDInTransaction reads a login session on a caller-owned transaction.
func (r *SessionRepository) GetByIDInTransaction(ctx context.Context, tx pgx.Tx, id string) (*models.AuthSession, error) {
	return r.getByIDWithQuerier(ctx, tx, id)
}

func (r *SessionRepository) getByIDWithQuerier(ctx context.Context, querier userCreateQuerier, id string) (*models.AuthSession, error) {
	query := `SELECT ` + sessionColumns + ` FROM auth_sessions WHERE id = $1`
	return scanSession(querier.QueryRow(ctx, query, id))
}

// RevokeByUserAndSession revokes a session only when it belongs to userID.
// A missing session and a session owned by another user deliberately return
// the same sentinel so callers cannot accidentally perform an unscoped
// mutation after resolving an account from the request URL.
func (r *SessionRepository) RevokeByUserAndSession(ctx context.Context, userID int, sessionID string) error {
	return revokeByUserAndSessionWithQuerier(ctx, r.pool, userID, sessionID)
}

// RevokeByUserAndSessionInTransaction applies the same ownership-scoped
// revocation on a caller-owned transaction.
func (r *SessionRepository) RevokeByUserAndSessionInTransaction(ctx context.Context, tx pgx.Tx, userID int, sessionID string) error {
	return revokeByUserAndSessionWithQuerier(ctx, tx, userID, sessionID)
}

func revokeByUserAndSessionWithQuerier(ctx context.Context, querier sessionExecQuerier, userID int, sessionID string) error {
	query := `UPDATE auth_sessions SET revoked_at = NOW() WHERE user_id = $1 AND id = $2`
	tag, err := querier.Exec(ctx, query, userID, sessionID)
	if err != nil {
		return fmt.Errorf("revoking session for user %d: %w", userID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// RevokeAllByUserAndProfiles atomically revokes every active direct-profile
// session belonging to userID and one of the caller-validated profile IDs.
// Account sessions and sessions for other users or profiles are preserved.
func (r *SessionRepository) RevokeAllByUserAndProfiles(ctx context.Context, userID int, profileIDs []string) error {
	return revokeAllByUserAndProfilesWithQuerier(ctx, r.pool, userID, profileIDs)
}

// RevokeAllByUserAndProfilesInTransaction applies the same account/profile
// intersection on a caller-owned transaction.
func (r *SessionRepository) RevokeAllByUserAndProfilesInTransaction(ctx context.Context, tx pgx.Tx, userID int, profileIDs []string) error {
	return revokeAllByUserAndProfilesWithQuerier(ctx, tx, userID, profileIDs)
}

func revokeAllByUserAndProfilesWithQuerier(ctx context.Context, querier sessionExecQuerier, userID int, profileIDs []string) error {
	query := `UPDATE auth_sessions
		SET revoked_at = NOW()
		WHERE user_id = $1
		  AND profile_id = ANY($2::text[])
		  AND revoked_at IS NULL`
	if _, err := querier.Exec(ctx, query, userID, profileIDs); err != nil {
		return fmt.Errorf("revoking sessions for user %d and selected profiles: %w", userID, err)
	}
	return nil
}

// RevokeAllByUserInTransaction revokes account sessions in the same commit as
// the credential or membership state that invalidates them.
func (r *SessionRepository) RevokeAllByUserInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	return revokeAllByUserWithQuerier(ctx, tx, userID)
}

// RevokeAllByImpersonatorInTransaction participates in a caller-owned
// account lifecycle transaction.
func (r *SessionRepository) RevokeAllByImpersonatorInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	return revokeAllByImpersonatorWithQuerier(ctx, tx, userID)
}

func revokeAllByUserWithQuerier(ctx context.Context, querier sessionExecQuerier, userID int) error {
	query := `UPDATE auth_sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := querier.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("revoking sessions for user %d: %w", userID, err)
	}
	return nil
}

func revokeAllByImpersonatorWithQuerier(ctx context.Context, querier sessionExecQuerier, userID int) error {
	query := `UPDATE auth_sessions SET revoked_at = NOW() WHERE impersonator_user_id = $1 AND revoked_at IS NULL`
	if _, err := querier.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("revoking impersonation sessions for user %d: %w", userID, err)
	}
	return nil
}

// normalizeSessionAuthMethod fills and validates a new session's auth method
// before createWithQuerier inserts it.
func normalizeSessionAuthMethod(session *models.AuthSession) error {
	// auth_sessions_direct_profile_binding_check ties the three profile columns
	// together: an account session carries no profile, and a direct-profile
	// session carries both the profile and the credential revision it was
	// authorized at. Defaulting to "account" whenever the caller left the method
	// blank produced a row that names a profile while claiming to be an account
	// session, which the constraint rejects with a raw 23514.
	if session.AuthMethod == "" {
		if session.ProfileID != nil {
			session.AuthMethod = "direct_profile"
		} else {
			session.AuthMethod = "account"
		}
	}
	if session.AuthMethod == "direct_profile" {
		// The constraint also demands a device: a direct-profile session is bound
		// to the client that authorized it, so an anonymous one cannot be revoked
		// by device the way the scoped revoke paths expect.
		if session.ProfileID == nil || session.ProfileCredentialRevision == nil || strings.TrimSpace(session.DeviceID) == "" {
			return fmt.Errorf("creating session: a direct profile session requires a profile, its credential revision, and a device id")
		}
	}
	if session.AuthMethod == "account" && session.ProfileID != nil {
		return fmt.Errorf("creating session: an account session cannot name a profile")
	}
	return nil
}
