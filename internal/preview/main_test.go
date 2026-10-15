package preview_test

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// The settings row is a singleton, so this package gets a database of its own.
func TestMain(m *testing.M) { os.Exit(bloemtestdb.RunIsolatedSuite(m.Run)) }
