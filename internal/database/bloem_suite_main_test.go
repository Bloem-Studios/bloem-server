package database_test

import (
	"os"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(m.Run))
}
