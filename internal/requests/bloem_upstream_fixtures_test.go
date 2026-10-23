package requests

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// routingModeRepository is the lifecycle test schema with the route and mode
// tables, the mode set to Standard.
func routingModeRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	repo, pool := lifecycleTestRepository(t)
	for _, stmt := range []string{
		`CREATE TABLE request_routes (LIKE public.request_routes INCLUDING ALL)`,
		`CREATE TRIGGER route_revision BEFORE INSERT OR UPDATE ON request_routes FOR EACH ROW EXECUTE FUNCTION public.advance_request_editor_revision()`,
		`CREATE TRIGGER routing_revision BEFORE INSERT OR UPDATE ON request_routing FOR EACH ROW EXECUTE FUNCTION public.advance_request_editor_revision()`,
		`INSERT INTO request_routing (id, mode) VALUES (true, 'standard')`,
	} {
		if _, err := pool.Exec(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	return repo, pool
}
