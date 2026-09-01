package remote

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestPostgresStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	user, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "remote-" + uuid.NewString(), Email: uuid.NewString() + "@remote.test", Password: "test-password", Role: models.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	store := NewPostgresStore(pool)
	sessionID := "session-" + uuid.NewString()

	if err := store.UpsertDeviceCapability(ctx, DeviceCapability{UserID: user.ID, ProfileID: "p1", DeviceID: "dev-1", Commands: []playback.CommandName{playback.CommandSeek, playback.CommandPause, playback.CommandPause}}); err != nil {
		t.Fatalf("upsert capability: %v", err)
	}
	if err := store.UpsertDeviceCapability(ctx, DeviceCapability{UserID: user.ID, ProfileID: "p1", DeviceID: "dev-1", Version: 2, Commands: []playback.CommandName{CommandReplan}}); err != nil {
		t.Fatalf("upsert capability again: %v", err)
	}
	capability, err := store.GetDeviceCapability(ctx, user.ID, "p1", "dev-1")
	if err != nil || capability == nil || capability.Version != 2 || !capability.Supports(CommandReplan) || capability.Supports(playback.CommandSeek) {
		t.Fatalf("capability = %+v err = %v", capability, err)
	}
	if missing, err := store.GetDeviceCapability(ctx, user.ID, "p1", "nope"); err != nil || missing != nil {
		t.Fatalf("missing capability = %+v err = %v", missing, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(time.Minute)
	cmd := &Command{ID: uuid.NewString(), Scope: ScopeSession, TargetSessionID: sessionID, TargetDeviceID: "dev-1", TargetUserID: user.ID, TargetProfileID: "p1", TenantID: "t1",
		Name: CommandReplan, Payload: json.RawMessage(`{"overrides":{"transcode":"force"}}`), IssuedBy: "admin:1", IssuerKind: IssuerAdmin, Reason: "buffering", State: StateSent, CreatedAt: now, SentAt: &now, ExpiresAt: &expires}
	if err := store.Insert(ctx, cmd); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.Get(ctx, cmd.ID)
	if err != nil || got.State != StateSent || got.Name != CommandReplan || got.SentAt == nil || got.Result != nil || string(got.Payload) != `{"overrides": {"transcode": "force"}}` {
		t.Fatalf("get = %+v err = %v", got, err)
	}
	if ok, err := store.Transition(ctx, cmd.ID, StateAccepted, nil, "", now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("ack transition ok=%v err=%v", ok, err)
	}
	ids, err := store.TransitionOpenSessionCommands(ctx, sessionID, CommandReplan, StateDone, json.RawMessage(`{"plan_id":"p2"}`), "", now.Add(2*time.Second))
	if err != nil || len(ids) != 1 || ids[0] != cmd.ID {
		t.Fatalf("session transition ids=%v err=%v", ids, err)
	}
	got, _ = store.Get(ctx, cmd.ID)
	if got.State != StateDone || got.AckedAt == nil || got.FinishedAt == nil || string(got.Result) != `{"plan_id": "p2"}` {
		t.Fatalf("done row = %+v", got)
	}
	if ok, _ := store.Transition(ctx, cmd.ID, StateRejected, nil, "late", now.Add(3*time.Second)); ok {
		t.Fatal("terminal row must not transition")
	}
	rows, err := store.ListAudit(ctx, AuditQuery{SessionID: sessionID, IssuerKind: IssuerAdmin, TenantID: "t1", Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].ID != cmd.ID {
		t.Fatalf("audit rows=%+v err=%v", rows, err)
	}
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	latest, err := store.LatestSessionCommand(ctx, sessionID, CommandReplan, []State{StateSent, StateAccepted, StateDone})
	if err != nil || latest == nil || latest.ID != cmd.ID {
		t.Fatalf("latest = %+v err = %v", latest, err)
	}
	if none, err := store.LatestSessionCommand(ctx, sessionID, CommandReplan, []State{StateSent}); err != nil || none != nil {
		t.Fatalf("latest in other states = %+v err = %v", none, err)
	}
	open := &Command{ID: uuid.NewString(), Scope: ScopeSession, TargetSessionID: sessionID, TargetUserID: user.ID, Name: playback.CommandPause, IssuedBy: "admin:1", IssuerKind: IssuerAdmin, State: StateSent, CreatedAt: now.Add(time.Minute)}
	if err := store.Insert(ctx, open); err != nil {
		t.Fatal(err)
	}
	ids, err = store.TransitionOpenSessionCommands(ctx, sessionID, "", StateExpired, nil, "session ended", now.Add(2*time.Minute))
	if err != nil || len(ids) != 1 || ids[0] != open.ID {
		t.Fatalf("any-name transition ids=%v err=%v", ids, err)
	}
	if err := store.DeleteDeviceCapability(ctx, user.ID, "p1", "dev-1"); err != nil {
		t.Fatal(err)
	}
	if gone, err := store.GetDeviceCapability(ctx, user.ID, "p1", "dev-1"); err != nil || gone != nil {
		t.Fatalf("capability after delete = %+v err = %v", gone, err)
	}
}
