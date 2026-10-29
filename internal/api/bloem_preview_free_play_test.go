package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/preview"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

type stoppedSessions []string

func (s *stoppedSessions) StopSession(id string) error { *s = append(*s, id); return nil }

type streamTokenSeen struct {
	called, authorized bool
	grant              string
}

// freePlayStreamFixture is a preview account with free play on, a live and a
// spent grant each bound to a playback session, and a subscriber account.
type freePlayStreamFixture struct {
	t                 *testing.T
	pool              *pgxpool.Pool
	guest, subscriber int
	live, spent       string
	stopped           stoppedSessions
	wrap              func(http.Handler) http.Handler
}

const freePlayStreamSecret = "free-play-stream-secret"

func newFreePlayStreamFixture(t *testing.T) *freePlayStreamFixture {
	t.Helper()
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	guestAccount, err := users.Create(ctx, models.CreateUserInput{Username: "free-play-preview", Email: "free-play-preview@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	subscriber, err := users.Create(ctx, models.CreateUserInput{Username: "free-play-subscriber", Email: "free-play-subscriber@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=true, free_play_enabled=true, account_id=$1, host='browse.example.test', issuer='operator-test', ticket_public_keys=ARRAY['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=']`, guestAccount.ID); err != nil {
		t.Fatal(err)
	}
	f := &freePlayStreamFixture{t: t, pool: pool, guest: guestAccount.ID, subscriber: subscriber.ID, live: "StreamStreamStreamStre", spent: "SpentStreamSpentStream"}
	for _, g := range []string{f.live, f.spent} {
		if _, err = pool.Exec(ctx, `INSERT INTO bloem_preview_play_grants(grant_id,allowance_seconds,expires_at) VALUES($1,1800,now()+interval '1 day')`, g); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_preview_play_grants SET used_seconds=1800 WHERE grant_id=$1`, f.spent); err != nil {
		t.Fatal(err)
	}
	for sid, g := range map[string]string{"play-live": f.live, "play-spent": f.spent} {
		if err = preview.BindPlayback(ctx, pool, sid, g); err != nil {
			t.Fatal(err)
		}
	}
	f.wrap = bloemStreamTokenAuth(apimw.NewAuthMiddleware(nil, nil, nil, nil), freePlayStreamSecret, &bloemFreePlayDelivery{pool: pool, settings: preview.NewReader(preview.NewStore(pool), 0), sessions: &f.stopped})
	return f
}

// serve sends path with a stream token for the account's playback session sid.
func (f *freePlayStreamFixture) serve(account int, sid, path string) (*httptest.ResponseRecorder, streamTokenSeen) {
	f.t.Helper()
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: sid, UserID: account, PlayMethod: "transcode"}, freePlayStreamSecret, time.Hour)
	if err != nil {
		f.t.Fatal(err)
	}
	var got streamTokenSeen
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.called, got.authorized = true, apimw.IsStreamTokenAuthorized(r.Context())
		got.grant, _ = previewcontext.FreePlayGrant(r.Context())
		if got.grant != "" && !previewcontext.Active(r.Context()) {
			f.t.Error("free-play delivery is not marked as a preview request")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	f.wrap(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?st="+token, nil))
	return w, got
}

func (f *freePlayStreamFixture) delivered(grant string) bool {
	f.t.Helper()
	var delivered bool
	if err := f.pool.QueryRow(f.t.Context(), `SELECT last_delivery_at IS NOT NULL FROM bloem_preview_play_grants WHERE grant_id=$1`, grant).Scan(&delivered); err != nil {
		f.t.Fatal(err)
	}
	return delivered
}

// Media requests carry a stream token and no bearer, so free play is metered
// where the token is checked: only the preview account's HLS playlists,
// segments and subtitles pass, and only segments charge the grant.
func TestFreePlayStreamTokensPassOnlyThePreviewAccountsHLS(t *testing.T) {
	f := newFreePlayStreamFixture(t)
	if w, got := f.serve(f.guest, "play-live", "/api/v2/playback/transcode/play-live/master.m3u8"); w.Code != http.StatusNoContent || !got.authorized || got.grant != f.live {
		t.Fatalf("playlist: %d %+v", w.Code, got)
	}
	if f.delivered(f.live) {
		t.Fatal("a playlist fetch charged the meter")
	}
	if w, got := f.serve(f.guest, "play-live", "/api/v2/playback/transcode/play-live/segment/seg_00001.m4s"); w.Code != http.StatusNoContent || got.grant != f.live {
		t.Fatalf("segment: %d %+v", w.Code, got)
	}
	if !f.delivered(f.live) {
		t.Fatal("a segment did not reach the meter")
	}
	if w, got := f.serve(f.guest, "play-live", "/api/v2/stream/play-live/subtitles/2.vtt"); w.Code != http.StatusNoContent || got.grant != f.live {
		t.Fatalf("subtitle: %d %+v", w.Code, got)
	}
	if w, got := f.serve(f.guest, "play-live", "/api/v2/stream/play-live"); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly || got.called {
		t.Fatalf("direct stream for free play: %d %s called=%v", w.Code, w.Header().Get("X-Bloem-Denial"), got.called)
	}
	if w, got := f.serve(f.guest, "play-live", "/api/v2/playback/transcode/play-other/segment/seg_00001.m4s"); w.Code != http.StatusForbidden || got.called {
		t.Fatalf("token for another session: %d called=%v", w.Code, got.called)
	}
	if w, got := f.serve(f.subscriber, "play-subscriber", "/api/v2/stream/play-subscriber"); w.Code != http.StatusNoContent || !got.authorized || got.grant != "" {
		t.Fatalf("subscriber's stream token: %d %+v", w.Code, got)
	}
}

// A spent grant, a session no grant started and free play switched off are
// all refused; the spent grant's playback is stopped.
func TestFreePlayStreamTokensRefuseWhatNoLiveGrantStarted(t *testing.T) {
	f := newFreePlayStreamFixture(t)
	if w, got := f.serve(f.guest, "play-spent", "/api/v2/playback/transcode/play-spent/segment/seg_00009.m4s"); w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodeFreePlayEnded || got.called {
		t.Fatalf("spent grant: %d %s called=%v", w.Code, w.Header().Get("X-Bloem-Denial"), got.called)
	}
	if len(f.stopped) != 1 || f.stopped[0] != "play-spent" {
		t.Fatalf("stopped sessions = %v", f.stopped)
	}
	if w, got := f.serve(f.guest, "play-nobody", "/api/v2/playback/transcode/play-nobody/master.m3u8"); w.Code != http.StatusForbidden || got.called {
		t.Fatalf("playback no grant started: %d called=%v", w.Code, got.called)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE bloem_preview_settings SET free_play_enabled=false`); err != nil {
		t.Fatal(err)
	}
	if w, got := f.serve(f.guest, "play-live", "/api/v2/playback/transcode/play-live/segment/seg_00002.m4s"); w.Code != http.StatusForbidden || got.called {
		t.Fatalf("free play switched off: %d called=%v", w.Code, got.called)
	}
}

// Through the whole router, on v1 and v2, the preview account's stream token
// cannot fetch the progressive original: the free-play wrapper sits in front
// of every stream-token check.
func TestFreePlayStreamTokensGuardBothRouters(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	account, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "free-play-router", Email: "free-play-router@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=true, free_play_enabled=true, account_id=$1, host='browse.example.test', issuer='operator-test', ticket_public_keys=ARRAY['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=']`, account.ID); err != nil {
		t.Fatal(err)
	}
	const secret = "free-play-router-secret"
	router := NewRouter(Dependencies{DB: pool, FolderRepo: catalog.NewFolderRepository(pool), FileRepo: scanner.NewFileRepository(pool), SessionMgr: playback.NewSessionManager(4, 2), UserStoreProvider: pgstore.NewPostgresProvider(pool), Config: &config.Config{Server: config.ServerConfig{PublicURL: "https://configured.example.test"}, Auth: config.AuthConfig{JWTSecret: secret, AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}}})
	sid := uuid.NewString()
	token, err := streamtoken.Sign(streamtoken.Claims{SessionID: sid, UserID: account.ID, PlayMethod: "direct_play"}, secret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v2/stream/" + sid, "/api/v1/stream/" + sid} {
		r := httptest.NewRequest(http.MethodGet, path+"?st="+token, nil)
		r.Host = "browse.example.test"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || w.Header().Get("X-Bloem-Denial") != apimw.CodePreviewOnly {
			t.Errorf("%s: %d %q %s", path, w.Code, w.Header().Get("X-Bloem-Denial"), w.Body)
		}
	}
}

// The gate's side of the service: a bearer media request is charged only to
// the grant its own session holds, and capacity is the preview's setting.
func TestFreePlayServiceDeliversOnlyTheGuestsOwnGrant(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	ctx := t.Context()
	account, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "free-play-service", Email: "free-play-service@example.test", Password: "synthetic-password", Role: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=true, free_play_enabled=true, free_play_max_active=1, account_id=$1, host='browse.example.test', issuer='operator-test', ticket_public_keys=ARRAY['AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=']`, account.ID); err != nil {
		t.Fatal(err)
	}
	mine, theirs := "MineMineMineMineMineMi", "TheirsTheirsTheirsThei"
	for _, g := range []string{mine, theirs} {
		if _, err = pool.Exec(ctx, `INSERT INTO bloem_preview_play_grants(grant_id,allowance_seconds,expires_at) VALUES($1,1800,now()+interval '1 day')`, g); err != nil {
			t.Fatal(err)
		}
	}
	stopped := &stoppedSessions{}
	svc := &bloemFreePlayDelivery{pool: pool, settings: preview.NewReader(preview.NewStore(pool), 0), sessions: stopped}
	if err = svc.BindPlayback(ctx, "bearer-mine", mine); err != nil {
		t.Fatal(err)
	}
	if err = svc.BindPlayback(ctx, "bearer-theirs", theirs); err != nil {
		t.Fatal(err)
	}
	deliver := func(path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(previewcontext.WithFreePlay(previewcontext.With(ctx), mine))
		w := httptest.NewRecorder()
		if _, ok := svc.Deliver(w, r); ok {
			return http.StatusNoContent
		}
		return w.Code
	}
	if code := deliver("/api/v2/playback/transcode/bearer-mine/segment/s1.m4s"); code != http.StatusNoContent {
		t.Fatalf("own segment: %d", code)
	}
	if code := deliver("/api/v2/playback/transcode/bearer-theirs/segment/s1.m4s"); code != http.StatusForbidden {
		t.Fatalf("another grant's segment: %d", code)
	}
	// A bearer request naming a playback session no grant started (someone
	// else's) is refused without touching that session.
	if code := deliver("/api/v2/playback/transcode/subscriber-playback/segment/s1.m4s"); code != http.StatusForbidden || len(*stopped) != 0 {
		t.Fatalf("unbound session: %d, stopped %v", code, *stopped)
	}
	if busy, err := svc.Busy(ctx, theirs); err != nil || !busy {
		t.Fatalf("capacity 1 with %s streaming: busy=%v err=%v", mine, busy, err)
	}
	if busy, err := svc.Busy(ctx, mine); err != nil || busy {
		t.Fatalf("own stream makes busy: busy=%v err=%v", busy, err)
	}
}
