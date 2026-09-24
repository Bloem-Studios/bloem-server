package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// userSource joins an account to the membership that represents it.
//
// models.User is account-shaped while policy is per-membership, so one row has
// to stand for the account. The default organization wins when the account
// belongs to it, which reproduces the pre-handoff behavior for every ordinary
// deployment; a tenant member that exists only inside its own organization
// projects that membership instead, which is the only one it has. Callers that
// need a specific organization's policy query organization_memberships directly
// and never come through here.
const userSource = ` FROM users u
	LEFT JOIN LATERAL (
		SELECT memberships.*
		FROM organization_memberships AS memberships
		JOIN organizations AS orgs ON orgs.id = memberships.organization_id
		WHERE memberships.account_id = u.id
		ORDER BY orgs.is_default DESC, memberships.created_at ASC, memberships.id ASC
		LIMIT 1
	) m ON TRUE`

type userCreateQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type userMutationQuerier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateInTransaction applies the canonical user creation path on an existing
// transaction. Tenant member provisioning uses this so the account and its
// quota-bearing membership commit, or roll back, as one unit.
func (r *UserRepository) CreateInTransaction(ctx context.Context, tx pgx.Tx, input models.CreateUserInput) (*models.User, error) {
	return r.createWithQuerier(ctx, tx, input)
}

// GetByIDInTransaction reads an account through a caller-owned transaction.
func (r *UserRepository) GetByIDInTransaction(ctx context.Context, tx pgx.Tx, id int) (*models.User, error) {
	query := `SELECT ` + allColumns + userSource + ` WHERE u.id = $1`
	return scanUser(tx.QueryRow(ctx, query, id))
}

// userUpdateColumn is one candidate column of a user update: it is written
// only when set, and bumpsAccessPolicy marks the columns whose change has to
// invalidate durable session/profile tokens by bumping
// access_policy_revision. Values are pre-computed, so every entry is safe to
// build even when set is false.
// membershipPolicyColumns are the columns 20260829085838_membership_policy_isolation
// moved off users, so an update naming one has to target the account's
// default-organization membership instead.
var membershipPolicyColumns = map[string]bool{
	"permissions": true, "library_ids": true, "max_playback_quality": true,
	"max_streams": true, "max_transcodes": true, "transcode_allowed": true,
	"audio_transcode_allowed": true, "max_profiles": true, "download_allowed": true,
	"download_transcode_allowed": true, "requests_allowed": true, "access_group_id": true,
}

// UpdateInTransaction applies the canonical normalization, hashing and access
// revision behavior while participating in the caller's transaction.
func (r *UserRepository) UpdateInTransaction(ctx context.Context, tx pgx.Tx, id int, input models.UpdateUserInput) error {
	return r.updateWithQuerier(ctx, tx, id, input)
}

// applyMembershipPolicyUpdate writes the policy half of an account update to the
// account's default-organization membership, which is where those columns moved.
func applyMembershipPolicyUpdate(ctx context.Context, querier userMutationQuerier, id int, set, predicates []string, args []any, alwaysBump bool, defaultGroupCTE string) error {
	if len(set) == 0 && !alwaysBump {
		return nil
	}
	switch {
	case alwaysBump:
		set = append(set, "access_policy_revision = access_policy_revision + 1")
	case len(predicates) > 0:
		set = append(set, fmt.Sprintf(
			"access_policy_revision = CASE WHEN %s THEN access_policy_revision + 1 ELSE access_policy_revision END",
			strings.Join(predicates, " OR "),
		))
	}
	set = append(set, "updated_at = NOW()")
	args = append(args, id)
	prefix := ""
	if defaultGroupCTE != "" {
		prefix = "WITH " + defaultGroupCTE + " "
	}
	// The v1 writer marker is transaction-local, and this querier is often a
	// pool rather than a transaction, so a separate SET LOCAL would not survive
	// to this statement. Evaluating set_config in the WHERE marks the same
	// implicit transaction that performs the update.
	statement := fmt.Sprintf(
		`%sUPDATE organization_memberships SET %s
		 WHERE id = (
			SELECT memberships.id
			FROM organization_memberships AS memberships
			JOIN organizations AS orgs ON orgs.id = memberships.organization_id
			WHERE memberships.account_id = $%d
			ORDER BY orgs.is_default DESC, memberships.created_at ASC, memberships.id ASC
			LIMIT 1
		   )
		   AND set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL`,
		prefix, strings.Join(set, ", "), len(args),
	)
	if _, err := querier.Exec(ctx, statement, args...); err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("%w: %s", ErrDuplicate, extractConstraint(err))
		}
		return fmt.Errorf("updating account membership policy: %w", err)
	}
	return nil
}

// DeleteInTransaction removes a user as part of a larger lifecycle change.
func (r *UserRepository) DeleteInTransaction(ctx context.Context, tx pgx.Tx, id int) error {
	return r.deleteWithQuerier(ctx, tx, id)
}

// CountInTransaction reads account cardinality on a caller-owned transaction.
func (r *UserRepository) CountInTransaction(ctx context.Context, tx pgx.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return count, nil
}

// insertDefaultMembershipPolicy places a new account's policy on its membership
// in the default organization, which is where the authority moved.
// seed_legacy_membership_policy requires the v1 writer marker once the authority
// is finalized, and the marker is transaction-local, so it is set on the same
// querier immediately before the insert.
func insertDefaultMembershipPolicy(ctx context.Context, querier userCreateQuerier, accountID int, legacyRole string, explicitGroupID *int64, cols []string, args []any, defaultGroupExpr string) error {
	// The membership belongs to the organization that owns the account's group,
	// not necessarily the default one: tenancy creates member accounts against a
	// tenant organization and hands us that organization's group, and
	// organization_memberships_organization_access_group_fkey ties the pair
	// together. Fall back to the default organization only when no group was
	// supplied. Organization-specific provisioning overrides that legacy
	// selection; the membership/group foreign key still rejects a foreign group.
	organizationExpr := `(SELECT COALESCE(
		$4::uuid,
		(SELECT g.organization_id FROM access_groups g WHERE g.id = $3),
		(SELECT id FROM organizations WHERE is_default)
	) WHERE set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL)`
	columns := append([]string{"organization_id", "account_id", "status", "legacy_role"}, cols...)
	values := []string{organizationExpr, "$1", "'active'", "$2"}
	insertArgs := []any{accountID, legacyRole, explicitGroupID, accountCreationOrganization(ctx)}
	for i, value := range args {
		values = append(values, fmt.Sprintf("$%d", i+5))
		insertArgs = append(insertArgs, value)
	}
	if defaultGroupExpr != "" {
		columns = append(columns, "access_group_id")
		values = append(values, defaultGroupExpr)
	}
	statement := fmt.Sprintf(
		"INSERT INTO organization_memberships (%s) VALUES (%s) ON CONFLICT (organization_id, account_id) DO NOTHING",
		strings.Join(columns, ", "), strings.Join(values, ", "),
	)
	if _, err := querier.Exec(ctx, statement, insertArgs...); err != nil {
		return fmt.Errorf("creating account membership policy: %w", err)
	}
	return nil
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

func (r *UserRepository) deleteWithQuerier(ctx context.Context, querier userMutationQuerier, id int) error {
	tag, err := querier.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}
