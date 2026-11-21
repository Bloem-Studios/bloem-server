package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/config"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/secret"
)

// The admin request usage read names another account, so the v2 listener
// must answer it from Bloem's tenant adapter: Silo's viewer service resolves
// request policy for the request's own tenant account only and refuses any
// other target. Silo assigns its service late in newChiRouter, so the
// adapter has to be installed after that, in the sealed dependency set.
func TestV2AdminRequestUsageUsesTheTenantAdapter(t *testing.T) {
	cfg, err := config.LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), "postgres://nobody:nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cipher, err := secret.New(bytes.Repeat([]byte{0}, secret.MinMasterKeyLen))
	if err != nil {
		t.Fatal(err)
	}
	var sealed *apiv2.Dependencies
	deps := Dependencies{
		Config:           cfg,
		AppContext:       context.Background(),
		DB:               pool,
		SecretCipher:     cipher,
		ClientIPResolver: clientip.NewResolver(nil),
		NodeID:           "v2-request-usage",
		PublicURL:        "https://silo.example.test",
		v2Wiring:         func(d apiv2.Dependencies) { sealed = &d },
	}
	if h := newChiRouter(deps); h == nil {
		t.Fatal("router did not build")
	}
	if sealed == nil {
		t.Fatal("v2 wiring probe never ran")
	}
	if _, ok := sealed.AdminRequestUsage.(*mediarequests.TenantAdminRequestUsageService); !ok {
		t.Fatalf("v2 AdminRequestUsage = %T, want the tenant adapter", sealed.AdminRequestUsage)
	}
}
