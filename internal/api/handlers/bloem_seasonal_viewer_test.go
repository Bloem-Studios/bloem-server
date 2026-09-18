package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/ambience"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type bloemSeasonalSourceStub struct {
	account int
	org     uuid.UUID
	calls   int
	err     error
}

func (s *bloemSeasonalSourceStub) ActiveForBloemViewer(_ context.Context, account int, org uuid.UUID) ([]ambience.Wire, error) {
	s.account, s.org = account, org
	s.calls++
	return []ambience.Wire{{ID: "eligible"}}, s.err
}

type bloemSeasonalProfilesStub struct {
	org uuid.UUID
	err error
}

func (s bloemSeasonalProfilesStub) ProfileOrganization(_ context.Context, account int, profile string) (uuid.UUID, error) {
	if account != 7 || profile != "viewer" {
		return uuid.Nil, tenancy.ErrTenantNotFoundOrHidden
	}
	return s.org, s.err
}
func seasonalViewerContext(org uuid.UUID) context.Context {
	ctx := apimw.SetClaims(context.Background(), &auth.Claims{UserID: 7, Role: "admin"})
	ctx = tenancy.WithContext(ctx, tenancy.Context{AccountID: 7, OrganizationID: org, MembershipID: uuid.New(), MembershipStatus: tenancy.MembershipActive, OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: 1, SecurityRevision: 1})
	ctx = apimw.SetProfileID(ctx, "viewer")
	return access.SetScope(ctx, access.Scope{UserID: 7, ProfileID: "viewer", ProfileVerified: true})
}

func TestBloemSeasonalViewerRequiresMatchingAuthenticatedTenantProfile(t *testing.T) {
	org := uuid.New()
	for _, test := range []struct {
		name       string
		change     func(context.Context) context.Context
		profileOrg uuid.UUID
		profileErr error
		want       int
	}{
		{name: "eligible", want: 200},
		{name: "unauthenticated", change: func(context.Context) context.Context { return context.Background() }, want: 401},
		{name: "missing tenant", change: func(ctx context.Context) context.Context { return tenancy.WithContext(ctx, tenancy.Context{}) }, want: 403},
		{name: "other account tenant", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.AccountID++
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "inactive tenant", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.OrganizationStatus = tenancy.OrganizationSuspended
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "inactive membership", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.MembershipStatus = tenancy.MembershipSuspended
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "missing profile", change: func(ctx context.Context) context.Context { return apimw.SetProfileID(ctx, "") }, want: 403},
		{name: "unverified profile", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 7, ProfileID: "viewer"})
		}, want: 403},
		{name: "different resolved profile", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 7, ProfileID: "sibling", ProfileVerified: true})
		}, want: 403},
		{name: "different resolved account", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 8, ProfileID: "viewer", ProfileVerified: true})
		}, want: 403},
		{name: "foreign profile organization", profileOrg: uuid.New(), want: 403},
		{name: "foreign profile owner", profileErr: tenancy.ErrTenantNotFoundOrHidden, want: 403},
		{name: "profile lookup unavailable", profileErr: errors.New("private database detail"), want: 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &bloemSeasonalSourceStub{}
			profileOrg := org
			if test.profileOrg != uuid.Nil {
				profileOrg = test.profileOrg
			}
			h := &BloemSeasonalViewerHandler{source: source, profiles: bloemSeasonalProfilesStub{org: profileOrg, err: test.profileErr}}
			ctx := seasonalViewerContext(org)
			if test.change != nil {
				ctx = test.change(ctx)
			}
			req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/ambience?organization_id="+uuid.NewString()+"&account_id=99", nil).WithContext(ctx)
			req.Header.Set("X-Organization-Id", uuid.NewString())
			rec := httptest.NewRecorder()
			h.HandleGet(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if test.want == 200 {
				if source.calls != 1 || source.account != 7 || source.org != org || !strings.Contains(rec.Body.String(), `"id":"eligible"`) {
					t.Fatalf("wrong viewer projection: %+v, body %s", source, rec.Body.String())
				}
			} else if source.calls != 0 || strings.Contains(rec.Body.String(), "eligible") || strings.Contains(rec.Body.String(), "private database detail") {
				t.Fatalf("denied viewer received pack data: %+v %s", source, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("viewer response must not be cached")
			}
		})
	}
}

func TestBloemSeasonalViewerUnavailableFailsClosed(t *testing.T) {
	org := uuid.New()
	for _, h := range []*BloemSeasonalViewerHandler{nil, {}, {source: &bloemSeasonalSourceStub{err: errors.New("private database detail")}, profiles: bloemSeasonalProfilesStub{org: org}}} {
		rec := httptest.NewRecorder()
		h.HandleGet(rec, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/ambience", nil).WithContext(seasonalViewerContext(org)))
		if rec.Code != 503 || strings.Contains(rec.Body.String(), "eligible") || strings.Contains(rec.Body.String(), "private database detail") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}
