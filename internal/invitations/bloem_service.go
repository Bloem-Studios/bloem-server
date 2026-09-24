package invitations

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type transactionalInvitationRepository interface {
	GetByTokenHashInTransaction(context.Context, pgx.Tx, string) (*models.Invitation, error)
	AcceptInTransaction(context.Context, pgx.Tx, string, int) error
}

type transactionalInvitationAccountCreator interface {
	CreateAccountForOrganizationInTransaction(context.Context, pgx.Tx, uuid.UUID, auth.CreateAccountInput) (auth.CreatedAccount, error)
}

type transactionalInvitationSessionStarter interface {
	StartAccountSessionInTransaction(context.Context, pgx.Tx, *models.User, string, string) (*auth.TokenPair, error)
}

// AcceptInTransaction locks and validates the invitation only after the
// lifecycle coordinator has checked for a completed receipt, then creates and
// claims every durable result on that coordinator-owned transaction.
func (s *Service) AcceptInTransaction(ctx context.Context, tx pgx.Tx, tokenHash, password, deviceName, ip string) (*auth.TokenPair, auth.CreatedAccount, error) {
	repo, ok := s.repo.(transactionalInvitationRepository)
	if !ok {
		return nil, auth.CreatedAccount{}, fmt.Errorf("invitation repository does not support transactional acceptance")
	}
	inv, err := repo.GetByTokenHashInTransaction(ctx, tx, tokenHash)
	if err != nil {
		return nil, auth.CreatedAccount{}, err
	}
	if inv.Status(s.now()) != models.InvitationStatusPending {
		return nil, auth.CreatedAccount{}, ErrNotFound
	}
	accounts, ok := s.accounts.(transactionalInvitationAccountCreator)
	if !ok {
		return nil, auth.CreatedAccount{}, fmt.Errorf("account provisioner does not support transactional invitation acceptance")
	}
	created, err := accounts.CreateAccountForOrganizationInTransaction(ctx, tx, inv.OrganizationID, auth.CreateAccountInput{
		User: models.CreateUserInput{
			Username: inv.Email, Email: inv.Email, Password: password, Role: inv.Role,
			LibraryIDs: inv.LibraryIDs, AccessGroupID: inv.AccessGroupID,
		},
		DefaultProfile: auth.DefaultProfileOptions{Enabled: inv.CreateProfile, Name: profileNameFromEmail(inv.Email)},
	})
	if err != nil {
		if auth.IsDuplicate(err) {
			return nil, auth.CreatedAccount{}, ErrNotClaimable
		}
		return nil, auth.CreatedAccount{}, fmt.Errorf("creating invited user: %w", err)
	}
	if err := repo.AcceptInTransaction(ctx, tx, tokenHash, created.User.ID); err != nil {
		return nil, auth.CreatedAccount{}, err
	}
	sessions, ok := s.sessions.(transactionalInvitationSessionStarter)
	if !ok {
		return nil, auth.CreatedAccount{}, fmt.Errorf("session service does not support transactional invitation acceptance")
	}
	pair, err := sessions.StartAccountSessionInTransaction(ctx, tx, created.User, deviceName, ip)
	if err != nil {
		return nil, auth.CreatedAccount{}, err
	}
	return pair, created, nil
}
