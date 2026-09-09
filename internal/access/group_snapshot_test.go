package access

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestGroupPolicyInTransactionRejectsUnvalidatedSubject(t *testing.T) {
	subject := GroupSubject{OrganizationID: uuid.New(), AccountID: 7, ProfileID: "profile"}
	contexts := []context.Context{
		context.Background(),
		tenancy.WithContext(context.Background(), tenancy.Context{OrganizationID: subject.OrganizationID, AccountID: 8}),
		tenancy.WithContext(context.Background(), tenancy.Context{OrganizationID: uuid.New(), AccountID: 7}),
	}
	for _, ctx := range contexts {
		// A rejected subject must never touch the transaction.
		if _, err := GroupPolicyInTransaction(ctx, nil, subject); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("unvalidated subject: %v", err)
		}
	}
}

func TestGroupPolicyInTransactionProfileIsolationDB(t *testing.T) {
	ctx, pool, store, suffix, organizationID := newGroupStoreDBTest(t)
	accountGroup := createTestGroup(t, ctx, store, organizationID, suffix, "account")
	profileGroup := createTestGroup(t, ctx, store, organizationID, suffix, "profile")
	accountID := insertAccessGroupTestUser(t, ctx, pool, suffix, &accountGroup.ID, 1)
	profileID := insertAccessGroupTestProfile(t, ctx, pool, suffix, accountID, organizationID, &profileGroup.ID)
	ctx = tenancy.WithContext(ctx, tenancy.Context{OrganizationID: organizationID, AccountID: accountID})
	subject := GroupSubject{OrganizationID: organizationID, AccountID: accountID, ProfileID: profileID}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	before, err := GroupPolicyInTransaction(ctx, tx, subject)
	if err != nil || before == nil || before.ID != profileGroup.ID {
		t.Fatalf("profile policy = %#v, %v", before, err)
	}
	// A concurrent policy change cannot leak into this transaction's snapshot.
	if _, err := pool.Exec(ctx, `UPDATE access_groups SET max_streams=max_streams+1 WHERE organization_id=$1 AND id=$2`, organizationID, profileGroup.ID); err != nil {
		t.Fatal(err)
	}
	after, err := GroupPolicyInTransaction(ctx, tx, subject)
	if err != nil || after == nil || after.MaxStreams != before.MaxStreams {
		t.Fatalf("snapshot changed: before %#v after %#v err %v", before, after, err)
	}
	live, err := store.ResolvePolicy(ctx, subject)
	if err != nil || live == nil || live.MaxStreams != before.MaxStreams+1 {
		t.Fatalf("outside transaction = %#v, %v", live, err)
	}
	legacyCtx := tenancy.WithContext(ctx, tenancy.Context{OrganizationID: organizationID, AccountID: accountID, Legacy: true})
	legacySubject := GroupSubject{OrganizationID: organizationID, AccountID: accountID, Legacy: true}
	legacy, err := GroupPolicyInTransaction(legacyCtx, tx, legacySubject)
	if err != nil || legacy == nil || legacy.ID != accountGroup.ID {
		t.Fatalf("legacy account policy = %#v, %v", legacy, err)
	}
	if _, err := GroupPolicyInTransaction(ctx, tx, legacySubject); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("caller enabled legacy fallback: %v", err)
	}
	for _, bad := range []GroupSubject{
		{OrganizationID: organizationID, AccountID: accountID, ProfileID: "missing-profile"},
		{OrganizationID: organizationID, AccountID: accountID},
		{OrganizationID: organizationID, AccountID: accountID + 1, ProfileID: profileID},
		{OrganizationID: uuid.New(), AccountID: accountID, ProfileID: profileID},
	} {
		badCtx := tenancy.WithContext(ctx, tenancy.Context{OrganizationID: bad.OrganizationID, AccountID: bad.AccountID})
		if _, err := GroupPolicyInTransaction(badCtx, tx, bad); !errors.Is(err, ErrGroupNotFound) {
			t.Fatalf("subject %#v accepted: %v", bad, err)
		}
	}
}
