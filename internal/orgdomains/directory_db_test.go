package orgdomains

import (
	"context"
	"testing"
	"time"
)

func TestDirectoryRefreshesAfterInvalidate(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	d := NewDirectory(store, time.Hour)
	if _, ok := d.Lookup(ctx, "main.domain.com"); ok {
		t.Fatal("unregistered host mapped")
	}
	org := seedOrganization(t, pool, "main")
	if _, err := store.AddDomain(ctx, org, "main.domain.com", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Lookup(ctx, "main.domain.com"); ok {
		t.Fatal("the cached directory changed before Invalidate")
	}
	d.Invalidate()
	if got, ok := d.Lookup(ctx, "Main.Domain.com"); !ok || got != org {
		t.Fatalf("after Invalidate: %v, %v; want %v", got, ok, org)
	}
	if !d.IsDefault(ctx, defaultOrganization(t, pool)) {
		t.Fatal("the default organization is not reported as default")
	}
}
