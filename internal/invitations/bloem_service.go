package invitations

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type transactionalInvitationAccountCreator interface {
	CreateAccountForOrganizationInTransaction(context.Context, pgx.Tx, uuid.UUID, auth.CreateAccountInput) (auth.CreatedAccount, error)
}

// createInvitedAccount creates the invited account in the invitation's
// organization, so accepting joins the organization that issued it.
func (s *Service) createInvitedAccount(ctx context.Context, tx pgx.Tx, inv *models.Invitation, input auth.CreateAccountInput) (auth.CreatedAccount, error) {
	if inv.OrganizationID != uuid.Nil {
		accounts, ok := s.accounts.(transactionalInvitationAccountCreator)
		if !ok {
			return auth.CreatedAccount{}, fmt.Errorf("account provisioner does not support organization invitations")
		}
		return accounts.CreateAccountForOrganizationInTransaction(ctx, tx, inv.OrganizationID, input)
	}
	return s.accounts.CreateAccountWithMembershipInTransaction(ctx, tx, input)
}
