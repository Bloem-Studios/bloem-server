package entitlements_test

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// Libraries are global and the standard template grants every enabled one, so
// tests that compare library sets get a database of their own: packages sharing
// a CI shard's database add and delete libraries while these run.
func TestMain(m *testing.M) { os.Exit(bloemtestdb.RunIsolatedSuite(m.Run)) }
