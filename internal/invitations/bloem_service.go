package invitations

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type organizationAccountCreator interface {
	CreateAccountForOrganizationInTransaction(context.Context, pgx.Tx, uuid.UUID, auth.CreateAccountInput) (*models.User, error)
}

// createInvitedAccount creates the invited account in the invitation's
// organization, or in the default one for an invitation that names none.
func (s *Service) createInvitedAccount(ctx context.Context, tx pgx.Tx, inv *models.Invitation, input auth.CreateAccountInput) (*models.User, error) {
	if inv.OrganizationID == uuid.Nil {
		return s.accounts.CreateAccountInTransaction(ctx, tx, input)
	}
	accounts, ok := s.accounts.(organizationAccountCreator)
	if !ok {
		return nil, errors.New("account provisioner does not support organization invitations")
	}
	return accounts.CreateAccountForOrganizationInTransaction(ctx, tx, inv.OrganizationID, input)
}
