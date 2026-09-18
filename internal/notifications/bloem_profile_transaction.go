package notifications

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

// CreateProfileInTransaction forwards the store-level primitive now required by
// atomic account setup. Preserving only the provider-level extension is not
// sufficient. A nontransactional backend must fail closed, never commit the
// profile separately; the whole-provider capability remains conditional.
func (s *interestTrackingStore) CreateProfileInTransaction(ctx context.Context, tx pgx.Tx, profile userstore.Profile) error {
	store, ok := s.UserStore.(interface {
		CreateProfileInTransaction(context.Context, pgx.Tx, userstore.Profile) error
	})
	if !ok {
		return errors.New("transactional profile creation is unavailable on the backing store")
	}
	return store.CreateProfileInTransaction(ctx, tx, profile)
}
