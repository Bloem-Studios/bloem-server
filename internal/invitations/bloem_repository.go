package invitations

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateForOrganization inserts an invitation bound to one organization.
func (r *Repository) CreateForOrganization(ctx context.Context, organizationID uuid.UUID, input models.CreateInvitationInput, tokenHash string) (*models.Invitation, error) {
	if organizationID == uuid.Nil {
		return nil, ErrNotFound
	}
	return r.create(ctx, organizationID, input, tokenHash)
}

// GetByTokenHashInTransaction locks an invitation before claimability is
// evaluated, serializing account creation with redemption.
func (r *Repository) GetByTokenHashInTransaction(ctx context.Context, tx pgx.Tx, tokenHash string) (*models.Invitation, error) {
	row := tx.QueryRow(ctx, `SELECT `+invitationColumns+invitationFrom+`WHERE i.token_hash=$1 FOR UPDATE OF i`, tokenHash)
	return scanInvitation(row)
}

// AcceptInTransaction claims an invitation on the caller-owned transaction.
func (r *Repository) AcceptInTransaction(ctx context.Context, tx pgx.Tx, tokenHash string, userID int) error {
	tag, err := tx.Exec(ctx, `
		UPDATE invitations SET accepted_at=now(),accepted_user_id=$2,updated_at=now()
		WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now()`, tokenHash, userID)
	if err != nil {
		return fmt.Errorf("accepting invitation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotClaimable
	}
	return nil
}

func nullableOrganizationID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// ListForOrganization returns only invitations in one organization. A nil
// identifier is the legacy default-organization projection (List).
func (r *Repository) ListForOrganization(ctx context.Context, organizationID uuid.UUID) ([]*models.Invitation, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+invitationColumns+invitationFrom+`WHERE i.organization_id=COALESCE($1,public.bloem_default_organization_id()) ORDER BY i.created_at DESC`, nullableOrganizationID(organizationID))
	if err != nil {
		return nil, fmt.Errorf("listing invitations: %w", err)
	}
	defer rows.Close()

	var invitations []*models.Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating invitations: %w", err)
	}
	return invitations, nil
}
