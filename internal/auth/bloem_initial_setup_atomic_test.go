package auth_test

// Bloem tenancy adapters for Silo's initial_setup_atomic_test.go.

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Match the production Bloem adapter, including both transactional seams.
// These callbacks must use the caller's transaction, not a second pool write.
type atomicSetupTenancy struct{ store *tenancy.Store }

func (b atomicSetupTenancy) ProvisionDefaultMembership(ctx context.Context, accountID int, role string) error {
	_, err := b.store.ProvisionDefaultMembership(ctx, accountID, role)
	return err
}

func (b atomicSetupTenancy) ProvisionDefaultMembershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int, role string) (uuid.UUID, uuid.UUID, error) {
	membership, err := b.store.ProvisionDefaultMembershipInTransaction(ctx, tx, accountID, role)
	return membership.OrganizationID, membership.ID, err
}

func (b atomicSetupTenancy) ActivateInitialOwnership(ctx context.Context, accountID int) error {
	_, err := b.store.ActivateInitialOwnership(ctx, accountID)
	return err
}

func (b atomicSetupTenancy) ActivateInitialOwnershipInTransaction(ctx context.Context, tx pgx.Tx, accountID int) error {
	_, err := b.store.ActivateInitialOwnershipInTransaction(ctx, tx, accountID)
	return err
}

func (f failingProfileStore) ForUser(ctx context.Context, id int) (userstore.UserStore, error) {
	store, err := f.UserStoreProvider.ForUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return failingTransactionalProfileStore{UserStore: store, err: f.err}, nil
}

type failingTransactionalProfileStore struct {
	userstore.UserStore
	err error
}

func (s failingTransactionalProfileStore) CreateProfileInTransaction(context.Context, pgx.Tx, userstore.Profile) error {
	return s.err
}
