package pgstore

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestBloemCollectionTransactionRollsBackEffects(t *testing.T) {
	pool, user := newConstraintTestUser(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	provider := NewBloemCollectionTransaction(pool, tx, user)
	store, err := provider.ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.CreateCollection(ctx, userstore.CreateCollectionInput{CreatorProfileID: "owner", Name: "Atomic bridge", CollectionType: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetCollection(ctx, c.ID); err != nil {
		t.Fatal("uncommitted read", err)
	}
	if _, err = provider.ForUser(ctx, user+1); err == nil {
		t.Fatal("foreign account acquired transaction")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = newStore(pool, user).GetCollection(ctx, c.ID); err == nil {
		t.Fatal("rollback leaked collection")
	}
}
