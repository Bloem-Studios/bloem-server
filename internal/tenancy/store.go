package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists tenancy state in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a PostgreSQL-backed tenancy store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const (
	organizationColumns = `id, slug, name, status, owner_account_id, policy_revision, is_default`
	legacyRoleAdmin     = "admin"
	legacyRoleUser      = "user"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrganization(row rowScanner) (Organization, error) {
	var organization Organization
	err := row.Scan(
		&organization.ID,
		&organization.Slug,
		&organization.Name,
		&organization.Status,
		&organization.OwnerAccountID,
		&organization.PolicyRevision,
		&organization.Default,
	)
	return organization, err
}

func scanMembership(row rowScanner) (Membership, error) {
	var membership Membership
	err := row.Scan(
		&membership.ID,
		&membership.OrganizationID,
		&membership.AccountID,
		&membership.Status,
		&membership.LegacyRole,
		&membership.SecurityRevision,
	)
	return membership, err
}

// DefaultOrganization returns the deployment's default organization.
func (s *Store) DefaultOrganization(ctx context.Context) (Organization, error) {
	organization, err := scanOrganization(s.pool.QueryRow(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations
		WHERE is_default`))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOwnershipResolutionRequired
	}
	if err != nil {
		return Organization{}, fmt.Errorf("load default organization: %w", err)
	}
	return organization, nil
}

// GetOrganization returns one organization regardless of its status.
func (s *Store) GetOrganization(ctx context.Context, organizationID uuid.UUID) (Organization, error) {
	organization, err := scanOrganization(s.pool.QueryRow(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations
		WHERE id = $1`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrOrganizationNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("load organization: %w", err)
	}
	return organization, nil
}

// ListMemberships returns every membership for an account, including suspended
// and invited memberships so callers can make their own authorization decision.
func (s *Store) ListMemberships(ctx context.Context, accountID int) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE account_id = $1
		ORDER BY organization_id, id`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()

	memberships := []Membership{}
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	return memberships, nil
}

// GetMembership returns one membership regardless of its status.
func (s *Store) GetMembership(ctx context.Context, accountID int, organizationID uuid.UUID) (Membership, error) {
	membership, err := scanMembership(s.pool.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE account_id = $1 AND organization_id = $2`, accountID, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrMembershipNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("load membership: %w", err)
	}
	return membership, nil
}

// ProvisionDefaultMembership creates the active default-organization
// membership required by every newly created account. Repeating the same
// request is a no-op; an existing membership with different role or state is
// rejected so provisioning never overwrites protected state.
func (s *Store) ProvisionDefaultMembership(ctx context.Context, accountID int, legacyRole string) (membership Membership, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Membership{}, fmt.Errorf("begin default membership provisioning: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	membership, err = s.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, legacyRole)
	if err != nil {
		return Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Membership{}, fmt.Errorf("commit default membership provisioning: %w", err)
	}
	return membership, nil
}

// ProvisionDefaultMembershipInTransaction applies default-organization
// membership provisioning on a caller-owned transaction. Lifecycle creates
// use this so the generated account incarnation, membership and receipt are a
// single commit unit.
func (s *Store) ProvisionDefaultMembershipInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	accountID int,
	legacyRole string,
) (Membership, error) {
	if legacyRole != legacyRoleAdmin && legacyRole != legacyRoleUser {
		return Membership{}, fmt.Errorf("provision default membership: invalid legacy role %q", legacyRole)
	}

	organization, err := scanOrganization(tx.QueryRow(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations
		WHERE is_default
		FOR UPDATE`))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrOwnershipResolutionRequired
	}
	if err != nil {
		return Membership{}, fmt.Errorf("lock default organization for membership: %w", err)
	}

	membership, err := scanMembership(tx.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT $1, $2, $3, $4
		WHERE set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL
		ON CONFLICT (organization_id, account_id) DO NOTHING
		RETURNING id, organization_id, account_id, status, legacy_role, security_revision`,
		organization.ID, accountID, MembershipActive, legacyRole))
	if err == nil {
		return membership, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, fmt.Errorf("create default membership: %w", err)
	}

	membership, err = scanMembership(tx.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE organization_id = $1 AND account_id = $2
		FOR UPDATE`, organization.ID, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, fmt.Errorf("load conflicting default membership: %w", ErrMembershipNotFound)
	}
	if err != nil {
		return Membership{}, fmt.Errorf("load conflicting default membership: %w", err)
	}
	if membership.Status != MembershipActive || membership.LegacyRole != legacyRole {
		return Membership{}, ErrMembershipConflict
	}
	return membership, nil
}

// ProvisionMembershipInTransaction creates an active membership in one
// explicit organization on a caller-owned transaction.
func (s *Store) ProvisionMembershipInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	organizationID uuid.UUID,
	accountID int,
	legacyRole string,
) (Membership, error) {
	if organizationID == uuid.Nil || (legacyRole != legacyRoleAdmin && legacyRole != legacyRoleUser) {
		return Membership{}, fmt.Errorf("provision membership: invalid organization or legacy role %q", legacyRole)
	}
	var status OrganizationStatus
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrOrganizationNotFound
	} else if err != nil {
		return Membership{}, fmt.Errorf("lock organization for membership: %w", err)
	}
	if status != OrganizationActive {
		return Membership{}, ErrTenantNotFoundOrHidden
	}
	membership, err := scanMembership(tx.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT $1,$2,$3,$4
		WHERE set_config('bloem.membership_policy_writer',
				CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized'
				     THEN 'v1' ELSE '' END, true) IS NOT NULL
		RETURNING id,organization_id,account_id,status,legacy_role,security_revision`,
		organizationID, accountID, MembershipActive, legacyRole))
	if err != nil {
		return Membership{}, fmt.Errorf("create organization membership: %w", err)
	}
	return membership, nil
}

// ActivateInitialOwnership atomically assigns the initial platform and default
// organization owner. Repeating the operation for that same owner is a no-op.
func (s *Store) ActivateInitialOwnership(ctx context.Context, accountID int) (state OwnershipState, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OwnershipState{}, fmt.Errorf("begin ownership activation: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	state, err = s.ActivateInitialOwnershipInTransaction(ctx, tx, accountID)
	if err != nil {
		return OwnershipState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OwnershipState{}, fmt.Errorf("commit ownership activation: %w", err)
	}
	return state, nil
}

// ActivateInitialOwnershipInTransaction assigns the initial platform and
// default-organization owner using a caller-owned transaction.
func (s *Store) ActivateInitialOwnershipInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	accountID int,
) (OwnershipState, error) {

	var (
		platformOwner      *int
		platformRevision   int64
		resolutionRequired bool
	)
	if err := tx.QueryRow(ctx, `
		SELECT owner_account_id, policy_revision, ownership_resolution_required
		FROM platform_security
		WHERE singleton
		FOR UPDATE`).Scan(&platformOwner, &platformRevision, &resolutionRequired); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OwnershipState{}, ErrOwnershipResolutionRequired
		}
		return OwnershipState{}, fmt.Errorf("lock platform ownership: %w", err)
	}

	organization, err := scanOrganization(tx.QueryRow(ctx, `
		SELECT `+organizationColumns+`
		FROM organizations
		WHERE is_default
		FOR UPDATE`))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OwnershipState{}, ErrOwnershipResolutionRequired
		}
		return OwnershipState{}, fmt.Errorf("lock default organization: %w", err)
	}

	if ownedByDifferentAccount(platformOwner, accountID) || ownedByDifferentAccount(organization.OwnerAccountID, accountID) {
		return OwnershipState{}, ErrOwnerAlreadyAssigned
	}

	var (
		membership     Membership
		accountRole    string
		accountEnabled bool
	)
	err = tx.QueryRow(ctx, `
		SELECT memberships.id, memberships.organization_id, memberships.account_id,
		       memberships.status, memberships.legacy_role, memberships.security_revision,
		       users.role, users.enabled
		FROM organization_memberships AS memberships
		JOIN users ON users.id = memberships.account_id
		WHERE memberships.organization_id = $1
		  AND memberships.account_id = $2
		FOR UPDATE OF memberships, users`, organization.ID, accountID).Scan(
		&membership.ID,
		&membership.OrganizationID,
		&membership.AccountID,
		&membership.Status,
		&membership.LegacyRole,
		&membership.SecurityRevision,
		&accountRole,
		&accountEnabled,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OwnershipState{}, ErrMembershipNotFound
		}
		return OwnershipState{}, fmt.Errorf("lock owner eligibility: %w", err)
	}
	if membership.Status != MembershipActive {
		return OwnershipState{}, ErrMembershipNotFound
	}
	if membership.LegacyRole != legacyRoleAdmin || accountRole != legacyRoleAdmin || !accountEnabled {
		return OwnershipState{}, ErrOwnerNotEligible
	}

	if platformOwner != nil && organization.OwnerAccountID != nil && organization.Status == OrganizationActive && !resolutionRequired {
		return OwnershipState{PlatformOwnerAccountID: accountID, Organization: organization}, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE platform_security
		SET owner_account_id = $1,
			policy_revision = policy_revision + 1,
			ownership_resolution_required = false,
			updated_at = now()
		WHERE singleton`, accountID); err != nil {
		return OwnershipState{}, mapOwnershipWriteError(err)
	}

	organization, err = scanOrganization(tx.QueryRow(ctx, `
		UPDATE organizations
		SET owner_account_id = $1,
			status = $2,
			policy_revision = policy_revision + 1,
			updated_at = now()
		WHERE id = $3
		RETURNING `+organizationColumns, accountID, OrganizationActive, organization.ID))
	if err != nil {
		return OwnershipState{}, mapOwnershipWriteError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET security_revision = security_revision + 1,
			updated_at = now()
		WHERE id = $1`, membership.ID); err != nil {
		return OwnershipState{}, mapOwnershipWriteError(err)
	}

	return OwnershipState{PlatformOwnerAccountID: accountID, Organization: organization}, nil
}

func ownedByDifferentAccount(ownerAccountID *int, accountID int) bool {
	return ownerAccountID != nil && *ownerAccountID != accountID
}

func mapOwnershipWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrOwnerAlreadyAssigned
		case "23514":
			return ErrOwnershipResolutionRequired
		}
	}
	return fmt.Errorf("write ownership activation: %w", err)
}

// AccountOrganization answers which organization represents an account when no
// profile names one.
//
// A Silo client only sends X-Profile-Id once a profile has been selected, so
// content requests before that arrive profileless. Resolving those through the
// deployment default organization tells a tenant's own end user that its tenant
// does not exist, because it holds no membership there.
//
// The account's own organization is the answer, chosen with the same precedence
// the account policy projection uses (auth.userSource): the default
// organization when the account belongs to it, otherwise its earliest
// membership. Keeping the two in step means there is one notion of "the
// account's organization" rather than two that can disagree.
func (s *Store) AccountOrganization(ctx context.Context, accountID int) (uuid.UUID, error) {
	if s == nil || s.pool == nil || accountID <= 0 {
		return uuid.Nil, ErrTenantUnavailable
	}
	var organizationID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT memberships.organization_id
		FROM organization_memberships AS memberships
		JOIN organizations AS orgs ON orgs.id = memberships.organization_id
		WHERE memberships.account_id = $1
		  AND memberships.status <> 'invited'
		ORDER BY orgs.is_default DESC, memberships.created_at ASC, memberships.id ASC
		LIMIT 1`, accountID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrMembershipNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: load account organization: %w", ErrTenantUnavailable, err)
	}
	return organizationID, nil
}
