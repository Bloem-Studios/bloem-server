package sections

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
)

func TestNativeLibrarySectionsFailClosed(t *testing.T) {
	var nilRepo *Repository
	for _, r := range []*Repository{nilRepo, NewRepository(nil)} {
		called := false
		authorize := func(context.Context, pgx.Tx) error { called = true; return nil }
		for _, err := range []error{
			r.SeedNativeLibraryDefaultsAuthorized(context.Background(), 1, authorize),
			r.SeedNativeHomeRecentAuthorized(context.Background(), 1, "Books", authorize),
			r.RequireNativeInitializationWitnessesTx(context.Background(), nil, 1),
		} {
			var unavailable *catalog.NativeOnboardingError
			if !errors.As(err, &unavailable) || unavailable.Code != "native_storage_unavailable" {
				t.Fatalf("missing dependency admitted: %v", err)
			}
		}
		if called {
			t.Fatal("missing dependency invoked authorizer")
		}
	}
}
