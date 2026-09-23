package pgstore

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// CreateProfileInTransaction inserts a profile using a caller-owned
// transaction. Account lifecycle creation uses it to bind the generated
// profile to the same receipt as the account and membership.
func (s *PostgresUserStore) CreateProfileInTransaction(ctx context.Context, tx pgx.Tx, p userstore.Profile) error {
	return createProfile(ctx, tx, s.userID, p)
}

// GetProfileInTransaction reads a profile through a caller-owned transaction.
func (s *PostgresUserStore) GetProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) (*userstore.Profile, error) {
	return getProfile(ctx, tx, s.userID, id)
}

// ListProfilesInTransaction lists profiles through a caller-owned transaction.
func (s *PostgresUserStore) ListProfilesInTransaction(ctx context.Context, tx pgx.Tx) ([]userstore.Profile, error) {
	return listProfiles(ctx, tx, s.userID)
}

// DeleteProfileInTransaction deletes a profile through a caller-owned transaction.
func (s *PostgresUserStore) DeleteProfileInTransaction(ctx context.Context, tx pgx.Tx, id string) error {
	return deleteProfile(ctx, tx, s.userID, id)
}
