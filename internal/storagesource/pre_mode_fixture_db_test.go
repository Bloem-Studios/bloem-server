//go:build integration

package storagesource

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreModeFixtureInputIsIndependent(t *testing.T) {
	historical := filepath.Join(t.TempDir(), "historical-private-input.json")
	t.Setenv("BLOEM_PRE_MODE_FIXTURE", historical)
	if got := preModeFixturePath(); got != historical {
		t.Fatal("PRE-MODE fixture ignored independent historical input and selected original CURRENT private source")
	}
	t.Setenv("BLOEM_PRE_MODE_FIXTURE", "")
	if got := preModeFixturePath(); got != "../../.superpowers/sdd/2026-10-06-native-storage-onboarding-implementation-plan-4/pre-mode-database.json" {
		t.Fatal("PRE-MODE default must be a separate historical private input")
	}
}

// The marker is deliberately synthetic on an owned exact historical caller.
// This proves the CURRENT-marker refusal boundary, not CURRENT schema acceptance.
func TestPreModeCurrentMarkerRefusesBeforeReset(t *testing.T) {
	if os.Getenv("BLOEM_PRE_MODE_REFUSAL_CHILD") == "1" {
		pool := preModeDatabase(t, true)
		source, _ := fixtureSource(t, pool)
		execSQL(t, pool, "CREATE TABLE public.bloem_native_libraries (sentinel text); INSERT INTO public.bloem_native_libraries VALUES ('CURRENT refusal sentinel')")
		t.Cleanup(func() {
			var present bool
			if err := pool.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM bloem_storage_sources WHERE key=$1) AND EXISTS(SELECT 1 FROM bloem_native_libraries WHERE sentinel='CURRENT refusal sentinel')", source.Key).Scan(&present); err != nil || !present {
				t.Error("CURRENT refusal mutated storage or marker before returning")
			} else {
				t.Log("PRE-MODE refusal sentinel and installed storage preserved")
			}
		})
		resetPreModeStorage(t, pool, false)
		t.Fatal("CURRENT native-mode marker reached historical Down/reset")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPreModeCurrentMarkerRefusesBeforeReset$", "-test.v")
	cmd.Env = append(os.Environ(), "BLOEM_PRE_MODE_REFUSAL_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("PRE-MODE fixture cannot reset a CURRENT native-mode schema")) || !bytes.Contains(out, []byte("PRE-MODE refusal sentinel and installed storage preserved")) || !bytes.Contains(out, []byte("cleanup verified")) {
		t.Fatalf("PRE-MODE CURRENT refusal failed at the guard or changed storage: %s", out)
	}
	t.Logf("PRE-MODE expected refusal subprocess (nonzero child exit):\n%s", out)
}

func TestPreModeFixtureRejectsUnversionedInput(t *testing.T) {
	// Permission/provenance controls never copy real connection authority into
	// the deliberately nonprivate negative fixture.
	input := preModeFixtureInput{
		DSN:      "postgresql://synthetic@localhost/bloem_storage_test_pre_mode_00000000000000000000000000000001",
		Commit:   "CURRENT",
		Manifest: preModeManifestSHA256,
		Schema:   strings.Repeat("0", 64),
	}
	data, _ := json.Marshal(input)
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreModeFixture(path); err == nil {
		t.Fatal("unversioned PRE-MODE source accepted")
	}
	input.Commit = preModeBaseline
	data, _ = json.Marshal(input)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPreModeFixture(path); err == nil {
		t.Fatal("nonprivate PRE-MODE source accepted")
	}
}

func TestPreModeHistoricalCallerIdentityAndGoMigrations(t *testing.T) {
	pool := preModeDatabase(t, true)
	input, err := readPreModeFixture(preModeFixturePath())
	if err != nil {
		t.Fatal(err)
	}
	var actual string
	if err := pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || !preModeUUIDName(actual, "bloem_storage_test_pm_") || strings.Contains(input.DSN, "/"+actual) {
		t.Fatal("PRE-MODE caller was not independently targeted")
	}
	var goMigrations int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM goose_db_version WHERE is_applied AND version_id IN (20260727010622,20260728132327,20260912120000)").Scan(&goMigrations); err != nil || goMigrations != 3 {
		t.Fatalf("exact PRE-MODE provider did not execute all three Go migrations: count=%d", goMigrations)
	}
	cfg := pool.Config()
	if cfg.BeforeAcquire == nil {
		t.Fatal("PRE-MODE caller lacks acquired-connection guard")
	}
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if !cfg.BeforeAcquire(t.Context(), conn.Conn()) {
		t.Fatal("actual PRE-MODE connection rejected")
	}
	guardPreModeAcquisitions(cfg, "bloem_storage_test_wrong_identity")
	if cfg.BeforeAcquire(t.Context(), conn.Conn()) {
		t.Fatal("wrong actual caller identity accepted")
	}
	t.Log("PRE-MODE distinct UUID caller, exact three applied Go migrations, and real acquired-connection mismatch refusal verified")
}
