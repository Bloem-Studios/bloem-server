package bridgeimport

import (
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/userdb"

	"github.com/Silo-Server/silo-server/internal/userdb/bridgeimport/testdata"
)

func TestImportSchema22AccountsForEveryColumn(t *testing.T) {
	source, err := testdata.NewSource(filepath.Join(t.TempDir(), "7.db"), 7)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close() //nolint:errcheck
	tx, err := source.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := validateSourceSchema(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	if len(sourceColumns) != 29 || len(Manifest()) != 29 {
		t.Fatal("complete mapping changed")
	}
	for _, mapping := range Manifest() {
		if len(sourceColumns[mapping.Source]) == 0 {
			t.Fatalf("no columns for %s", mapping.Source)
		}
	}
}

func TestImportSchemaRefusesUnclassifiedState(t *testing.T) {
	for name, statement := range map[string]string{
		"generated column": "ALTER TABLE favorites ADD COLUMN private_generated TEXT GENERATED ALWAYS AS (media_item_id) VIRTUAL",
		"extra column":     "ALTER TABLE favorites ADD COLUMN private_extra TEXT",
		"extra table":      "CREATE TABLE private_extra(value TEXT)",
		"missing table":    "DROP TABLE favorites",
		"legacy rows":      "INSERT INTO playback_sessions VALUES('s','p',1,'direct',0,0,'2026-01-01','2026-01-01')",
		"future version":   "PRAGMA user_version=23",
	} {
		t.Run(name, func(t *testing.T) {
			source, err := testdata.NewSource(filepath.Join(t.TempDir(), "7.db"), 7)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close() //nolint:errcheck
			assertSchema22(t, source)
			if _, err := source.DB.Exec(statement); err != nil {
				t.Fatal(err)
			}
			tx, err := source.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback() //nolint:errcheck
			if err := validateSourceSchema(t.Context(), tx); err == nil {
				t.Fatal("unsupported source accepted")
			}
		})
	}
}

func assertSchema22(t *testing.T, source *userdb.UserDB) {
	t.Helper()
	tx, err := source.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := validateSourceSchema(t.Context(), tx); err != nil {
		t.Fatalf("baseline schema22 invalid: %v", err)
	}
}

func TestImportSchemaRefusesNewAndUpgraded23(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		name := "new"
		if upgrade {
			name = "upgraded"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "7.db")
			if upgrade {
				old, err := testdata.NewSource(path, 7)
				if err != nil {
					t.Fatal(err)
				}
				assertSchema22(t, old)
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
			}
			source, err := userdb.NewUserDB(path, 7)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close() //nolint:errcheck
			var version int
			if err := source.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 23 {
				t.Fatalf("expected schema23: %d %v", version, err)
			}
			tx, err := source.DB.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback() //nolint:errcheck
			if err := validateSourceSchema(t.Context(), tx); err == nil {
				t.Fatal("schema23 accepted")
			}
		})
	}
}
