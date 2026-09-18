package notifications

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

type setupProfileTransactionStore struct {
	userstore.UserStore
	profile userstore.Profile
	err     error
}

func (s *setupProfileTransactionStore) CreateProfileInTransaction(_ context.Context, _ pgx.Tx, profile userstore.Profile) error {
	s.profile = profile
	return s.err
}

func TestBloemNotificationDecoratorPreservesAtomicProfileCreation(t *testing.T) {
	failure := errors.New("transaction failed")
	inner := &setupProfileTransactionStore{err: failure}
	provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: inner}, &System{})
	store, err := provider.ForUser(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	atomic, ok := store.(interface {
		CreateProfileInTransaction(context.Context, pgx.Tx, userstore.Profile) error
	})
	if !ok {
		t.Fatal("lost atomic profile creation")
	}
	profile := userstore.Profile{ID: "profile", Name: "Parent", IsPrimary: true}
	if err := atomic.CreateProfileInTransaction(context.Background(), nil, profile); !errors.Is(err, failure) || inner.profile.ID != profile.ID {
		t.Fatalf("profile=%+v err=%v", inner.profile, err)
	}
}
func TestBloemNotificationDecoratorNeverEmulatesAtomicProfileCreation(t *testing.T) {
	store := &interestTrackingStore{UserStore: struct{ userstore.UserStore }{}}
	if err := store.CreateProfileInTransaction(context.Background(), nil, userstore.Profile{}); err == nil {
		t.Fatal("unsupported backend must fail closed")
	}
}
