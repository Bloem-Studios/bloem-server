package adminjob

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// TestMain runs the package on its own prepared database. Silo's lifecycle
// tests claim the oldest queued admin job and recover stale ones across the
// whole admin_jobs table, so on a database other packages share they could
// take another package's job.
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(m.Run))
}
