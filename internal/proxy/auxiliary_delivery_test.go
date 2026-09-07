package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/google/uuid"
)

func TestProxyAuxiliaryOriginAndSingleHop(t *testing.T) {
	for _, mode := range []string{"pass", "redirect", "disconnect", "missing", "foreign-recipe"} {
		t.Run(mode, func(t *testing.T) {
			card := playback.NewDirectRecipeCard("logical", 7, "profile", 42)
			card.InputPath = "/owned/media"
			card.Executor = &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
			card.TranscodeTransportID = "transport"
			card.RoutingWorkload = string(noderouting.WorkloadDirectPlay)
			card.RoutingExecution = string(noderouting.ExecutionNone)
			card.RoutingEgress = string(noderouting.EgressProxy)
			card.RoutingEgressNodeID = 2
			p := proxyExecutorFixture(t, &card)
			var calls, opened, closed atomic.Int32
			producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get(playback.AuxiliaryTransferHeaderV3) != "exact-permit" || r.Header.Get("X-Silo-Stream-Token") == "" || r.Header.Get("Authorization") != "" || r.Header.Get(playback.OutputTransferHeaderV3) != "" {
					t.Error("wrong internal authority")
				}
				if r.URL.Path != "/internal/playback/auxiliary/logical/subtitles/0.ass" || r.URL.Query().Get("file_id") != "42" || r.URL.Query().Get("token") != "" {
					t.Error("wrong producer request")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "http://untrusted.invalid/")
					w.WriteHeader(302)
					return
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.Header().Set(playback.AuxiliaryTransferHeaderV3, "private")
				w.Header().Set("X-Silo-Stream-Token", "private")
				w.Header().Set("Set-Cookie", "private")
				w.Header().Set("Content-Type", "text/x-ssa")
				_, _ = io.WriteString(w, "owned auxiliary")
			}))
			defer producer.Close()
			open := func(context.Context, string, playback.ExecutorNamespaceV3) (string, func(), error) {
				opened.Add(1)
				return "exact-permit", func() { closed.Add(1) }, nil
			}
			for _, origin := range []string{"", "file:///tmp", "https://user:pass@example.invalid", "http://example.invalid/path", "http://example.invalid?query=1", "http://example.invalid#fragment"} {
				if _, err := p.WithAuxiliaryProducer(origin, open); err == nil {
					t.Fatal("invalid configured origin", origin)
				}
			}
			if mode != "missing" {
				if _, err := p.WithAuxiliaryProducer(producer.URL, open); err != nil {
					t.Fatal(err)
				}
			}
			token, err := streamtoken.Sign(card.ToClaims(), "executor-test", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "foreign-recipe" {
				card.MediaFileID++
			}
			edge := httptest.NewServer(p.Handler())
			defer edge.Close()
			response, err := edge.Client().Get(edge.URL + "/stream/subtitles/" + token + "/0.ass?file_id=42&token=untrusted")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			expected := 503
			if mode == "pass" {
				expected = 200
			}
			if mode == "redirect" {
				expected = 502
			}
			if response.StatusCode != expected {
				t.Fatalf("status %d body %s", response.StatusCode, body)
			}
			if mode == "pass" && string(body) != "owned auxiliary" {
				t.Fatal("body mismatch")
			}
			for _, key := range []string{playback.AuxiliaryTransferHeaderV3, "X-Silo-Stream-Token", "Set-Cookie", "Location"} {
				if response.Header.Get(key) != "" {
					t.Fatal("private response header", key)
				}
			}
			want := int32(1)
			if mode == "missing" || mode == "foreign-recipe" {
				want = 0
			}
			if calls.Load() != want || opened.Load() != want || closed.Load() != want {
				t.Fatalf("calls/permits %d/%d/%d", calls.Load(), opened.Load(), closed.Load())
			}
			if strings.Contains(string(body), "untrusted.invalid") {
				t.Fatal("redirect reflected")
			}
		})
	}
}

func TestProxyAuxiliaryViewerGrantRoute(t *testing.T) {
	card := playback.NewDirectRecipeCard("logical", 7, "profile", 42)
	card.InputPath = "/owned/media"
	card.Executor = &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	card.TranscodeTransportID = "transport"
	card.RoutingWorkload = string(noderouting.WorkloadDirectPlay)
	card.RoutingExecution = string(noderouting.ExecutionNone)
	card.RoutingEgress = string(noderouting.EgressProxy)
	card.RoutingEgressNodeID = 2
	p := proxyExecutorFixture(t, &card)
	cfg := p.watcher.Config()
	cfg.Auth.JWTSecret = grantTestSecret
	p.watcher.SetConfigForTest(cfg)
	p.SetMediaGrantAuthority(stubGrantStore{cards: map[string]playback.RecipeCard{card.SessionID: card}}, stubLoginSessions{valid: map[string]bool{"login-1": true}})
	var calls atomic.Int32
	producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("viewer credential forwarded")
		}
		_, _ = io.WriteString(w, "owned")
	}))
	defer producer.Close()
	if _, err := p.WithAuxiliaryProducer(producer.URL, func(context.Context, string, playback.ExecutorNamespaceV3) (string, func(), error) {
		return "permit", func() {}, nil
	}); err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(p.Handler())
	defer edge.Close()
	for _, tc := range []struct {
		bearer, profile string
		status          int
	}{{grantAccessToken(t, 7, "login-1"), "profile", 200}, {grantAccessToken(t, 8, "login-1"), "profile", 403}, {grantAccessToken(t, 7, "login-1"), "other", 403}, {grantAccessToken(t, 7, "expired"), "profile", 401}} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, edge.URL+"/stream/v3/logical/subtitles/0.ass", nil)
		req.Header.Set("Authorization", "Bearer "+tc.bearer)
		req.Header.Set("X-Profile-Id", tc.profile)
		resp, err := edge.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("viewer authority %d want%d", resp.StatusCode, tc.status)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("refused viewer reached producer", calls.Load())
	}
}

func TestProxyAuxiliaryBrowserPreflight(t *testing.T) {
	card := playback.NewDirectRecipeCard("preflight", 7, "profile", 42)
	server := proxyExecutorFixture(t, &card)
	edge := httptest.NewServer(server.Handler())
	defer edge.Close()
	for _, suffix := range []string{"", "/fonts"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, edge.URL+"/stream/v3/preflight/subtitles/0.ass"+suffix, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "https://web.example.test")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "authorization,x-profile-id")
		response, err := edge.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		allowed := strings.ToLower(strings.Join(response.Header.Values("Access-Control-Allow-Headers"), ","))
		if response.StatusCode != http.StatusOK || response.Header.Get("Access-Control-Allow-Origin") == "" || !strings.Contains(allowed, "authorization") || !strings.Contains(allowed, "x-profile-id") {
			t.Fatalf("auxiliary preflight refused: status=%d headers=%v", response.StatusCode, response.Header)
		}
	}
}
