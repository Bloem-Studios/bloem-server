//go:build integration

package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/catalog/reattribute"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/sections"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// P1Paired is deliberately the only schedule here. Removing fresh keyed
// classification from an original column guard would let its waiting UPDATE
// cross the observed native COMMIT; removing Run admission would let the real
// companion mutate user state. Existing protection may pass this new coverage.
const promotionEvidence = "../../.superpowers/sdd/2026-10-06-native-storage-onboarding-implementation-plan-4/task-A-promotion-matrix-fix-1-states"

type promotionColumn struct {
	id, table, column string
	pk                []string
}

var promotionColumns = []promotionColumn{
	{"c01", "admin_playback_history", "media_item_id", []string{"session_id"}},
	{"c02", "user_downloads", "media_item_id", []string{"user_id", "id"}},
	{"c03", "downloads", "content_id", []string{"id"}},
	{"c04", "downloads", "episode_id", []string{"id"}},
	{"c05", "user_watch_history", "media_item_id", []string{"user_id", "id"}},
	{"c06", "user_watch_progress", "media_item_id", []string{"user_id", "profile_id", "media_item_id"}},
	{"c07", "user_favorites", "media_item_id", []string{"user_id", "profile_id", "media_item_id"}},
	{"c08", "user_watchlist", "media_item_id", []string{"user_id", "profile_id", "media_item_id"}},
	{"c09", "user_ratings", "media_item_id", []string{"user_id", "profile_id", "media_item_id"}},
	{"c10", "user_personal_collection_items", "media_item_id", []string{"user_id", "collection_id", "media_item_id", "sub_item_id"}},
	{"c11", "library_collection_items", "media_item_id", []string{"collection_id", "media_item_id"}},
	{"c12", "user_home_item_dismissals", "media_item_id", []string{"user_id", "profile_id", "surface", "media_item_id"}},
	{"c13", "user_home_item_dismissals", "series_id", []string{"user_id", "profile_id", "surface", "media_item_id"}},
	{"c14", "user_history_hidden_items", "media_item_id", []string{"user_id", "profile_id", "media_item_id"}},
	{"c15", "user_audio_preferences", "series_id", []string{"user_id", "profile_id", "series_id"}},
	{"c16", "user_subtitle_preferences", "series_id", []string{"user_id", "profile_id", "series_id"}},
	{"c17", "user_series_playback_preferences", "series_id", []string{"user_id", "profile_id", "series_id"}},
	{"c18", "user_dropped_series", "series_id", []string{"user_id", "profile_id", "series_id"}},
	{"c19", "watch_provider_rating_items", "media_item_id", []string{"connection_id", "media_item_id"}},
	{"c20", "watch_provider_dropped_items", "series_id", []string{"connection_id", "series_id"}},
}

// Every row, every column, sorted inside ONE observer snapshot. This fixed list
// includes all original sites, publication, authority, discovery, search and
// provider state. Sequence allocation is intentionally outside logical equality.
var promotionTables = strings.Fields(`media_items media_files media_item_libraries episode_libraries episodes seasons media_extras
 media_item_provider_ids media_item_roots media_item_groups item_people ebook_series
 user_watch_progress user_personal_collection_items user_personal_collections user_personal_collection_profiles
 library_collections library_collection_items user_favorites user_watchlist user_ratings user_home_item_dismissals
 user_history_hidden_items user_audio_preferences user_subtitle_preferences user_series_playback_preferences
 user_watch_history admin_playback_history user_downloads downloads user_dropped_series watch_provider_rating_items
 watch_provider_dropped_items watch_provider_connections bloem_storage_file_refs bloem_storage_bindings bloem_storage_sources
 bloem_storage_entries bloem_storage_ingestion bloem_storage_scan_runs bloem_storage_scan_directories bloem_storage_scan_cursors
 bloem_storage_installations bloem_native_libraries bloem_native_publication_permits catalog_search_index_events
 media_folders media_folder_paths users user_profiles auth_sessions organizations organization_memberships resource_owners
 organization_entitlements plugin_installations`)

func promotionCheck(t *testing.T, err error, operation string) {
	t.Helper()
	if err != nil {
		var pe *pgconn.PgError
		code := "none"
		if errors.As(err, &pe) {
			code = pe.Code
		}
		t.Fatalf("%s failed: class=%T SQLSTATE=%s", operation, err, code)
	}
}
func promotionSQL(t *testing.T, ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, sql string, args ...any) pgconn.CommandTag {
	t.Helper()
	tag, err := q.Exec(ctx, sql, args...)
	promotionCheck(t, err, "fixture SQL")
	return tag
}
func promotionSave(t *testing.T, pool *pgxpool.Pool, label string, v any) {
	t.Helper()
	dir := filepath.Join(promotionEvidence, pool.Config().ConnConfig.Database)
	promotionCheck(t, os.MkdirAll(dir, 0700), "evidence directory")
	data, err := json.MarshalIndent(v, "", "  ")
	promotionCheck(t, err, "evidence encoding")
	promotionCheck(t, os.WriteFile(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "__")+"-"+label+".json"), append(data, '\n'), 0600), "private evidence write")
}
func promotionSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) map[string]json.RawMessage {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	promotionCheck(t, err, "observer snapshot begin")
	defer promotionRollback(tx)
	state := map[string]json.RawMessage{}
	for _, table := range promotionTables {
		var raw []byte
		err = tx.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM "+pgx.Identifier{"public", table}.Sanitize()+" r").Scan(&raw)
		promotionCheck(t, err, "full logical snapshot "+table)
		state[table] = json.RawMessage(raw)
	}
	promotionCheck(t, tx.Commit(ctx), "observer snapshot commit")
	promotionSave(t, pool, label, state)
	return state
}
func promotionEqual(t *testing.T, a, b map[string]json.RawMessage) {
	t.Helper()
	for _, table := range promotionTables {
		if !bytes.Equal(a[table], b[table]) {
			t.Errorf("full logical rows changed in %s after refusal/rollback (private snapshots retained)", table)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}
func promotionRollback(tx pgx.Tx) {
	if tx != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}
}

// The CURRENT helper sequence is repeated locally only to propagate the setup
// context through the actual lifecycle calls, rather than helpers' t.Context.
func promotionClone(t *testing.T, ctx context.Context, trace *promotionTracer) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	const private = "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(private)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	data, err := os.ReadFile(private)
	promotionCheck(t, err, "private fixture read")
	original, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil || !strings.HasPrefix(original.ConnConfig.Database, "bloem_storage_test_") {
		t.Fatal("unapproved private template")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(ctx, strings.TrimSpace(string(data)), false)
	promotionCheck(t, err, "clone(false)")
	var pool *pgxpool.Pool
	var actual string
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cleanup(dropCtx)
		if err != nil {
			t.Error("owned UUID clone cleanup failed")
		} else {
			t.Logf("promotion UUID cleanup verified absent: %s", actual)
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	promotionCheck(t, err, "exact returned guarded URI")
	cfg.MaxConns = 8
	cfg.ConnConfig.Tracer = trace
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	promotionCheck(t, err, "guarded pool open")
	err = pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual)
	promotionCheck(t, err, "independent UUID observation")
	const prefix = "bloem_storage_test_acore_"
	id, parseErr := uuid.Parse(strings.TrimPrefix(actual, prefix))
	if actual != cfg.ConnConfig.Database || actual == original.ConnConfig.Database || len(actual) != len(prefix)+32 || !strings.HasPrefix(actual, prefix) || parseErr != nil || id == uuid.Nil || strings.ReplaceAll(id.String(), "-", "") != strings.TrimPrefix(actual, prefix) {
		t.Fatal("actual owned UUID mismatch")
	}
	t.Logf("promotion actual UUID clone: %s", actual)
	promotionCheck(t, bloemtestdb.PrepareNativeOnboardingPool(ctx, pool), "SAME-pool CURRENT preparation")
	if !catalog.NativeStorageSchemaReady(ctx, pool) {
		t.Fatal("CURRENT schema inventory unavailable")
	}
	return pool
}

type promotionFixture struct {
	pool                                                 *pgxpool.Pool
	trace                                                *promotionTracer
	source                                               storagesource.SourceConfig
	binding                                              storagesource.Binding
	folder                                               *models.MediaFolder
	r                                                    *storagesource.Repository
	s                                                    *Scanner
	lease                                                storagesource.IngestionLease
	files                                                map[string]*nativeTestFile
	user, localFolder, file                              int
	profile, unrelated, personal, collection, connection string
	provider                                             *watchsync.PostgresRepository
}

func promotionSetup(t *testing.T, ctx context.Context) *promotionFixture {
	t.Helper()
	trace := &promotionTracer{}
	pool := promotionClone(t, ctx, trace)
	source, binding, folder := promotionLifecycle(t, ctx, pool)
	x := &promotionFixture{pool: pool, trace: trace, source: source, binding: binding, folder: folder, r: storagesource.NewRepository(pool), s: NewScanner(NewFileRepository(pool), "", nil, 1, false, 0), files: map[string]*nativeTestFile{}}
	user, err := auth.NewUserRepository(pool).Create(ctx, models.CreateUserInput{Username: "promotion-" + uuid.NewString(), Email: uuid.NewString() + "@example.test", Password: "promotion-profile-password", Role: "user"})
	promotionCheck(t, err, "actual profile account Create")
	x.user = user.ID
	store, err := pgstore.NewPostgresProvider(pool).ForUser(ctx, user.ID)
	promotionCheck(t, err, "actual ForUser")
	x.profile = uuid.NewString()
	promotionCheck(t, store.CreateProfile(ctx, userstore.Profile{ID: x.profile, Name: "Promotion"}), "actual CreateProfile")
	var membership int
	promotionCheck(t, pool.QueryRow(ctx, `SELECT count(*) FROM user_profiles p JOIN organization_memberships m ON m.organization_id=p.organization_id AND m.account_id=p.user_id WHERE p.user_id=$1 AND p.id=$2 AND m.status='active' AND m.security_revision>0`, x.user, x.profile).Scan(&membership), "profile authority witness")
	if membership != 1 {
		t.Fatal("profile membership missing")
	}
	promotionCheck(t, pool.QueryRow(ctx, "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Promotion prerequisites',bloem_platform_resource_owner_id()) RETURNING id").Scan(&x.localFolder), "unrelated local folder")
	x.unrelated = "promotion-unrelated-" + uuid.NewString()
	promotionSQL(t, ctx, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'movie','matched','Unrelated')", x.unrelated)
	f, err := NewFileRepository(pool).Upsert(ctx, models.MediaFile{ContentID: x.unrelated, MediaFolderID: x.localFolder, FilePath: "/promotion/" + x.unrelated})
	promotionCheck(t, err, "real unrelated local file Upsert")
	x.file = f.ID
	x.personal = "promotion-personal-" + uuid.NewString()
	x.collection = "promotion-library-" + uuid.NewString()
	promotionSQL(t, ctx, pool, "INSERT INTO user_personal_collections(id,user_id,profile_id,name) VALUES($1,$2,$3,'Promotion')", x.personal, x.user, x.profile)
	promotionSQL(t, ctx, pool, "INSERT INTO library_collections(id,library_id,slug,title,collection_type) VALUES($1,$2,$1,'Promotion','manual')", x.collection, x.localFolder)
	cipher, err := secret.New([]byte(strings.Repeat("promotion-provider-key", 3)))
	promotionCheck(t, err, "synthetic provider cipher")
	x.provider = watchsync.NewPostgresRepository(pool, cipher)
	connection, err := x.provider.UpsertConnection(ctx, watchsync.Connection{Provider: "promotion", UserID: x.user, ProfileID: x.profile, ProviderAccountID: "promotion-account", AccessToken: "synthetic", SyncDroppedEnabled: true})
	promotionCheck(t, err, "actual provider connection")
	x.connection = connection.ID
	return x
}

// Fixed row payloads and full PK predicates; no schema-driven value guessing.
type promotionRow struct {
	c      promotionColumn
	values map[string]any
}

func (x *promotionFixture) row(c promotionColumn, from string) promotionRow {
	stamp := time.Date(2026, 10, 6, 12, 0, 0, 123456000, time.UTC)
	v := map[string]any{}
	base := func() { v["user_id"] = x.user; v["profile_id"] = x.profile }
	id := uuid.NewString()
	switch c.id {
	case "c01":
		base()
		v["session_id"] = id
		v["media_item_id"] = from
		v["media_file_id"] = x.file
		v["play_method"] = "direct"
		v["started_at"] = stamp
		v["ended_at"] = stamp.Add(time.Second)
	case "c02":
		base()
		v["id"] = id
		v["media_item_id"] = from
		v["media_file_id"] = x.file
	case "c03", "c04":
		base()
		v["id"] = id
		v["media_file_id"] = x.file
		v["content_id"] = from
		v["episode_id"] = nil
		v["device_id"] = nil
		if c.id == "c04" {
			v["content_id"] = x.unrelated
			v["episode_id"] = from
		}
	case "c05":
		base()
		v["id"] = id
		v["media_item_id"] = from
		v["watched_at"] = stamp
	case "c06":
		base()
		v["media_item_id"] = from
		v["position_seconds"] = 17
		v["duration_seconds"] = 120
		v["updated_at"] = stamp
		v["last_file_id"] = nil
	case "c07", "c08":
		base()
		v["media_item_id"] = from
		v["added_at"] = stamp
	case "c09":
		base()
		v["media_item_id"] = from
		v["rating"] = 3
		v["rated_at"] = stamp
	case "c10":
		v["user_id"] = x.user
		v["collection_id"] = x.personal
		v["media_item_id"] = from
		v["sub_item_id"] = ""
		v["position"] = 7
		v["added_at"] = stamp
	case "c11":
		v["collection_id"] = x.collection
		v["media_item_id"] = from
		v["position"] = 7
		v["source_rank"] = 2
		v["created_at"] = stamp
		v["updated_at"] = stamp
	case "c12", "c13":
		base()
		v["surface"] = "continue_watching"
		v["media_item_id"] = from
		v["series_id"] = nil
		v["dismissed_at"] = stamp
		if c.id == "c13" {
			v["media_item_id"] = x.unrelated
			v["series_id"] = from
		}
	case "c14":
		base()
		v["media_item_id"] = from
		v["hidden_before"] = stamp
		v["updated_at"] = stamp
	case "c15":
		base()
		v["series_id"] = from
		v["audio_track_index"] = 2
		v["audio_language"] = "en"
		v["updated_at"] = stamp
	case "c16":
		base()
		v["series_id"] = from
		v["subtitle_language"] = "en"
		v["subtitle_track_index"] = 2
		v["subtitle_mode"] = "always"
		v["updated_at"] = stamp
	case "c17":
		base()
		v["series_id"] = from
		v["resolution"] = "1080p"
		v["hdr"] = false
		v["codec_video"] = "h264"
		v["updated_at"] = stamp
	case "c18":
		base()
		v["series_id"] = from
		v["dropped_at"] = stamp
	case "c19":
		v["connection_id"] = x.connection
		v["provider_account_id"] = "promotion-account"
		v["media_item_id"] = from
		v["kind"] = "ebook"
		v["provider_item_key"] = "before"
		v["synced_rating"] = 3
		v["remote_seen"] = true
		v["updated_at"] = stamp
	case "c20":
		v["connection_id"] = x.connection
		v["provider_account_id"] = "promotion-account"
		v["series_id"] = from
		v["provider_item_key"] = "before"
		v["remote_seen"] = true
		v["updated_at"] = stamp
	default:
		panic("unassigned promotion column")
	}
	return promotionRow{c, v}
}
func (r promotionRow) predicate(identity string, offset int) (string, []any) {
	var parts []string
	var args []any
	for _, col := range r.c.pk {
		value, ok := r.values[col]
		if !ok {
			panic("missing fixed PK")
		}
		if col == r.c.column {
			value = identity
		}
		args = append(args, value)
		parts = append(parts, fmt.Sprintf("%s=$%d", pgx.Identifier{col}.Sanitize(), offset+len(args)))
	}
	return strings.Join(parts, " AND "), args
}
func (r promotionRow) update(from, to string) (string, []any) {
	where, args := r.predicate(from, 1)
	return "UPDATE " + pgx.Identifier{"public", r.c.table}.Sanitize() + " SET " + pgx.Identifier{r.c.column}.Sanitize() + "=$1 WHERE " + where, append([]any{to}, args...)
}
func (r promotionRow) read(t *testing.T, ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, identity string) map[string]any {
	t.Helper()
	where, args := r.predicate(identity, 0)
	var raw []byte
	promotionCheck(t, q.QueryRow(ctx, "SELECT to_jsonb(r) FROM "+pgx.Identifier{"public", r.c.table}.Sanitize()+" r WHERE "+where, args...).Scan(&raw), "selected full row read")
	var result map[string]any
	promotionCheck(t, json.Unmarshal(raw, &result), "selected row decode")
	return result
}
func (x *promotionFixture) seed(t *testing.T, ctx context.Context, r promotionRow) {
	t.Helper()
	var required, pk []string
	promotionCheck(t, x.pool.QueryRow(ctx, `SELECT COALESCE(array_agg(column_name::text ORDER BY ordinal_position),'{}') FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND is_nullable='NO' AND column_default IS NULL AND is_identity='NO'`, r.c.table).Scan(&required), "fixed required column inventory")
	for _, name := range required {
		if _, ok := r.values[name]; !ok {
			t.Fatalf("unassigned required fixture column %s.%s", r.c.table, name)
		}
	}
	promotionCheck(t, x.pool.QueryRow(ctx, `SELECT array_agg(a.attname::text ORDER BY k.n) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace ns ON ns.oid=c.relnamespace CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum,n) JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.attnum WHERE ns.nspname='public' AND c.relname=$1 AND i.indisprimary`, r.c.table).Scan(&pk), "fixed primary key inventory")
	if !reflect.DeepEqual(pk, r.c.pk) {
		t.Fatalf("fixed primary key mismatch %s: observed=%v expected=%v", r.c.table, pk, r.c.pk)
	}
	if r.c.id == "c18" {
		promotionCheck(t, catalog.NewDroppedSeriesRepo(x.pool).Drop(ctx, x.user, x.profile, r.values[r.c.column].(string)), "actual Drop seed")
		promotionSQL(t, ctx, x.pool, "UPDATE user_dropped_series SET dropped_at=$1 WHERE user_id=$2 AND profile_id=$3 AND series_id=$4", r.values["dropped_at"], x.user, x.profile, r.values[r.c.column])
		return
	}
	if r.c.id == "c20" {
		promotionCheck(t, x.provider.UpsertDroppedSyncStates(ctx, []watchsync.DroppedSyncState{{ConnectionID: x.connection, ProviderAccountID: "promotion-account", SeriesID: r.values[r.c.column].(string), ProviderItemKey: "before", RemoteSeen: true}}), "actual UpsertDroppedSyncStates seed")
		promotionSQL(t, ctx, x.pool, "UPDATE watch_provider_dropped_items SET updated_at=$1 WHERE connection_id=$2 AND series_id=$3", r.values["updated_at"], x.connection, r.values[r.c.column])
		return
	}
	var cols []string
	for name := range r.values {
		cols = append(cols, name)
	}
	sort.Strings(cols)
	var marks, quoted []string
	var args []any
	for i, col := range cols {
		quoted = append(quoted, pgx.Identifier{col}.Sanitize())
		marks = append(marks, fmt.Sprintf("$%d", i+1))
		args = append(args, r.values[col])
	}
	tag := promotionSQL(t, ctx, x.pool, "INSERT INTO "+pgx.Identifier{"public", r.c.table}.Sanitize()+"("+strings.Join(quoted, ",")+") VALUES("+strings.Join(marks, ",")+")", args...)
	if tag.RowsAffected() != 1 {
		t.Fatal("fixed fixture INSERT did not affect exactly one row")
	}
}

// Context-scoped receipts carry real query/PID results. The mutex protects
// receipt memory only; all scheduling proof comes from PostgreSQL below.
type promotionScopeKey struct{}
type promotionQueryKey struct{}
type promotionReceipt struct {
	PID uint32
	Key string
}
type promotionEvent struct {
	SQL  string
	Rows int64
	Code string
}
type promotionOperation struct {
	mu                                     sync.Mutex
	kind                                   string
	attempt                                int
	events                                 []promotionEvent
	keys                                   []string
	permit                                 []any
	start, key, armed                      chan promotionReceipt
	startRelease, keyRelease, armedRelease chan struct{}
	releases                               [3]sync.Once
	done                                   chan error
	finished                               chan struct{}
	cleanup                                promotionPublisherCleanup
	cancel                                 context.CancelFunc
}

func promotionOp(kind string) *promotionOperation {
	return &promotionOperation{kind: kind, start: make(chan promotionReceipt, 1), key: make(chan promotionReceipt, 1), armed: make(chan promotionReceipt, 1), startRelease: make(chan struct{}), keyRelease: make(chan struct{}), armedRelease: make(chan struct{}), done: make(chan error, 1), finished: make(chan struct{})}
}
func (o *promotionOperation) release(i int) {
	ch := []chan struct{}{o.startRelease, o.keyRelease, o.armedRelease}
	o.releases[i].Do(func() { close(ch[i]) })
}
func (o *promotionOperation) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 3; i++ {
		o.release(i)
	}
	if o.cancel != nil {
		o.cancel()
	}
	if o.kind == "publisher" {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if !o.join(cleanup) {
			t.Error("independent publisher rollback/completion deadline exceeded")
		}
	}
}
func (o *promotionOperation) join(ctx context.Context) bool {
	select {
	case <-o.finished:
		return true
	case <-ctx.Done():
		return false
	}
}

// This receipt is separate from the immutable publication/permit/claim facts.
type promotionPublisherCleanup struct {
	mu                                sync.Mutex
	checkouts, returned               int
	rollbackStarted, rollbackFinished bool
	rollbackDeadline                  time.Time
}
type promotionRollbackContextKey struct{}
type promotionRollbackContext struct {
	o      *promotionOperation
	cancel context.CancelFunc
}
type promotionTracer struct {
	mu     sync.Mutex
	owners map[*pgx.Conn]*promotionOperation
}

// AcquireEnd runs after the guarded acquire and before Begin. Release runs
// before pgxpool returns the resource to any later borrower.
func (*promotionTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return ctx
}
func (tr *promotionTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	o, _ := ctx.Value(promotionScopeKey{}).(*promotionOperation)
	if o == nil || o.kind != "publisher" || data.Err != nil || data.Conn == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.owners == nil {
		tr.owners = make(map[*pgx.Conn]*promotionOperation)
	}
	tr.owners[data.Conn] = o
	o.cleanup.mu.Lock()
	o.cleanup.checkouts++
	o.cleanup.mu.Unlock()
}
func (tr *promotionTracer) TraceRelease(_ *pgxpool.Pool, data pgxpool.TraceReleaseData) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if o := tr.owners[data.Conn]; o != nil {
		delete(tr.owners, data.Conn)
		o.cleanup.mu.Lock()
		o.cleanup.returned++
		o.cleanup.mu.Unlock()
	}
}

type promotionQuery struct {
	sql  string
	args []any
}

func (tr *promotionTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// The real repository defers Rollback(context.Background()). Bound only
	// that query on the exact still-checked-out publisher connection. pgx uses
	// this returned context for I/O and unwatches it before pool release.
	if data.SQL == "rollback" && ctx.Done() == nil {
		tr.mu.Lock()
		o := tr.owners[conn]
		tr.mu.Unlock()
		if o != nil {
			cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
			deadline, _ := cleanup.Deadline()
			o.cleanup.mu.Lock()
			o.cleanup.rollbackStarted = true
			o.cleanup.rollbackDeadline = deadline
			o.cleanup.mu.Unlock()
			return context.WithValue(cleanup, promotionRollbackContextKey{}, promotionRollbackContext{o, cancel})
		}
	}
	o, _ := ctx.Value(promotionScopeKey{}).(*promotionOperation)
	if o == nil {
		return ctx
	}
	sql := strings.Join(strings.Fields(data.SQL), " ")
	args := append([]any(nil), data.Args...)
	if sql == "SELECT bloem_native_lock_item_keys($1::text[])" || sql == "SELECT public.bloem_native_lock_item_keys($1::text[])" {
		if len(args) == 1 {
			if keys, ok := args[0].([]string); ok {
				o.mu.Lock()
				o.keys = append([]string(nil), keys...)
				o.mu.Unlock()
				args[0] = append([]string(nil), keys...)
			}
		}
	}
	if o.kind == "publisher" && sql == "SELECT bloem_native_lock_item_keys($1::text[])" {
		o.mu.Lock()
		keys := append([]string(nil), o.keys...)
		o.mu.Unlock()
		key := ""
		if len(keys) == 1 {
			key = keys[0]
		}
		o.start <- promotionReceipt{conn.PgConn().PID(), key}
		select {
		case <-o.startRelease:
		case <-ctx.Done():
		}
	}
	return context.WithValue(ctx, promotionQueryKey{}, promotionQuery{sql, args})
}
func (*promotionTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if rollback, ok := ctx.Value(promotionRollbackContextKey{}).(promotionRollbackContext); ok {
		rollback.cancel()
		rollback.o.cleanup.mu.Lock()
		rollback.o.cleanup.rollbackFinished = true
		rollback.o.cleanup.mu.Unlock()
	}
	o, _ := ctx.Value(promotionScopeKey{}).(*promotionOperation)
	if o == nil {
		return
	}
	q, _ := ctx.Value(promotionQueryKey{}).(promotionQuery)
	code := ""
	var pe *pgconn.PgError
	if errors.As(data.Err, &pe) {
		code = pe.Code
	}
	if data.Err != nil && code == "" {
		code = "nonSQL"
	}
	o.mu.Lock()
	o.events = append(o.events, promotionEvent{q.sql, data.CommandTag.RowsAffected(), code})
	if strings.HasPrefix(q.sql, "INSERT INTO bloem_native_publication_permits(") && data.Err == nil {
		o.permit = append([]any(nil), q.args...)
	}
	keys := append([]string(nil), o.keys...)
	o.mu.Unlock()
	if o.kind != "publisher" || data.Err != nil {
		return
	}
	key := ""
	if len(keys) == 1 {
		key = keys[0]
	}
	var receipt chan promotionReceipt
	var release chan struct{}
	switch {
	case q.sql == "SELECT bloem_native_lock_item_keys($1::text[])":
		receipt, release = o.key, o.keyRelease
	case strings.HasPrefix(q.sql, "INSERT INTO bloem_native_publication_permits("):
		receipt, release = o.armed, o.armedRelease
	default:
		return
	}
	receipt <- promotionReceipt{conn.PgConn().PID(), key}
	select {
	case <-release:
	case <-ctx.Done():
	}
}
func promotionWait(t *testing.T, ctx context.Context, o *promotionOperation, ch <-chan promotionReceipt, label string) promotionReceipt {
	t.Helper()
	observe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case r := <-ch:
		return r
	case err := <-o.done:
		promotionCheck(t, err, "publisher before "+label)
		t.Fatal("publisher completed before " + label)
	case <-observe.Done():
		t.Fatal("bounded receipt missing: " + label)
	}
	return promotionReceipt{}
}
func promotionLocks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid uint32, keys []string, want bool) {
	t.Helper()
	for _, key := range keys {
		var held bool
		promotionCheck(t, pool.QueryRow(ctx, `WITH h AS (SELECT hashtextextended('bloem:native-item:'||$2,8500003) AS n) SELECT EXISTS(SELECT 1 FROM pg_locks l,h WHERE l.locktype='advisory' AND l.objsubid=1 AND l.classid=((h.n>>32)&4294967295)::oid AND l.objid=(h.n&4294967295)::oid AND l.granted AND l.pid=$1 AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, int(pid), key).Scan(&held), "exact advisory key observation")
		if held != want {
			t.Fatalf("exact key exclusion witness mismatch pid=%d held=%t want=%t", pid, held, want)
		}
	}
}
func promotionRowWait(t *testing.T, ctx context.Context, pool *pgxpool.Pool, writer, gate uint32, sql string) {
	t.Helper()
	observe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		// Activity fields can be NULL while dispatch/wait state changes. Only
		// an observed TRUE of the complete predicate admits this barrier.
		var blocked *bool
		promotionCheck(t, pool.QueryRow(observe, `SELECT state='active' AND wait_event_type='Lock' AND query=$3 AND $2::int=ANY(pg_blocking_pids(pid)) AND cardinality(pg_blocking_pids(pid))=1 FROM pg_stat_activity WHERE pid=$1`, int(writer), int(gate), sql).Scan(&blocked), "actual UPDATE command snapshot/row wait")
		if blocked != nil && *blocked {
			return
		}
		runtime.Gosched()
	}
}
func promotionClass(t *testing.T, ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, key string, exists bool, want string) {
	t.Helper()
	var actual bool
	var class string
	promotionCheck(t, q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1),bloem_native_item_class($1)", key).Scan(&actual, &class), "actual key class/snapshot")
	if actual != exists || class != want {
		t.Fatalf("key snapshot exists=%t class=%s; want exists=%t class=%s", actual, class, exists, want)
	}
}
func promotionState(t *testing.T, err error, want string) {
	t.Helper()
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != want {
		var code string
		if pe != nil {
			code = pe.Code
		}
		t.Fatalf("expected genuine SQLSTATE=%s got class=%T SQLSTATE=%s", want, err, code)
	}
}

func promotionActor(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (auth.AdminContextClaims, auth.AdminContextClaims) {
	t.Helper()

	users := auth.NewUserRepository(pool)
	tenants := tenancy.NewStore(pool)
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username='a-current-scanner-admin'").Scan(&count); err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	user, err := users.GetByUsername(ctx, "a-current-scanner-admin")
	if count == 0 {
		user, err = users.Create(ctx, models.CreateUserInput{Username: "a-current-scanner-admin", Email: "a-current-scanner-admin@example.test", Password: "domain-fixture-password", Role: "admin"})
		if err != nil {
			promotionCheck(t, err, "actual lifecycle")
		}
		if _, err = tenants.ActivateInitialOwnership(ctx, user.ID); err != nil {
			promotionCheck(t, err, "actual lifecycle")
		}
	}
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	sessions := auth.NewSessionRepository(pool)
	jwt := auth.NewJWTService("native-domain-fixture-signing", time.Hour, 24*time.Hour)
	svc := auth.NewService(auth.NewLocalProvider(users, sessions), jwt, sessions, users, auth.NewInviteCodeRepository(pool), nil, nil)
	pair, actual, err := svc.Login(ctx, user.Username, "domain-fixture-password", "native-domain", "127.0.0.1")
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	login, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	tokens := auth.NewAdminContextTokenService("native-domain-fixture-signing")
	// Claims are derived from the actual enabled account and actual login session,
	// then round-tripped through the signed native administrative context service.
	platformToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID, Scope: auth.AdminScopePlatform})
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	platform, err := tokens.Parse(platformToken)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	var orgID uuid.UUID
	if err = pool.QueryRow(ctx, "SELECT id FROM organizations WHERE is_default").Scan(&orgID); err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	org, err := tenants.GetOrganization(ctx, orgID)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	member, err := tenants.GetMembership(ctx, user.ID, org.ID)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	orgToken, err := tokens.Mint(auth.AdminContextClaims{AccountID: actual.ID, AccountIncarnationID: actual.AccountIncarnationID, SessionID: login.SessionID,
		Scope: auth.AdminScopeOrganization, OrganizationID: org.ID, MembershipID: member.ID, PolicyRevision: org.PolicyRevision, SecurityRevision: member.SecurityRevision, EffectiveAuthority: "organization_admin"})
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	organization, err := tokens.Parse(orgToken)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	return platform, organization
}

// Scanner-only parsed-entry controls use actual domain commands/retained rows;
// the actual RPC executable chain is independently exercised by libraryingest.
func promotionLifecycle(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (storagesource.SourceConfig, storagesource.Binding, *models.MediaFolder) {
	t.Helper()
	actor, _ := promotionActor(t, ctx, pool)
	executable, err := os.Executable()
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.a-current.scanner." + uuid.NewString(), Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "storage", JsonSchema: `{"type":"object","additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("a-current-scanner-key", 3)))
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"scanner": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	source, err := nativestorage.NewSourceManagement(pool, registry).Install(ctx, actor, nativestorage.InstallCommand{ArtifactKey: "scanner", ProviderSourceID: "books", RootEntryID: "root", Enabled: true, Binary: binary, Config: map[string]map[string]any{"storage": {}}})
	if err != nil {
		promotionCheck(t, err, "actual Install")
	}
	var owner uuid.UUID
	if err = pool.QueryRow(ctx, "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", source.SourceKey).Scan(&owner); err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	snapshot, err := registry.Snapshot(ctx, source.SourceKey, owner)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	folders := catalog.NewFolderRepository(pool)
	libraries := nativestorage.NewLibraryManagement(pool, folders, sections.NewRepository(pool), resourcetenancy.NewStore(pool), nil)
	l1, err := libraries.Create(ctx, actor, nativestorage.LibraryCreateCommand{Name: "Current scanner", MetadataLanguage: "en"})
	if err != nil {
		promotionCheck(t, err, "actual Create")
	}
	l2, err := libraries.Initialize(ctx, actor, l1.LibraryID, l1.LibraryRevision)
	if err != nil {
		promotionCheck(t, err, "actual Initialize")
	}
	bound, err := libraries.Bind(ctx, actor, source.SourceKey, l2.LibraryID, source.ConfigurationRevision, l2.LibraryRevision)
	if err != nil {
		promotionCheck(t, err, "actual Bind")
	}
	if bound.FolderID != l1.LibraryID || l2.CreationKey != l1.CreationKey {
		t.Fatal("identity replaced")
	}
	binding, ok, err := storagesource.NewRepository(pool).FolderBinding(ctx, l1.LibraryID)
	if err != nil || !ok {
		promotionCheck(t, err, "binding absent")
	}
	folder, err := folders.GetByID(ctx, l1.LibraryID)
	if err != nil {
		promotionCheck(t, err, "actual lifecycle")
	}
	return snapshot.Source, binding, folder
}

func (x *promotionFixture) discover(t *testing.T, ctx context.Context, cases []promotionColumn) {
	t.Helper()
	var entries []*storagev1.Entry
	for _, c := range cases {
		for _, direction := range []string{"target", "source"} {
			name := c.id + "-" + direction
			title := "Promotion " + name
			var data bytes.Buffer
			z := zip.NewWriter(&data)
			for _, part := range []struct{ name, body string }{
				{"mimetype", "application/epub+zip"},
				{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
				{"OEBPS/content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + title + `</dc:title><dc:language>en</dc:language></metadata><manifest/><spine/></package>`},
			} {
				w, err := z.Create(part.name)
				promotionCheck(t, err, "unique EPUB part")
				_, err = w.Write([]byte(part.body))
				promotionCheck(t, err, "unique EPUB bytes")
			}
			promotionCheck(t, z.Close(), "unique EPUB completion")
			f := nativeBytes("book.epub", data.Bytes())
			f.info.LogicalPath = "matrix/P1Paired/" + name + "/book.epub"
			f.info.Revision = "v1"
			x.files[name] = f
			entries = append(entries, &storagev1.Entry{Id: name, Name: f.info.Name, LogicalPath: f.info.LogicalPath, Revision: f.info.Revision, Size: f.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE})
		}
	}
	run, err := x.r.Begin(ctx, x.source.Key, "promotion-discovery", time.Minute)
	promotionCheck(t, err, "actual Begin discovery")
	cp, ok, err := x.r.NextDirectory(ctx, run)
	promotionCheck(t, err, "actual NextDirectory")
	if !ok {
		t.Fatal("root directory not claimed")
	}
	promotionCheck(t, x.r.ApplyPage(ctx, run, cp, &storagev1.ListResponse{Entries: entries, Complete: true}), "actual ApplyPage")
	promotionCheck(t, x.r.Complete(ctx, run), "actual Complete discovery")
	x.source, err = x.r.Source(ctx, x.source.Key)
	promotionCheck(t, err, "actual source reload")
	x.lease, err = x.r.BeginIngestion(ctx, run.RunID, x.binding.ID, "promotion-ingestion", time.Minute)
	promotionCheck(t, err, "actual BeginIngestion")
}
func (x *promotionFixture) next(t *testing.T, ctx context.Context, name string) *nativeIngestFixture {
	t.Helper()
	promotionCheck(t, x.r.RenewIngestion(ctx, x.lease, time.Minute), "between-cell real lease renewal")
	claim, ok, err := x.r.NextIngestion(ctx, x.lease)
	promotionCheck(t, err, "actual NextIngestion")
	if !ok || claim.Entry.Id != name || claim.Lease != x.lease || claim.Token == uuid.Nil {
		t.Fatal("fixed serial claim identity mismatch")
	}
	return &nativeIngestFixture{s: x.s, r: x.r, pool: x.pool, folder: x.folder, source: x.source, binding: x.binding, claim: claim, file: x.files[name]}
}
func promotionPending(t *testing.T, ctx context.Context, x *nativeIngestFixture, p *nativeEbookPrepared, prepared nativeEbookPrepared, claim storagesource.IngestionClaim) {
	t.Helper()
	if !reflect.DeepEqual(*p, prepared) || x.claim.Lease != claim.Lease || x.claim.Token != claim.Token || !proto.Equal(x.claim.Entry, claim.Entry) {
		t.Fatal("same prepared key/claim mutated across retry")
	}
	source, err := x.r.Source(ctx, x.source.Key)
	promotionCheck(t, err, "retained source reload")
	if !reflect.DeepEqual(source, x.source) {
		t.Fatal("actual source/config tuple changed across attempt")
	}
	c, ok, err := x.r.NextIngestion(ctx, x.claim.Lease)
	promotionCheck(t, err, "real pending claim reload")
	if !ok || c.Lease != claim.Lease || c.Token != claim.Token || !proto.Equal(c.Entry, claim.Entry) {
		t.Fatal("claim epoch/token/location/config advanced on refusal")
	}
	var present bool
	var class string
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM media_files WHERE file_path=$1),bloem_native_item_class($2)", p.location, p.contentID).Scan(&present, &class), "retained prepared absence witness")
	if present || class != "local" || p.existingID != "" || !p.itemVersion.IsZero() {
		t.Fatal("failed attempt changed prepared absence/native witness")
	}
}
func promotionPublisher(t *testing.T, ctx context.Context, x *nativeIngestFixture, p *nativeEbookPrepared, attempt int) *promotionOperation {
	t.Helper()
	o := promotionOp("publisher")
	o.attempt = attempt
	worker, cancel := context.WithTimeout(context.WithValue(ctx, promotionScopeKey{}, o), 10*time.Second)
	o.cancel = cancel
	// The existing adapter calls the actual authorized repository and actual
	// Begin/base/StoredFile/Finish/checkpoint/COMMIT methods with unchanged p.
	go func() { defer close(o.finished); o.done <- x.publishPreparedAuthorized(worker, p) }()
	return o
}
func promotionPermit(t *testing.T, ctx context.Context, o *promotionOperation, x *nativeIngestFixture, p *nativeEbookPrepared) {
	t.Helper()
	o.mu.Lock()
	args := append([]any(nil), o.permit...)
	o.mu.Unlock()
	if len(args) != 30 {
		t.Fatal("actual armed permit tuple not captured")
	}
	// All claim/location/preparation fields must be the actual retained tuple.
	expected := map[int]any{0: p.contentID, 1: nil, 2: nil, 3: x.folder.ID, 6: x.source.Key, 8: x.binding.ID, 9: x.claim.Lease.RunID,
		12: x.claim.Lease.ConfigurationRevision, 13: 1, 14: x.claim.Lease.Owner, 15: x.claim.Lease.Epoch, 16: x.claim.Token, 17: x.claim.Entry.Id, 18: x.claim.Entry.Revision,
		19: x.claim.Entry.LogicalPath, 20: p.location, 21: x.claim.Entry.Kind, 22: x.claim.Entry.Size, 23: x.claim.Entry.ModifiedUnixNano,
		24: normalizeFileModifiedAt(time.Unix(0, x.claim.Entry.ModifiedUnixNano)), 25: p.book.Format, 26: p.groupKey, 29: (*int64)(nil)}
	for i, v := range expected {
		if !reflect.DeepEqual(args[i], v) {
			t.Fatalf("real prepared permit tuple differs at fixed argument %d", i+1)
		}
	}
	for _, i := range []int{5, 7} {
		if id, ok := args[i].(uuid.UUID); !ok || id == uuid.Nil {
			t.Fatal("actual permit owner absent")
		}
	}
	for _, i := range []int{4, 10, 11} {
		if n, ok := args[i].(int64); !ok || n <= 0 {
			t.Fatal("actual permit revision/installation/generation absent")
		}
	}
	if args[4] != (int64(3)) || x.source.InstallationID == nil || args[10] != *x.source.InstallationID || args[7] != x.source.OwnerID {
		t.Fatal("actual permit retained lifecycle/source mismatch")
	}
	var generation int64
	var folderOwner uuid.UUID
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT i.runtime_generation,f.owner_id FROM plugin_installations i,media_folders f WHERE i.id=$1 AND f.id=$2", *x.source.InstallationID, x.folder.ID).Scan(&generation, &folderOwner), "independent retained generation/folder owner")
	if args[11] != generation || args[5] != folderOwner {
		t.Fatal("actual permit generation/folder owner mismatch")
	}
	var shape map[string]any
	raw, ok := args[27].([]byte)
	if !ok {
		t.Fatal("actual permit file shape missing")
	}
	promotionCheck(t, json.Unmarshal(raw, &shape), "actual permit file shape")
	if shape["content_id"] != p.contentID || shape["file_path"] != p.location || shape["content_group_key"] != p.groupKey || shape["probe_source"] != "native" || shape["container"] != p.book.Format {
		t.Fatal("actual permit file identity/shape mismatch")
	}
	if raw, ok := args[28].([]byte); !ok || string(raw) != "[]" {
		t.Fatal("unexpected permit sidecar shape")
	}
}
func promotionNativeTuple(t *testing.T, ctx context.Context, x *nativeIngestFixture, p *nativeEbookPrepared, o *promotionOperation) {
	t.Helper()
	promotionClass(t, ctx, x.pool, p.contentID, true, "native")
	var file int
	var key, root, observed, group, format, probe, entry, revision, path, location string
	var folder int
	var binding uuid.UUID
	var config int64
	promotionCheck(t, x.pool.QueryRow(ctx, `SELECT f.id,f.content_id,f.media_folder_id,f.file_path,f.canonical_root_path,f.observed_root_path,f.content_group_key,f.container,f.probe_source,r.binding_id,r.entry_id,r.revision,r.logical_path,r.configuration_revision FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.file_path=$1`, p.location).Scan(&file, &key, &folder, &location, &root, &observed, &group, &format, &probe, &binding, &entry, &revision, &path, &config), "independently committed file/ref tuple")
	if file <= 0 || key != p.contentID || folder != x.folder.ID || location != p.location || root != p.location || observed != p.location || group != p.groupKey || format != p.book.Format || probe != "native" || binding != x.binding.ID || entry != x.claim.Entry.Id || revision != x.claim.Entry.Revision || path != x.claim.Entry.LogicalPath || config != x.claim.Lease.ConfigurationRevision {
		t.Fatal("committed native file/ref tuple mismatch")
	}
	var persisted, shape map[string]any
	var raw []byte
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT to_jsonb(f) FROM media_files f WHERE id=$1", file).Scan(&raw), "full committed file row")
	promotionCheck(t, json.Unmarshal(raw, &persisted), "committed file JSON")
	o.mu.Lock()
	shapeBytes := append([]byte(nil), o.permit[27].([]byte)...)
	permitArgs := append([]any(nil), o.permit...)
	o.mu.Unlock()
	promotionCheck(t, json.Unmarshal(shapeBytes, &shape), "armed canonical file shape")
	for name, want := range shape {
		if !reflect.DeepEqual(persisted[name], want) {
			t.Fatalf("committed canonical file differs from armed permit field %s", name)
		}
	}
	var modified time.Time
	var size int64
	var title, itemType string
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT f.file_modified_at,f.file_size,i.title,i.type FROM media_files f JOIN media_items i ON i.content_id=f.content_id WHERE f.id=$1", file).Scan(&modified, &size, &title, &itemType), "committed item/normalized file values")
	if !modified.Equal(normalizeFileModifiedAt(time.Unix(0, x.claim.Entry.ModifiedUnixNano))) || size != x.claim.Entry.Size || title != p.book.Title || itemType != "ebook" {
		t.Fatal("complete committed item/file tuple differs")
	}
	promotionSave(t, x.pool, fmt.Sprintf("publication-%d-full-armed-permit", o.attempt), permitArgs)
	var members, permits int
	var last, pending, rev string
	var token *uuid.UUID
	var epoch int64
	var owner string
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM media_item_libraries WHERE content_id=$1 AND media_folder_id=$2),(SELECT count(*) FROM bloem_native_publication_permits)", p.contentID, x.folder.ID).Scan(&members, &permits), "independent committed member/permit state")
	promotionCheck(t, x.pool.QueryRow(ctx, "SELECT last_entry_id,pending_entry_id,pending_revision,pending_token,lease_epoch,owner FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", x.claim.Lease.RunID, x.binding.ID).Scan(&last, &pending, &rev, &token, &epoch, &owner), "independent committed claim checkpoint")
	if members != 1 || permits != 0 || last != entry || pending != "" || rev != "" || token != nil || epoch != x.claim.Lease.Epoch || owner != x.claim.Lease.Owner {
		t.Fatal("COMMIT member/permit/claim tuple mismatch")
	}
	o.mu.Lock()
	events := append([]promotionEvent(nil), o.events...)
	o.mu.Unlock()
	phases := []string{"INSERT INTO bloem_native_publication_permits(", "UPDATE bloem_native_publication_permits SET stored_file_key=", "UPDATE bloem_native_publication_permits SET phase='finished'", "DELETE FROM bloem_native_publication_permits", "UPDATE bloem_storage_ingestion SET last_entry_id=", "commit"}
	pos := 0
	for _, e := range events {
		if pos < len(phases) && strings.HasPrefix(e.SQL, phases[pos]) && e.Code == "" {
			pos++
		}
	}
	if pos != len(phases) {
		t.Fatalf("real Begin/Record/Finish/checkpoint/COMMIT trace incomplete: phases=%d", pos)
	}
	promotionSave(t, x.pool, "committed-native-tuple", map[string]any{"key": key, "file": file, "folder": folder, "binding": binding, "entry": entry, "revision": revision, "logicalPath": path, "group": group, "configurationRevision": config, "leaseEpoch": epoch, "pendingCleared": true, "member": members, "permits": permits, "events": events})
	t.Logf("observed real native COMMIT key=%s file=%d entry=%s; member/ref/permit/claim tuple verified", key, file, entry)
}
func promotionCompletePublisher(t *testing.T, ctx context.Context, x *nativeIngestFixture, p *nativeEbookPrepared, o *promotionOperation, success bool, shell bool) {
	t.Helper()
	key := promotionWait(t, ctx, o, o.key, "successful Begin key exclusion")
	if key.Key != p.contentID {
		t.Fatal("actual publisher key differs from prepared allocator")
	}
	promotionLocks(t, ctx, x.pool, key.PID, []string{p.contentID}, true)
	promotionClass(t, ctx, x.pool, p.contentID, shell, "local")
	o.release(1)
	if success {
		armed := promotionWait(t, ctx, o, o.armed, "successful armed Begin")
		if armed.PID != key.PID || armed.Key != p.contentID {
			t.Fatal("armed publisher PID/key changed")
		}
		promotionPermit(t, ctx, o, x, p)
		promotionLocks(t, ctx, x.pool, armed.PID, []string{p.contentID}, true)
		promotionClass(t, ctx, x.pool, p.contentID, false, "local")
		o.release(2)
	}
	var err error
	select {
	case err = <-o.done:
	case <-ctx.Done():
		t.Fatal("bounded publisher completion missing")
	}
	if !o.join(ctx) {
		t.Fatal("bounded publisher rollback/completion receipt missing")
	}
	if success {
		promotionCheck(t, err, "actual prepared publication")
		promotionNativeTuple(t, ctx, x, p, o)
	} else {
		var typed *catalog.NativeOnboardingError
		if !errors.As(err, &typed) || typed.Code != "native_storage_unavailable" || typed.Cause != nil {
			var code string
			if typed != nil {
				code = typed.Code
			}
			t.Fatalf("retained invariant must refuse actual local/drop evidence; class=%T code=%s", err, code)
		}
		o.mu.Lock()
		armed := len(o.permit) != 0
		events := append([]promotionEvent(nil), o.events...)
		o.mu.Unlock()
		if armed {
			t.Fatal("invariant refusal armed a permit")
		}
		for _, e := range events {
			if e.SQL == "commit" || strings.HasPrefix(e.SQL, "UPDATE bloem_storage_ingestion SET last_entry_id=") {
				t.Fatal("refused publication advanced checkpoint/COMMIT")
			}
		}
		promotionSave(t, x.pool, fmt.Sprintf("refused-publication-%d-events", o.attempt), events)
	}
	promotionLocks(t, ctx, x.pool, key.PID, []string{p.contentID}, false)
	t.Logf("prepared publication attempt outcome=%s key=%s", map[bool]string{true: "COMMIT", false: "invariant-refusal"}[success], p.contentID)
}

func promotionRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tx pgx.Tx, r promotionRow, from, to string, local bool) *promotionOperation {
	t.Helper()
	o := promotionOp("run")
	runCtx, cancel := context.WithTimeout(context.WithValue(ctx, promotionScopeKey{}, o), 10*time.Second)
	defer cancel()
	report, err := reattribute.Run(runCtx, tx, reattribute.Options{FromContentID: from, ToContentID: to, WholeItem: true})
	if local {
		promotionCheck(t, err, "real local Run")
		if report == nil {
			t.Fatal("real local Run report absent")
		}
	} else {
		promotionState(t, err, "BN001")
		if report != nil {
			t.Fatal("native Run produced mutation report")
		}
	}
	keys := []string{from, to}
	sort.Strings(keys)
	o.mu.Lock()
	actual := append([]string(nil), o.keys...)
	events := append([]promotionEvent(nil), o.events...)
	o.mu.Unlock()
	if !reflect.DeepEqual(keys, actual) {
		t.Fatal("real Run did not coordinate both actual changed endpoints")
	}
	promotionLocks(t, ctx, pool, tx.Conn().PgConn().PID(), keys, true)
	selected := 0
	dml := 0
	classified := 0
	for _, e := range events {
		if strings.HasPrefix(e.SQL, "SELECT public.bloem_native_item_class(") {
			classified++
		}
		upper := strings.ToUpper(e.SQL)
		if strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "DELETE ") {
			dml++
			if strings.HasPrefix(e.SQL, "UPDATE "+r.c.table+" ") && strings.Contains(e.SQL, r.c.column+" = p.to_id") && e.Rows == 1 && e.Code == "" {
				selected++
			}
		}
	}
	if !local && (dml != 0 || classified == 0) {
		t.Fatal("real Run did not refuse at writer preflight before DML")
	}
	if local && selected != 1 {
		t.Fatalf("actual selected Run UPDATE1 missing: count=%d", selected)
	}
	promotionSave(t, pool, "real-Run-events", map[string]any{"nativeRefusal": !local, "keys": keys, "selectedUpdate1": selected, "report": report, "events": events})
	return o
}
func promotionMoved(t *testing.T, r promotionRow, before, after map[string]any, to string, run bool) {
	t.Helper()
	expected := make(map[string]any, len(before))
	for k, v := range before {
		expected[k] = v
	}
	expected[r.c.column] = to
	if run && r.c.id == "c20" {
		expected["remote_seen"] = false
		expected["provider_item_key"] = ""
		old, ok1 := before["updated_at"].(string)
		next, ok2 := after["updated_at"].(string)
		if !ok1 || !ok2 || old == next {
			t.Fatal("real provider Run timestamp did not advance")
		}
		expected["updated_at"] = next
	}
	if !reflect.DeepEqual(expected, after) {
		t.Fatal("selected row's exact identity/payload movement mismatch (private snapshots retained)")
	}
}
func promotionOnlyMoved(t *testing.T, r promotionRow, before, after map[string]json.RawMessage) {
	t.Helper()
	for _, table := range promotionTables {
		if table == r.c.table {
			var a, b []json.RawMessage
			promotionCheck(t, json.Unmarshal(before[table], &a), "local before rows")
			promotionCheck(t, json.Unmarshal(after[table], &b), "local after rows")
			if len(a) != 1 || len(b) != 1 {
				t.Fatal("local Run did not preserve the sole original selected child")
			}
			continue
		}
		if !bytes.Equal(before[table], after[table]) {
			t.Errorf("local Run changed unrelated full logical table %s", table)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}
func (x *promotionFixture) cell(t *testing.T, ctx context.Context, c promotionColumn, direction string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	var writer, gate, run pgx.Tx
	var ops []*promotionOperation
	var workers sync.WaitGroup
	// Registered before any held transaction or worker. Expired work never owns
	// rollback/drain; all workers join before this group's pool/clone cleanup.
	defer func() {
		cancel()
		for _, o := range ops {
			o.drain(t)
		}
		workers.Wait()
		promotionRollback(gate)
		promotionRollback(writer)
		promotionRollback(run)
	}()
	ingest := x.next(t, ctx, c.id+"-"+direction)
	p, err := x.s.prepareNativeEbook(ctx, ingest.claim, x.folder, ingest.file, NativeEbookSidecars{Complete: true})
	promotionCheck(t, err, "actual prepareNativeEbook once")
	if p.contentID == "" || p.existingID != "" || !p.itemVersion.IsZero() || p.book.Title != "Promotion "+c.id+"-"+direction || p.book.ISBN != "" || len(p.book.Authors) != 0 || p.book.Series != "" || p.book.Cover != nil || len(p.sidecars) != 0 {
		t.Fatal("unique allocator/preparation witness invalid")
	}
	prepared := *p
	retainedClaim := ingest.claim
	retainedClaim.Entry = proto.Clone(ingest.claim.Entry).(*storagev1.Entry)
	native := p.contentID
	local := "promotion-local-" + uuid.NewString()
	from, to := local, native
	if direction == "source" {
		from, to = native, local
	}
	invariant := direction == "source" && (c.id == "c11" || c.id == "c18" || c.id == "c20")
	shell := invariant && c.id == "c11"
	first := promotionPublisher(t, ctx, ingest, p, 1)
	ops = append(ops, first)
	start := promotionWait(t, ctx, first, first.start, "prepared Begin before key dispatch")
	if start.Key != native {
		t.Fatal("actual prepared allocator key not used by Begin")
	}
	promotionLocks(t, ctx, x.pool, start.PID, []string{native, local}, false)
	promotionSQL(t, ctx, x.pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Local')", local)
	if shell {
		promotionSQL(t, ctx, x.pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'ebook','matched','Local shell')", native)
	}
	row := x.row(c, from)
	x.seed(t, ctx, row)
	original := row.read(t, ctx, x.pool, from)
	promotionClass(t, ctx, x.pool, native, shell, "local")
	before := promotionSnapshot(t, ctx, x.pool, "before-primary")
	writer, err = x.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	promotionCheck(t, err, "direct RC WriterTx begin")
	promotionClass(t, ctx, writer, native, shell, "local")
	if !reflect.DeepEqual(row.read(t, ctx, writer, from), original) {
		t.Fatal("writer old row snapshot differs")
	}
	if !invariant {
		run, err = x.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		promotionCheck(t, err, "old RC RunTx begin")
		promotionClass(t, ctx, run, native, false, "local")
		if !reflect.DeepEqual(row.read(t, ctx, run, from), original) {
			t.Fatal("companion old child snapshot differs")
		}
	}
	gate, err = x.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	promotionCheck(t, err, "soft row GateTx begin")
	where, args := row.predicate(from, 0)
	var gateRow []byte
	promotionCheck(t, gate.QueryRow(ctx, "SELECT to_jsonb(r) FROM "+pgx.Identifier{"public", c.table}.Sanitize()+" r WHERE "+where+" FOR UPDATE", args...).Scan(&gateRow), "one exact source soft row gate")
	sql, updateArgs := row.update(from, to)
	direct := promotionOp("direct")
	ops = append(ops, direct)
	workerCtx, stop := context.WithTimeout(context.WithValue(ctx, promotionScopeKey{}, direct), 10*time.Second)
	direct.cancel = stop
	type directResult struct {
		tag pgconn.CommandTag
		err error
	}
	done := make(chan directResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		tag, err := writer.Exec(workerCtx, sql, updateArgs...)
		done <- directResult{tag, err}
	}()
	writerPID, gatePID := writer.Conn().PgConn().PID(), gate.Conn().PgConn().PID()
	promotionRowWait(t, ctx, x.pool, writerPID, gatePID, sql)
	promotionLocks(t, ctx, x.pool, writerPID, []string{native, local}, false)
	promotionLocks(t, ctx, x.pool, gatePID, []string{native, local}, false)
	promotionSave(t, x.pool, "stale-command-witness", map[string]any{"sql": sql, "writerPID": writerPID, "gatePID": gatePID, "promoterPID": start.PID, "key": native, "oneRowBlocker": true, "noWriterItemExclusion": true, "sourceSnapshot": shell, "direction": direction, "column": c})
	t.Logf("stale UPDATE dispatched before publication; sole soft-row blocker writer=%d gate=%d promoter=%d key=%s", writerPID, gatePID, start.PID, native)
	first.release(0)
	promotionCompletePublisher(t, ctx, ingest, p, first, !invariant, shell)
	afterP := promotionSnapshot(t, ctx, x.pool, "after-primary")
	if invariant {
		promotionEqual(t, before, afterP)
		promotionPending(t, ctx, ingest, p, prepared, retainedClaim)
	}
	promotionRollback(gate)
	gate = nil
	var result directResult
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("direct UPDATE completion exceeded cell budget")
	}
	workers.Wait()
	if invariant {
		promotionCheck(t, result.err, "lawful invariant direct UPDATE")
		if result.tag.RowsAffected() != 1 {
			t.Fatal("invariant direct UPDATE did not move exactly one child")
		}
		promotionMoved(t, row, original, row.read(t, ctx, writer, to), to, false)
	} else {
		promotionState(t, result.err, "BN001")
		direct.mu.Lock()
		events := append([]promotionEvent(nil), direct.events...)
		direct.mu.Unlock()
		promotionSave(t, x.pool, "direct-column-refusal-events", events)
		var pe *pgconn.PgError
		_ = errors.As(result.err, &pe)
		book := strings.Contains(pe.Where, "bloem_native_guard_book_drop")
		if (c.id == "c18" || c.id == "c20") && direction == "target" && !book {
			t.Fatal("target drop refusal did not witness the actual book precheck")
		}
		t.Logf("direct stale UPDATE refused BN001; bookPrecheck=%t", book)
	}
	promotionRollback(writer)
	writer = nil
	promotionEqual(t, afterP, promotionSnapshot(t, ctx, x.pool, "after-direct-rollback"))
	attempts := 1
	rowRemoved := false
	if !invariant {
		promotionRun(t, ctx, x.pool, run, row, from, to, false)
		runPID := run.Conn().PgConn().PID()
		promotionRollback(run)
		run = nil
		promotionLocks(t, ctx, x.pool, runPID, []string{native, local}, false)
		promotionEqual(t, afterP, promotionSnapshot(t, ctx, x.pool, "after-old-Run-refusal"))
		t.Log("previously opened RC RunTx reclassified native endpoint; BN001 before all DML; full rollback retained")
	} else {
		run, err = x.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		promotionCheck(t, err, "fresh actual local RunTx")
		promotionRun(t, ctx, x.pool, run, row, from, to, true)
		moved := row.read(t, ctx, run, to)
		promotionMoved(t, row, original, moved, to, true)
		runPID := run.Conn().PgConn().PID()
		promotionCheck(t, run.Commit(ctx), "actual local Run owner COMMIT")
		run = nil
		promotionLocks(t, ctx, x.pool, runPID, []string{native, local}, false)
		afterRun := promotionSnapshot(t, ctx, x.pool, "after-local-Run-COMMIT")
		promotionOnlyMoved(t, row, afterP, afterRun)
		promotionMoved(t, row, original, row.read(t, ctx, x.pool, to), to, true)
		promotionPending(t, ctx, ingest, p, prepared, retainedClaim)
		retry := promotionPublisher(t, ctx, ingest, p, 2)
		ops = append(ops, retry)
		promotionWait(t, ctx, retry, retry.start, "same prepared retry start")
		retry.release(0)
		promotionCompletePublisher(t, ctx, ingest, p, retry, !shell, shell)
		attempts++
		if shell {
			promotionEqual(t, afterRun, promotionSnapshot(t, ctx, x.pool, "after-post-Run-shell-refusal"))
			promotionPending(t, ctx, ingest, p, prepared, retainedClaim)
			where, args = row.predicate(to, 0)
			tag := promotionSQL(t, ctx, x.pool, "DELETE FROM "+pgx.Identifier{"public", c.table}.Sanitize()+" WHERE "+where, args...)
			if tag.RowsAffected() != 1 {
				t.Fatal("legitimate local collection cleanup did not remove named child")
			}
			rowRemoved = true
			_, err = x.s.itemRepo.Delete(ctx, native)
			promotionCheck(t, err, "ordinary local shell ItemRepository.Delete")
			promotionClass(t, ctx, x.pool, native, false, "local")
			_ = promotionSnapshot(t, ctx, x.pool, "after-legitimate-local-shell-cleanup")
			promotionPending(t, ctx, ingest, p, prepared, retainedClaim)
			third := promotionPublisher(t, ctx, ingest, p, 3)
			ops = append(ops, third)
			promotionWait(t, ctx, third, third.start, "same prepared post-cleanup start")
			third.release(0)
			promotionCompletePublisher(t, ctx, ingest, p, third, true, false)
			attempts++
		}
		t.Log("primary source-invariant refusal retained; direct movement rolled back, actual one-child local Run COMMIT, same prepared key completed only after lawful movement/cleanup")
	}
	if !rowRemoved {
		identity := from
		if invariant {
			identity = to
		}
		where, args = row.predicate(identity, 0)
		tag := promotionSQL(t, ctx, x.pool, "DELETE FROM "+pgx.Identifier{"public", c.table}.Sanitize()+" WHERE "+where, args...)
		if tag.RowsAffected() != 1 {
			t.Fatal("named local soft-row cleanup did not remove exactly the selected child")
		}
	}
	promotionClass(t, ctx, x.pool, native, true, "native")
	promotionSave(t, x.pool, "cell-outcome", map[string]any{"column": c.id, "table": c.table, "identityColumn": c.column, "direction": direction, "key": native, "primaryNativePromotion": !invariant, "primaryInvariantRefusal": invariant, "directStatementBeforePublisherCommit": true, "realRunNativeRefusal": !invariant, "realRunLocalCommit": invariant, "preparedPublicationAttempts": attempts, "eventualFixtureCommits": 1, "samePreparedUnchanged": reflect.DeepEqual(*p, prepared)})
	t.Logf("P1Paired primary=%s realRun=%s publicationAttempts=%d eventualFixtureCommits=1", map[bool]string{true: "invariant-refusal", false: "native-promotion"}[invariant], map[bool]string{true: "local-COMMIT", false: "native-BN001"}[invariant], attempts)
	return attempts
}
func TestNativeOnboardingPromotionIdentityColumnsDB(t *testing.T) {
	tables := map[string]bool{}
	for i, c := range promotionColumns {
		if c.id != fmt.Sprintf("c%02d", i+1) {
			t.Fatal("original column inventory order changed")
		}
		tables[c.table] = true
	}
	if len(promotionColumns) != 20 || len(tables) != 18 {
		t.Fatal("fixed original20/18 inventory changed")
	}
	t.Run("P1Paired", func(t *testing.T) {
		for group := 0; group < 4; group++ {
			t.Run(fmt.Sprintf("g%02d", group+1), func(t *testing.T) {
				started := time.Now()
				work, stopWork := context.WithDeadline(t.Context(), started.Add(270*time.Second))
				defer stopWork()
				setup, cancel := context.WithTimeout(work, 90*time.Second)
				x := promotionSetup(t, setup)
				cases := promotionColumns[group*5 : (group+1)*5]
				x.discover(t, setup, cases)
				promotionCheck(t, setup.Err(), "bounded <=90s group setup")
				cancel()
				t.Logf("group setup completed in %s; exactly one verified lifecycle clone", time.Since(started))
				leaves, attempts := 0, 0
				// Source sorts before target in the repository's real entry-ID keyset.
				for _, c := range cases {
					for _, direction := range []string{"source", "target"} {
						if work.Err() != nil || time.Until(started.Add(270*time.Second)) < 15*time.Second {
							t.Fatal("group work budget exhausted before next leaf; smaller explicit group required")
						}
						ok := t.Run(c.id+"/"+direction, func(t *testing.T) { attempts += x.cell(t, work, c, direction); leaves++ })
						if !ok {
							t.Fatal("dependent leaves stopped after failed cell")
						}
					}
				}
				if leaves != 10 {
					t.Fatalf("focused group executed %d leaves, want exactly10", leaves)
				}
				expected := 10
				if group >= 2 {
					expected = 12
				}
				if attempts != expected {
					t.Fatalf("actual prepared attempt ledger=%d want=%d", attempts, expected)
				}
				final, cancel := context.WithTimeout(work, 10*time.Second)
				defer cancel()
				_, ok, err := x.r.NextIngestion(final, x.lease)
				promotionCheck(t, err, "actual final NextIngestion completion")
				if ok {
					t.Fatal("group left an uncompleted real claim")
				}
				t.Logf("group complete: primaryCells=%d actualPreparedPublicationAttempts=%d eventualFixtureCommits=10", leaves, attempts)
			})
		}
	})
}

// Cancellation after a real armed permit reaches the retained repository's
// background rollback. This exercises cancellation/receipt/checkout/row cleanup;
// it does not inject or claim an actual stalled network response.
func TestNativeOnboardingPromotionPublisherCancellationDB(t *testing.T) {
	setup, stopSetup := context.WithTimeout(t.Context(), 90*time.Second)
	defer stopSetup()
	x := promotionSetup(t, setup)
	x.discover(t, setup, promotionColumns[:1])
	promotionCheck(t, setup.Err(), "bounded cancellation fixture setup")
	stopSetup()
	work, stopWork := context.WithTimeout(t.Context(), 15*time.Second)
	defer stopWork()
	ingest := x.next(t, work, "c01-source")
	p, err := x.s.prepareNativeEbook(work, ingest.claim, x.folder, ingest.file, NativeEbookSidecars{Complete: true})
	promotionCheck(t, err, "actual cancellation prepareNativeEbook")
	prepared := *p
	claim := ingest.claim
	claim.Entry = proto.Clone(ingest.claim.Entry).(*storagev1.Entry)
	before := promotionSnapshot(t, work, x.pool, "before-publisher-cancellation")
	o := promotionPublisher(t, work, ingest, p, 1)
	defer o.drain(t)
	start := promotionWait(t, work, o, o.start, "cancellation actual Begin start")
	if start.Key != p.contentID {
		t.Fatal("cancellation publisher lost its prepared key")
	}
	o.release(0)
	key := promotionWait(t, work, o, o.key, "cancellation successful key exclusion")
	if key != start {
		t.Fatal("cancellation publisher changed key/connection")
	}
	promotionLocks(t, work, x.pool, key.PID, []string{p.contentID}, true)
	o.release(1)
	armed := promotionWait(t, work, o, o.armed, "cancellation actual armed permit")
	if armed != key {
		t.Fatal("cancellation armed permit changed key/connection")
	}
	promotionPermit(t, work, o, ingest, p)
	o.cancel()
	o.drain(t)
	if t.Failed() {
		return
	}
	if err := <-o.done; err == nil {
		t.Fatal("canceled real publisher unexpectedly committed")
	}
	o.cleanup.mu.Lock()
	checkouts, returned := o.cleanup.checkouts, o.cleanup.returned
	started, finished := o.cleanup.rollbackStarted, o.cleanup.rollbackFinished
	deadline := o.cleanup.rollbackDeadline
	o.cleanup.mu.Unlock()
	if checkouts != 1 || returned != 1 || !started || !finished || deadline.IsZero() {
		t.Fatal("canceled publisher lacks bounded background rollback/return/completion receipts")
	}
	observe, stopObserve := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopObserve()
	promotionLocks(t, observe, x.pool, key.PID, []string{p.contentID}, false)
	promotionEqual(t, before, promotionSnapshot(t, observe, x.pool, "after-publisher-cancellation"))
	promotionPending(t, observe, ingest, p, prepared, claim)
	if x.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("canceled publisher left an acquired pool connection")
	}
	promotionSave(t, x.pool, "publisher-cancellation-cleanup", map[string]any{
		"actualArmedPermit": true, "backgroundRollbackStarted": started,
		"backgroundRollbackFinished": finished, "independentRollbackDeadline": true,
		"checkouts": checkouts, "returned": returned, "publisherJoined": true,
		"fullLogicalRollbackEqual": true, "samePreparedClaimRetained": true,
		"networkStallInjected": false,
	})
	t.Log("actual armed publisher canceled; bounded background rollback/completion/checkout return and full logical rollback observed")
}
