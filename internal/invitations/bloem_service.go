package invitations

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// bloemInvitationAccountContext prepares Silo's account creation in Accept
// for the invitation's organization: the organization must be active, and the
// account joins it. An invitation that names none creates the account in the
// default organization, as Silo's call does on its own.
func bloemInvitationAccountContext(ctx context.Context, tx pgx.Tx, inv *models.Invitation) (context.Context, error) {
	if inv.OrganizationID == uuid.Nil {
		return ctx, nil
	}
	return auth.OrganizationAccountContextInTransaction(ctx, tx, inv.OrganizationID)
}
