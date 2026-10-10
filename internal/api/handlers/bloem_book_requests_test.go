package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type bookRequestsServiceStub struct {
	viewer  bookrequests.Viewer
	key     string
	action  string
	id      string
	in      bookrequests.CreateInput
	input   bookrequests.ActionInput
	calls   int
	err     error
	created bool
}

func (s *bookRequestsServiceStub) record(v bookrequests.Viewer) { s.viewer = v; s.calls++ }
func (s *bookRequestsServiceStub) Capability(_ context.Context, v bookrequests.Viewer) (bookrequests.Capability, error) {
	s.record(v)
	return bookrequests.Capability{Available: true}, s.err
}
func (s *bookRequestsServiceStub) List(_ context.Context, v bookrequests.Viewer, _, _ int) ([]bookrequests.Request, error) {
	s.record(v)
	return nil, s.err
}
func (s *bookRequestsServiceStub) Get(_ context.Context, v bookrequests.Viewer, id string) (bookrequests.Request, error) {
	s.record(v)
	s.id = id
	return bookrequests.Request{ID: id}, s.err
}
func (s *bookRequestsServiceStub) Create(_ context.Context, v bookrequests.Viewer, in bookrequests.CreateInput, key string) (bookrequests.Request, bool, error) {
	s.record(v)
	s.key, s.in = key, in
	return bookrequests.Request{ID: "request"}, s.created, s.err
}
func (s *bookRequestsServiceStub) Act(_ context.Context, v bookrequests.Viewer, id, action string, in bookrequests.ActionInput) (bookrequests.Request, error) {
	s.record(v)
	s.id, s.action, s.input = id, action, in
	return bookrequests.Request{ID: id}, s.err
}

func TestBloemBookRequestViewerAuthority(t *testing.T) {
	org := uuid.New()
	for _, test := range []struct {
		name       string
		change     func(context.Context) context.Context
		profileOrg uuid.UUID
		profileErr error
		want       int
	}{
		{name: "verified hosted organization", want: 200},
		{name: "unauthenticated", change: func(context.Context) context.Context { return context.Background() }, want: 401},
		{name: "unverified profile", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 7, ProfileID: "viewer"})
		}, want: 403},
		{name: "foreign scope account", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 8, ProfileID: "viewer", ProfileVerified: true})
		}, want: 403},
		{name: "foreign scope profile", change: func(ctx context.Context) context.Context {
			return access.SetScope(ctx, access.Scope{UserID: 7, ProfileID: "sibling", ProfileVerified: true})
		}, want: 403},
		{name: "foreign tenant account", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.AccountID++
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "suspended organization", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.OrganizationStatus = tenancy.OrganizationSuspended
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "suspended membership", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.MembershipStatus = tenancy.MembershipSuspended
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "missing revision", change: func(ctx context.Context) context.Context {
			tenant, _ := tenancy.FromContext(ctx)
			tenant.SecurityRevision = 0
			return tenancy.WithContext(ctx, tenant)
		}, want: 403},
		{name: "foreign profile organization", profileOrg: uuid.New(), want: 403},
		{name: "deleted profile", profileErr: tenancy.ErrTenantNotFoundOrHidden, want: 403},
		{name: "resolver unavailable", profileErr: errors.New("private database detail"), want: 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &bookRequestsServiceStub{}
			profileOrg := org
			if test.profileOrg != uuid.Nil {
				profileOrg = test.profileOrg
			}
			h := NewBloemBookRequestsHandler(s, bloemSeasonalProfilesStub{org: profileOrg, err: test.profileErr})
			ctx := seasonalViewerContext(org)
			if test.change != nil {
				ctx = test.change(ctx)
			}
			r := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/book-requests/capability?organization_id="+uuid.NewString(), nil).WithContext(ctx)
			r.Header.Set("X-Organization-Id", uuid.NewString())
			w := httptest.NewRecorder()
			h.HandleCapability(w, r)
			if w.Code != test.want || strings.Contains(w.Body.String(), "private database detail") {
				t.Fatalf("response %d %s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				if s.calls != 1 || s.viewer.OrganizationID != org || s.viewer.UserID != 7 || s.viewer.ProfileID != "viewer" || s.viewer.Admin {
					t.Fatalf("wrong viewer: %+v", s)
				}
			} else if s.calls != 0 {
				t.Fatalf("denied viewer called service: %+v", s)
			}
			if w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("viewer response must not be cached")
			}
		})
	}
}

func TestBloemBookRequestCreateStrictBodyAndIdempotency(t *testing.T) {
	org := uuid.New()
	for _, test := range []struct {
		name, body string
		created    bool
		err        error
		want       int
	}{
		{name: "new", body: `{"title":"Book","author":"Author"}`, created: true, want: 201},
		{name: "replay", body: `{"title":"Book","isbn":"9780306406157"}`, want: 200},
		{name: "unknown authority field", body: `{"title":"Book","organization_id":"foreign"}`, want: 400},
		{name: "extra body", body: `{"title":"Book"}{"title":"Other"}`, want: 400},
		{name: "truncated", body: `{"title":`, want: 400},
		{name: "oversized", body: `{"title":"` + strings.Repeat("x", 17<<10) + `"}`, want: 400},
		{name: "key conflict", body: `{"title":"Book"}`, err: bookrequests.ErrConflict, want: 409},
		{name: "duplicate", body: `{"title":"Book"}`, err: bookrequests.ErrDuplicate, want: 409},
		{name: "quota", body: `{"title":"Book"}`, err: bookrequests.ErrQuota, want: 429},
		{name: "private internal error", body: `{"title":"Book"}`, err: errors.New("private database detail"), want: 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &bookRequestsServiceStub{created: test.created, err: test.err}
			h := NewBloemBookRequestsHandler(s, bloemSeasonalProfilesStub{org: org})
			r := httptest.NewRequest(http.MethodPost, NativeAPIPrefix+"/book-requests", strings.NewReader(test.body)).WithContext(seasonalViewerContext(org))
			r.Header.Set("Idempotency-Key", "stable-client-key-123")
			w := httptest.NewRecorder()
			h.HandleCreate(w, r)
			if w.Code != test.want || strings.Contains(w.Body.String(), "private database detail") {
				t.Fatalf("response %d %s", w.Code, w.Body.String())
			}
			if test.want == 400 {
				if s.calls != 0 {
					t.Fatal("invalid JSON reached service")
				}
			} else if s.calls != 1 || s.key != "stable-client-key-123" {
				t.Fatalf("create authority/key not forwarded: %+v", s)
			}
		})
	}
}

func TestBloemBookRequestAdminContextAndRevision(t *testing.T) {
	org := uuid.New()
	id := uuid.NewString()
	for _, test := range []struct {
		name   string
		change func(context.Context) context.Context
		err    error
		want   int
	}{
		{name: "organization admin", want: 200},
		{name: "platform cannot escalate", change: func(ctx context.Context) context.Context {
			return apimw.SetAdminContextClaims(ctx, auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform})
		}, want: 403},
		{name: "foreign organization context", change: func(ctx context.Context) context.Context {
			claims, _ := apimw.GetAdminContextClaims(ctx)
			claims.OrganizationID = uuid.New()
			return apimw.SetAdminContextClaims(ctx, claims)
		}, want: 403},
		{name: "foreign membership", change: func(ctx context.Context) context.Context {
			claims, _ := apimw.GetAdminContextClaims(ctx)
			claims.MembershipID = uuid.New()
			return apimw.SetAdminContextClaims(ctx, claims)
		}, want: 403},
		{name: "stale request", err: bookrequests.ErrConflict, want: 409},
		{name: "foreign request", err: bookrequests.ErrNotFound, want: 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &bookRequestsServiceStub{err: test.err}
			h := NewBloemBookRequestsHandler(s, nil)
			r := organizationRequest(http.MethodPost, NativeAPIPrefix+"/admin/organization/book-requests/"+id+"/approve", `{"expected_revision":4,"reason":"Reviewed"}`, org, 1, map[string]string{"id": id, "action": "approve"})
			if test.change != nil {
				r = r.WithContext(test.change(r.Context()))
			}
			w := httptest.NewRecorder()
			h.HandleAdminAction(w, r)
			if w.Code != test.want {
				t.Fatalf("response %d %s", w.Code, w.Body.String())
			}
			if test.want == 403 {
				if s.calls != 0 {
					t.Fatal("denied admin called service")
				}
			} else if s.calls != 1 || !s.viewer.Admin || s.viewer.OrganizationID != org || s.viewer.ProfileID != "" || s.input.ExpectedRevision != 4 || s.action != "approve" {
				t.Fatalf("wrong admin input: %+v", s)
			}
		})
	}
	// The route parameter is authoritative; action input cannot redirect it.
	s := &bookRequestsServiceStub{}
	h := NewBloemBookRequestsHandler(s, nil)
	router := chi.NewRouter()
	router.Post("/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		chi.RouteContext(r.Context()).URLParams.Add("action", "approve")
		h.HandleAdminAction(w, r)
	})
	w := httptest.NewRecorder()
	r := organizationRequest(http.MethodPost, "/"+id+"/approve", `{"expected_revision":4,"action":"approve"}`, org, 1, nil)
	router.ServeHTTP(w, r)
	if w.Code != 400 || s.calls != 0 {
		t.Fatalf("unknown action field accepted: %d %+v", w.Code, s)
	}
}

func TestBloemBookRequestInvalidPaginationAndUnavailableAdmin(t *testing.T) {
	org := uuid.New()
	for _, query := range []string{"limit=0", "limit=101", "limit=-1", "offset=-1", "offset=NaN"} {
		s := &bookRequestsServiceStub{}
		h := NewBloemBookRequestsHandler(s, bloemSeasonalProfilesStub{org: org})
		w := httptest.NewRecorder()
		h.HandleMine(w, httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/book-requests/mine?"+query, nil).WithContext(seasonalViewerContext(org)))
		if w.Code != 400 || s.calls != 0 {
			t.Fatalf("pagination %s status=%d service=%+v", query, w.Code, s)
		}
	}
	var h *BloemBookRequestsHandler
	w := httptest.NewRecorder()
	h.HandleAdminList(w, organizationRequest(http.MethodGet, NativeAPIPrefix+"/admin/organization/book-requests", "", org, 1, nil))
	if w.Code != 503 {
		t.Fatalf("unavailable admin status=%d", w.Code)
	}
}
