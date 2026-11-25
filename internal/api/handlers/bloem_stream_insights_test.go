package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemgeo"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type insightsSessions struct{ rows []AdminPlaybackSessionView }

func (s insightsSessions) ReadAdminPlaybackSessions(_ context.Context, q PlaybackSessionsQuery, after string, limit int) ([]AdminPlaybackSessionView, error) {
	if after != "" {
		return nil, nil
	}
	return s.rows, nil
}

func bloemInsightsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clean := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bloem_stream_records WHERE session_id LIKE 'si-test-%'`)
		_, _ = pool.Exec(context.Background(), `DELETE FROM bloem_limit_refusals WHERE user_id BETWEEN 9300 AND 9399`)
	}
	clean()
	t.Cleanup(clean)
	return pool
}

func insightsEvent(session string, userID string, at time.Time) bloeminsights.LifecycleEvent {
	node := "4"
	return bloeminsights.LifecycleEvent{SessionID: session, Seq: 1, At: at, State: "playing", Session: &bloeminsights.LifecycleSession{
		UserID: userID, ProfileID: "p1", StartedAt: at, ClientName: "Bloem TV", PlayMethod: "transcode", EffectivePlayMethod: "transcode",
		VideoDecision: "transcode", RoutingExecutionNodeID: &node, RoutingExecutionNodeName: "a310",
	}}
}

type insightsFixture struct {
	handler *BloemStreamInsightsHandler
	router  http.Handler
	claims  *auth.AdminContextClaims
}

func newInsightsFixture(t *testing.T, geo *bloemgeo.Resolver) *insightsFixture {
	t.Helper()
	pool := bloemInsightsTestPool(t)
	store := bloeminsights.NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	viewer := &bloemgeo.Location{City: "Leiden", Country: "Netherlands", CountryCode: "NL", Latitude: 52.16, Longitude: 4.49}
	full := &bloeminsights.FactsSummary{
		Latest: bloeminsights.RequestFacts{Proto: "HTTP/1.1", CFColo: "AMS", Edge: &bloeminsights.EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: "cloudflare"}},
		Counts: map[bloeminsights.Transport]int64{bloeminsights.TransportCloudflare: 3},
	}
	if err := store.ApplyLifecycle(ctx, insightsEvent("si-test-full", "9301", now.Add(-time.Minute)), viewer, full); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, insightsEvent("si-test-plain", "9302", now.Add(-2*time.Minute)), viewer, nil); err != nil {
		t.Fatal(err)
	}
	facts := bloeminsights.NewMemoryFactsStore(time.Hour, nil)
	_ = facts.Record(ctx, "si-test-full", full.Latest)
	node := 4
	sessions := insightsSessions{rows: []AdminPlaybackSessionView{
		{SessionID: "si-test-full", UserID: 9301, Username: "anna", EffectivePlayMethod: "transcode", RoutingExecutionNodeID: &node},
		{SessionID: "si-test-plain", UserID: 9302, Username: "ben", EffectivePlayMethod: "direct"},
	}}
	h := NewBloemStreamInsightsHandler(BloemStreamInsightsConfig{
		Store: store, Facts: facts, Geo: geo, Sessions: sessions,
		Limits: func(context.Context, int) (playback.SessionLimits, error) {
			return playback.SessionLimits{MaxStreams: 2, MaxTranscodes: 1}, nil
		},
		Positions: func(context.Context) bloeminsights.Positions {
			return bloeminsights.Positions{
				Server: &bloeminsights.Hop{Kind: bloeminsights.HopServer, Label: "Home", Latitude: 52, Longitude: 5},
				Nodes:  map[int]bloeminsights.Hop{4: {Kind: bloeminsights.HopNode, Label: "a310", Latitude: 51.9, Longitude: 4.4}},
			}
		},
		WatchTimeSQL: adminPlaybackWatchTimeSQL("h"),
	})
	f := &insightsFixture{handler: h, claims: &auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopePlatform}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if f.claims != nil {
				req = req.WithContext(middleware.SetAdminContextClaims(req.Context(), *f.claims))
			}
			next.ServeHTTP(w, req)
		})
	})
	h.MountPlatform(r)
	f.router = r
	return f
}

func (f *insightsFixture) get(t *testing.T, path string) (int, map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, rec.Body.String()
}

func TestStreamInsightsPlatformRoutes(t *testing.T) {
	f := newInsightsFixture(t, nil)
	code, body, raw := f.get(t, "/platform/stream-insights/capabilities")
	if code != 200 || body["available"] != true || body["locations"] != false || body["edge_header"] != "X-Bloem-Edge" || body["retention_days"] != float64(90) {
		t.Fatalf("capabilities = %d %s", code, raw)
	}
	checkLiveInsights(t, f)
	checkLiveTrails(t, f)
	code, body, raw = f.get(t, "/platform/streams/transport?window=24h")
	if code != 200 || body["counts"].(map[string]any)["cloudflare"].(float64) < 1 || len(body["series"].([]any)) != 24 {
		t.Fatalf("transport = %d %s", code, raw)
	}
	code, body, raw = f.get(t, "/platform/limits/usage")
	if code != 200 || len(body["people"].([]any)) != 2 {
		t.Fatalf("usage = %d %s", code, raw)
	}
	if code, _, raw := f.get(t, "/platform/limits/refusals?days=7"); code != 200 {
		t.Fatalf("refusals = %d %s", code, raw)
	}
	// Platform admins read Silo's playback Insights (/api/v2/admin/insights) for these; only the
	// organization's own stats remain Bloem's.
	for _, path := range []string{"/platform/stats/top-people", "/platform/stats/heatmap", "/platform/stats/clients", "/platform/stats/transcode-causes"} {
		if code, _, raw := f.get(t, path); code != http.StatusNotFound {
			t.Fatalf("%s = %d %s, want 404: the platform stats are retired", path, code, raw)
		}
	}
}

func checkLiveInsights(t *testing.T, f *insightsFixture) {
	t.Helper()
	code, body, raw := f.get(t, "/platform/streams/insights")
	items, _ := body["items"].([]any)
	if code != 200 || len(items) != 2 {
		t.Fatalf("insights = %d %s", code, raw)
	}
	first := items[0].(map[string]any)
	if first["session_id"] != "si-test-full" || first["cloudflare"] == nil || first["edge"] == nil || first["server"] == nil || first["node"] == nil {
		t.Fatalf("full stream = %v", first)
	}
	if transport := first["transport"].(map[string]any); transport["viewer"] != "cloudflare" {
		t.Fatalf("full stream transport = %v", transport)
	}
	limits := first["limits"].(map[string]any)
	if limits["user_id"] != float64(9301) || limits["transcodes"].(map[string]any)["active"] != float64(1) {
		t.Fatalf("full stream limits = %v", limits)
	}
	second := items[1].(map[string]any)
	if second["cloudflare"] != nil || second["edge"] != nil || second["viewer"] == nil {
		t.Fatalf("plain stream = %v, want its recorded viewer and no Cloudflare or edge hop", second)
	}
}

func checkLiveTrails(t *testing.T, f *insightsFixture) {
	t.Helper()
	code, body, raw := f.get(t, "/platform/streams/trails?window=live")
	if code != 200 || body["truncated"] != false {
		t.Fatalf("trails = %d %s", code, raw)
	}
	trails := map[string][]string{}
	for _, item := range body["trails"].([]any) {
		trail := item.(map[string]any)
		var kinds []string
		for _, hop := range trail["hops"].([]any) {
			kinds = append(kinds, hop.(map[string]any)["kind"].(string))
		}
		trails[trail["session_id"].(string)] = kinds
	}
	if got := strings.Join(trails["si-test-full"], ">"); got != "viewer>cloudflare>edge>server>node" {
		t.Fatalf("full trail = %s", got)
	}
	if got := strings.Join(trails["si-test-plain"], ">"); got != "viewer>server>node" {
		t.Fatalf("plain trail = %s", got)
	}
}

func TestStreamInsightsValidation(t *testing.T) {
	f := newInsightsFixture(t, nil)
	for _, path := range []string{"/platform/limits/refusals?days=0", "/platform/limits/refusals?days=91", "/platform/streams/trails?window=30d",
		"/platform/streams/transport?window=x", "/platform/limits/refusals?cursor=not*base64"} {
		if code, _, raw := f.get(t, path); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d %s, want 422", path, code, raw)
		}
	}
}

func TestStreamInsightsNeedPlatformAuthority(t *testing.T) {
	f := newInsightsFixture(t, nil)
	f.claims = &auth.AdminContextClaims{AccountID: 2, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopeOrganization, OrganizationID: uuid.New(), MembershipID: uuid.New()}
	for _, path := range []string{"/platform/stream-insights/capabilities", "/platform/streams/insights", "/platform/streams/trails", "/platform/limits/usage", "/platform/limits/refusals"} {
		if code, _, raw := f.get(t, path); code != http.StatusForbidden || !strings.Contains(raw, "insufficient_platform_authority") {
			t.Fatalf("%s with an organization token = %d %s, want 403", path, code, raw)
		}
	}
	f.claims = nil
	if code, _, _ := f.get(t, "/platform/streams/insights"); code != http.StatusForbidden {
		t.Fatalf("no admin context = %d, want 403", code)
	}
}

func TestStreamInsightsCapabilityTokenFollowsTheRoutes(t *testing.T) {
	probe := func(h *BloemSystemHandler) string {
		rec := httptest.NewRecorder()
		h.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
		return rec.Body.String()
	}
	plain := NewBloemSystemHandler(nil)
	if strings.Contains(probe(plain), StreamInsightsCapabilityToken) {
		t.Fatal("a server without stream insights advertises it")
	}
	system := NewBloemSystemHandler(nil)
	system.SetStreamInsightsAvailable(true)
	if !strings.Contains(probe(system), `"`+StreamInsightsCapabilityToken+`"`) {
		t.Fatal("mounted stream insights are not advertised")
	}
	if (*BloemStreamInsightsHandler)(nil).Available() {
		t.Fatal("a nil handler is available")
	}
	if NewBloemStreamInsightsHandler(BloemStreamInsightsConfig{}) != nil {
		t.Fatal("a handler without a store was built")
	}
}
