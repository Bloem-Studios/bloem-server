package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// absTenantCheckingDownloads stands in for the download service: under a
// tenant-scoped group policy it refuses a request that carries no validated
// tenant for the account, as downloads.Service does.
type absTenantCheckingDownloads struct{ checks, serves int }

func (d *absTenantCheckingDownloads) requireTenant(ctx context.Context, userID int) error {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.AccountID != userID {
		return errors.New("access group not found")
	}
	return nil
}

func (d *absTenantCheckingDownloads) CheckDownloadAllowed(ctx context.Context, userID int) error {
	d.checks++
	return d.requireTenant(ctx, userID)
}

func (d *absTenantCheckingDownloads) ServeDirect(ctx context.Context, _ http.ResponseWriter, _ *http.Request, userID, _ int, _ string, _ catalog.AccessFilter) error {
	d.serves++
	return d.requireTenant(ctx, userID)
}

type absFakeTenants struct {
	tenant tenancy.Context
	err    error
	asked  []string
}

func (f *absFakeTenants) ResolveSubjectTenant(_ context.Context, accountID int, profileID string) (tenancy.Context, error) {
	f.asked = append(f.asked, profileID)
	if f.err != nil {
		return tenancy.Context{}, f.err
	}
	tenant := f.tenant
	tenant.AccountID = accountID
	return tenant, nil
}

func TestBloemABSDownloadsResolveTheAccountTenant(t *testing.T) {
	service := &absTenantCheckingDownloads{}
	tenants := &absFakeTenants{tenant: tenancy.Context{OrganizationID: uuid.New()}}
	d := bloemABSDownloads{service: service, tenants: tenants}

	if err := d.CheckDownloadAllowed(t.Context(), 7); err != nil {
		t.Fatalf("CheckDownloadAllowed: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/items/x/file/1/download", nil)
	if err := d.ServeDirect(t.Context(), httptest.NewRecorder(), req, 7, 1, "", catalog.AccessFilter{}); err != nil {
		t.Fatalf("ServeDirect: %v", err)
	}
	if service.checks != 1 || service.serves != 1 {
		t.Fatalf("checks/serves = %d/%d, want 1/1", service.checks, service.serves)
	}
	for _, profileID := range tenants.asked {
		if profileID != "" {
			t.Fatalf("resolved profile %q; the Audiobookshelf listener checks the account", profileID)
		}
	}
}

func TestBloemABSDownloadsRefuseWhenTheTenantDoesNotResolve(t *testing.T) {
	service := &absTenantCheckingDownloads{}
	d := bloemABSDownloads{service: service, tenants: &absFakeTenants{err: tenancy.ErrTenantUnavailable}}

	if err := d.CheckDownloadAllowed(t.Context(), 7); !errors.Is(err, downloads.ErrDownloadNotAllowed) {
		t.Fatalf("CheckDownloadAllowed err = %v, want ErrDownloadNotAllowed", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/items/x/file/1/download", nil)
	if err := d.ServeDirect(t.Context(), httptest.NewRecorder(), req, 7, 1, "", catalog.AccessFilter{}); !errors.Is(err, downloads.ErrDownloadNotAllowed) {
		t.Fatalf("ServeDirect err = %v, want ErrDownloadNotAllowed", err)
	}
	if service.checks != 0 || service.serves != 0 {
		t.Fatalf("service reached without a tenant: checks/serves = %d/%d", service.checks, service.serves)
	}
}
