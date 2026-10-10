package tenancy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/sessioninvalidation"
)

// RetireOrganization atomically removes the tenant's memberships and retires
// its organization. Shared accounts and their other organizations survive.
func (s *MemberService) RetireOrganization(ctx context.Context, tenantID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenancy: begin tenant retirement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	memberIDs, err := s.RetireOrganizationInTransaction(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenancy: commit tenant retirement: %w", err)
	}
	return s.CompleteOrganizationRetirementAfterCommit(ctx, tenantID, memberIDs)
}

// RetireOrganizationInTransaction reuses membership deletion's account
// obligation checks before clearing the tenant's external identity. The tenant
// row lock prevents member provisioning from racing the retirement snapshot.
func (s *MemberService) RetireOrganizationInTransaction(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]int, error) {
	if _, err := s.store.lockTenantOrganization(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	memberIDs, err := s.store.TenantMemberAccountIDsInTransaction(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, userID := range memberIDs {
		if err := s.DeleteInTransaction(ctx, tx, tenantID, userID); err != nil {
			return nil, err
		}
	}
	if err := s.store.DeleteTenantOrganizationInTransaction(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	return memberIDs, nil
}

// CompleteOrganizationRetirementAfterCommit preserves the ordinary membership
// cleanup for surviving accounts, even when the request has been canceled.
func (s *MemberService) CompleteOrganizationRetirementAfterCommit(ctx context.Context, tenantID uuid.UUID, memberIDs []int) error {
	s.store.CompleteTenantOrganizationMutationAfterCommit()
	return sessioninvalidation.Run(ctx, func(cleanupCtx context.Context) error {
		for _, userID := range memberIDs {
			if err := s.CompleteDeleteAfterCommit(cleanupCtx, tenantID, userID); err != nil {
				return err
			}
		}
		return nil
	})
}
