package notifications

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

// newMigratedTestPool creates a disposable database with every migration
// applied. Skips without SILO_TEST_DATABASE_URL unless
// SILO_REQUIRE_TEST_DATABASE=1 (the repo's test-database convention).
func newMigratedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_alerts_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testConfig.ConnConfig.Database = name
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return pool
}

func insertAlertRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repo *DeliveryRepository, deliveries []Delivery) []InsertedDelivery {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inserted, err := repo.BulkInsert(ctx, tx, deliveries)
	if err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return inserted
}

func rowIDs(rows []DeliveryRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
}

func TestDeliveryRepositoryFiltersExpiredAndDismissed(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	repo := NewDeliveryRepository(pool)
	const profile = "profile-alerts"

	future := time.Now().Add(time.Hour).UTC()
	past := time.Now().Add(-time.Hour).UTC()
	rows := []Delivery{
		{ID: "active", UserID: 1, ProfileID: profile, Type: DeliveryTypeSystemAlert,
			Body: []byte(`{"title":"Active","severity":"warning","dismissible":true,"expires_at":"` + future.Format(time.RFC3339Nano) + `"}`)},
		{ID: "expired", UserID: 1, ProfileID: profile, Type: DeliveryTypeSystemAlert,
			Body: []byte(`{"title":"Expired","severity":"info","dismissible":true,"expires_at":"` + past.Format(time.RFC3339Nano) + `"}`)},
		{ID: "critical", UserID: 1, ProfileID: profile, Type: DeliveryTypeSystemAlert,
			Body: []byte(`{"title":"Critical","severity":"critical","dismissible":false}`)},
		{ID: "plain", UserID: 1, ProfileID: profile, Type: DeliveryTypeWebhookAutoDisabled},
	}
	if got := insertAlertRows(t, ctx, pool, repo, rows); len(got) != 4 {
		t.Fatalf("inserted %d rows, want 4", len(got))
	}

	// expires_at is derived from the body by the single writer.
	var expires *time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM notification_deliveries WHERE id = 'expired'`).Scan(&expires); err != nil || expires == nil {
		t.Fatalf("expires_at not derived from body: %v %v", expires, err)
	}

	inbox, err := repo.ListInbox(ctx, profile, false, false, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(inbox); len(got) != 3 || containsString(got, "expired") {
		t.Fatalf("inbox should hide the expired row: %v", got)
	}
	for _, row := range inbox {
		if row.ID == "active" && (row.ExpiresAt == nil || row.Body == nil) {
			t.Fatalf("active row lost its body/expiry: %+v", row.Delivery)
		}
	}

	// Dismiss: the dismissible row transitions once; the critical row refuses.
	if ok, err := repo.Dismiss(ctx, profile, "active"); err != nil || !ok {
		t.Fatalf("dismiss active: ok=%v err=%v", ok, err)
	}
	if ok, err := repo.Dismiss(ctx, profile, "active"); err != nil || ok {
		t.Fatalf("second dismiss should be a no-op: ok=%v err=%v", ok, err)
	}
	if _, err := repo.Dismiss(ctx, profile, "critical"); !errors.Is(err, ErrDeliveryNotDismissible) {
		t.Fatalf("critical dismiss: got %v, want ErrDeliveryNotDismissible", err)
	}
	if ok, err := repo.Dismiss(ctx, "other-profile", "plain"); err != nil || ok {
		t.Fatalf("dismiss across profiles must not transition: ok=%v err=%v", ok, err)
	}
	if ok, err := repo.Dismiss(ctx, profile, "expired"); err != nil || ok {
		t.Fatalf("expired rows are not dismissible: ok=%v err=%v", ok, err)
	}
	if row, err := repo.GetByID(ctx, profile, "expired"); err != nil || row != nil {
		t.Fatalf("GetByID must hide expired rows: row=%v err=%v", row, err)
	}
	// Plain rows (no body) can be dismissed too.
	if ok, err := repo.Dismiss(ctx, profile, "plain"); err != nil || !ok {
		t.Fatalf("dismiss plain: ok=%v err=%v", ok, err)
	}

	inbox, _ = repo.ListInbox(ctx, profile, false, false, 10, nil)
	if got := rowIDs(inbox); len(got) != 1 || got[0] != "critical" {
		t.Fatalf("default inbox should hide dismissed rows: %v", got)
	}
	inbox, _ = repo.ListInbox(ctx, profile, false, true, 10, nil)
	if got := rowIDs(inbox); len(got) != 3 || containsString(got, "expired") {
		t.Fatalf("include_dismissed inbox: %v", got)
	}
	// Dismiss never touched read state.
	if unread, _ := repo.UnreadCount(ctx, profile); unread != 1 {
		t.Fatalf("unread count = %d, want 1 (only the non-dismissed critical row counts)", unread)
	}
	synced, _ := repo.ListSync(ctx, profile, nil, false, 10)
	if got := rowIDs(synced); len(got) != 1 || got[0] != "critical" {
		t.Fatalf("sync default: %v", got)
	}
	synced, _ = repo.ListSync(ctx, profile, &Cursor{CreatedAt: past, ID: ""}, true, 10)
	if got := rowIDs(synced); len(got) != 3 || containsString(got, "expired") {
		t.Fatalf("sync include_dismissed from cursor: %v", got)
	}
	recent, _ := repo.RecentUnread(ctx, profile, 10)
	if got := rowIDs(recent); len(got) != 1 || got[0] != "critical" {
		t.Fatalf("snapshot: %v", got)
	}
	loaded, err := repo.GetRowsByIDs(ctx, []string{"active", "expired"})
	if err != nil || len(loaded) != 2 {
		t.Fatalf("GetRowsByIDs: %d rows, %v", len(loaded), err)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// recordingDispatcher captures post-commit dispatches.
type recordingDispatcher struct {
	mu   sync.Mutex
	rows []DeliveryRow
}

func (d *recordingDispatcher) Dispatch(_ context.Context, row DeliveryRow) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rows = append(d.rows, row)
	return nil
}

func TestAnnouncementServiceCreateFansOutAndWithdraws(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	dispatcher := &recordingDispatcher{}
	system := &System{
		Deliveries:  NewDeliveryRepository(pool),
		Preferences: NewPreferencesRepository(pool),
		dispatcher:  dispatcher,
		pool:        pool,
		logger:      slog.Default(),
	}
	src := &fakeRecipientSource{
		users:    []*models.User{{ID: 1, Role: models.RoleUser, Enabled: true}, {ID: 2, Role: models.RoleUser, Enabled: true}},
		profiles: map[int][]userstore.Profile{1: {{ID: "p1"}, {ID: "p1-optout"}}, 2: {{ID: "p2"}}},
		orgs:     map[uuid.UUID][]int{},
	}
	svc := &AnnouncementService{system: system, repo: NewAnnouncementRepository(pool), recipients: src, now: time.Now}
	system.Announcements = svc

	// p1-optout has the master toggle off: announcements skip it, alerts do not.
	optOut := DefaultPreferences("p1-optout")
	optOut.Enabled = false
	if err := system.Preferences.Upsert(ctx, optOut); err != nil {
		t.Fatal(err)
	}

	created, err := svc.Create(ctx, 7, AnnouncementInput{
		Type:      DeliveryTypeSystemAnnouncement,
		Body:      AlertBody{Title: "Welcome", Body: "Hi", Severity: "info", Dismissible: true},
		Targeting: AnnouncementTargeting{Audience: AudienceAll},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.RecipientCount != 2 || created.CreatedBy == nil || *created.CreatedBy != 7 {
		t.Fatalf("unexpected announcement: %+v", created)
	}
	if len(dispatcher.rows) != 2 {
		t.Fatalf("realtime dispatch count = %d, want 2", len(dispatcher.rows))
	}
	for _, row := range dispatcher.rows {
		if row.Type != DeliveryTypeSystemAnnouncement || row.AnnouncementID == nil || *row.AnnouncementID != created.ID {
			t.Fatalf("dispatched row not linked to announcement: %+v", row.Delivery)
		}
		if payload := PayloadForRow(row); payload.Title != "Welcome" || payload.Dismissible == nil || !*payload.Dismissible {
			t.Fatalf("dispatched payload lost alert fields: %+v", payload)
		}
	}

	alert, err := svc.Create(ctx, 7, AnnouncementInput{
		Type:      DeliveryTypeSystemAlert,
		Body:      AlertBody{Title: "Outage", Severity: "critical", Dismissible: true},
		Targeting: AnnouncementTargeting{Audience: AudienceExplicit, ProfileIDs: []string{"p1-optout"}},
	})
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	if alert.RecipientCount != 1 || alert.Body.Dismissible {
		t.Fatalf("critical alert should reach opted-out profiles and be non-dismissible: %+v", alert)
	}
	if _, err := svc.Create(ctx, 7, AnnouncementInput{
		Body:      AlertBody{Title: "Nobody"},
		Targeting: AnnouncementTargeting{Audience: AudienceRole, Role: models.RoleAdmin},
	}); !errors.Is(err, ErrAnnouncementNoRecipients) {
		t.Fatalf("empty audience: got %v, want ErrAnnouncementNoRecipients", err)
	}
	if _, err := svc.Create(ctx, 7, AnnouncementInput{Body: AlertBody{Severity: "info"}}); !errors.Is(err, ErrAlertBodyInvalid) {
		t.Fatalf("missing title: got %v, want ErrAlertBodyInvalid", err)
	}

	listed, err := svc.List(ctx)
	if err != nil || len(listed) != 2 || listed[0].ID != alert.ID {
		t.Fatalf("list: %d items (%v), newest first expected", len(listed), err)
	}

	// Withdraw: p1 read the announcement (row expires), p2 did not (row deleted).
	if _, err := system.Deliveries.MarkRead(ctx, "p1", dispatchedID(dispatcher, "p1")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Withdraw(ctx, created.ID); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_deliveries WHERE announcement_id = $1`, created.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("unread rows should be deleted on withdraw; %d remain", remaining)
	}
	for _, profile := range []string{"p1", "p2"} {
		rows, err := system.Deliveries.ListInbox(ctx, profile, false, true, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.AnnouncementID != nil && *row.AnnouncementID == created.ID {
				t.Fatalf("withdrawn announcement still visible to %s", profile)
			}
		}
	}
	// The read-then-withdrawn row is expired: GET /notifications/{id} 404s.
	if row, err := system.Deliveries.GetByID(ctx, "p1", dispatchedID(dispatcher, "p1")); err != nil || row != nil {
		t.Fatalf("withdrawn read row must not be fetchable by id: row=%v err=%v", row, err)
	}
	got, _ := svc.repo.Get(ctx, created.ID)
	if got == nil || got.WithdrawnAt == nil {
		t.Fatalf("announcement should be marked withdrawn: %+v", got)
	}
	if err := svc.Withdraw(ctx, created.ID); err != nil {
		t.Fatalf("withdraw must be idempotent: %v", err)
	}
	if err := svc.Withdraw(ctx, ulid.Make().String()); !errors.Is(err, ErrAnnouncementNotFound) {
		t.Fatalf("withdraw unknown: got %v", err)
	}
}

func dispatchedID(d *recordingDispatcher, profileID string) string {
	for _, row := range d.rows {
		if row.ProfileID == profileID {
			return row.ID
		}
	}
	return ""
}
