package database

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/migrations"
)

// Every migration path (MigrateAtStartup, the goose provider, the restore's
// version list) opens the migration directory through migrationSubFS, so
// Bloem's schema adapters apply even when a caller passes Silo's unadapted
// migrations.FS, as the startup sequence in cmd/silo does.
func TestMigrationSubFSAppliesBloemSchemaAdapters(t *testing.T) {
	const name = "20260926181741_request_group_limits.sql"
	for _, source := range []struct {
		label string
		fsys  fs.FS
	}{{"silo", migrations.FS}, {"bloem", migrations.BloemFS}} {
		sub, err := migrationSubFS(source.fsys, "sql")
		if err != nil {
			t.Fatalf("%s: migrationSubFS: %v", source.label, err)
		}
		raw, err := fs.ReadFile(sub, name)
		if err != nil {
			t.Fatalf("%s: read %s: %v", source.label, name, err)
		}
		marker := "-- bloem-schema-adapter:v1:sql/" + name + "\n"
		if !strings.HasPrefix(string(raw), marker) {
			t.Fatalf("%s: %s was not adapted for Bloem", source.label, name)
		}
		if strings.Count(string(raw), marker) != 1 {
			t.Fatalf("%s: %s was adapted more than once", source.label, name)
		}
	}
}
