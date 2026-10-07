package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeStorageHiddenInstallations(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := time.Now().UnixNano()
	var ordinary, native int
	for _, id := range []*int{&ordinary, &native} {
		if err := pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES($1,'1','/synthetic') RETURNING id`, fmt.Sprintf("hidden.fixture.%d.%p", suffix, id)).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organization_entitlements WHERE plugin_installation_id = ANY($1)`, []int{ordinary, native})
		_, _ = pool.Exec(context.Background(), `DELETE FROM plugin_installations WHERE id = ANY($1)`, []int{ordinary, native})
	})
	store := NewNativeStorageHiddenInstallations(NewInstallationStore(pool), nativeIsolationFixture{ids: map[int]bool{native: true}})

	for name, list := range map[string]func(context.Context) ([]*Installation, error){"List": store.List, "ListEnabled": store.ListEnabled} {
		rows, err := list(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int]bool{}
		for _, row := range rows {
			seen[row.ID] = true
		}
		if !seen[ordinary] || seen[native] {
			t.Fatalf("%s: ordinary=%v native=%v", name, seen[ordinary], seen[native])
		}
	}
	if _, err := store.GetByID(ctx, native); !errors.Is(err, ErrInstallationNotFound) {
		t.Fatalf("native installation visible by ID: %v", err)
	}
	if _, err := store.GetByID(ctx, ordinary); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, native); !errors.Is(err, ErrInstallationNotFound) {
		t.Fatalf("native installation deletable through plugin handlers: %v", err)
	}
	if _, err := NewInstallationStore(pool).GetByID(ctx, native); err != nil {
		t.Fatalf("native installation removed: %v", err)
	}
}
