package backuptest

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// bloemPrepareTemplate installs Bloem's fixture-compatibility triggers into
// the migrated template. Silo's backup tests seed accounts, profiles and
// libraries with bare SQL written before organizations existed; the shared
// test database gets the triggers from cmd/bloem-testdb, and this package
// migrates a template of its own. Copies of the template, and databases
// restored from their dumps, keep the triggers, which arm themselves in a
// database whose name marks it as a test database.
func bloemPrepareTemplate(ctx context.Context, pool *pgxpool.Pool) error {
	return bloemtestdb.InstallFixtureCompat(ctx, pool)
}
