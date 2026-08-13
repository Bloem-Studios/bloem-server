package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
	"github.com/Silo-Server/silo-server/internal/settingskeys"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/migrations"
)

// countingQueryTracer records every statement pgx sends on behalf of a caller.
type countingQueryTracer struct {
	mu      sync.Mutex
	queries []string
}

func (c *countingQueryTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, data.SQL)
	return ctx
}

func (c *countingQueryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *countingQueryTracer) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = nil
}

func (c *countingQueryTracer) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

// TestPostgresResolutionIssuesOneQuery pins the read path's normative rule: a
// batched resolution request costs one query no matter how many scopes, keys or
// content contexts it spans. Five sequential index lookups per key per item is a
// rejected implementation, and nothing else in the suite would notice the
// difference — the returned rows would be identical.
func TestPostgresResolutionIssuesOneQuery(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database url: %v", err)
	}
	tracer := &countingQueryTracer{}
	config.ConnConfig.Tracer = tracer
	// One connection keeps the trace deterministic: a second connection would
	// replay session setup statements into the count.
	config.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	var table *string
	err = pool.QueryRow(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'user_setting_values'`).Scan(&table)
	if errors.Is(err, pgx.ErrNoRows) || table == nil {
		t.Skip("settings contract storage migration has not been applied")
	}
	if err != nil {
		t.Fatalf("check migration: %v", err)
	}

	var userID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`,
		fmt.Sprintf("conf-onequery-%d", time.Now().UnixNano()),
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	provisionTestMembership(t, pool, userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	store := newStore(pool, userID)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "p1", Name: "Alice"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	const key = "playback.audio_language"
	identities := []userstore.SettingIdentity{
		{Key: key, Scope: settingscontract.ScopeAccount},
		{Key: key, Scope: settingscontract.ScopeProfile, ProfileID: "p1"},
		{Key: key, Scope: settingscontract.ScopeProfileDevice, ProfileID: "p1", DeviceID: "apple-tv"},
		{Key: key, Scope: settingscontract.ScopeProfileLibrary, ProfileID: "p1", LibraryID: 42},
		{Key: key, Scope: settingscontract.ScopeProfileSeries, ProfileID: "p1", SeriesID: "s-1"},
		{Key: key, Scope: settingscontract.ScopeProfileSeries, ProfileID: "p1", SeriesID: "s-2"},
		{Key: key, Scope: settingscontract.ScopeProfileSeries, ProfileID: "p1", SeriesID: "s-3"},
	}
	for _, id := range identities {
		if _, err := store.UpsertSettingValue(ctx, id, json.RawMessage(`"en"`)); err != nil {
			t.Fatalf("UpsertSettingValue(%+v): %v", id, err)
		}
	}

	tracer.reset()
	rows, err := store.ListSettingValuesForResolution(ctx, userstore.SettingResolutionQuery{
		Keys:       []string{key, "playback.subtitle_mode", "playback.subtitle_language"},
		ProfileIDs: []string{"p1"},
		DeviceID:   "apple-tv",
		LibraryIDs: []int{42, 43},
		SeriesIDs:  []string{"s-1", "s-2", "s-3"},
	})
	if err != nil {
		t.Fatalf("ListSettingValuesForResolution: %v", err)
	}
	if len(rows) != len(identities) {
		t.Fatalf("resolution returned %d rows, want %d", len(rows), len(identities))
	}

	issued := tracer.snapshot()
	if len(issued) != 1 {
		t.Fatalf("resolution issued %d queries, want 1:\n%v", len(issued), issued)
	}
}

// TestPostgresPreferenceSettingsTransactionsSerializePerUser pins the lock
// shared by profile creation and legacy account-setting fan-out. Without it,
// profile creation can snapshot an old account value, a concurrent writer can
// miss the uncommitted profile, and the creator can then publish the stale
// value after the newer write has committed.
func TestPostgresPreferenceSettingsTransactionsSerializePerUser(t *testing.T) {
	pool, userID := newConstraintTestUser(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	store := newStore(pool, userID)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "p1", Name: "Main"}); err != nil {
		t.Fatalf("CreateProfile p1: %v", err)
	}
	if err := store.SetSetting(ctx, settingskeys.SearchMediaScope, "movie"); err != nil {
		t.Fatalf("seed legacy account setting: %v", err)
	}

	transactioner := userstore.PreferenceSettingsTransactioner(store)
	creatorReady := make(chan struct{})
	releaseCreator := make(chan struct{})
	creatorErr := make(chan error, 1)
	go func() {
		creatorErr <- transactioner.WithPreferenceSettingsTransaction(ctx, func(tx userstore.PreferenceSettingsWriter) error {
			if err := tx.CreateProfile(ctx, userstore.Profile{ID: "p2", Name: "Guest"}); err != nil {
				return err
			}
			entries, err := tx.ListSettings(ctx)
			if err != nil {
				return err
			}
			value := ""
			for _, entry := range entries {
				if entry.Key == settingskeys.SearchMediaScope {
					value = entry.Value
					break
				}
			}
			if value == "" {
				return errors.New("legacy account setting was not visible to profile creator")
			}
			close(creatorReady)
			select {
			case <-releaseCreator:
			case <-ctx.Done():
				return ctx.Err()
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			_, err = tx.UpsertSettingValue(ctx, userstore.SettingIdentity{
				Key: settingskeys.SearchMediaScope, Scope: settingscontract.ScopeProfile, ProfileID: "p2",
			}, encoded)
			return err
		})
	}()
	<-creatorReady

	writerAttempted := make(chan struct{})
	writerEntered := make(chan struct{})
	writerErr := make(chan error, 1)
	go func() {
		close(writerAttempted)
		writerErr <- transactioner.WithPreferenceSettingsTransaction(ctx, func(tx userstore.PreferenceSettingsWriter) error {
			close(writerEntered)
			if err := tx.SetSetting(ctx, settingskeys.SearchMediaScope, "audiobook"); err != nil {
				return err
			}
			profileIDs, err := tx.ListProfileIDs(ctx)
			if err != nil {
				return err
			}
			for _, profileID := range profileIDs {
				if _, err := tx.UpsertSettingValue(ctx, userstore.SettingIdentity{
					Key: settingskeys.SearchMediaScope, Scope: settingscontract.ScopeProfile, ProfileID: profileID,
				}, json.RawMessage(`"audiobook"`)); err != nil {
					return err
				}
			}
			return nil
		})
	}()
	<-writerAttempted

	enteredBeforeRelease := false
	select {
	case <-writerEntered:
		enteredBeforeRelease = true
	case <-time.After(250 * time.Millisecond):
	}
	close(releaseCreator)
	if err := <-creatorErr; err != nil {
		t.Fatalf("profile creator transaction: %v", err)
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("legacy writer transaction: %v", err)
	}
	if enteredBeforeRelease {
		t.Fatal("same-user preference transaction entered while profile creation still held the lock")
	}

	legacy, err := store.GetSetting(ctx, settingskeys.SearchMediaScope)
	if err != nil || legacy != "audiobook" {
		t.Fatalf("legacy value = %q, err=%v; want audiobook", legacy, err)
	}
	canonical, err := store.GetSettingValue(ctx, userstore.SettingIdentity{
		Key: settingskeys.SearchMediaScope, Scope: settingscontract.ScopeProfile, ProfileID: "p2",
	})
	if err != nil || canonical == nil || string(canonical.Value) != `"audiobook"` {
		t.Fatalf("new profile canonical value = %+v, err=%v; want audiobook", canonical, err)
	}
}

// insertSettingValueSQL writes a row without going through the store, so the
// schema is the only thing standing between the test and the table.
const insertSettingValueSQL = `
	INSERT INTO user_setting_values (user_id, key, scope, profile_id, device_id, library_id, series_id, value)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// TestPostgresSettingValueConstraints pins the schema's own guarantees rather
// than the Go validation in front of them. SettingIdentity.Validate normally
// keeps a malformed row from reaching SQL, so nothing else here would notice if
// the CHECK constraints or the partial unique indexes were dropped — and the
// one-time migration will write these rows in bulk, without going through the
// per-request path. internal/userdb runs the same checks against SQLite.
func TestPostgresSettingValueConstraints(t *testing.T) {
	ctx := context.Background()
	pool, userID := newConstraintTestUser(t)
	store := newStore(pool, userID)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "p1", Name: "Alice"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "p2", Name: "Bob"}); err != nil {
		t.Fatalf("CreateProfile(p2): %v", err)
	}

	const key = "playback.audio_language"
	rejected := []struct {
		name string
		args []any
	}{
		{"unknown scope", []any{userID, key, "wishful", nil, nil, nil, nil, []byte(`"en"`)}},
		{"account scope carrying a profile", []any{userID, key, "account", "p1", nil, nil, nil, []byte(`"en"`)}},
		{"profile scope without a profile", []any{userID, key, "profile", nil, nil, nil, nil, []byte(`"en"`)}},
		{"device scope without a device", []any{userID, key, "profile_device", "p1", nil, nil, nil, []byte(`"en"`)}},
		{"library scope carrying a series", []any{userID, key, "profile_library", "p1", nil, 42, "s-1", []byte(`"en"`)}},
		{"series scope without a series", []any{userID, key, "profile_series", "p1", nil, nil, nil, []byte(`"en"`)}},
		{"profile that does not exist", []any{userID, key, "profile", "ghost", nil, nil, nil, []byte(`"en"`)}},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, insertSettingValueSQL, tc.args...); err == nil {
				t.Fatalf("insert was accepted; the table must reject %s", tc.name)
			}
		})
	}

	duplicates := []struct {
		name     string
		row      []any
		neighbor []any
	}{
		{
			"account",
			[]any{userID, key, "account", nil, nil, nil, nil, []byte(`"en"`)},
			[]any{userID, key + ".other", "account", nil, nil, nil, nil, []byte(`"en"`)},
		},
		{
			"profile",
			[]any{userID, key, "profile", "p1", nil, nil, nil, []byte(`"en"`)},
			[]any{userID, key, "profile", "p2", nil, nil, nil, []byte(`"en"`)},
		},
		{
			"profile_device",
			[]any{userID, key, "profile_device", "p1", "apple-tv", nil, nil, []byte(`"en"`)},
			[]any{userID, key, "profile_device", "p1", "iphone", nil, nil, []byte(`"en"`)},
		},
		{
			"profile_library",
			[]any{userID, key, "profile_library", "p1", nil, 42, nil, []byte(`"en"`)},
			[]any{userID, key, "profile_library", "p1", nil, 43, nil, []byte(`"en"`)},
		},
		{
			"profile_series",
			[]any{userID, key, "profile_series", "p1", nil, nil, "s-1", []byte(`"en"`)},
			[]any{userID, key, "profile_series", "p1", nil, nil, "s-2", []byte(`"en"`)},
		},
	}
	for _, tc := range duplicates {
		t.Run("unique/"+tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, insertSettingValueSQL, tc.row...); err != nil {
				t.Fatalf("first insert: %v", err)
			}
			if _, err := pool.Exec(ctx, insertSettingValueSQL, tc.row...); err == nil {
				t.Fatal("duplicate identity was accepted; the partial unique index is missing")
			}
			if _, err := pool.Exec(ctx, insertSettingValueSQL, tc.neighbor...); err != nil {
				t.Fatalf("neighboring identity was refused: %v", err)
			}
		})
	}
}

func newConstraintTestUser(t *testing.T) (*pgxpool.Pool, int) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	var table *string
	err = pool.QueryRow(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'user_setting_values'`).Scan(&table)
	if errors.Is(err, pgx.ErrNoRows) || table == nil {
		t.Skip("settings contract storage migration has not been applied")
	}
	if err != nil {
		t.Fatalf("check migration: %v", err)
	}

	var userID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`,
		fmt.Sprintf("conf-constraints-%d", time.Now().UnixNano()),
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	provisionTestMembership(t, pool, userID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	return pool, userID
}

func provisionTestMembership(t *testing.T, pool *pgxpool.Pool, userID int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT id, $1, 'active', 'user'
		FROM organizations
		WHERE is_default
		ON CONFLICT (organization_id, account_id) DO NOTHING`, userID); err != nil {
		t.Fatalf("provision test membership: %v", err)
	}
}

func TestProfileOrganizationAndAccessGroupPersistence(t *testing.T) {
	pool, userID := newProfileIdentityTestUser(t)
	ctx := context.Background()

	var organizationID string
	var accessGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT o.id::text, g.id
		FROM organizations o
		JOIN access_groups g ON g.organization_id = o.id
		WHERE o.is_default
		ORDER BY g.id
		LIMIT 1`).Scan(&organizationID, &accessGroupID); err != nil {
		t.Fatalf("load default profile identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET access_group_id = $2 WHERE id = $1`, userID, accessGroupID); err != nil {
		t.Fatalf("assign legacy group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')
		ON CONFLICT (organization_id, account_id) DO NOTHING`, organizationID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	store := newStore(pool, userID)
	if err := store.CreateProfile(ctx, userstore.Profile{
		ID:             "tenant-profile",
		Name:           "Tenant Profile",
		OrganizationID: organizationID,
		AccessGroupID:  &accessGroupID,
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	profile, err := store.GetProfile(ctx, "tenant-profile")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile == nil || profile.OrganizationID != organizationID || profile.AccessGroupID == nil || *profile.AccessGroupID != accessGroupID {
		t.Fatalf("profile identity = %#v", profile)
	}
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].OrganizationID != organizationID || profiles[0].AccessGroupID == nil || *profiles[0].AccessGroupID != accessGroupID {
		t.Fatalf("listed profiles = %#v", profiles)
	}

	var legacyGroupID *int64
	if err := pool.QueryRow(ctx, `SELECT access_group_id FROM users WHERE id = $1`, userID).Scan(&legacyGroupID); err != nil {
		t.Fatalf("load legacy assignment: %v", err)
	}
	if legacyGroupID == nil || *legacyGroupID != accessGroupID {
		t.Fatalf("legacy access group changed: %v", legacyGroupID)
	}
	var legacyMemberCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE access_group_id = $1`, accessGroupID).Scan(&legacyMemberCount); err != nil {
		t.Fatalf("count legacy members: %v", err)
	}
	if legacyMemberCount < 1 {
		t.Fatalf("legacy member count = %d, want at least seeded account", legacyMemberCount)
	}
}

func TestProfileAccessGroupRejectsDifferentOrganization(t *testing.T) {
	pool, userID := newProfileIdentityTestUser(t)
	ctx := context.Background()
	var defaultOrganizationID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')
		ON CONFLICT (organization_id, account_id) DO NOTHING`, defaultOrganizationID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	var otherOrganizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status)
		VALUES ($1, 'Other', 'active') RETURNING id::text`, fmt.Sprintf("profile-other-%d", time.Now().UnixNano())).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("seed other organization: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, otherOrganizationID) })
	var otherGroupID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (name, organization_id)
		VALUES ($1, $2) RETURNING id`, fmt.Sprintf("profile-other-%d", time.Now().UnixNano()), otherOrganizationID).Scan(&otherGroupID); err != nil {
		t.Fatalf("seed other group: %v", err)
	}

	err := newStore(pool, userID).CreateProfile(ctx, userstore.Profile{
		ID:             "cross-tenant-profile",
		Name:           "Cross Tenant",
		OrganizationID: defaultOrganizationID,
		AccessGroupID:  &otherGroupID,
	})
	if err == nil {
		t.Fatal("CreateProfile accepted an access group from another organization")
	}
}

func newProfileIdentityTestUser(t *testing.T) (*pgxpool.Pool, int) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_tenancy_" + hex.EncodeToString(random)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}

	var userID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`,
		fmt.Sprintf("profile-identity-%d", time.Now().UnixNano()),
	).Scan(&userID); err != nil {
		t.Fatalf("seed profile identity user: %v", err)
	}
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(ctx, userID, "user"); err != nil {
		t.Fatalf("provision profile identity membership: %v", err)
	}
	return pool, userID
}
