package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// ActiveSessionRole keeps older Bloem adapters on the upstream authority lookup.
func (r *SessionRepository) ActiveSessionRole(ctx context.Context, id string, userID int) (string, bool, error) {
	authority, active, err := r.ActiveSession(ctx, id, userID)
	return authority.Role, active, err
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

// RevokeAllByUserInTransaction revokes account sessions in the same commit as
// the credential or membership state that invalidates them. It takes the
// account's identities and withdraws the device sign-ins it approved first,
// in the order Silo's revocation and a plugin uninstall take them
// (BloemWithdrawDeviceSignIns); callers hold the account row.
func (r *SessionRepository) RevokeAllByUserInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	return revokeAllByUserWithQuerier(ctx, tx, userID)
}

// BloemRevokeAllByUser is RevokeAllByUserInTransaction in a transaction of
// its own that locks the account row first, for a route that signs one
// account out everywhere outside an account change. ErrNotFound is an
// account that does not exist.
func (r *SessionRepository) BloemRevokeAllByUser(ctx context.Context, userID int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning session revocation for user %d: %w", userID, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockUser(ctx, tx, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return fmt.Errorf("locking user %d: %w", userID, err)
	}
	if err := revokeAllByUserWithQuerier(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing session revocation for user %d: %w", userID, err)
	}
	return nil
}

// RevokeAllByImpersonatorInTransaction participates in a caller-owned
// account lifecycle transaction.
func (r *SessionRepository) RevokeAllByImpersonatorInTransaction(ctx context.Context, tx pgx.Tx, userID int) error {
	return revokeAllByImpersonatorWithQuerier(ctx, tx, userID)
}

func revokeAllByUserWithQuerier(ctx context.Context, tx pgx.Tx, userID int) error {
	if _, err := BloemWithdrawDeviceSignIns(ctx, tx, userID); err != nil {
		return err
	}
	query := `UPDATE auth_sessions SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`
	if _, err := tx.Exec(ctx, query, userID); err != nil {
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
// before createWithQuerier inserts it. Silo's insert leaves auth_method to its
// column default, 'account', so that is the only method a session created
// there may carry.
func normalizeSessionAuthMethod(session *models.AuthSession) error {
	if session.AuthMethod == AuthMethodPreviewGuest {
		return fmt.Errorf("creating session: a preview guest session is opened only by a ticket exchange")
	}
	if session.AuthMethod == "" {
		session.AuthMethod = "account"
	}
	if session.AuthMethod != "account" {
		return fmt.Errorf("creating session: auth method %q is not an account session", session.AuthMethod)
	}
	return nil
}

// Begin supplies the transaction boundary used by upstream OAuth completion.
func (r *SessionRepository) Begin(ctx context.Context) (pgx.Tx, error) { return r.pool.Begin(ctx) }

// ListByUser returns all sessions for a given user, ordered by created_at
// descending (newest first). Silo deleted it with its v1 API (Silo #3200);
// Bloem's platform account routes still list every sign-in session with it.
func (r *SessionRepository) ListByUser(ctx context.Context, userID int) ([]*models.AuthSession, error) {
	query := `SELECT ` + sessionColumns + ` FROM auth_sessions WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("listing sessions for user %d: %w", userID, err)
	}
	defer rows.Close()

	return scanSessions(rows)
}
