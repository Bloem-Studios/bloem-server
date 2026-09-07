package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the HTTP sync handler and its shared v2 service against a real first
// admission. No server attempt exists for these client-owned audiobook reports.
func TestFirstAdmissionHTTPProgressSync(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ctx := t.Context()
	name := "sync-admission-" + uuid.NewString()
	var userID int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username) VALUES($1) RETURNING id`, name).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
		if err != nil {
			t.Error(err)
		}
	})
	installation := "ab860a0a-7da8-408d-a8de-5eb0fcd482d2"
	if _, err := pool.Exec(ctx, `INSERT INTO server_settings(key,value) VALUES('diagnostics.server_instance_id',$1),('userdb.backend','postgres') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, installation); err != nil {
		t.Fatal(err)
	}
	provider := pgstore.NewPostgresProvider(pool)
	store, err := provider.ForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewProgressHandler(provider)
	send := func(item string, force bool, queued bool) syncProgressResultItem {
		t.Helper()
		suffix := ""
		if queued {
			suffix = fmt.Sprintf(",\"updated_at\":%q", time.Now().UTC().Format(time.RFC3339Nano))
		}
		body := fmt.Sprintf(`{"items":[{"media_item_id":%q,"position":60,"duration":100,"force_overwrite":%t%s}]}`, item, force, suffix)
		req := httptest.NewRequest("POST", "/api/v1/sync/progress", strings.NewReader(body))
		requestCtx := apimw.SetProfileID(apimw.SetClaims(ctx, &auth.Claims{UserID: userID}), "p")
		out := httptest.NewRecorder()
		handler.HandleSyncProgress(out, req.WithContext(requestCtx))
		var result syncProgressResponse
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &result) != nil || len(result.Results) != 1 {
			t.Fatalf("HTTP %d: %s", out.Code, out.Body.String())
		}
		return result.Results[0]
	}
	for _, item := range []string{"live", "forced", "queued"} {
		if err := store.SetProgress(ctx, "p", item, 20, 100, userstore.ProgressThresholds{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := send("before", true, false); got.Status != "ok" {
		t.Fatalf("pre-admission: %+v", got)
	}
	intent := pgstore.FirstAdmissionIntent{InstallationID: installation, AccountID: userID, ExpectedUsername: name, Backend: "postgres", SourceID: uuid.NewString(), IntentID: uuid.NewString()}
	if _, err := provider.FirstAdmission(ctx, intent, true); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		item          string
		force, queued bool
	}{{"live", false, false}, {"forced", true, false}, {"queued", false, true}} {
		got := send(variant.item, variant.force, variant.queued)
		if got.Status != "error" {
			t.Errorf("admitted unbound %s accepted: %+v", variant.item, got)
		}
		progress, err := store.GetProgress(ctx, "p", variant.item)
		if err != nil || progress == nil || progress.PositionSeconds != 20 {
			t.Errorf("%s changed: %+v %v", variant.item, progress, err)
		}
	}
	// Trusted manual edits and imports are separate store operations. They do
	// not inherit authority from the sync transport or its force/timestamp flags.
	if err := store.SetProgress(ctx, "p", "manual", 35, 100, userstore.ProgressThresholds{}); err != nil {
		t.Fatal(err)
	}
	if changed, err := store.SetProgressIfNewer(ctx, "p", "import", 45, 100, false, time.Now()); err != nil || !changed {
		t.Fatalf("import blocked: %v %v", changed, err)
	}
}
