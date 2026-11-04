package policy

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// TestBloemViewerResolverBatchedReadsMatchEachDB pins the viewer gate's
// database cost under Bloem tenancy: a request with a tenant reads its account,
// profile, the profile's group in the tenant, its viewer preferences and the
// tenant's libraries as one batch and no other statement. The batch must answer
// exactly what reading each fact on its own answers, for every scope and every
// refusal Silo's batch is held to.
func TestBloemViewerResolverBatchedReadsMatchEachDB(t *testing.T) {
	ctx := context.Background()
	f := newViewerBatchFixture(t, ctx)
	pdp := newViewerResolverTestPDP(t, ctx)
	groups := access.NewTenantGroupStore(f.pool)
	libraries := resourcetenancy.NewStore(f.pool)
	tenants := tenancy.NewResolver(tenancy.NewStore(f.pool))
	each := NewViewerResolver(auth.NewUserRepository(f.pool), f.provider, f.tokens, pdp, groups).
		WithBloemTenancy(libraries)
	batched := NewViewerResolver(auth.NewUserRepository(f.pool), f.provider, f.tokens, pdp, groups).
		WithSharedTables(f.pool).WithBloemTenancy(libraries)

	for _, tt := range viewerBatchCases(t, f) {
		t.Run(tt.name, func(t *testing.T) {
			requestCtx := ctx
			tenant, err := tenants.Resolve(ctx, tt.input.UserID, nil, true)
			withTenant := err == nil
			if withTenant {
				requestCtx = tenancy.WithContext(ctx, tenant)
			}
			f.tracer.take()
			want, wantErr := each.Resolve(requestCtx, tt.input)
			if statements, sends := f.tracer.take(); statements == 0 || sends != 0 {
				t.Fatalf("each-fact reads sent %d statements and %d batches", statements, sends)
			}
			got, gotErr := batched.Resolve(requestCtx, tt.input)
			statements, sends := f.tracer.take()
			if withTenant && (statements != 0 || sends != 1) {
				t.Fatalf("batched reads sent %d statements and %d batches, want 0 and 1", statements, sends)
			}
			switch {
			case tt.errAny:
				if wantErr == nil || gotErr == nil {
					t.Fatalf("errors = each %v, batched %v; want failures", wantErr, gotErr)
				}
			case tt.wantErr != nil:
				if !errors.Is(wantErr, tt.wantErr) || !errors.Is(gotErr, tt.wantErr) {
					t.Fatalf("errors = each %v, batched %v; want %v", wantErr, gotErr, tt.wantErr)
				}
			case (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()):
				t.Fatalf("errors = each %v, batched %v", wantErr, gotErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("scope mismatch\nbatched: %#v\neach:    %#v", got, want)
			}
		})
	}
}
