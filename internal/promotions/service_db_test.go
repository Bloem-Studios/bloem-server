package promotions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

// newMigratedTestPool mirrors the ambience / notifications DB tests: a
// disposable database with every migration applied, skipped without
// SILO_TEST_DATABASE_URL unless SILO_REQUIRE_TEST_DATABASE=1.
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
	name := "bloem_promotions_" + hex.EncodeToString(random[:])
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

func seedAccount(t *testing.T, pool *pgxpool.Pool, username, role string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', $3, true) RETURNING id`, username, username+"@example.test", role).Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return id
}

func seedOrganization(t *testing.T, pool *pgxpool.Pool, slug string, owner int, members ...int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id)
		VALUES ($1, $1, 'active', $2) RETURNING id`, slug, owner).Scan(&id); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id, name, is_default) VALUES ($1, $2, true)`, id, slug+" default"); err != nil {
		t.Fatalf("seed access group: %v", err)
	}
	for _, m := range members {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
			VALUES ($1, $2, 'active', 'user')
			ON CONFLICT (organization_id, account_id) DO UPDATE SET status = 'active'`, id, m); err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}
	return id
}

func ids(cards []Card) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.ID)
	}
	return out
}

func TestServiceCRUDRoundTrip(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	svc := NewService(pool, recipes.FixedClock(promoStart), nil)

	in := validInput()
	w, h := 1920, 1080
	in.ImageWidth, in.ImageHeight = &w, &h
	pos := 2
	in.Placement = Placement{HomePosition: &pos, DetailSlot: "below_hero", ContentIDs: []string{"movie-1"}}
	in.Targeting = Targeting{Audience: "role", Role: "admin"}
	created, err := svc.Create(ctx, 7, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedBy != 7 || created.Priority != 5 || !created.Dismissible || created.CTA == nil || created.CTA.Label != "Browse" ||
		*created.ImageWidth != 1920 || *created.Placement.HomePosition != 2 || created.Placement.DetailSlot != "below_hero" || created.Targeting.Role != "admin" || created.OrganizationID != nil {
		t.Fatalf("unexpected created promotion: %+v", created)
	}
	if _, err := svc.Create(ctx, 7, Input{Headline: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid input must not reach the database: %v", err)
	}
	unknownOrg := uuid.New()
	bad := validInput()
	bad.OrganizationID = &unknownOrg
	if _, err := svc.Create(ctx, 7, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown organization must be a validation error: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil || got.Headline != in.Headline || !got.StartsAt.Equal(promoStart) || got.Placement.ContentIDs[0] != "movie-1" {
		t.Fatalf("get: %+v %v", got, err)
	}

	upd := validInput()
	upd.Headline = "Updated"
	upd.CTA = nil
	updated, err := svc.Update(ctx, created.ID, upd)
	if err != nil || updated.Headline != "Updated" || updated.CTA != nil || updated.ImageWidth != nil || updated.Placement.HomePosition != nil || updated.Targeting.Audience != "all" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := svc.Update(ctx, "missing", upd); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := svc.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestActiveFiltersWindowSurfaceOrganizationTargetingAndOrdersByPriority(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	admin := seedAccount(t, pool, "admin", "admin")
	member := seedAccount(t, pool, "member", "user")
	outsider := seedAccount(t, pool, "outsider", "user")
	org := seedOrganization(t, pool, "acme", admin, member)
	otherOrg := seedOrganization(t, pool, "other", admin)

	svc := NewService(pool, recipes.FixedClock(promoStart.Add(24*time.Hour)), nil)
	create := func(name string, mutate func(*Input)) string {
		t.Helper()
		in := validInput()
		in.Headline = name
		mutate(&in)
		p, err := svc.Create(ctx, admin, in)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return p.ID
	}
	low := create("low", func(i *Input) { i.Priority = 1 })
	high := create("high", func(i *Input) { i.Priority = 9 })
	create("expired", func(i *Input) { i.StartsAt, i.EndsAt = promoStart.Add(-48*time.Hour), promoStart })
	create("future", func(i *Input) { i.StartsAt, i.EndsAt = promoEnd, promoEnd.Add(time.Hour) })
	create("detail only", func(i *Input) { i.Surfaces = []string{"detail"} })
	orgOnly := create("org only", func(i *Input) { i.OrganizationID = &org; i.Priority = 20 })
	create("other org", func(i *Input) { i.OrganizationID = &otherOrg; i.Priority = 30 })
	adminsOnly := create("admins", func(i *Input) { i.Targeting = Targeting{Audience: "role", Role: "admin"}; i.Priority = 40 })
	explicit := create("explicit", func(i *Input) {
		i.Targeting = Targeting{Audience: "explicit", UserIDs: []int{outsider}}
		i.Priority = 50
	})
	orgTargeted := create("org targeted", func(i *Input) {
		i.Targeting = Targeting{Audience: "organization", OrganizationID: org.String()}
		i.Priority = 60
	})
	libraryTargeted := create("library", func(i *Input) { i.Targeting = Targeting{Audience: "library", LibraryID: 3}; i.Priority = 70 })

	assert := func(name string, v Viewer, want ...string) {
		t.Helper()
		cards, err := svc.Active(ctx, Query{Surface: SurfaceHome, Viewer: v})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := ids(cards)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v want %v", name, got, want)
			}
		}
	}
	// Unrestricted library access sees library-targeted rows; ordering is priority DESC.
	assert("member", Viewer{UserID: member, ProfileID: "p-member"}, libraryTargeted, orgTargeted, orgOnly, high, low)
	// The owner is not seeded as a member, so organization rows stay hidden for it.
	assert("admin", Viewer{UserID: admin, ProfileID: "p-admin"}, libraryTargeted, adminsOnly, high, low)
	assert("outsider", Viewer{UserID: outsider, ProfileID: "p-out"}, libraryTargeted, explicit, high, low)
	assert("outsider restricted", Viewer{UserID: outsider, ProfileID: "p-out", LibraryIDs: []int{1, 2}}, explicit, high, low)
	assert("outsider with library", Viewer{UserID: outsider, ProfileID: "p-out", LibraryIDs: []int{3}}, libraryTargeted, explicit, high, low)

	if _, err := svc.Active(ctx, Query{Surface: "login"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown surface: %v", err)
	}
}

func TestActiveHonoursContentIDsAndDismissals(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	user := seedAccount(t, pool, "viewer", "user")
	stores := pgstore.NewPostgresProvider(pool)
	svc := NewService(pool, recipes.FixedClock(promoStart.Add(time.Hour)), stores)

	in := validInput()
	in.Surfaces = []string{"detail", "pre_playback"}
	in.Placement.ContentIDs = []string{"movie-1"}
	scoped, err := svc.Create(ctx, user, in)
	if err != nil {
		t.Fatal(err)
	}
	in = validInput()
	in.Surfaces = []string{"detail", "pre_playback", "home"}
	in.Priority = 1
	general, err := svc.Create(ctx, user, in)
	if err != nil {
		t.Fatal(err)
	}
	v := Viewer{UserID: user, ProfileID: "p1"}

	cards, err := svc.Active(ctx, Query{Surface: SurfaceDetail, ContentID: "movie-1", Viewer: v})
	if err != nil || len(cards) != 2 || cards[0].ID != scoped.ID {
		t.Fatalf("detail movie-1: %v %v", ids(cards), err)
	}
	cards, err = svc.Active(ctx, Query{Surface: SurfaceDetail, ContentID: "movie-2", Viewer: v})
	if err != nil || len(cards) != 1 || cards[0].ID != general.ID {
		t.Fatalf("detail movie-2: %v %v", ids(cards), err)
	}
	cards, err = svc.Active(ctx, Query{Surface: SurfacePrePlayback, Viewer: v})
	if err != nil || len(cards) != 1 || cards[0].ID != general.ID {
		t.Fatalf("pre_playback without content id: %v %v", ids(cards), err)
	}

	// Dismissal round trip through the existing per-profile home dismissal
	// store: dismissing on one surface hides the card on that surface only.
	store, err := stores.ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertHomeDismissal(ctx, userstore.HomeItemDismissal{ProfileID: "p1", Surface: DismissalSurface(SurfaceDetail), MediaItemID: general.ID, DismissedAt: promoStart.Format(time.RFC3339)}); err != nil {
		t.Fatalf("upsert promo dismissal: %v", err)
	}
	cards, err = svc.Active(ctx, Query{Surface: SurfaceDetail, ContentID: "movie-2", Viewer: v})
	if err != nil || len(cards) != 0 {
		t.Fatalf("dismissed detail card must be filtered: %v %v", ids(cards), err)
	}
	cards, position, err := svc.ActiveHome(ctx, v)
	if err != nil || len(cards) != 1 || cards[0].ID != general.ID || position != DefaultHomePosition {
		t.Fatalf("home unaffected by detail dismissal: %v %d %v", ids(cards), position, err)
	}
	other, err := stores.ForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := other.ListHomeDismissals(ctx, "p2", DismissalSurface(SurfaceDetail)); err != nil || len(rows) != 0 {
		t.Fatalf("dismissals are per profile: %v %v", rows, err)
	}
	if err := store.DeleteHomeDismissal(ctx, "p1", DismissalSurface(SurfaceDetail), general.ID); err != nil {
		t.Fatal(err)
	}
	cards, err = svc.Active(ctx, Query{Surface: SurfaceDetail, ContentID: "movie-2", Viewer: v})
	if err != nil || len(cards) != 1 {
		t.Fatalf("undismissed card returns: %v %v", ids(cards), err)
	}
	_ = notifications.AudienceAll
}

func TestActiveHomeUsesFirstCardsPlacementPosition(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	user := seedAccount(t, pool, "viewer", "user")
	svc := NewService(pool, recipes.FixedClock(promoStart.Add(time.Hour)), nil)
	pos := 3
	in := validInput()
	in.Priority = 9
	in.Placement.HomePosition = &pos
	if _, err := svc.Create(ctx, user, in); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, user, validInput()); err != nil {
		t.Fatal(err)
	}
	cards, position, err := svc.ActiveHome(ctx, Viewer{UserID: user, ProfileID: "p1"})
	if err != nil || len(cards) != 2 || position != 3 {
		t.Fatalf("home: %v %d %v", ids(cards), position, err)
	}
}
