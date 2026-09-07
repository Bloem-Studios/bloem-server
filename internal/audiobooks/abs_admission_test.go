package audiobooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/audiobooks/abs"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var absAdmissionMigrations sync.Once
var absAdmissionMigrationError error

type admissionMedia struct{ abs.MediaStore }

func (admissionMedia) GetAudiobookByID(_ context.Context, id string, _ catalog.AccessFilter) (*models.MediaItem, error) {
	return &models.MediaItem{ContentID: id, Type: "audiobook", Title: "Synthetic admission fixture", Runtime: 100}, nil
}
func (admissionMedia) GetMediaFiles(context.Context, string, catalog.AccessFilter) ([]*models.MediaFile, error) {
	return nil, nil
}

type admissionConfig struct{ abs.ConfigProvider }

func (admissionConfig) JWTSecret(context.Context) ([]byte, error) {
	return []byte("synthetic-abs-admission-test-secret"), nil
}

type absAdmissionFixture struct {
	pool     *pgxpool.Pool
	progress *ABSProgressStore
	sessions *ABSPlaybackSessionStore
	router   http.Handler
	token    string
	uid      string
	intent   pgstore.FirstAdmissionIntent
}

func newABSAdmissionFixture(t *testing.T) *absAdmissionFixture {
	t.Helper()
	dsn := os.Getenv("SILO_ABS_ADMISSION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires owned isolated ABS admission PostgreSQL")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Database != "abs_admission_test" || cfg.ConnConfig.Port < 30000 {
		t.Fatal("refusing non-isolated ABS test database")
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.RuntimeParams["application_name"] = "abs-admission-" + uuid.NewString()
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	absAdmissionMigrations.Do(func() { absAdmissionMigrationError = database.RunMigrations(t.Context(), pool, migrations.FS, "sql") })
	if absAdmissionMigrationError != nil {
		t.Fatal(absAdmissionMigrationError)
	}
	name := "abs-admission-" + uuid.NewString()
	var uid int
	if err = pool.QueryRow(t.Context(), `INSERT INTO users(username,role) VALUES($1,'user') RETURNING id`, name).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	installation := "ab860a0a-7da8-408d-a8de-5eb0fcd482d2"
	if _, err = pool.Exec(t.Context(), `INSERT INTO server_settings(key,value) VALUES('diagnostics.server_instance_id',$1),('userdb.backend','postgres') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, installation); err != nil {
		t.Fatal(err)
	}
	f := &absAdmissionFixture{pool: pool, progress: &ABSProgressStore{Pool: pool}, sessions: &ABSPlaybackSessionStore{Pool: pool}, uid: strconv.Itoa(uid), intent: pgstore.FirstAdmissionIntent{InstallationID: installation, AccountID: uid, ExpectedUsername: name, Backend: "postgres", SourceID: uuid.NewString(), IntentID: uuid.NewString()}}
	tokens := &ABSSessionStore{Pool: pool}
	jti := uuid.NewString()
	if err = tokens.InsertToken(t.Context(), abs.ABSToken{JTI: jti, UserID: f.uid, ProfileID: "p", Type: "access", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	f.token, err = abs.IssueAccessToken([]byte("synthetic-abs-admission-test-secret"), f.uid, "p", jti, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := abs.New(abs.Dependencies{MediaStore: admissionMedia{}, ProgressStore: f.progress, PlaybackSessionStore: f.sessions, TokenStore: tokens, Config: admissionConfig{}})
	r := chi.NewRouter()
	h.Mount(r)
	f.router = r
	return f
}
func (f *absAdmissionFixture) admit(t *testing.T) {
	t.Helper()
	if _, err := pgstore.NewPostgresProvider(f.pool).FirstAdmission(t.Context(), f.intent, true); err != nil {
		t.Fatal(err)
	}
}
func (f *absAdmissionFixture) request(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func TestABSAdmissionRefusesHTTPProgress(t *testing.T) {
	f := newABSAdmissionFixture(t)
	if err := f.progress.UpsertProgress(t.Context(), abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "existing", CurrentSeconds: 10, DurationSeconds: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO media_items(content_id,type,title) VALUES('existing','audiobook','Synthetic fixture') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if err := f.sessions.InsertPlaybackSession(t.Context(), abs.ABSPlaybackSession{ID: sid, UserID: f.uid, ProfileID: "p", ContentID: "existing"}); err != nil {
		t.Fatal(err)
	}
	f.admit(t)
	events := &admissionEvents{}
	f.mount(admissionMedia{}, nil, events)
	for _, tc := range []struct{ name, method, path, body string }{
		{"fresh report", "PATCH", "/api/me/progress/fresh", `{"currentTime":50,"duration":100}`},
		{"session sync", "POST", "/api/session/" + sid + "/sync", `{"currentTime":50,"timeListening":10}`},
		{"queued existing", "POST", "/api/session/local", `{"id":"queued","libraryItemId":"existing","currentTime":60}`},
		{"queued fresh", "POST", "/api/session/local", `{"id":"queued-new","libraryItemId":"offline","currentTime":60}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(tc.method, tc.path, tc.body)
			if w.Code < 400 {
				t.Errorf("unbound playback accepted: status %d body %s", w.Code, w.Body.String())
			}
		})
	}
	w := f.request("POST", "/api/session/local-all", `{"sessions":[{"id":"queued","libraryItemId":"existing","currentTime":70}]}`)
	var batch struct {
		Results []struct {
			Success        bool
			ProgressSynced bool `json:"progressSynced"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Success || batch.Results[0].ProgressSynced {
		t.Errorf("queued intent falsely acknowledged: %s", w.Body.String())
	}
	for _, id := range []string{"existing", "fresh", "offline"} {
		row, err := f.progress.GetProgress(t.Context(), f.uid, "p", id)
		if err != nil {
			t.Fatal(err)
		}
		if id == "existing" {
			if row == nil || row.CurrentSeconds != 10 {
				t.Errorf("saved progress changed: %+v", row)
			}
		} else if row != nil {
			t.Errorf("created refused progress: %+v", row)
		}
	}
	if events.count.Load() != 0 {
		t.Fatal("refused playback published success events")
	}
	t.Log(fmt.Sprintf("checked ABS HTTP source account %s after synthetic admission", f.uid))
}

func (f *absAdmissionFixture) awaitLock(t *testing.T, pattern string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $2)`, f.pool.Config().ConnConfig.RuntimeParams["application_name"], pattern).Scan(&waiting)
		if err != nil {
			t.Fatalf("wait for observable lock %q: %v", pattern, err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}
func admissionResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(8 * time.Second):
		t.Fatal("admission/write deadlocked")
		return nil
	}
}
func (f *absAdmissionFixture) startAdmission(ctx context.Context) <-chan error {
	ch := make(chan error, 1)
	go func() { _, err := pgstore.NewPostgresProvider(f.pool).FirstAdmission(ctx, f.intent, true); ch <- err }()
	return ch
}
func TestABSAdmissionOrdersProgressTransactions(t *testing.T) {
	for _, first := range []string{"writer", "admission"} {
		t.Run(first, func(t *testing.T) {
			f := newABSAdmissionFixture(t)
			ctx := userstore.WithLegacyPlaybackWrite(t.Context())
			if err := f.progress.UpsertProgress(t.Context(), abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "ordered", CurrentSeconds: 10, DurationSeconds: 100}); err != nil {
				t.Fatal(err)
			}
			blocker, err := f.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			writes := make(chan error, 1)
			var admissions <-chan error
			if first == "writer" {
				if _, err = blocker.Exec(t.Context(), `SELECT 1 FROM user_watch_progress WHERE user_id=$1 FOR UPDATE`, f.intent.AccountID); err != nil {
					t.Fatal(err)
				}
				go func() { writes <- f.progress.UpdateProgressPosition(ctx, f.uid, "p", "ordered", 30) }()
				f.awaitLock(t, "%UPDATE user_watch_progress%")
				admissions = f.startAdmission(t.Context())
				f.awaitLock(t, "%pg_advisory_xact_lock(hashtextextended%")
			} else {
				if _, err = blocker.Exec(t.Context(), `SELECT 1 FROM users WHERE id=$1 FOR UPDATE`, f.intent.AccountID); err != nil {
					t.Fatal(err)
				}
				admissions = f.startAdmission(t.Context())
				f.awaitLock(t, "%SELECT username FROM users%")
				go func() {
					writes <- f.progress.UpsertProgress(ctx, abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "ordered", CurrentSeconds: 30, DurationSeconds: 100})
				}()
				f.awaitLock(t, "%pg_advisory_xact_lock_shared%")
			}
			if err = blocker.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = admissionResult(t, admissions); err != nil {
				t.Fatal(err)
			}
			err = admissionResult(t, writes)
			if first == "writer" && err != nil {
				t.Fatal(err)
			}
			if first == "admission" && !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
				t.Fatalf("write after admission: %v", err)
			}
			row, err := f.progress.GetProgress(t.Context(), f.uid, "p", "ordered")
			if err != nil {
				t.Fatal(err)
			}
			want := 10.0
			if first == "writer" {
				want = 30
			}
			if row.CurrentSeconds != want {
				t.Fatalf("position %v want %v", row.CurrentSeconds, want)
			}
			if err = f.progress.UpdateProgressPosition(ctx, f.uid, "p", "ordered", 70); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
				t.Fatalf("delayed update: %v", err)
			}
			if err = f.progress.UpsertProgress(ctx, abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "queued-new", CurrentSeconds: 70}); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
				t.Fatalf("delayed upsert: %v", err)
			}
		})
	}
}

type admissionLaunchMedia struct {
	admissionMedia
	lookup func(context.Context)
	files  []*models.MediaFile
}

func (m admissionLaunchMedia) GetMediaFiles(ctx context.Context, _ string, _ catalog.AccessFilter) ([]*models.MediaFile, error) {
	if m.lookup != nil {
		m.lookup(ctx)
	}
	return m.files, nil
}

type admissionNative struct {
	*playback.SessionManager
	nested func(context.Context) error
}

func (n admissionNative) StartSessionWithFilesContext(ctx context.Context, u int, p string, e, r int, m playback.PlayMethod, a bool) (*playback.Session, error) {
	if err := n.nested(ctx); err != nil {
		return nil, err
	}
	return n.SessionManager.StartSessionWithFilesContext(ctx, u, p, e, r, m, a)
}
func (f *absAdmissionFixture) mount(media abs.MediaStore, native abs.PlaybackSessionManager, publisher abs.EventPublisher) {
	h := abs.New(abs.Dependencies{MediaStore: media, ProgressStore: f.progress, PlaybackSessionStore: f.sessions, TokenStore: &ABSSessionStore{Pool: f.pool}, Config: admissionConfig{}, NativeSessions: native, Publisher: publisher})
	r := chi.NewRouter()
	h.Mount(r)
	f.router = r
}
func TestABSAdmissionLaunchLeaseAndNestedMirror(t *testing.T) {
	f := newABSAdmissionFixture(t)
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO media_items(content_id,type,title) VALUES('existing','audiobook','Synthetic fixture') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	var folder, file int
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO media_folders(name,type) VALUES($1,'audiobooks') RETURNING id`, t.TempDir()).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.mp3")
	if err := os.WriteFile(path, []byte("synthetic-audio-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(t.Context(), `INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES('existing',$1,$2) RETURNING id`, folder, path).Scan(&file); err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	once := sync.OnceFunc(func() { close(entered); <-resume })
	media := admissionLaunchMedia{files: []*models.MediaFile{{ID: file, ContentID: "existing", FilePath: path, Duration: 100}}, lookup: func(context.Context) { once() }}
	mgr := playback.NewSessionManager(0, 0)
	native := admissionNative{SessionManager: mgr, nested: func(ctx context.Context) error {
		nested, release, err := f.progress.AcquireLegacyPlaybackAdmission(ctx, f.intent.AccountID)
		if err != nil {
			return err
		}
		defer release()
		return f.progress.UpsertProgress(nested, abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "existing", CurrentSeconds: 20, DurationSeconds: 100})
	}}
	f.mount(media, native, nil)
	responses := make(chan *httptest.ResponseRecorder, 1)
	go func() { responses <- f.request("POST", "/api/items/existing/play", "") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("launch did not enter catalog")
	}
	admissions := f.startAdmission(t.Context())
	f.awaitLock(t, "%pg_advisory_xact_lock(hashtextextended%")
	close(resume)
	var w *httptest.ResponseRecorder
	select {
	case w = <-responses:
	case <-time.After(8 * time.Second):
		t.Fatal("nested launch deadlocked behind admission")
	}
	if w.Code != 200 {
		t.Fatalf("launch: %d %s", w.Code, w.Body.String())
	}
	if err := admissionResult(t, admissions); err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.GetSession(response.ID); err != nil {
		t.Fatal("native mirror missing", err)
	}
	if _, err := f.sessions.GetPlaybackSession(t.Context(), response.ID); err != nil {
		t.Fatal("ABS session missing", err)
	}
	for _, route := range []struct{ method, path string }{{"POST", "/api/items/existing/play"}, {"GET", "/public/session/" + response.ID + "/track/1"}, {"GET", "/api/items/existing/file/0"}} {
		got := f.request(route.method, route.path, "")
		if got.Code != http.StatusConflict {
			t.Errorf("post-admission publication %s: %d", route.path, got.Code)
		}
	}
}

func TestABSAdmissionPreservesExplicitManualEdits(t *testing.T) {
	f := newABSAdmissionFixture(t)
	f.admit(t)
	w := f.request("PATCH", "/api/me/progress/manual", `{"isFinished":true}`)
	if w.Code != 200 {
		t.Fatalf("manual finish: %d %s", w.Code, w.Body.String())
	}
	w = f.request("PATCH", "/api/me/progress/mixed", `{"isFinished":true,"currentTime":100}`)
	if w.Code < 400 {
		t.Fatal("playback completion relabeled manual")
	}
	for _, hidden := range []bool{true, false} {
		if err := f.progress.SetHideFromContinue(t.Context(), f.uid, "p", "manual", hidden); err != nil {
			t.Fatal(err)
		}
	}
	w = f.request("DELETE", "/api/me/progress/manual", "")
	if w.Code != http.StatusNoContent && w.Code != 200 {
		t.Fatalf("manual reset: %d", w.Code)
	}

}

func TestABSAdmissionLeaseRequiresExactPoolAndAccount(t *testing.T) {
	f := newABSAdmissionFixture(t)
	ctx, release, err := f.progress.AcquireLegacyPlaybackAdmission(userstore.WithLegacyPlaybackWrite(t.Context()), f.intent.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	other := newABSAdmissionFixture(t)
	other.admit(t)
	// The held account's lease cannot authorize a different account, even on
	// the exact same pool.
	if err = f.progress.UpsertProgress(ctx, abs.ProgressRow{UserID: other.uid, ProfileID: "p", ContentID: "cross-account", CurrentSeconds: 4}); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("cross-account lease: %v", err)
	}
	second, err := pgxpool.NewWithConfig(t.Context(), f.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	admissions := f.startAdmission(t.Context())
	f.awaitLock(t, "%pg_advisory_xact_lock(hashtextextended%")
	writes := make(chan error, 1)
	go func() {
		writes <- (&ABSProgressStore{Pool: second}).UpsertProgress(ctx, abs.ProgressRow{UserID: f.uid, ProfileID: "p", ContentID: "cross-pool", CurrentSeconds: 4})
	}()
	f.awaitLock(t, "%pg_advisory_xact_lock_shared%")
	release()
	if err = admissionResult(t, admissions); err != nil {
		t.Fatal(err)
	}
	if err = admissionResult(t, writes); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("different-pool lease reused: %v", err)
	}
	if err = f.progress.UpdateProgressPosition(ctx, f.uid, "p", "cross-pool", 8); !errors.Is(err, userstore.ErrPlaybackSourceUnbound) {
		t.Fatalf("released context reused: %v", err)
	}
}

type admissionEvents struct{ count atomic.Int64 }

func (e *admissionEvents) Publish(string, string, any) { e.count.Add(1) }
func (e *admissionEvents) Broadcast(string, any)       { e.count.Add(1) }

type blockedMediaResponse struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *blockedMediaResponse) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.resume })
	return w.ResponseRecorder.Write(b)
}
func TestABSAdmissionReleasesBeforeMediaTransfer(t *testing.T) {
	f := newABSAdmissionFixture(t)
	path := filepath.Join(t.TempDir(), "fixture.mp3")
	if err := os.WriteFile(path, []byte("synthetic-stream-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	f.mount(admissionLaunchMedia{files: []*models.MediaFile{{ID: 1, FilePath: path}}}, nil, nil)
	req := httptest.NewRequest("GET", "/api/items/fixture/file/0", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	w := &blockedMediaResponse{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan struct{})
	go func() { f.router.ServeHTTP(w, req); close(done) }()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not begin")
	}
	admissions := f.startAdmission(t.Context())
	err := admissionResult(t, admissions)
	close(w.resume)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("pre-admission transfer: %d", w.Code)
	}
}
