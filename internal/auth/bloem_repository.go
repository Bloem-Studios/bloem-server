package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Account policy lives on users, as in Silo. A membership only links the
// account to its one organization, so Bloem adds three things to Silo's
// account path: a new account joins an organization, the default access group
// is that organization's, and the account's role reaches its membership.

type userCreateQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type userMutationQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// createAccount is Create in one transaction, so an account never commits
// without its membership.
func (r *UserRepository) createAccount(ctx context.Context, input models.CreateUserInput) (*models.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := createUser(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return user, nil
}

// CreateInTransaction applies the canonical user creation path on an existing
// transaction. Tenant member provisioning uses this so the account and its
// membership commit, or roll back, as one unit.
func (r *UserRepository) CreateInTransaction(ctx context.Context, tx pgx.Tx, input models.CreateUserInput) (*models.User, error) {
	return createUser(ctx, tx, input)
}

// linkAccountMembership gives a new account its membership: in the
// organization the creating request names, else the one owning the account's
// access group, else the default organization.
func linkAccountMembership(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, account *models.User) error {
	var id uuid.UUID
	err := db.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT COALESCE(
			$2::uuid,
			(SELECT g.organization_id FROM access_groups g WHERE g.id = $3),
			(SELECT id FROM organizations WHERE is_default)
		), $1, 'active', $4
		ON CONFLICT (account_id) DO NOTHING
		RETURNING id`,
		account.ID, accountCreationOrganization(ctx), account.AccessGroupID, membershipLegacyRole(account.Role),
	).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("creating account membership: %w", err)
	}
	return nil
}

// defaultAccessGroup is the default group of the organization a new account
// joins. Each organization has its own default group, so Silo's unscoped
// "the default group" names more than one.
func defaultAccessGroup(ctx context.Context, args *[]any) string {
	*args = append(*args, accountCreationOrganization(ctx))
	return fmt.Sprintf(`(SELECT g.id FROM access_groups g WHERE g.is_default
		AND g.organization_id = COALESCE($%d::uuid, (SELECT id FROM organizations WHERE is_default)))`, len(*args))
}

// accountDefaultAccessGroup is the default group of the updated account's own
// organization, for an account demoted from admin.
const accountDefaultAccessGroup = `(SELECT g.id FROM access_groups g
	JOIN organization_memberships m ON m.organization_id = g.organization_id
	WHERE g.is_default AND m.account_id = users.id)`

// syncMembershipRole carries an account's role to its membership, which keeps
// the two roles organization membership understands.
func syncMembershipRole(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, id int, input models.UpdateUserInput) error {
	if input.Role == nil {
		return nil
	}
	if _, err := db.Exec(ctx, `
		UPDATE organization_memberships SET legacy_role = $2, updated_at = now()
		WHERE account_id = $1 AND legacy_role IS DISTINCT FROM $2`, id, membershipLegacyRole(*input.Role)); err != nil {
		return fmt.Errorf("updating account membership role: %w", err)
	}
	return nil
}

// TenantUserInTransaction reads the signed-in account through the exact
// tenant membership bound to ctx, in a caller-owned transaction. It fails
// closed with pgx.ErrNoRows unless ctx carries that account's tenant and the
// organization and membership are still active at the bound revisions.
// Viewer-scoped callers (progress sync) use it; flows with no signed-in
// tenant, such as completing a password reset link, use Silo's
// UserInTransaction instead.
func TenantUserInTransaction(ctx context.Context, tx pgx.Tx, id int) (*models.User, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.AccountID != id || tenant.OrganizationID == uuid.Nil || tenant.MembershipID == uuid.Nil {
		return nil, pgx.ErrNoRows
	}
	return scanUser(tx.QueryRow(ctx, `SELECT `+allColumns+` FROM users WHERE id = $1 AND EXISTS (
		SELECT 1 FROM organization_memberships m JOIN organizations o ON o.id = m.organization_id
		WHERE m.account_id = users.id AND m.organization_id = $2 AND m.id = $3
		  AND o.status = 'active' AND m.status = 'active'
		  AND o.policy_revision = $4 AND m.security_revision = $5)`,
		id, tenant.OrganizationID, tenant.MembershipID, tenant.PolicyRevision, tenant.SecurityRevision))
}

// GetByIDInTransaction reads an account through a caller-owned transaction.
func (r *UserRepository) GetByIDInTransaction(ctx context.Context, tx pgx.Tx, id int) (*models.User, error) {
	return userByID(ctx, tx, id)
}

// UpdateInTransaction applies the canonical normalization, hashing and access
// revision behavior while participating in the caller's transaction.
// The caller already holds its transaction, so a new password is hashed inside
// it; Update and the v2 admin path hash before they begin.
func (r *UserRepository) UpdateInTransaction(ctx context.Context, tx pgx.Tx, id int, input models.UpdateUserInput) error {
	passwordHash, err := hashUpdatePassword(ctx, &input)
	if err != nil {
		return err
	}
	return updateUser(ctx, tx, id, input, passwordHash)
}

// DeleteInTransaction removes a user as part of a larger lifecycle change.
func (r *UserRepository) DeleteInTransaction(ctx context.Context, tx pgx.Tx, id int) error {
	tag, err := tx.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountInTransaction reads account cardinality on a caller-owned transaction.
func (r *UserRepository) CountInTransaction(ctx context.Context, tx pgx.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// membershipLegacyRole narrows an account role to the two values
// organization_memberships.legacy_role accepts. Roles beyond admin are ordinary
// members as far as tenant membership is concerned; the account keeps its full
// role on users.
func membershipLegacyRole(role string) string {
	if role == models.RoleAdmin {
		return models.RoleAdmin
	}
	return "user"
}

// Begin supplies the transaction boundary used by upstream local sign-in.
func (r *UserRepository) Begin(ctx context.Context) (pgx.Tx, error) { return r.pool.Begin(ctx) }
