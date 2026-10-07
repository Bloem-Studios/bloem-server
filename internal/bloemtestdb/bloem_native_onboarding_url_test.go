package bloemtestdb

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeOnboardingCloneURL(t *testing.T) {
	name := "bloem_storage_test_acore_00000000000000000000000000000001"
	for _, original := range []string{
		"postgres://fixture:fixture@127.0.0.1:15432/bloem_storage_test_template?sslmode=disable",
		"postgresql://fixture:fixture@localhost:15432/bloem_storage_test_template?application_name=test-fixture&sslmode=disable",
		"postgres://fixture:fixture@127.0.0.1:15432/bloem_storage_test_template?dbname=bloem_storage_test_template&sslmode=disable",
	} {
		rewritten, err := nativeOnboardingCloneURL(original, name)
		if err != nil {
			t.Fatal("clone URL")
		}
		got, err := pgxpool.ParseConfig(rewritten)
		if err != nil {
			t.Fatal("clone configuration")
		}
		before, err := pgxpool.ParseConfig(original)
		if err != nil {
			t.Fatal("original configuration")
		}
		if got.ConnConfig.Database != name {
			t.Fatal("clone URL still points at template")
		}
		if got.ConnConfig.Host != before.ConnConfig.Host || got.ConnConfig.Port != before.ConnConfig.Port ||
			got.ConnConfig.User != before.ConnConfig.User || got.ConnConfig.Password != before.ConnConfig.Password {
			t.Fatal("clone URL changed private connection authority")
		}
	}
}

func TestNativeOnboardingCloneRequiresCleanupRegistration(t *testing.T) {
	_, _, err := CloneNativeOnboarding(context.Background(), "not-a-dsn", true)
	if err == nil || err.Error() != "register clone cleanup before opening its pool; use CloneNativeOnboarding with prepare=false" {
		t.Fatal("preparation must refuse before any database access until caller owns cleanup")
	}
}
func TestNativeOnboardingCloneURLRejectsUnownedNames(t *testing.T) {
	for _, name := range []string{"bloem_storage_test_template", "postgres", "bloem_storage_test_acore_notuuid", "bloem_storage_test_acore_00000000000000000000000000000000", "bloem_storage_test_acore_00000000000000000000000000000001/path"} {
		if _, err := nativeOnboardingCloneURL("postgres://fixture:fixture@localhost/bloem_storage_test_template", name); err == nil {
			t.Fatal("unowned target accepted")
		}
	}
}

func TestNativeOnboardingPoolTargetConfiguration(t *testing.T) {
	for _, dsn := range []string{
		"invalid",
		"postgres://fixture:fixture@localhost/bloem_storage_test_template",
		"postgres://fixture:fixture@localhost/bloem_storage_test_acore_notuuid",
		"postgres://fixture:fixture@localhost/bloem_storage_test_acore_00000000000000000000000000000001?dbname=bloem_storage_test_template",
	} {
		if _, err := NativeOnboardingPoolConfig(dsn); err == nil {
			t.Fatal("unowned caller pool admitted")
		}
	}
	cfg, err := NativeOnboardingPoolConfig("postgres://fixture:fixture@localhost/bloem_storage_test_acore_00000000000000000000000000000001")
	if err != nil || cfg.BeforeAcquire == nil {
		t.Fatal("per-connection identity gate absent")
	}
	if err := PrepareNativeOnboardingPool(context.Background(), nil); err == nil {
		t.Fatal("nil pool prepared")
	}
}
