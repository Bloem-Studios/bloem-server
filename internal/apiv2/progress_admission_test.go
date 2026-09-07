package apiv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type admissionProgressLookup struct{ handlers.ProgressLibraryLookup }

func (admissionProgressLookup) FilterAccessibleContentIDs(_ context.Context, ids []string, _, _ []int, _ string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func TestSyncProgressFirstAdmissionHTTP(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	name := "v2-sync-admission-" + uuid.NewString()
	var userID int
	if err := pool.QueryRow(t.Context(), `INSERT INTO users(username) VALUES($1) RETURNING id`, name).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Error(err)
		}
	})
	installation := "ab860a0a-7da8-408d-a8de-5eb0fcd482d2"
	if _, err := pool.Exec(t.Context(), `INSERT INTO server_settings(key,value) VALUES('diagnostics.server_instance_id',$1),('userdb.backend','postgres') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, installation); err != nil {
		t.Fatal(err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	store, err := provider.ForUser(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	service := handlers.NewProgressHandler(provider)
	service.LibraryLookup = admissionProgressLookup{}
	deps := pilotDeps(nil, nil)
	deps.Progress = service
	deps.Auth = apimw.NewAuthMiddleware(fakeTokens{map[string]*auth.Claims{memberToken: {UserID: userID, Role: "user", SessionID: "s1", TokenType: auth.TokenTypeAccess}}}, fakeSessions{map[string]bool{"s1": true}}, fakeAPIKeys{}, fakeUsers{map[int]*models.User{userID: {ID: userID, Role: "user", Enabled: true}}})
	h := newTestHandler(t, deps)
	headers := with(bearer(memberToken), "X-Profile-Id", "p-owner")
	send := func() ProgressSyncBatchResult {
		t.Helper()
		body := fmt.Sprintf(`{"items":[{"media_item_id":"live","position_ms":60000,"duration_ms":100000},{"media_item_id":"forced","position_ms":60000,"duration_ms":100000,"force_overwrite":true},{"media_item_id":"queued","position_ms":60000,"duration_ms":100000,"updated_at":%q}]}`, time.Now().UTC().Format(time.RFC3339Nano))
		rec := do(t, h, http.MethodPost, "/api/v2/sync/progress", body, headers)
		var result ProgressSyncBatchResult
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		return result
	}
	if result := send(); result.Summary.Succeeded != 3 {
		t.Fatalf("pre-admission: %+v", result)
	}
	for _, item := range []string{"live", "forced", "queued"} {
		if err := store.SetProgress(t.Context(), "p-owner", item, 20, 100, userstore.ProgressThresholds{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.FirstAdmission(t.Context(), pgstore.FirstAdmissionIntent{InstallationID: installation, AccountID: userID, ExpectedUsername: name, Backend: "postgres", SourceID: uuid.NewString(), IntentID: uuid.NewString()}, true); err != nil {
		t.Fatal(err)
	}
	if result := send(); result.Summary.Failed != 3 || result.Summary.Succeeded != 0 {
		t.Fatalf("unbound reports admitted: %+v", result)
	}
	for _, item := range []string{"live", "forced", "queued"} {
		progress, err := store.GetProgress(t.Context(), "p-owner", item)
		if err != nil || progress == nil || progress.PositionSeconds != 20 {
			t.Fatalf("%s mutated: %+v %v", item, progress, err)
		}
	}
}
