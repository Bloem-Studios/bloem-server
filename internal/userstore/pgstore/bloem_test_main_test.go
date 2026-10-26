package pgstore

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// TestMain runs the package on its own prepared database, so EXPLAIN fixtures
// are not affected by other packages' schema changes or statistics.
//
// With SILO_REQUIRE_TEST_DATABASE=1 a missing SILO_TEST_DATABASE_URL fails the
// package instead of letting its PostgreSQL tests skip (Bloem CI contract; the
// Silo test files themselves only skip).
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(m.Run))
}
