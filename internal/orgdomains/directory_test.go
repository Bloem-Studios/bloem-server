package orgdomains

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

var (
	testDefaultOrg = uuid.MustParse("00000000-0000-7000-8000-000000000001")
	testOrg2       = uuid.MustParse("00000000-0000-7000-8000-000000000002")
)

func testSnapshot() snapshot {
	return snapshot{
		defaultOrg: testDefaultOrg,
		hosts:      map[string]uuid.UUID{"lib.example.org": testDefaultOrg, "main.domain.com": testOrg2},
		primary:    map[uuid.UUID]string{testDefaultOrg: "lib.example.org", testOrg2: "main.domain.com"},
		branding:   map[uuid.UUID]map[string]string{testOrg2: {"branding.server_name": "Main"}},
	}
}

// newTestDirectory serves snap until its time to live runs out.
func newTestDirectory(snap snapshot) *Directory {
	return newDirectory(func(context.Context) (snapshot, error) { return snap, nil }, time.Minute)
}

// requestOrganizationThrough serves req through WrapListener and reports the
// organization the wrapped handler saw.
func requestOrganizationThrough(t *testing.T, resolver *clientip.Resolver, d *Directory, req *http.Request) (uuid.UUID, bool) {
	t.Helper()
	var org uuid.UUID
	var ok bool
	var remote string
	WrapListener(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		org, ok = RequestOrganization(r.Context())
		remote = r.RemoteAddr
	}), resolver, d).ServeHTTP(httptest.NewRecorder(), req)
	if remote != req.RemoteAddr {
		t.Fatalf("WrapListener changed RemoteAddr from %q to %q", req.RemoteAddr, remote)
	}
	return org, ok
}

func TestWrapListenerMapsRegisteredHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "Main.Domain.com:443"
	org, ok := requestOrganizationThrough(t, clientip.NewResolver(nil), newTestDirectory(testSnapshot()), req)
	if !ok || org != testOrg2 {
		t.Fatalf("organization = %v, %v; want org 2", org, ok)
	}
}

func TestWrapListenerUnknownHostHasNoOrganization(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "10.0.0.5:8090"
	if org, ok := requestOrganizationThrough(t, clientip.NewResolver(nil), newTestDirectory(testSnapshot()), req); ok {
		t.Fatalf("unregistered host got organization %v", org)
	}
}

func TestWrapListenerIgnoresUntrustedForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:4000"
	req.Host = "internal.example.net"
	req.Header.Set("X-Forwarded-Host", "main.domain.com")
	if org, ok := requestOrganizationThrough(t, clientip.NewResolver(nil), newTestDirectory(testSnapshot()), req); ok {
		t.Fatalf("an untrusted peer selected organization %v", org)
	}
}

func TestWrapListenerTrustsProxyForwardedHost(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4000"
	req.Host = "internal.example.net"
	req.Header.Set("X-Forwarded-Host", "main.domain.com")
	org, ok := requestOrganizationThrough(t, clientip.NewResolver([]*net.IPNet{loopback}), newTestDirectory(testSnapshot()), req)
	if !ok || org != testOrg2 {
		t.Fatalf("organization = %v, %v; want org 2 from the trusted proxy's host", org, ok)
	}
}

func TestWrapListenerWithoutDirectoryIsTheRoot(t *testing.T) {
	root := http.NotFoundHandler()
	if got := WrapListener(root, nil, nil); got == nil {
		t.Fatal("nil handler")
	}
}

func TestOrganizationForPrefersExplicitThenTenantThenRequest(t *testing.T) {
	explicit, tenant, request := uuid.New(), uuid.New(), uuid.New()
	ctx := WithRequestOrganization(context.Background(), request)
	if got, _ := OrganizationFor(ctx); got != request {
		t.Fatalf("request only: %v", got)
	}
	ctx = tenancy.WithContext(ctx, tenancy.Context{OrganizationID: tenant})
	if got, _ := OrganizationFor(ctx); got != tenant {
		t.Fatalf("tenant over request: %v", got)
	}
	ctx = WithOrganization(ctx, explicit)
	if got, _ := OrganizationFor(ctx); got != explicit {
		t.Fatalf("explicit over tenant: %v", got)
	}
	if _, ok := OrganizationFor(context.Background()); ok {
		t.Fatal("empty context has an organization")
	}
}

func TestDirectoryAnswersFromSnapshot(t *testing.T) {
	d := newTestDirectory(testSnapshot())
	ctx := context.Background()
	if host, ok := d.PrimaryHost(ctx, testOrg2); !ok || host != "main.domain.com" {
		t.Fatalf("PrimaryHost = %q, %v", host, ok)
	}
	if v, ok := d.BrandingValue(ctx, testOrg2, "branding.server_name"); !ok || v != "Main" {
		t.Fatalf("BrandingValue = %q, %v", v, ok)
	}
	if !d.IsDefault(ctx, testDefaultOrg) || d.IsDefault(ctx, testOrg2) || d.IsDefault(ctx, uuid.Nil) {
		t.Fatal("IsDefault answers wrong")
	}
	var nilDirectory *Directory
	if _, ok := nilDirectory.Lookup(ctx, "main.domain.com"); ok {
		t.Fatal("a nil directory maps hosts")
	}
}

func TestDirectoryKeepsLastSnapshotOnLoadError(t *testing.T) {
	calls := 0
	d := newDirectory(func(context.Context) (snapshot, error) {
		calls++
		if calls == 1 {
			return testSnapshot(), nil
		}
		return snapshot{}, errors.New("database unavailable")
	}, time.Minute)
	ctx := context.Background()
	if _, ok := d.Lookup(ctx, "main.domain.com"); !ok {
		t.Fatal("first load")
	}
	d.Invalidate()
	if org, ok := d.Lookup(ctx, "main.domain.com"); !ok || org != testOrg2 {
		t.Fatalf("after a failed reload: %v, %v; want the previous snapshot", org, ok)
	}
	if calls != 2 {
		t.Fatalf("loads = %d, want 2", calls)
	}
}
