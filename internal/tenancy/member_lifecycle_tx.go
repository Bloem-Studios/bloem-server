package tenancy

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/models"
)

// UpdateInTransaction applies an already-resolved tenant-member identity
// mutation on a caller-owned transaction. The caller must lock the tenant,
// membership, and account before invoking it.
func (s *MemberService) UpdateInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userID int, input UpdateMemberInput) (models.User, error) {
	if s == nil || tx == nil || s.accounts == nil || tenantID == uuid.Nil || userID <= 0 {
		return models.User{}, ErrMemberNotFound
	}
	update, err := normalizeMemberUpdate(input)
	if err != nil {
		return models.User{}, err
	}
	conflict, err := s.accounts.UpdateUserInTransaction(ctx, tx, userID, update)
	if conflict {
		return models.User{}, ErrUsernameConflict
	}
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: update member: %w", err)
	}
	if s.sessions != nil {
		if err := s.sessions.RevokeAllByUserInTransaction(ctx, tx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke changed member sessions: %w", err)
		}
	}
	return loadLifecycleMember(ctx, tx, tenantID, userID)
}

// SetSuspendedInTransaction changes the account and membership state in the
// caller's transaction. Compatibility-session invalidation remains a
// post-commit effect and is exposed separately below.
func (s *MemberService) SetSuspendedInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userID int, suspended bool) (models.User, error) {
	if s == nil || tx == nil || s.accounts == nil || tenantID == uuid.Nil || userID <= 0 {
		return models.User{}, ErrMemberNotFound
	}
	enabled := !suspended
	if _, err := s.accounts.UpdateUserInTransaction(ctx, tx, userID, models.UpdateUserInput{Enabled: &enabled}); err != nil {
		if isMembershipPolicyFence(err) {
			return models.User{}, ErrMembershipPolicyWriteUnavailable
		}
		return models.User{}, fmt.Errorf("tenancy: update member enabled state: %w", err)
	}
	status := MembershipActive
	if suspended {
		status = MembershipSuspended
	}
	tag, err := tx.Exec(ctx, `
UPDATE organization_memberships
SET status=$3,security_revision=security_revision+1,updated_at=now()
WHERE organization_id=$1 AND account_id=$2`, tenantID, userID, status)
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: update member status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return models.User{}, ErrMemberNotFound
	}
	if suspended && s.sessions != nil {
		if err := s.sessions.RevokeAllByUserInTransaction(ctx, tx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke suspended member sessions: %w", err)
		}
	}
	return loadLifecycleMember(ctx, tx, tenantID, userID)
}

func isMembershipPolicyFence(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "P0001" &&
		(pgErr.Message == "membership_policy_fenced" || pgErr.Message == "membership_policy_not_finalized")
}

// ResetPasswordInTransaction replaces the password and revokes native login
// sessions atomically with the caller's transaction.
func (s *MemberService) ResetPasswordInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userID int, password string) (models.User, error) {
	if s == nil || tx == nil || s.accounts == nil || tenantID == uuid.Nil || userID <= 0 {
		return models.User{}, ErrMemberNotFound
	}
	if len(password) < 8 || len(password) > 72 {
		return models.User{}, ErrInvalidMemberCommand
	}
	if _, err := s.accounts.UpdateUserInTransaction(ctx, tx, userID, models.UpdateUserInput{Password: &password}); err != nil {
		return models.User{}, fmt.Errorf("tenancy: reset member password: %w", err)
	}
	if s.sessions != nil {
		if err := s.sessions.RevokeAllByUserInTransaction(ctx, tx, userID); err != nil {
			return models.User{}, fmt.Errorf("tenancy: revoke reset member sessions: %w", err)
		}
	}
	return loadLifecycleMember(ctx, tx, tenantID, userID)
}

// InvalidateCompatSessionsAfterCommit runs the volatile compatibility
// invalidation only after the durable transaction has committed.
func (s *MemberService) InvalidateCompatSessionsAfterCommit(ctx context.Context, userID int, operation string) error {
	return s.invalidateCompatSessionsAfterCommit(ctx, userID, operation)
}

func normalizeMemberUpdate(input UpdateMemberInput) (models.UpdateUserInput, error) {
	update := models.UpdateUserInput{}
	if input.Username != nil {
		username := strings.TrimSpace(*input.Username)
		if username == "" || len(username) > 255 || strings.IndexFunc(username, unicode.IsControl) >= 0 {
			return models.UpdateUserInput{}, ErrInvalidMemberCommand
		}
		update.Username = &username
	}
	if input.Email != nil {
		email := strings.TrimSpace(*input.Email)
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || !strings.Contains(email, "@") || len(email) > 320 {
			return models.UpdateUserInput{}, ErrInvalidMemberCommand
		}
		update.Email = &email
	}
	if update.Username == nil && update.Email == nil {
		return models.UpdateUserInput{}, ErrInvalidMemberCommand
	}
	return update, nil
}

func loadLifecycleMember(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, userID int) (models.User, error) {
	var user models.User
	err := tx.QueryRow(ctx, `
SELECT u.id,u.username,u.email,u.enabled
FROM users u
JOIN organization_memberships m ON m.account_id=u.id
WHERE m.organization_id=$1 AND u.id=$2`, tenantID, userID).Scan(&user.ID, &user.Username, &user.Email, &user.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.User{}, ErrMemberNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("tenancy: load lifecycle member: %w", err)
	}
	return user, nil
}
