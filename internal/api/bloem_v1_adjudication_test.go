package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Historical snapshots remain byte-for-byte frozen. These exact downstream
// namespace decisions are recorded in docs/architecture/bloem-contract-adjudications.md.
// No prefix-wide exemption is allowed: every displaced method must be absent at
// v1 and mounted at its native counterpart, including HEAD and all admin writes.
const bloemRelocatedLiveTVRoutes = `DELETE /api/v1/livetv/guide-sources/{sourceId}
DELETE /api/v1/livetv/recordings/{recordingId}
DELETE /api/v1/livetv/series-rules/{ruleId}
DELETE /api/v1/livetv/sessions/{sessionId}
DELETE /api/v1/livetv/tuners/{tunerId}
GET /api/v1/livetv/channels
GET /api/v1/livetv/guide
GET /api/v1/livetv/guide-sources
GET /api/v1/livetv/live-hls/{playbackId}/{name}
GET /api/v1/livetv/programs/{programId}
GET /api/v1/livetv/recordings
GET /api/v1/livetv/series-rules
GET /api/v1/livetv/sessions/{sessionId}/stream
GET /api/v1/livetv/tuners
HEAD /api/v1/livetv/sessions/{sessionId}/stream
PATCH /api/v1/livetv/channels/{channelId}
PATCH /api/v1/livetv/guide-sources/{sourceId}
POST /api/v1/livetv/channels/{channelId}/session
POST /api/v1/livetv/guide-sources
POST /api/v1/livetv/guide-sources/schedules-direct/lineups
POST /api/v1/livetv/guide-sources/xml-sync/lineups
POST /api/v1/livetv/guide-sources/{sourceId}/sync
POST /api/v1/livetv/recordings
POST /api/v1/livetv/series-rules
POST /api/v1/livetv/sessions/{sessionId}/heartbeat
POST /api/v1/livetv/tuners
POST /api/v1/livetv/tuners/discover
POST /api/v1/livetv/tuners/{tunerId}/scan`

var bloemUpstreamOAuthRoutes = []string{
	"GET /api/v1/auth/oauth/{install_id}/callback",
	"POST /api/v1/auth/oauth/complete",
	"POST /api/v1/auth/oauth/{install_id}/init",
}

func bloemAdjudicatedV1Baseline(t *testing.T, file string, raw []byte, router chi.Router) []string {
	t.Helper()
	mounted := make(map[string]bool)
	if err := chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		mounted[method+" "+strings.TrimSuffix(path, "/*")] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want, err := bloemReviewV1Baseline(file, raw, mounted)
	if err != nil {
		t.Fatal(err)
	}
	return want
}

func TestBloemV1AdjudicationClosedSet(t *testing.T) {
	raw, err := os.ReadFile(v1RouteGoldenDatabase)
	if err != nil {
		t.Fatal(err)
	}
	fixture := func() map[string]bool {
		mounted := make(map[string]bool)
		for _, route := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			mounted[route] = true
		}
		for _, route := range strings.Split(bloemRelocatedLiveTVRoutes, "\n") {
			delete(mounted, route)
			mounted[strings.Replace(route, "/api/v1/", "/api/bloem/v1/", 1)] = true
		}
		for _, route := range bloemUpstreamOAuthRoutes {
			mounted[route] = true
		}
		return mounted
	}
	for _, mutation := range []string{"native_missing", "legacy_restored", "oauth_missing", "baseline_changed"} {
		t.Run(mutation, func(t *testing.T) {
			mounted := fixture()
			baseline := raw
			switch mutation {
			case "native_missing":
				delete(mounted, "HEAD /api/bloem/v1/livetv/sessions/{sessionId}/stream")
			case "legacy_restored":
				mounted["POST /api/v1/livetv/tuners"] = true
			case "oauth_missing":
				delete(mounted, bloemUpstreamOAuthRoutes[0])
			case "baseline_changed":
				baseline = append(append([]byte(nil), raw...), '\n')
			}
			if _, err := bloemReviewV1Baseline(v1RouteGoldenDatabase, baseline, mounted); err == nil {
				t.Fatal("accepted unreviewed drift")
			}
		})
	}
	mounted := fixture()
	want, err := bloemReviewV1Baseline(v1RouteGoldenDatabase, raw, mounted)
	if err != nil {
		t.Fatal(err)
	}
	delete(mounted, "GET /api/v1/auth/me")
	mounted["POST /api/v1/unreviewed"] = true
	var actual []string
	for route := range mounted {
		if strings.Contains(route, " /api/v1/") {
			actual = append(actual, route)
		}
	}
	added, removed := routeDifference(actual, want)
	if len(added) != 1 || added[0] != "POST /api/v1/unreviewed" || len(removed) != 1 || removed[0] != "GET /api/v1/auth/me" {
		t.Fatalf("unrelated drift was hidden: added=%v removed=%v", added, removed)
	}
}

func bloemReviewV1Baseline(file string, raw []byte, mounted map[string]bool) ([]string, error) {
	want := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if file == v1RouteGoldenMinimal {
		return want, nil
	}
	pins := map[string]string{
		v1RouteGoldenDatabase: "10b8a996b5580f6f760a037355461ce45cfc7eb96de3b48dac3eaa3f301a7bba",
		v1RouteSiloContract:   "83f68f702ccb313e71d52f37f980ccd592f51a80b810d98ef145d117c52c340e",
	}
	if pin, ok := pins[file]; !ok || fmt.Sprintf("%x", sha256.Sum256(raw)) != pin {
		return nil, fmt.Errorf("unreviewed v1 baseline change: %s", file)
	}
	remaining := make(map[string]bool, len(want))
	for _, route := range want {
		remaining[route] = true
	}
	for _, route := range strings.Split(bloemRelocatedLiveTVRoutes, "\n") {
		native := strings.Replace(route, "/api/v1/livetv/", "/api/bloem/v1/livetv/", 1)
		if !remaining[route] || mounted[route] || !mounted[native] {
			return nil, fmt.Errorf("reviewed Live TV relocation is not exact: %s -> %s", route, native)
		}
		delete(remaining, route)
	}
	if file == v1RouteGoldenDatabase {
		for _, route := range bloemUpstreamOAuthRoutes {
			if remaining[route] || !mounted[route] {
				return nil, fmt.Errorf("reviewed upstream OAuth addition is not exact: %s", route)
			}
			remaining[route] = true
		}
	}
	result := make([]string, 0, len(remaining))
	for route := range remaining {
		result = append(result, route)
	}
	sort.Strings(result)
	return result, nil
}
