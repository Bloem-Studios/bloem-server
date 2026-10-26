package invitations

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

// CreateForOrganization inserts an invitation bound to one organization.
func (r *Repository) CreateForOrganization(ctx context.Context, organizationID uuid.UUID, input models.CreateInvitationInput, tokenHash string) (*models.Invitation, error) {
	if organizationID == uuid.Nil {
		return nil, ErrNotFound
	}
	return r.create(ctx, organizationID, input, tokenHash)
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
