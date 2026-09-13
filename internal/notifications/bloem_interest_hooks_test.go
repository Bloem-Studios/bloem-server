package notifications

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
)

type lifecycleCapableStore struct {
	userstore.UserStore
	called bool
}

func (s *lifecycleCapableStore) WithProfileLifecycleTransaction(
	_ context.Context,
	_ pgx.Tx,
	_ func(userstore.ProfileLifecycleWriter) error,
) error {
	s.called = true
	return nil
}

func TestInterestTrackingStoreForwardsProfileLifecycle(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := userdb.InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	inner := &lifecycleCapableStore{UserStore: userdb.NewSQLiteUserStore(db)}
	provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: inner}, &System{})
	wrapped, err := provider.ForUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("ForUser: %v", err)
	}

	transactioner, ok := wrapped.(userstore.ProfileLifecycleTransactioner)
	if !ok {
		t.Fatal("interest-tracking wrapper dropped ProfileLifecycleTransactioner; every profile lifecycle mutation 503s")
	}
	if err := transactioner.WithProfileLifecycleTransaction(context.Background(), nil, nil); err != nil {
		t.Fatalf("WithProfileLifecycleTransaction: %v", err)
	}
	if !inner.called {
		t.Error("wrapper advertised the lifecycle capability but did not delegate to the backing store")
	}
}

func TestInterestTrackingStoreReportsProfileLifecycleUnsupported(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := userdb.InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	inner := userdb.NewSQLiteUserStore(db)
	if _, ok := userstore.UserStore(inner).(userstore.ProfileLifecycleTransactioner); ok {
		t.Fatal("test setup: SQLite store unexpectedly implements ProfileLifecycleTransactioner")
	}
	provider := WrapUserStoreProvider(preferenceTransactionTestProvider{store: inner}, &System{})
	wrapped, err := provider.ForUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("ForUser: %v", err)
	}
	transactioner, ok := wrapped.(userstore.ProfileLifecycleTransactioner)
	if !ok {
		t.Fatal("wrapper must advertise the capability and report unsupported at call time")
	}
	err = transactioner.WithProfileLifecycleTransaction(context.Background(), nil, nil)
	if !errors.Is(err, userstore.ErrProfileLifecycleUnsupported) {
		t.Errorf("want ErrProfileLifecycleUnsupported, got %v", err)
	}
}
