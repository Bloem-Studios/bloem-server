package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/artworkstore"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
	"github.com/Silo-Server/silo-server/internal/secret"
)

// withLocalAvatarRowFixture supplies the profile-list capability prerequisite
// without S3: the supported filesystem store and signed artwork delivery.
// Scope it to this row so absent-storage scenarios keep their original wiring.
func (e *Env) withLocalAvatarRowFixture(row scenariocatalog.Row) func() {
	if !e.HasDatabase() || row.Method != http.MethodGet || strings.TrimSuffix(row.Path, "/") != "/api/v1/profiles" {
		return func() {}
	}
	return e.withLocalAvatarFixture()
}

func (e *Env) withLocalAvatarFixture() func() {
	store, err := artworkstore.NewFilesystem(e.t.TempDir())
	if err != nil {
		e.t.Fatalf("scenario executor: local avatar store: %v", err)
	}
	if err := store.Probe(e.ctx); err != nil {
		e.t.Fatalf("scenario executor: local avatar storage unavailable: %v", err)
	}
	signer := artworkurl.NewSigner(jwtSecret, 15*time.Minute)
	cipher, err := secret.New([]byte(masterKey))
	if err != nil {
		e.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	server := httptest.NewServer(api.NewRouter(api.Dependencies{
		Config: e.config(), AppContext: ctx, DB: e.pool, SecretCipher: cipher,
		ClientIPResolver: clientip.NewResolver(nil), NodeID: "fixture-node", PublicURL: publicURL,
		UserStoreProvider: e.stores, PolicySystem: e.policy,
		Artwork: store, ArtworkBackend: artworkstore.BackendLocal,
		ArtworkSigner: signer, ArtworkResolver: artworkurl.NewServerResolver(signer),
	}))
	previous := e.live
	e.live = server
	return func() {
		e.live = previous
		server.Close()
		cancel()
	}
}
