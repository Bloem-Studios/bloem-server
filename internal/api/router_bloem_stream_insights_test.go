package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/config"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// With a database and a hub, the wiring keeps a stream record for a
// published lifecycle event, and the platform routes it mounts list it.
func TestStreamInsightsWiringRecordsAndServesALifecycleEvent(t *testing.T) {
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bloem_stream_records WHERE session_id = 'si-wiring-1'`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		SetStreamInsightsObserver(nil, nil, "")
		playback.SetAdmissionRefusalObserver(nil)
	})
	hub := evt.NewHub("wiring-test", nil)
	deps := Dependencies{DB: pool, EventsHub: hub, AppContext: ctx, SessionMgr: playback.NewSessionManager(0, 0),
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "stream-insights-wiring-secret-0123456789"}}}
	configureBloemStreamInsights(&deps)
	if deps.StreamInsights == nil || deps.StreamInsights.Store == nil || deps.StreamInsights.Facts == nil {
		t.Fatalf("runtime = %+v", deps.StreamInsights)
	}
	h := bloemStreamInsightsHandler(deps, nil)
	if !h.Available() {
		t.Fatal("no handler with a database")
	}
	ev := bloeminsights.LifecycleEvent{SessionID: "si-wiring-1", Seq: 1, At: time.Now().UTC(), State: "playing",
		Session: &bloeminsights.LifecycleSession{UserID: "7", ProfileID: "p", StartedAt: time.Now().UTC(), PlayMethod: "direct"}}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopePlatform}
			next.ServeHTTP(w, r.WithContext(apimw.SetAdminContextClaims(r.Context(), claims)))
		})
	})
	h.MountPlatform(router)
	deadline := time.Now().Add(10 * time.Second)
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		// The observer subscribes on its own goroutine: publish until it is heard.
		if err := hub.PublishJSON(ctx, evt.ChannelSessions, "session.started", ev, evt.PublishOptions{AdminOnly: true}); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/platform/streams/trails?window=live", nil))
		if rec.Code == 200 && strings.Contains(rec.Body.String(), `"si-wiring-1"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trails never listed the published session: %d %s", rec.Code, rec.Body.String())
		}
		<-poll.C
	}
}

// Without a database nothing is mounted and nothing is advertised.
func TestStreamInsightsWiringWithoutADatabase(t *testing.T) {
	deps := Dependencies{AppContext: context.Background()}
	configureBloemStreamInsights(&deps)
	if deps.StreamInsights != nil {
		t.Fatalf("runtime without a database = %+v", deps.StreamInsights)
	}
	if bloemStreamInsightsHandler(deps, nil) != nil {
		t.Fatal("a handler without a database")
	}
}
