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

const organizationColumns = `id, slug, name, status, owner_account_id, policy_revision, is_default`

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

	membership, err := scanMembership(tx.QueryRow(ctx, `
		SELECT id, organization_id, account_id, status, legacy_role, security_revision
		FROM organization_memberships
		WHERE organization_id = $1
		  AND account_id = $2
		  AND status = $3
		FOR UPDATE`, organization.ID, accountID, MembershipActive))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OwnershipState{}, ErrMembershipNotFound
		}
		return OwnershipState{}, fmt.Errorf("lock active owner membership: %w", err)
	}

	if platformOwner != nil && organization.OwnerAccountID != nil && organization.Status == OrganizationActive && !resolutionRequired {
		if err := tx.Commit(ctx); err != nil {
			return OwnershipState{}, fmt.Errorf("commit idempotent ownership activation: %w", err)
		}
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

	if err := tx.Commit(ctx); err != nil {
		return OwnershipState{}, fmt.Errorf("commit ownership activation: %w", err)
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
