package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestBloemSetupAdapterSupportsTransactionalOwnership(t *testing.T) {
	// The auth service discovers this narrow extension at runtime. The former
	// production adapter satisfied the old interface but made fresh setup fail.
	var adapter any = tenancyOwnershipBootstrapper{}
	if _, ok := adapter.(interface {
		ActivateInitialOwnershipInTransaction(context.Context, pgx.Tx, int) error
	}); !ok {
		t.Fatal("production ownership adapter cannot participate in atomic setup")
	}
}
