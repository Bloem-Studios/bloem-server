package ambience

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/migrations"
)

// newMigratedTestPool mirrors the notifications DB tests: a disposable
// database with every migration applied, skipped without
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
	name := "bloem_ambience_" + hex.EncodeToString(random[:])
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

func seedAccount(t *testing.T, pool *pgxpool.Pool, username string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', 'user', true) RETURNING id`, username, username+"@example.test").Scan(&id); err != nil {
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
	// The legacy membership-policy trigger requires a default access group
	// for non-admin active members.
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

var (
	winterStart = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	winterEnd   = time.Date(2027, 1, 7, 0, 0, 0, 0, time.UTC)
)

func winterInput(effect string, org *uuid.UUID) Input {
	return Input{EffectID: effect, Window: Window{StartsAt: winterStart, EndsAt: winterEnd}, OrganizationID: org}
}

func TestServiceCRUDRoundTrip(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	svc := NewService(pool, recipes.FixedClock(winterStart), nil)

	in := winterInput("snow", nil)
	i := 0.25
	in.Intensity = &i
	in.Surfaces = []string{"login"}
	in.Assets = Assets{BannerURL: "https://cdn.example/snow.png", Sprites: []string{"https://cdn.example/flake.png"}}
	created, err := svc.Create(ctx, 7, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedBy != 7 || created.Intensity != 0.25 || created.Surfaces[0] != "login" || created.Assets.BannerURL != in.Assets.BannerURL || created.OrganizationID != nil {
		t.Fatalf("unexpected created pack: %+v", created)
	}
	if _, err := svc.Create(ctx, 7, Input{EffectID: "bad"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid input must not reach the database: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil || got.EffectID != "snow" || !got.Window.StartsAt.Equal(winterStart) || len(got.Assets.Sprites) != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}

	upd := winterInput("hearts", nil)
	updated, err := svc.Update(ctx, created.ID, upd)
	if err != nil || updated.EffectID != "hearts" || updated.Intensity != 1.0 || updated.Surfaces[0] != "all" || updated.Assets.BannerURL != "" {
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
}

func TestActiveWindowsFollowTheSeasonalClock(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	svc := NewService(pool, recipes.FixedClock(winterStart), nil)
	if _, err := svc.Create(ctx, 1, winterInput("snow", nil)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		at   time.Time
		want int
	}{
		{"before start", winterStart.Add(-time.Second), 0},
		{"at start (inclusive)", winterStart, 1},
		{"inside", winterStart.AddDate(0, 0, 20), 1},
		{"just before end", winterEnd.Add(-time.Second), 1},
		{"at end (exclusive)", winterEnd, 0},
		{"after end", winterEnd.AddDate(0, 6, 0), 0},
	}
	for _, tc := range cases {
		svc.clock = recipes.FixedClock(tc.at)
		got, err := svc.ActivePublic(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tc.want {
			t.Errorf("%s: %d active, want %d", tc.name, len(got), tc.want)
		}
	}
	svc.clock = recipes.FixedClock(winterStart)
	got, _ := svc.ActivePublic(ctx)
	if got[0].EffectID != "snow" || !got[0].Window.EndsAt.Equal(winterEnd) || got[0].Surfaces[0] != "all" {
		t.Fatalf("wire shape must carry effect, bounds and surfaces: %+v", got[0])
	}
}

func TestActiveWindowsScopeOrganizationPacks(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	member := seedAccount(t, pool, "amb-member")
	outsider := seedAccount(t, pool, "amb-outsider")
	org := seedOrganization(t, pool, "amb-org", member, member)
	otherOrg := seedOrganization(t, pool, "amb-other", outsider, outsider)

	svc := NewService(pool, recipes.FixedClock(winterStart.AddDate(0, 0, 3)), nil)
	for _, in := range []Input{winterInput("snow", nil), winterInput("org-lights", &org), winterInput("other-lights", &otherOrg)} {
		if _, err := svc.Create(ctx, 1, in); err != nil {
			t.Fatal(err)
		}
	}
	effects := func(ws []Wire) []string {
		out := []string{}
		for _, w := range ws {
			out = append(out, w.EffectID)
		}
		return out
	}
	public, err := svc.ActivePublic(ctx)
	if err != nil || len(public) != 1 || public[0].EffectID != "snow" {
		t.Fatalf("public payload must only carry deployment-wide packs: %v %v", effects(public), err)
	}
	forMember, err := svc.ActiveForAccount(ctx, member)
	if err != nil || len(forMember) != 2 || forMember[0].EffectID != "snow" && forMember[1].EffectID != "snow" {
		t.Fatalf("member must see deployment-wide + own org: %v %v", effects(forMember), err)
	}
	for _, w := range forMember {
		if w.EffectID == "other-lights" {
			t.Fatalf("member must not see another org's pack: %v", effects(forMember))
		}
	}
	stranger := seedAccount(t, pool, "amb-stranger")
	forStranger, _ := svc.ActiveForAccount(ctx, stranger)
	if len(forStranger) != 1 || forStranger[0].EffectID != "snow" {
		t.Fatalf("account without memberships sees deployment-wide only: %v", effects(forStranger))
	}
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships SET status = 'suspended' WHERE account_id = $1`, member); err != nil {
		t.Fatal(err)
	}
	forMember, _ = svc.ActiveForAccount(ctx, member)
	if len(forMember) != 1 {
		t.Fatalf("suspended membership must not grant org packs: %v", effects(forMember))
	}
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships SET status = 'active' WHERE account_id = $1`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status = 'suspended' WHERE id = $1`, org); err != nil {
		t.Fatal(err)
	}
	forMember, _ = svc.ActiveForAccount(ctx, member)
	if len(forMember) != 1 {
		t.Fatalf("suspended organization must not grant org packs: %v", effects(forMember))
	}

	unknown := uuid.New()
	if _, err := svc.Create(ctx, 1, winterInput("ghost", &unknown)); !errors.Is(err, ErrInvalid) || err == nil || !strings.Contains(err.Error(), "organization_id does not exist") {
		t.Fatalf("unknown organization must be a validation error: %v", err)
	}
}

type memoryAssetStore struct{ objects map[string][]byte }

func (m *memoryAssetStore) PutObject(_ context.Context, _, key string, data []byte) error {
	m.objects[key] = data
	return nil
}
func (m *memoryAssetStore) GetObject(_ context.Context, _, key string) ([]byte, error) {
	if d, ok := m.objects[key]; ok {
		return d, nil
	}
	return nil, errors.New("missing")
}
func (m *memoryAssetStore) Bucket() string { return "public" }

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAttachAssetStoresAndServesArtwork(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := &memoryAssetStore{objects: map[string][]byte{}}
	svc := NewService(pool, recipes.FixedClock(winterStart), store)
	pack, err := svc.Create(ctx, 1, winterInput("pumpkins", nil))
	if err != nil {
		t.Fatal(err)
	}
	img := pngBytes(t)
	updated, url, err := svc.AttachAsset(ctx, pack.ID, SlotBanner, img)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Assets.BannerURL != url || !IsAssetURL(url) || len(store.objects) != 1 {
		t.Fatalf("banner not attached: %+v url=%s", updated.Assets, url)
	}
	updated, spriteURL, err := svc.AttachAsset(ctx, pack.ID, SlotSprite, img)
	if err != nil || len(updated.Assets.Sprites) != 1 || updated.Assets.Sprites[0] != spriteURL || updated.Assets.BannerURL != url {
		t.Fatalf("sprite append: %+v %v", updated.Assets, err)
	}
	data, contentType, err := svc.ServeAsset(ctx, url[len(AssetURLBase):])
	if err != nil || contentType != "image/png" || !bytes.Equal(data, img) {
		t.Fatalf("serve: ct=%s err=%v", contentType, err)
	}
	if _, _, err := svc.ServeAsset(ctx, "../secret"); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("traversal ref must 404: %v", err)
	}
	if _, _, err := svc.AttachAsset(ctx, pack.ID, SlotBanner, []byte("<svg onload=alert(1)/>")); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("non-raster upload must be rejected: %v", err)
	}
	if _, _, err := svc.AttachAsset(ctx, pack.ID, "poster", img); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("unknown slot: %v", err)
	}
	if _, _, err := svc.AttachAsset(ctx, "missing", SlotBanner, img); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown pack: %v", err)
	}
	if _, _, err := NewService(pool, nil, nil).AttachAsset(ctx, pack.ID, SlotBanner, img); !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("no storage: %v", err)
	}

	// Standalone store: object written, nothing attached, checksum reported.
	before := len(store.objects)
	stored, err := svc.StoreAsset(ctx, StoreRequest{AssetID: "garden-asset-1", Kind: KindSeasonBanner, Data: pngBytesSized(t, 3)})
	if err != nil || !IsAssetURL(stored.URL) || len(stored.Checksum) != 64 || stored.ContentType != "image/png" || len(store.objects) != before+1 {
		t.Fatalf("store: %+v %v objects=%d", stored, err, len(store.objects))
	}
	if p, _ := svc.Get(ctx, pack.ID); len(p.Assets.Sprites) != 1 || p.Assets.BannerURL != url {
		t.Fatalf("standalone store must not touch packs: %+v", p.Assets)
	}
	// Retry with the same asset_id + checksum: same URL, no second object.
	retry, err := svc.StoreAsset(ctx, StoreRequest{AssetID: "garden-asset-1", Kind: KindSeasonBanner, Data: pngBytesSized(t, 3)})
	if err != nil || retry.URL != stored.URL || retry.Checksum != stored.Checksum || len(store.objects) != before+1 {
		t.Fatalf("retry must be idempotent: %+v %v objects=%d", retry, err, len(store.objects))
	}
	var rows int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ambience_assets WHERE asset_id = 'garden-asset-1'`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("one registry row per asset_id, got %d", rows)
	}
	// Same asset_id, different bytes: replaced.
	replaced, err := svc.StoreAsset(ctx, StoreRequest{AssetID: "garden-asset-1", Kind: KindSeasonSprite, Data: pngBytesSized(t, 9)})
	if err != nil || replaced.URL == stored.URL || len(store.objects) != before+2 {
		t.Fatalf("different checksum must replace: %+v %v objects=%d", replaced, err, len(store.objects))
	}
	var ref, kind string
	_ = pool.QueryRow(ctx, `SELECT ref, kind FROM ambience_assets WHERE asset_id = 'garden-asset-1'`).Scan(&ref, &kind)
	if ref != replaced.Ref || kind != KindSeasonSprite {
		t.Fatalf("registry row not replaced: ref=%s kind=%s", ref, kind)
	}
	if _, err := svc.StoreAsset(ctx, StoreRequest{AssetID: "x", Kind: "poster", Data: pngBytesSized(t, 2)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}

	// Sprite cap is enforced before any object is stored.
	full := Input{EffectID: "full", Window: pack.Window, Assets: Assets{Sprites: make([]string, 32)}}
	for i := range full.Assets.Sprites {
		full.Assets.Sprites[i] = "https://cdn.example/s.png"
	}
	fullPack, err := svc.Create(ctx, 1, full)
	if err != nil {
		t.Fatal(err)
	}
	before = len(store.objects)
	if _, _, err := svc.AttachAsset(ctx, fullPack.ID, SlotSprite, pngBytesSized(t, 4)); !errors.Is(err, ErrInvalid) || len(store.objects) != before {
		t.Fatalf("sprite cap must reject before storing: err=%v objects=%d", err, len(store.objects))
	}

	// Concurrent sprite attaches never lose entries (row lock).
	racePack, _ := svc.Create(ctx, 1, winterInput("race", nil))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, _, err := svc.AttachAsset(ctx, racePack.ID, SlotSprite, pngBytesSized(t, 5+n)); err != nil {
				t.Errorf("concurrent attach: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if p, _ := svc.Get(ctx, racePack.ID); len(p.Assets.Sprites) != 8 {
		t.Fatalf("lost sprite attaches: %d/8", len(p.Assets.Sprites))
	}
}

func pngBytesSized(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, n, n))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnnualRegistryPersistsAndExpires(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	owner := seedAccount(t, pool, "seasonal-owner")
	svc := NewService(pool, recipes.FixedClock(instant("2029-12-15T00:00:00Z")), nil)
	in := Input{EffectID: "snow", Window: Window{StartsAt: instant("2026-11-30T23:00:00Z"), EndsAt: instant("2026-12-31T23:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}}
	created, err := svc.Create(ctx, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := svc.Get(ctx, created.ID)
	if err != nil || !saved.Window.RepeatYearly || saved.Window.Timezone != "Europe/Amsterdam" {
		t.Fatalf("stored schedule: %+v %v", saved, err)
	}
	active, err := svc.ActivePublic(ctx)
	if err != nil || len(active) != 1 {
		t.Fatalf("active: %+v %v", active, err)
	}
	if active[0].Window.StartsAt != instant("2029-11-30T23:00:00Z") || active[0].Window.RepeatYearly || active[0].Window.Timezone != "" {
		t.Fatal("client must receive only this occurrence", active[0])
	}
	svc.clock = recipes.FixedClock(instant("2029-12-31T23:00:00Z"))
	active, err = svc.ActivePublic(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("expired: %+v %v", active, err)
	}
	in.Window.RepeatYearly = false
	if _, err = svc.Update(ctx, created.ID, in); err != nil {
		t.Fatal(err)
	}
	saved, err = svc.Get(ctx, created.ID)
	if err != nil || saved.Window.RepeatYearly {
		t.Fatalf("updated schedule: %+v %v", saved, err)
	}
	if err = svc.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
}
