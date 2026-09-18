package invitations

import (
	"context"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

// ResendForOrganization rotates only the selected organization's current link.
// Acceptance, revocation and replacement serialize on the same invitation row.
func (r *Repository) ResendForOrganization(ctx context.Context, organizationID uuid.UUID, id, actorID int64, tokenHash string, expires time.Time) (*models.Invitation, error) {
	if organizationID == uuid.Nil || id <= 0 || actorID <= 0 {
		return nil, ErrNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	prior, err := scanInvitation(tx.QueryRow(ctx, `SELECT `+invitationColumns+invitationFrom+`WHERE i.id=$1 AND i.organization_id=$2 FOR UPDATE OF i`, id, organizationID))
	if err != nil {
		return nil, err
	}
	// Organization authority must never rotate a platform-admin invitation.
	if prior.Role != "user" || prior.AcceptedAt != nil || prior.RevokedAt != nil {
		return nil, ErrNotClaimable
	}
	item, err := createInvitation(ctx, tx, organizationID, models.CreateInvitationInput{
		Email: prior.Email, Role: prior.Role, AccessGroupID: prior.AccessGroupID, LibraryIDs: prior.LibraryIDs,
		CreateProfile: prior.CreateProfile, ShowTour: prior.ShowTour, Note: prior.Note,
		InvitedBy: actorID, ExpiresAt: expires,
	}, tokenHash)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit organization invitation rotation: %w", err)
	}
	return item, nil
}

func (r *Repository) RevokeForOrganization(ctx context.Context, organizationID uuid.UUID, id int64) error {
	if organizationID == uuid.Nil || id <= 0 {
		return ErrNotFound
	}
	result, err := r.pool.Exec(ctx, `UPDATE invitations SET revoked_at=now(),updated_at=now()
  WHERE id=$1 AND organization_id=$2 AND role='user' AND accepted_at IS NULL AND revoked_at IS NULL`, id, organizationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
