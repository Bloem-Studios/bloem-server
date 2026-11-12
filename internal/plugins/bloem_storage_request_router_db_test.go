//go:build integration

package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

func nativeRouterCapabilities(t *testing.T, pool *pgxpool.Pool, installation int) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT capability_type||':'||capability_id||':'||(metadata->'request_router' IS NOT NULL)::text FROM plugin_capabilities WHERE plugin_installation_id=$1 ORDER BY 1`, installation)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A native storage plugin may also serve request_router.v1. Install and
// upgrade publish its declared request routers as capability rows, which is
// what the request lanes join on, and an upgrade replaces them.
func TestNativeStorageRequestRouterCapabilitiesDB(t *testing.T) {
	r, installed, req := nativeRegistryFixture(t)
	if got := nativeRouterCapabilities(t, r.pool, installed.Installation.ID); len(got) != 0 {
		t.Fatalf("plain storage installation published capabilities %v", got)
	}

	// Upgrade to a release that also serves ebook and audiobook requests.
	next := append(append([]byte(nil), req.Binary...), []byte("requests")...)
	sum := sha256.Sum256(next)
	a := approvedNativeFixture()
	a.Checksum = hex.EncodeToString(sum[:])
	a.Manifest.Checksum, a.Manifest.Version = a.Checksum, "1.1.0"
	a.Manifest.Capabilities = []*publicv1.CapabilityDescriptor{
		{Type: "request_router.v1", Id: "ebooks", RequestRouter: &publicv1.RequestRouterDescriptor{}},
		{Type: "request_router.v1", Id: "audiobooks", RequestRouter: &publicv1.RequestRouterDescriptor{ReportsDownloadProgress: true}},
	}
	approvals, err := newNativeStorageApprovals(map[string]NativeStorageArtifact{"requests": a})
	if err != nil {
		t.Fatalf("request router manifest refused: %v", err)
	}
	r.approved["requests"] = approvals["requests"]
	allow := func(context.Context, pgx.Tx) error { return nil }
	upgraded, err := r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "requests", next, allow)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"request_router.v1:audiobooks:true", "request_router.v1:ebooks:true"}
	if got := nativeRouterCapabilities(t, r.pool, installed.Installation.ID); !slices.Equal(got, want) {
		t.Fatalf("upgrade capabilities %v, want %v", got, want)
	}
	if _, err = r.Snapshot(t.Context(), upgraded.Source.Key, upgraded.Source.OwnerID); err != nil {
		t.Fatalf("upgraded installation not runnable: %v", err)
	}
	// The ordinary plugin service still cannot see the installation.
	hidden := NewNativeStorageHiddenInstallations(NewInstallationStore(r.pool), r)
	if _, err = hidden.ListCapabilities(t.Context(), installed.Installation.ID); !errors.Is(err, ErrInstallationNotFound) {
		t.Fatalf("native capabilities exposed to the plugin service: %v", err)
	}

	// A later release that drops audiobooks drops its row.
	last := append(append([]byte(nil), next...), []byte("ebooks-only")...)
	sum = sha256.Sum256(last)
	b := approvedNativeFixture()
	b.Checksum = hex.EncodeToString(sum[:])
	b.Manifest.Checksum, b.Manifest.Version = b.Checksum, "1.2.0"
	b.Manifest.Capabilities = []*publicv1.CapabilityDescriptor{proto.CloneOf(a.Manifest.Capabilities[0])}
	r.approved["ebooks"] = b
	if _, err = r.UpgradeAuthorized(t.Context(), installed.Installation.ID, installed.Source.Key, installed.Source.OwnerID, "ebooks", last, allow); err != nil {
		t.Fatal(err)
	}
	if got := nativeRouterCapabilities(t, r.pool, installed.Installation.ID); !slices.Equal(got, []string{"request_router.v1:ebooks:true"}) {
		t.Fatalf("capabilities after narrowing upgrade %v", got)
	}

	// A fresh install of a request-serving release publishes its rows too.
	req.ArtifactKey, req.Binary = "ebooks", last
	req.Source.Key, req.Source.ProviderSourceID = [16]byte{}, "second"
	fresh, err := r.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := nativeRouterCapabilities(t, r.pool, fresh.Installation.ID); !slices.Equal(got, []string{"request_router.v1:ebooks:true"}) {
		t.Fatalf("install capabilities %v", got)
	}
}
