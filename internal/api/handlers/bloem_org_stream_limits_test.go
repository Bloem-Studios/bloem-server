package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type orgInsightsSessions struct {
	byOrg map[uuid.UUID][]AdminPlaybackSessionView
}

func (s orgInsightsSessions) ReadAdminPlaybackSessions(_ context.Context, q PlaybackSessionsQuery, after string, _ int) ([]AdminPlaybackSessionView, error) {
	if after != "" {
		return nil, nil
	}
	return s.byOrg[q.OrganizationID], nil
}

func seedOrgInsights(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for id, name := range map[int]string{9311: "org-a-member", 9312: "org-b-member", 9313: "org-a-owner", 9314: "org-b-owner"} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, username, email, password_hash, role) VALUES ($1, $2, $3, '', 'user') ON CONFLICT (id) DO NOTHING`,
			id, name, name+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	var orgA, orgB uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ('si-org-a', 'A', 'active', 9313) RETURNING id`).Scan(&orgA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ('si-org-b', 'B', 'active', 9314) RETURNING id`).Scan(&orgB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organization_memberships WHERE account_id IN (9311, 9312)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role) VALUES ($1, 9311, 'active', 'user'), ($2, 9312, 'active', 'user')`, orgA, orgB); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_limit_refusals (user_id, kind, scope, refused_at) VALUES (9311, 'streams', 'person', $1), (9312, 'transcodes', 'person', $1)`, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM bloem_limit_refusals WHERE user_id IN (9311, 9312)`)
		_, _ = pool.Exec(ctx, `DELETE FROM organization_memberships WHERE organization_id IN ($1, $2)`, orgA, orgB)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id IN ($1, $2)`, orgA, orgB)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id BETWEEN 9311 AND 9314`)
	})
	return orgA, orgB
}

func TestOrganizationStreamLimitsSeeOnlyTheirMembers(t *testing.T) {
	pool := insightsTestPool(t)
	orgA, orgB := seedOrgInsights(t, pool)
	sessions := orgInsightsSessions{byOrg: map[uuid.UUID][]AdminPlaybackSessionView{
		orgA: {{SessionID: "si-test-a", UserID: 9311, Username: "org-a-member", EffectivePlayMethod: "transcode", ClientIP: "198.51.100.7"}},
		orgB: {{SessionID: "si-test-b", UserID: 9312, Username: "org-b-member", EffectivePlayMethod: "direct"}},
	}}
	h := NewBloemStreamInsightsHandler(BloemStreamInsightsConfig{Store: bloeminsights.NewStore(pool), Sessions: sessions,
		Limits: func(context.Context, int) (playback.SessionLimits, error) {
			return playback.SessionLimits{MaxStreams: 1}, nil
		}})
	membership := uuid.New()
	claims := auth.AdminContextClaims{AccountID: 9313, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopeOrganization, OrganizationID: orgA, MembershipID: membership}
	tenant := tenancy.Context{OrganizationID: orgA, MembershipID: membership, AccountID: 9313}
	router := func(c *auth.AdminContextClaims, tc *tenancy.Context) http.Handler {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := req.Context()
				if c != nil {
					ctx = middleware.SetAdminContextClaims(ctx, *c)
				}
				if tc != nil {
					ctx = tenancy.WithContext(ctx, *tc)
				}
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		h.MountOrganization(r)
		return r
	}
	get := func(handler http.Handler, path string) (int, string) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	admin := router(&claims, &tenant)
	code, body := get(admin, "/organization/limits/usage")
	if code != 200 || !strings.Contains(body, `"org-a-member"`) || strings.Contains(body, "org-b-member") {
		t.Fatalf("usage = %d %s", code, body)
	}
	code, body = get(admin, "/organization/limits/refusals?days=7")
	if code != 200 || !strings.Contains(body, `"org-a-member"`) || strings.Contains(body, "org-b-member") {
		t.Fatalf("refusals = %d %s", code, body)
	}
	forbidden := regexp.MustCompile(`"(viewer|edge|cloudflare|transport|lat|lon|client_ip)"`)
	for _, path := range []string{"/organization/limits/usage", "/organization/limits/refusals", "/organization/stats/top-people",
		"/organization/stats/heatmap?tz=Europe/Amsterdam", "/organization/stats/clients", "/organization/stats/transcode-causes"} {
		code, body := get(admin, path)
		if code != 200 {
			t.Fatalf("%s = %d %s", path, code, body)
		}
		if key := forbidden.FindString(body); key != "" {
			t.Fatalf("%s carries %s: %s", path, key, body)
		}
	}
	platform := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopePlatform}
	if code, body := get(router(&platform, nil), "/organization/limits/usage"); code != http.StatusForbidden || !strings.Contains(body, "insufficient_organization_authority") {
		t.Fatalf("platform token on an organization route = %d %s", code, body)
	}
	other := tenancy.Context{OrganizationID: orgB, MembershipID: membership, AccountID: 9313}
	if code, _ := get(router(&claims, &other), "/organization/limits/usage"); code != http.StatusForbidden {
		t.Fatalf("a context for another organization = %d, want 403", code)
	}
	if code, _ := get(admin, "/organization/stats/clients?days=0"); code != http.StatusUnprocessableEntity {
		t.Fatalf("days=0 = %d, want 422", code)
	}
}

func TestOrganizationStreamLimitsCapabilityToken(t *testing.T) {
	system := NewBloemSystemHandler(nil)
	probe := func() string {
		rec := httptest.NewRecorder()
		system.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
		return rec.Body.String()
	}
	if strings.Contains(probe(), OrgStreamLimitsCapabilityToken) {
		t.Fatal("advertised before the routes are mounted")
	}
	system.SetOrgStreamLimitsAvailable(true)
	if !strings.Contains(probe(), `"`+OrgStreamLimitsCapabilityToken+`"`) {
		t.Fatal("not advertised once mounted")
	}
}
