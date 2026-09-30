package requests

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

// Upstream lifecycle fixtures clone request tables but do not copy triggers or
// accounts. Give only those isolated tables a single-organization default;
// production and tenant-isolation tests continue to require real memberships.
func bloemPrepareLifecycleFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var schema string
	if err := pool.QueryRow(t.Context(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if len(schema) < len("request_lifecycle_") || schema[:len("request_lifecycle_")] != "request_lifecycle_" {
		t.Fatalf("refuse fixture changes in schema %q", schema)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`ALTER TABLE media_requests ALTER COLUMN organization_id SET DEFAULT '%s'::uuid`, uuid.NewString())); err != nil {
		t.Fatal(err)
	}
}
