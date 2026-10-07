//go:build integration

package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/catalog/filesplit"
	"github.com/Silo-Server/silo-server/internal/catalog/reattribute"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/progresssync"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/sections"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var writerCorrectionStamp = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

type writerCorrectionEvent struct {
	SQL, State string
	Args       []any
}

type writerCorrectionTrace struct {
	mu                 sync.Mutex
	active             bool
	events             []writerCorrectionEvent
	startHook, endHook func(context.Context, writerCorrectionEvent)
}

type writerCorrectionQueryKey struct{}

func (q *writerCorrectionTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	active, hook := q.active, q.startHook
	q.mu.Unlock()
	if active {
		event := writerCorrectionEvent{SQL: strings.Join(strings.Fields(d.SQL), " "), Args: append([]any(nil), d.Args...)}
		if hook != nil {
			hook(ctx, event)
		}
		return context.WithValue(ctx, writerCorrectionQueryKey{}, event)
	}
	return ctx
}

func (q *writerCorrectionTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	event, ok := ctx.Value(writerCorrectionQueryKey{}).(writerCorrectionEvent)
	if !ok {
		return
	}
	var pgerr *pgconn.PgError
	if errors.As(d.Err, &pgerr) {
		event.State = pgerr.Code
	} else if d.Err != nil {
		event.State = "non-SQL-error"
	}
	q.mu.Lock()
	q.events = append(q.events, event)
	hook := q.endHook
	q.mu.Unlock()
	if hook != nil {
		hook(ctx, event)
	}
}

func (q *writerCorrectionTrace) start() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active, q.events = true, nil
}

func (q *writerCorrectionTrace) stop() []writerCorrectionEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.active, q.startHook, q.endHook = false, nil, nil
	return append([]writerCorrectionEvent(nil), q.events...)
}

// Install the tracer before SAME-pool preparation; preparing another pool would
// reset the clone and erase the legal lifecycle fixture.
func writerCorrectionDatabase(t *testing.T) (*pgxpool.Pool, *writerCorrectionTrace) {
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
	if err != nil {
		t.Fatal("private fixture read failed")
	}
	savedDSN := strings.TrimSpace(string(data))
	original, err := pgxpool.ParseConfig(savedDSN)
	if err != nil {
		t.Fatal("private fixture parse failed")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), savedDSN, false)
	if err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error("writer correction clone cleanup", err)
		} else {
			t.Log("writer correction actual UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal("unowned clone configuration")
	}
	trace := &writerCorrectionTrace{}
	cfg.MaxConns, cfg.ConnConfig.Tracer = 8, trace
	pool, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("guarded clone open failed")
	}
	var actual string
	if err = pool.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil || actual != cfg.ConnConfig.Database || actual == original.ConnConfig.Database {
		t.Fatal("actual UUID clone identity unverified")
	}
	t.Logf("writer correction actual UUID clone: %s", actual)
	if err = bloemtestdb.PrepareNativeOnboardingPool(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	if !catalog.NativeStorageSchemaReady(t.Context(), pool) {
		t.Fatal("CURRENT default schema not ready")
	}
	return pool, trace
}

// Full rows of the writer's logical item/file/member/reference/user-state
// families, including exact timestamps, payloads and queued rename records.
func writerCorrectionSnapshot(t *testing.T, pool *pgxpool.Pool, label string) map[string]json.RawMessage {
	t.Helper()
	state := map[string]json.RawMessage{}
	for _, table := range strings.Fields(`media_items media_files media_item_libraries episode_libraries episodes seasons media_extras
		media_item_provider_ids media_item_roots media_item_groups user_watch_progress user_personal_collection_items
		user_personal_collections user_personal_collection_profiles library_collection_items user_favorites user_watchlist user_ratings
		user_home_item_dismissals user_history_hidden_items user_audio_preferences user_subtitle_preferences user_series_playback_preferences
		user_watch_history admin_playback_history user_downloads downloads bloem_storage_file_refs bloem_storage_bindings
		bloem_storage_sources bloem_storage_entries bloem_storage_ingestion bloem_native_libraries bloem_native_publication_permits catalog_search_index_events`) {
		var raw []byte
		if err := pool.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM "+pgx.Identifier{table}.Sanitize()+" r").Scan(&raw); err != nil {
			t.Fatalf("logical snapshot %s: %v", table, err)
		}
		state[table] = json.RawMessage(raw)
	}
	dir := filepath.Join("../../.superpowers/sdd/2026-10-06-native-storage-onboarding-implementation-plan-4/task-A-writer-correction-fix-1-states", pool.Config().ConnConfig.Database)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	name := strings.ReplaceAll(t.Name(), "/", "__") + "-" + label + ".json"
	if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func writerCorrectionRetained(t *testing.T, before, after map[string]json.RawMessage) {
	t.Helper()
	for table, rows := range before {
		if !reflect.DeepEqual(rows, after[table]) {
			t.Errorf("complete logical %s rows changed after refusal/rollback", table)
		}
	}
}

func writerCorrectionLocal(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := "writer-local-" + uuid.NewString()
	metadataExec(t, pool, "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'movie','matched','Writer local')", id)
	return id
}

func writerCorrectionFile(t *testing.T, pool *pgxpool.Pool, id string) int {
	t.Helper()
	var folder int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('movies','Writer local',bloem_platform_resource_owner_id()) RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	file, err := scanner.NewFileRepository(pool).Upsert(t.Context(), models.MediaFile{ContentID: id, MediaFolderID: folder, FilePath: "/writer/" + uuid.NewString() + ".mkv"})
	if err != nil {
		t.Fatal(err)
	}
	return file.ID
}

func writerCorrectionProgress(t *testing.T, pool *pgxpool.Pool, user int, profile, id string, position float64, stamp time.Time, file int) {
	t.Helper()
	metadataExec(t, pool, `INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds,updated_at,last_file_id)
		VALUES($1,$2,$3,$4,100,$5,NULLIF($6,0))`, user, profile, id, position, stamp, file)
}

func writerCorrectionCleanProgress(t *testing.T, pool *pgxpool.Pool, ids ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, "DELETE FROM user_watch_progress WHERE media_item_id=ANY($1::text[])", ids); err != nil {
			t.Error("progress cleanup", err)
		}
	})
}

func writerCorrectionNoCommit(t *testing.T, events []writerCorrectionEvent) {
	t.Helper()
	for _, e := range events {
		if e.SQL == "commit" || e.SQL == "COMMIT" {
			t.Error("refusing actual writer attempted Commit")
		}
		if e.State != "" {
			t.Logf("actual refusing statement SQLSTATE=%s SQL=%s", e.State, e.SQL)
		}
	}
}

func TestNativeWriterCorrectionRebindDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	for _, age := range []struct {
		name  string
		delta time.Duration
	}{
		{"sourceOlder", -time.Microsecond}, {"sourceEqual", 0}, {"sourceNewer", time.Microsecond},
	} {
		t.Run(age.name, func(t *testing.T) {
			local := writerCorrectionLocal(t, pool)
			writerCorrectionCleanProgress(t, pool, local, native)
			writerCorrectionProgress(t, pool, user, profile, local, 20, writerCorrectionStamp.Add(age.delta), 0)
			writerCorrectionProgress(t, pool, user, profile, native, 60, writerCorrectionStamp, 0)
			before := writerCorrectionSnapshot(t, pool, "before")
			trace.start()
			err := (&MetadataService{dbPool: pool}).rebindItemToExistingItem(t.Context(), local, native, false)
			events := trace.stop()
			after := writerCorrectionSnapshot(t, pool, "after")
			t.Logf("actual false-flag rebind %s err=%T progressStateRetained=%t", age.name, err, reflect.DeepEqual(before["user_watch_progress"], after["user_watch_progress"]))
			writerCorrectionRetained(t, before, after)
			writerCorrectionNoCommit(t, events)
			writerCorrectionNoDML(t, events)
			metadataState(t, err, "BN001")
		})
	}
}

func TestNativeWriterCorrectionSubsetDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	for _, direction := range []string{"localToNative", "nativeToLocal"} {
		for _, age := range []struct {
			name  string
			delta time.Duration
		}{{"older", -time.Microsecond}, {"equal", 0}, {"newer", time.Microsecond}} {
			t.Run(direction+"/"+age.name, func(t *testing.T) {
				local := writerCorrectionLocal(t, pool)
				file := writerCorrectionFile(t, pool, local)
				from, to := local, native
				if direction == "nativeToLocal" {
					from, to = native, local
				}
				writerCorrectionCleanProgress(t, pool, from, to)
				writerCorrectionProgress(t, pool, user, profile, from, 20, writerCorrectionStamp.Add(age.delta), file)
				writerCorrectionProgress(t, pool, user, profile, to, 60, writerCorrectionStamp, 0)
				before := writerCorrectionSnapshot(t, pool, "before")
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, tx)
				defer tx.Rollback(context.Background()) //nolint:errcheck
				trace.start()
				_, runErr := reattribute.Run(t.Context(), tx, reattribute.Options{FromContentID: from, ToContentID: to, MovedFileIDs: []int{file}, Mode: reattribute.HistoryModeKeep})
				events := trace.stop()
				if runErr == nil {
					err = tx.Commit(t.Context())
				} else {
					err = tx.Rollback(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				after := writerCorrectionSnapshot(t, pool, "after")
				t.Logf("actual subset %s %s err=%T progressStateRetained=%t", direction, age.name, runErr, reflect.DeepEqual(before["user_watch_progress"], after["user_watch_progress"]))
				writerCorrectionRetained(t, before, after)
				writerCorrectionNoCommit(t, events)
				writerCorrectionNoDML(t, events)
				metadataState(t, runErr, "BN001")
			})
		}
	}
}

func writerCorrectionDuplicate(ctx context.Context, pool *pgxpool.Pool, entrypoint, source, target string, allow bool) (string, error) {
	if entrypoint == "chooseCanonical" {
		return canonicalizeProviderIDDuplicate(ctx, pool, source, target, allow)
	}
	return canonicalizeProviderIDDuplicateInto(ctx, pool, source, target, allow)
}

func writerCorrectionCollection(t *testing.T, store userstore.UserStore, profile string) string {
	t.Helper()
	c, err := store.CreateCollection(t.Context(), userstore.CreateCollectionInput{Name: "Writer collection", CreatorProfileID: profile, CollectionType: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func writerCorrectionCollectionRow(t *testing.T, pool *pgxpool.Pool, user int, collection, item, sub string, pos int, stamp time.Time) {
	t.Helper()
	metadataExec(t, pool, `INSERT INTO user_personal_collection_items(user_id,collection_id,media_item_id,sub_item_id,position,added_at) VALUES($1,$2,$3,$4,$5,$6)`, user, collection, item, sub, pos, stamp)
}

func TestNativeWriterCorrectionDuplicateLocalDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	_, _, _ = metadataCurrentLifecycle(t, pool) // Unrelated legal native library.
	user, profile, store := metadataCurrentProfile(t, pool)
	for _, entrypoint := range []string{"chooseCanonical", "explicitCanonical"} {
		for _, shape := range []string{"empty", "wholeRows", "subItemRows"} {
			t.Run(entrypoint+"/"+shape, func(t *testing.T) {
				source, target := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
				// Both matched, fileless: deterministic canonical owner is older target.
				metadataExec(t, pool, "UPDATE media_items SET created_at=$2 WHERE content_id=$1", target, writerCorrectionStamp.Add(-time.Hour))
				var collection string
				if shape != "empty" {
					collection = writerCorrectionCollection(t, store, profile)
					writerCorrectionCollectionRow(t, pool, user, collection, source, "", 8, writerCorrectionStamp.Add(-3*time.Microsecond))
					writerCorrectionCollectionRow(t, pool, user, collection, target, "", 2, writerCorrectionStamp.Add(3*time.Microsecond))
					if shape == "subItemRows" {
						writerCorrectionCollectionRow(t, pool, user, collection, source, "sub-A", 1, writerCorrectionStamp.Add(4*time.Microsecond))
						writerCorrectionCollectionRow(t, pool, user, collection, target, "sub-A", 9, writerCorrectionStamp.Add(-4*time.Microsecond))
						writerCorrectionCollectionRow(t, pool, user, collection, source, "sub-B", 7, writerCorrectionStamp)
						writerCorrectionCollectionRow(t, pool, user, collection, target, "sub-C", 11, writerCorrectionStamp)
						other := writerCorrectionCollection(t, store, profile)
						writerCorrectionCollectionRow(t, pool, user, other, source, "sub-A", 31, writerCorrectionStamp.Add(time.Hour))
						otherUser, otherProfile, otherStore := metadataCurrentProfile(t, pool)
						otherCollection := writerCorrectionCollection(t, otherStore, otherProfile)
						writerCorrectionCollectionRow(t, pool, otherUser, otherCollection, source, "sub-A", 41, writerCorrectionStamp.Add(2*time.Hour))
					}
				}
				before := writerCorrectionSnapshot(t, pool, "before")
				trace.start()
				chosen, err := writerCorrectionDuplicate(t.Context(), pool, entrypoint, source, target, true)
				events := trace.stop()
				after := writerCorrectionSnapshot(t, pool, "after")
				for _, e := range events {
					if e.State != "" {
						t.Logf("actual refusing statement SQLSTATE=%s SQL=%s", e.State, e.SQL)
					}
				}
				if err != nil {
					t.Fatal("actual ordinary duplicate must commit", err)
				}
				if chosen != target {
					t.Fatalf("canonical ID=%s want %s", chosen, target)
				}
				var remaining int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM media_items WHERE content_id=$1", source).Scan(&remaining); err != nil || remaining != 0 {
					t.Fatal("source not removed", err)
				}
				var oldRows []struct {
					User       int       `json:"user_id"`
					Collection string    `json:"collection_id"`
					Item       string    `json:"media_item_id"`
					Sub        string    `json:"sub_item_id"`
					Position   int       `json:"position"`
					Added      time.Time `json:"added_at"`
				}
				if err := json.Unmarshal(before["user_personal_collection_items"], &oldRows); err != nil {
					t.Fatal(err)
				}
				type key struct {
					user                  int
					collection, item, sub string
				}
				type value struct {
					position int
					added    time.Time
				}
				want := map[key]value{}
				for _, row := range oldRows {
					item := row.Item
					if item == source {
						item = target
					}
					k := key{row.User, row.Collection, item, row.Sub}
					v, exists := want[k]
					if !exists {
						v = value{row.Position, row.Added}
					} else {
						if row.Position < v.position {
							v.position = row.Position
						}
						if row.Added.Before(v.added) {
							v.added = row.Added
						}
					}
					want[k] = v
				}
				var newRows []struct {
					User       int       `json:"user_id"`
					Collection string    `json:"collection_id"`
					Item       string    `json:"media_item_id"`
					Sub        string    `json:"sub_item_id"`
					Position   int       `json:"position"`
					Added      time.Time `json:"added_at"`
				}
				if err := json.Unmarshal(after["user_personal_collection_items"], &newRows); err != nil {
					t.Fatal(err)
				}
				got := map[key]value{}
				for _, row := range newRows {
					got[key{row.User, row.Collection, row.Item, row.Sub}] = value{row.Position, row.Added}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("actual complete-key collection rows/minima differ")
				}
				t.Logf("actual %s %s Commit; canonical/source deletion and %d complete collection keys/minima verified", entrypoint, shape, len(got))
			})
		}
	}
}

func TestNativeWriterCorrectionDuplicateNativeDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, store := metadataCurrentProfile(t, pool)
	for _, entrypoint := range []string{"chooseCanonical", "explicitCanonical"} {
		for _, shape := range []string{"emptyChildren", "wholeSourceOnly", "wholeCollision", "wholeTargetOnly", "subSourceOnly", "subCollision", "subTargetOnly"} {
			t.Run(entrypoint+"/"+shape, func(t *testing.T) {
				local := writerCorrectionLocal(t, pool)
				if shape != "emptyChildren" {
					collection := writerCorrectionCollection(t, store, profile)
					sub := ""
					if strings.HasPrefix(shape, "sub") {
						sub = "sub-A"
					}
					if !strings.HasSuffix(shape, "TargetOnly") {
						writerCorrectionCollectionRow(t, pool, user, collection, local, sub, 8, writerCorrectionStamp.Add(-time.Microsecond))
					}
					if !strings.HasSuffix(shape, "SourceOnly") {
						writerCorrectionCollectionRow(t, pool, user, collection, native, sub, 2, writerCorrectionStamp)
					}
				}
				before := writerCorrectionSnapshot(t, pool, "before")
				trace.start()
				_, err := writerCorrectionDuplicate(t.Context(), pool, entrypoint, local, native, true)
				events := trace.stop()
				after := writerCorrectionSnapshot(t, pool, "after")
				writerCorrectionRetained(t, before, after)
				writerCorrectionNoCommit(t, events)
				metadataState(t, err, "BN001")
				if shape == "emptyChildren" {
					stamp, collection, refusedDelete := -1, -1, -1
					for i, e := range events {
						if strings.HasPrefix(e.SQL, "UPDATE media_items dest SET imdb_id") && e.State == "" {
							stamp = i
						}
						if strings.HasPrefix(e.SQL, "INSERT INTO user_personal_collection_items") && e.State == "" {
							collection = i
						}
						if e.SQL == "DELETE FROM media_items WHERE content_id = $1" && e.State == "BN001" {
							refusedDelete = i
						}
					}
					if stamp < 0 || collection <= stamp || refusedDelete <= collection {
						t.Fatal("empty-child native stamp → successful collection statement → final source DELETE BN001 not observed")
					}
					t.Log("actual native target UPDATE stamp, empty four-key collection merge and unconditional source DELETE BN001/full rollback verified")
				}
			})
		}
	}
}

func writerCorrectionNoDML(t *testing.T, events []writerCorrectionEvent) {
	t.Helper()
	for _, event := range events {
		for _, verb := range []string{"INSERT ", "UPDATE ", "DELETE "} {
			if strings.HasPrefix(event.SQL, verb) {
				t.Errorf("Run executed DML before complete admission: %s", event.SQL)
			}
		}
	}
}

func TestNativeWriterCorrectionEpisodeAdmissionDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	for _, kind := range []string{"nativeEpisodeTarget", "nativeEpisodeSource", "inconsistentEndpoint", "invalidEmptyPair", "invalidSelfPair", "dedupSortedKeys"} {
		t.Run(kind, func(t *testing.T) {
			from, to := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
			writerCorrectionProgress(t, pool, user, profile, from, 20, writerCorrectionStamp.Add(-time.Microsecond), 0)
			writerCorrectionProgress(t, pool, user, profile, to, 60, writerCorrectionStamp, 0)
			pair := reattribute.IDPair{From: from, To: native}
			var alias string
			switch kind {
			case "nativeEpisodeSource":
				pair = reattribute.IDPair{From: native, To: to}
			case "inconsistentEndpoint":
				alias = "writer-alias-" + uuid.NewString()
				metadataExec(t, pool, "INSERT INTO media_extras(content_id,parent_id,kind) VALUES($1,$2,'trailer')", alias, from)
				pair.To = alias
			case "invalidEmptyPair":
				pair.To = ""
			case "invalidSelfPair":
				pair.To = pair.From
			case "dedupSortedKeys":
				pair = reattribute.IDPair{From: to, To: from}
			}
			before := writerCorrectionSnapshot(t, pool, "before")
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			writerCorrectionTrackTx(t, tx)
			defer tx.Rollback(context.Background()) //nolint:errcheck
			if kind == "inconsistentEndpoint" {
				// Active guards allow the transient local role collision, but the
				// deferred association constraint forbids committing it. Run must
				// detect it in this same transaction before its own first mutation.
				if _, err := tx.Exec(t.Context(), "INSERT INTO media_items(content_id,type,status,title) VALUES($1,'movie','matched','Colliding local role')", alias); err != nil {
					t.Fatal(err)
				}
				var class string
				if err := tx.QueryRow(t.Context(), "SELECT public.bloem_native_item_class($1)", alias).Scan(&class); err != nil || class != "inconsistent" {
					t.Fatal("transaction-local structural inconsistency not established", err)
				}
			}
			trace.start()
			_, runErr := reattribute.Run(t.Context(), tx, reattribute.Options{FromContentID: from, ToContentID: to, WholeItem: true, EpisodePairs: []reattribute.IDPair{pair, pair}})
			events := trace.stop()
			if err := tx.Rollback(t.Context()); err != nil {
				t.Fatal(err)
			}
			writerCorrectionRetained(t, before, writerCorrectionSnapshot(t, pool, "after"))
			if kind == "dedupSortedKeys" {
				if runErr != nil {
					t.Fatal(runErr)
				}
				var got []string
				for _, e := range events {
					if e.SQL == "SELECT public.bloem_native_lock_item_keys($1::text[])" {
						got, _ = e.Args[0].([]string)
					}
				}
				want := []string{from, to}
				sort.Strings(want)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("actual complete root/episode key set not sorted/deduplicated")
				}
			} else {
				writerCorrectionNoDML(t, events)
				if kind == "invalidEmptyPair" || kind == "invalidSelfPair" {
					if runErr == nil || !strings.Contains(runErr.Error(), "invalid id pair") {
						t.Fatal("invalid episode pair admitted", runErr)
					}
				} else {
					metadataState(t, runErr, "BN001")
				}
			}
		})
	}
}

func TestNativeWriterCorrectionLocalProgressDB(t *testing.T) {
	pool, _ := writerCorrectionDatabase(t)
	_, _ = metadataNativeItem(t, pool)
	user, profile, store := metadataCurrentProfile(t, pool)
	secondProfile := uuid.NewString()
	if err := store.CreateProfile(t.Context(), userstore.Profile{ID: secondProfile, Name: "Second real profile"}); err != nil {
		t.Fatal(err)
	}
	otherUser, otherProfile, _ := metadataCurrentProfile(t, pool)
	for _, mode := range []string{"whole", "subset"} {
		for _, shape := range []string{"older", "equal", "newer", "sourceOnly", "targetOnly", "nonselected", "otherProfile", "otherAccount"} {
			t.Run(mode+"/"+shape, func(t *testing.T) {
				from, to := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
				file := writerCorrectionFile(t, pool, from)
				selected := file
				stamp := writerCorrectionStamp.Add(-time.Microsecond)
				if shape == "equal" {
					stamp = writerCorrectionStamp
				}
				if shape == "newer" {
					stamp = writerCorrectionStamp.Add(time.Microsecond)
				}
				if shape == "nonselected" {
					selected = writerCorrectionFile(t, pool, from)
				}
				if shape != "targetOnly" {
					writerCorrectionProgress(t, pool, user, profile, from, 20, stamp, file)
				}
				destUser, destProfile := user, profile
				if shape == "otherProfile" {
					destProfile = secondProfile
				}
				if shape == "otherAccount" {
					destUser, destProfile = otherUser, otherProfile
				}
				if shape != "sourceOnly" {
					writerCorrectionProgress(t, pool, destUser, destProfile, to, 60, writerCorrectionStamp, 0)
				}
				before := writerCorrectionSnapshot(t, pool, "before")
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, tx)
				defer tx.Rollback(context.Background()) //nolint:errcheck
				report, err := reattribute.Run(t.Context(), tx, reattribute.Options{FromContentID: from, ToContentID: to, WholeItem: mode == "whole", MovedFileIDs: []int{selected}, Mode: reattribute.HistoryModeKeep})
				if err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				after := writerCorrectionSnapshot(t, pool, "after")
				moved, conflicts := 0, 1
				sourceStays := mode == "subset" && shape == "nonselected"
				sourceWins := shape == "newer" || shape == "sourceOnly" || shape == "otherProfile" || shape == "otherAccount"
				if sourceWins {
					moved = 1
				}
				if shape == "sourceOnly" || shape == "otherProfile" || shape == "otherAccount" {
					conflicts = 0
				}
				if shape == "targetOnly" || sourceStays {
					moved, conflicts = 0, 0
				}
				if report.ProgressMoved != moved || report.ProgressConflicts != conflicts {
					t.Fatalf("report moved/conflicts=%d/%d want %d/%d", report.ProgressMoved, report.ProgressConflicts, moved, conflicts)
				}
				var sourceRows int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_watch_progress WHERE media_item_id=$1", from).Scan(&sourceRows); err != nil {
					t.Fatal(err)
				}
				if sourceStays {
					if sourceRows != 1 || !reflect.DeepEqual(before["user_watch_progress"], after["user_watch_progress"]) {
						t.Fatal("nonselected subset progress changed")
					}
				} else {
					if sourceRows != 0 {
						t.Fatal("source progress unexpectedly retained")
					}
					var position, duration float64
					var updated time.Time
					checkUser, checkProfile := destUser, destProfile
					wantPosition, wantStamp := float64(60), writerCorrectionStamp
					if sourceWins {
						checkUser, checkProfile, wantPosition, wantStamp = user, profile, 20, stamp
					}
					if err := pool.QueryRow(t.Context(), `SELECT position_seconds,duration_seconds,updated_at FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`, checkUser, checkProfile, to).Scan(&position, &duration, &updated); err != nil || position != wantPosition || duration != 100 || !updated.Equal(wantStamp) {
						t.Fatal("winner payload/timestamp changed", err)
					}
					if shape == "otherProfile" || shape == "otherAccount" {
						if err := pool.QueryRow(t.Context(), `SELECT position_seconds FROM user_watch_progress WHERE user_id=$1 AND profile_id=$2 AND media_item_id=$3`, destUser, destProfile, to).Scan(&position); err != nil || position != 60 {
							t.Fatal("cross-profile/account destination collapsed", err)
						}
					}
				}
				writerCorrectionExpectedProgress(t, before["user_watch_progress"], after["user_watch_progress"], from, to, user, profile, sourceWins, sourceStays)
				t.Logf("actual local %s %s report=%d/%d; payload/timestamp/key scope verified with unrelated native present", mode, shape, moved, conflicts)
			})
		}
	}
}

func TestNativeWriterCorrectionDuplicateBoundariesDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	native, _ := metadataNativeItem(t, pool)
	for _, entrypoint := range []string{"chooseCanonical", "explicitCanonical"} {
		for _, boundary := range []string{"selfID", "matchedSourceFalse", "unmatchedSourceFalse", "bothRealContent"} {
			t.Run(entrypoint+"/"+boundary, func(t *testing.T) {
				local := writerCorrectionLocal(t, pool)
				source, target, allow := local, native, true
				if boundary == "selfID" {
					source = native
				}
				if boundary == "matchedSourceFalse" {
					allow = false
				}
				if boundary == "unmatchedSourceFalse" {
					metadataExec(t, pool, "UPDATE media_items SET status='pending' WHERE content_id=$1", local)
					// Ordinary local target: unmatched permission is a positive control.
					target = writerCorrectionLocal(t, pool)
					_ = writerCorrectionFile(t, pool, target)
					allow = false
				}
				if boundary == "bothRealContent" {
					_ = writerCorrectionFile(t, pool, local)
				}
				before := writerCorrectionSnapshot(t, pool, "before")
				trace.start()
				chosen, err := writerCorrectionDuplicate(t.Context(), pool, entrypoint, source, target, allow)
				events := trace.stop()
				after := writerCorrectionSnapshot(t, pool, "after")
				if boundary == "unmatchedSourceFalse" {
					if err != nil || chosen != target {
						t.Fatal("ordinary unmatched source false-flag control failed", err)
					}
					var n int
					if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM media_items WHERE content_id=$1", source).Scan(&n); err != nil || n != 0 {
						t.Fatal("unmatched source not removed", err)
					}
					return
				}
				writerCorrectionRetained(t, before, after)
				writerCorrectionNoDML(t, events)
				if boundary == "selfID" {
					if err != nil || chosen != native {
						t.Fatal("self-ID result changed", err)
					}
				} else {
					if err == nil {
						t.Fatal("permission/edition boundary accepted")
					}
					writerCorrectionNoCommit(t, events)
				}
			})
		}
	}
}

type writerCorrectionPublicationKey struct{}

func writerCorrectionPublication(t *testing.T, pool *pgxpool.Pool) (storagesource.SourceConfig, storagesource.Binding, *models.MediaFolder, storagesource.IngestionClaim, *metadataNativeFile) {
	t.Helper()
	source, binding, folder := metadataCurrentLifecycle(t, pool)
	var buffer bytes.Buffer
	z := zip.NewWriter(&buffer)
	for name, content := range map[string]string{
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/content.opf":        `<package><metadata><title>Actual writer promotion</title><language>en</language></metadata></package>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	file := &metadataNativeFile{Reader: bytes.NewReader(buffer.Bytes()), info: mediasource.Info{Name: "writer.epub", LogicalPath: "Books/writer.epub", Revision: "v1", Size: int64(buffer.Len())}}
	repo := storagesource.NewRepository(pool)
	run, err := repo.Begin(t.Context(), source.Key, "writer-discovery", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := repo.NextDirectory(t.Context(), run)
	if err != nil || !ok {
		t.Fatal("actual discovery directory", err)
	}
	e := &storagev1.Entry{Id: "writer-book", Name: file.info.Name, LogicalPath: file.info.LogicalPath, Revision: file.info.Revision, Size: file.info.Size, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = repo.ApplyPage(t.Context(), run, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{e}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.BeginIngestion(t.Context(), run.RunID, binding.ID, "writer-ingestion", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.NextIngestion(t.Context(), lease)
	if err != nil || !ok {
		t.Fatal("actual publication claim", err)
	}
	return source, binding, folder, claim, file
}

func writerCorrectionLockAttempt(t *testing.T, pool *pgxpool.Pool, key string, want string) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writerCorrectionTrackTx(t, tx)
	defer tx.Rollback(context.Background()) //nolint:errcheck
	_, err = tx.Exec(t.Context(), "SELECT public.bloem_native_lock_item_keys($1::text[])", []string{key})
	if want == "" {
		if err != nil {
			t.Fatal("expected released key", err)
		}
	} else {
		metadataState(t, err, want)
	}
}

func TestNativeWriterCorrectionAdmissionLifetimeDB(t *testing.T) {
	pool, trace := writerCorrectionDatabase(t)
	source, binding, folder, claim, file := writerCorrectionPublication(t, pool)
	fileBytes := make([]byte, file.Reader.Size())
	if n, err := file.Reader.ReadAt(fileBytes, 0); err != nil || n != len(fileBytes) {
		t.Fatal("actual EPUB bytes unavailable", err)
	}
	var native string
	for _, order := range []string{"reattributeFirst", "publicationFirst"} {
		t.Run(order, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), writerCorrectionPublicationKey{}, true), 30*time.Second)
			defer cancel()
			held := make(chan string, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			barrier := func(ctx context.Context, e writerCorrectionEvent) {
				if ctx.Value(writerCorrectionPublicationKey{}) != true || e.SQL != "SELECT bloem_native_lock_item_keys($1::text[])" || e.State != "" {
					return
				}
				keys, ok := e.Args[0].([]string)
				if !ok || len(keys) != 1 {
					return
				}
				held <- keys[0]
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			local := writerCorrectionLocal(t, pool)
			beforePublication := writerCorrectionSnapshot(t, pool, "before-publication")
			trace.start()
			trace.mu.Lock()
			if order == "reattributeFirst" {
				trace.startHook = barrier
			} else {
				trace.endHook = barrier
			}
			trace.mu.Unlock()
			type result struct {
				id  string
				err error
			}
			done := make(chan result, 1)
			go func() {
				// Each actual scanner attempt parses the real prepared EPUB anew.
				attempt := &metadataNativeFile{Reader: bytes.NewReader(fileBytes), info: file.info}
				id, err := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0).PublishAuthorizedNativeEbook(ctx, storagesource.NewRepository(pool), claim, folder, attempt, scanner.NativeEbookSidecars{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
					return resourcetenancy.NewStore(pool).RequireNativeScanTx(ctx, tx, binding, source)
				})
				done <- result{id, err}
			}()
			// Always join the publisher before returning, including a failed assertion.
			var joined bool
			defer func() {
				unblock()
				cancel()
				if !joined {
					<-done
				}
				trace.stop()
			}()
			var key string
			select {
			case key = <-held:
			case r := <-done:
				joined = true
				t.Fatal("actual publisher failed before keyed barrier", r.err)
			case <-ctx.Done():
				t.Fatal("actual publication keyed barrier timeout")
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			writerCorrectionTrackTx(t, tx)
			defer tx.Rollback(context.Background()) //nolint:errcheck
			_, runErr := reattribute.Run(ctx, tx, reattribute.Options{FromContentID: key, ToContentID: local, WholeItem: true})
			if order == "reattributeFirst" {
				if runErr != nil {
					t.Fatal("absent-key local Run admission failed", runErr)
				}
				unblock()
				r := <-done
				joined = true
				metadataState(t, r.err, "BN003")
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				writerCorrectionLockAttempt(t, pool, key, "")
				writerCorrectionRetained(t, beforePublication, writerCorrectionSnapshot(t, pool, "after-refused-publication"))
				t.Log("actual Run retained absent key; real authorized publication refused BN003; owning rollback released key")
			} else {
				metadataState(t, runErr, "BN003")
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				unblock()
				r := <-done
				joined = true
				if r.err != nil || r.id != key {
					t.Fatal("actual publication did not Commit its coordinated item key", r.err)
				}
				native = r.id
				_ = writerCorrectionSnapshot(t, pool, "after-committed-publication")
				var class string
				var complete int
				if err := pool.QueryRow(ctx, "SELECT public.bloem_native_item_class($1)", native).Scan(&class); err != nil || class != "native" {
					t.Fatal("actual committed item classification", err)
				}
				if err := pool.QueryRow(ctx, "SELECT count(*) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id JOIN media_item_libraries m ON m.content_id=f.content_id AND m.media_folder_id=f.media_folder_id WHERE f.content_id=$1 AND r.binding_id=$2", native, binding.ID).Scan(&complete); err != nil || complete != 1 {
					t.Fatal("actual committed file/member/ref tuple", err)
				}
				tx, err = pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, tx)
				_, runErr = reattribute.Run(ctx, tx, reattribute.Options{FromContentID: key, ToContentID: local, WholeItem: true})
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				metadataState(t, runErr, "BN001")
				t.Log("actual publisher retained absent key; Run refused BN003 while held; committed real native item causes fresh Run BN001")
			}
		})
	}
	if native == "" {
		t.Fatal("actual native publication prerequisite missing")
	}
	for _, kind := range []string{"emptyLocal", "emptyNative", "unavailableTransaction", "refusalHeld", "previewRollback", "savepointRollback", "connectionReuse", "nativeStampSavepoint", "filesplitRollback", "filesplitPreview"} {
		t.Run(kind, func(t *testing.T) {
			from, to := writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)
			before := writerCorrectionSnapshot(t, pool, "before")
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			writerCorrectionTrackTx(t, tx)
			defer tx.Rollback(context.Background()) //nolint:errcheck
			opts := reattribute.Options{FromContentID: from, ToContentID: to, WholeItem: true}
			switch kind {
			case "emptyNative", "refusalHeld", "connectionReuse":
				opts.ToContentID = native
				trace.start()
				_, err = reattribute.Run(t.Context(), tx, opts)
				events := trace.stop()
				writerCorrectionNoDML(t, events)
				metadataState(t, err, "BN001")
				var one int
				if err := tx.QueryRow(t.Context(), "SELECT 1").Scan(&one); err != nil || one != 1 {
					t.Fatal("Go refusal unexpectedly poisoned owning transaction", err)
				}
				writerCorrectionLockAttempt(t, pool, native, "BN003")
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				writerCorrectionLockAttempt(t, pool, native, "")
				if kind == "connectionReuse" {
					tx, err = conn.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					writerCorrectionTrackTx(t, tx)
					_, err = tx.Exec(t.Context(), "DELETE FROM media_items WHERE content_id=$1", from)
					if err != nil {
						t.Fatal("native refusal stamp leaked into next transaction on same connection", err)
					}
					if err = tx.Rollback(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
			case "unavailableTransaction":
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				_, err = reattribute.Run(t.Context(), tx, opts)
				metadataState(t, err, "BN003")
			case "savepointRollback":
				if _, err = reattribute.Run(t.Context(), tx, opts); err != nil {
					t.Fatal(err)
				}
				sp, err := tx.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, sp)
				_, err = reattribute.Run(t.Context(), sp, reattribute.Options{FromContentID: native, ToContentID: from, WholeItem: true})
				metadataState(t, err, "BN001")
				writerCorrectionLockAttempt(t, pool, native, "BN003")
				if err = sp.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				writerCorrectionLockAttempt(t, pool, native, "")
				writerCorrectionLockAttempt(t, pool, from, "BN003")
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				writerCorrectionLockAttempt(t, pool, from, "")
			case "nativeStampSavepoint":
				sp, err := tx.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, sp)
				if _, err = sp.Exec(t.Context(), "UPDATE media_items SET title=title WHERE content_id=$1", native); err != nil {
					t.Fatal(err)
				}
				_, err = sp.Exec(t.Context(), "DELETE FROM media_items WHERE content_id=$1", from)
				metadataState(t, err, "BN001")
				if err = sp.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), "DELETE FROM media_items WHERE content_id=$1", from); err != nil {
					t.Fatal("native target stamp survived savepoint rollback", err)
				}
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "filesplitRollback", "filesplitPreview":
				// Seed before opening the actual Move transaction, then snapshot its rows.
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				file := writerCorrectionFile(t, pool, from)
				var folderID int
				var path string
				if err = pool.QueryRow(t.Context(), "SELECT media_folder_id,file_path FROM media_files WHERE id=$1", file).Scan(&folderID, &path); err != nil {
					t.Fatal(err)
				}
				before = writerCorrectionSnapshot(t, pool, "before-file")
				tx, err = conn.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, tx)
				target := to
				if kind == "filesplitRollback" {
					target = native
				}
				trace.start()
				_, moveErr := filesplit.Move(t.Context(), tx, filesplit.Options{FromContentID: from, ToContentID: target, ItemType: "movie", Files: []filesplit.File{{ID: file, ContentID: from, MediaFolderID: folderID, FilePath: path}}, HistoryMode: reattribute.HistoryModeKeep})
				events := trace.stop()
				if kind == "filesplitRollback" {
					metadataState(t, moveErr, "BN001")
					writerCorrectionNoCommit(t, events)
				} else {
					if moveErr != nil {
						t.Fatal(moveErr)
					}
					var actual string
					if err = tx.QueryRow(t.Context(), "SELECT content_id FROM media_files WHERE id=$1", file).Scan(&actual); err != nil || actual != to {
						t.Fatal("actual filesplit preview did not move file", err)
					}
				}
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
			default:
				if kind == "previewRollback" {
					if err = tx.Rollback(t.Context()); err != nil {
						t.Fatal(err)
					}
					user, profile, _ := metadataCurrentProfile(t, pool)
					writerCorrectionProgress(t, pool, user, profile, from, 20, writerCorrectionStamp, 0)
					before = writerCorrectionSnapshot(t, pool, "before-progress")
					tx, err = conn.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					writerCorrectionTrackTx(t, tx)
				}
				report, err := reattribute.Run(t.Context(), tx, opts)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "previewRollback" && report.ProgressMoved != 1 {
					t.Fatal("actual preview did not move source progress")
				}
				if err = tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			writerCorrectionRetained(t, before, writerCorrectionSnapshot(t, pool, "after"))
		})
	}
}

func writerCorrectionTrackTx(t *testing.T, tx pgx.Tx) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	})
}

func TestNativeWriterCorrectionIsolationDB(t *testing.T) {
	// Prepare once with the normal guarded pool. Actual snapshot authority
	// rechecks need another connection while their own transaction is open.
	pool, trace := writerCorrectionDatabase(t)
	native, folder := metadataNativeItem(t, pool)
	user, profile, _ := metadataCurrentProfile(t, pool)
	tenants := tenancy.NewStore(pool)
	tenant, err := tenancy.NewSubjectResolver(tenancy.NewResolver(tenants), tenants).ResolveSubjectTenant(t.Context(), user, profile)
	if err != nil {
		t.Fatal(err)
	}
	ctx := tenancy.WithContext(t.Context(), tenant)
	var localFolder int
	if err = pool.QueryRow(ctx, "INSERT INTO media_folders(type,name,owner_id) SELECT 'movies','Snapshot local',id FROM resource_owners WHERE kind='organization' AND organization_id=$1 RETURNING id", tenant.OrganizationID).Scan(&localFolder); err != nil {
		t.Fatal(err)
	}
	locals := []string{writerCorrectionLocal(t, pool), writerCorrectionLocal(t, pool)}
	for _, id := range locals {
		metadataExec(t, pool, "INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", id, localFolder)
	}
	engine, err := policy.NewEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := sections.NewRepository(pool)
	sectionRows, err := repo.ListByScopeAll(t.Context(), "library", &folder)
	if err != nil || len(sectionRows) == 0 {
		t.Fatal("actual native initialization sections missing", err)
	}
	for _, iso := range []struct {
		name  string
		level pgx.TxIsoLevel
		sql   string
	}{{"repeatableRead", pgx.RepeatableRead, "repeatable read"}, {"serializable", pgx.Serializable, "serializable"}} {
		t.Run(iso.name, func(t *testing.T) {
			// Reopen the exact UUID via its guarded configuration, without any
			// preparation/reset. Every connection inherits the requested default;
			// nested real API authority reads can use the same isolation safely.
			cfg := pool.Config()
			cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = iso.sql
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal("guarded isolation pool unavailable")
			}
			t.Cleanup(pool.Close)
			var database, actual string
			if err = pool.QueryRow(t.Context(), "SELECT current_database(),current_setting('transaction_isolation')").Scan(&database, &actual); err != nil || database != cfg.ConnConfig.Database || actual != iso.sql {
				t.Fatal("actual UUID/requested isolation unverified", err)
			}
			provider := pgstore.NewPostgresProvider(pool)
			store, err := provider.ForUser(t.Context(), user)
			if err != nil {
				t.Fatal(err)
			}
			resolver := policy.NewViewerResolver(auth.NewUserRepository(pool), provider, nil, policy.NewPDP(engine), resourcetenancy.NewStore(pool), access.NewTenantGroupStore(pool))
			actor := progresssync.Actor{Input: access.ResolveInput{UserID: user, ProfileID: profile}}
			actor.Recheck = func(ctx context.Context) (access.Scope, error) { return resolver.Resolve(ctx, actor.Input) }
			snapshots := progresssync.NewService(pool, provider, catalog.NewServerSettingsRepo(pool), resolver)
			repo := sections.NewRepository(pool)
			for _, shape := range []string{"changedExisting", "changedAbsent"} {
				t.Run(shape, func(t *testing.T) {
					from := locals[0]
					if shape == "changedAbsent" {
						from = "writer-absent-" + uuid.NewString()
					}
					before := writerCorrectionSnapshot(t, pool, "before")
					tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso.level})
					if err != nil {
						t.Fatal(err)
					}
					writerCorrectionTrackTx(t, tx)
					var isolation string
					var n int
					if err = tx.QueryRow(t.Context(), "SELECT current_setting('transaction_isolation'),count(*) FROM media_items WHERE content_id=$1", from).Scan(&isolation, &n); err != nil || isolation != iso.sql {
						t.Fatal("stale/absent snapshot prerequisite", err)
					}
					trace.start()
					_, runErr := reattribute.Run(t.Context(), tx, reattribute.Options{FromContentID: from, ToContentID: locals[1], WholeItem: true})
					events := trace.stop()
					metadataState(t, runErr, "BN003")
					writerCorrectionNoDML(t, events)
					for _, e := range events {
						if strings.HasPrefix(strings.ToLower(e.SQL), "set ") {
							t.Fatal("Run changed isolation")
						}
					}
					if err = tx.Rollback(t.Context()); err != nil {
						t.Fatal(err)
					}
					writerCorrectionRetained(t, before, writerCorrectionSnapshot(t, pool, "after"))
				})
			}
			t.Run("sameIDProgress", func(t *testing.T) {
				if err := store.SetProgressAt(ctx, profile, native, 22, 100, false, writerCorrectionStamp); err != nil {
					t.Fatal(err)
				}
				changed, err := store.SetProgressIfNewer(ctx, profile, native, 33, 100, false, writerCorrectionStamp.Add(time.Second))
				if err != nil || !changed {
					t.Fatal("actual same-ID newer progress", err)
				}
				changed, err = store.SetProgressIfNewer(ctx, profile, native, 1, 100, false, writerCorrectionStamp)
				if err != nil || changed {
					t.Fatal("actual stale progress replaced newer row", err)
				}
				saved, err := store.GetProgress(ctx, profile, native)
				if err != nil || saved == nil || saved.PositionSeconds != 33 {
					t.Fatal("actual progress payload", err)
				}
				if err := store.ClearProgress(ctx, profile, native); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("sameIDCollection", func(t *testing.T) {
				collection := writerCorrectionCollection(t, store, profile)
				if err := store.AddCollectionItem(ctx, collection, native, 9); err != nil {
					t.Fatal(err)
				}
				if err := store.ReorderCollectionItems(ctx, collection, []string{native}); err != nil {
					t.Fatal(err)
				}
				items, err := store.ListCollectionItems(ctx, collection)
				if err != nil || len(items) != 1 || items[0].MediaItemID != native || items[0].Position != 0 {
					t.Fatal("actual collection reorder payload", err)
				}
				if err := store.RemoveCollectionItem(ctx, collection, native); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteCollection(ctx, collection); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("sameIDSection", func(t *testing.T) {
				section, err := repo.GetByID(ctx, sectionRows[0].ID)
				if err != nil {
					t.Fatal(err)
				}
				section.Title = "Actual same-ID section control"
				trace.start()
				err = repo.Update(ctx, section)
				events := trace.stop()
				if err != nil {
					t.Fatal(err)
				}
				serializable := false
				for _, e := range events {
					if strings.Contains(strings.ToLower(e.SQL), "begin isolation level serializable") {
						serializable = true
					}
				}
				if !serializable {
					t.Fatal("actual section writer's SERIALIZABLE transaction not observed")
				}
				saved, err := repo.GetByID(ctx, section.ID)
				if err != nil || saved.Title != section.Title {
					t.Fatal("actual same-ID section payload", err)
				}
				// Explicit RR/SER unchanged same-key section UPDATE compatibility.
				tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso.level})
				if err != nil {
					t.Fatal(err)
				}
				writerCorrectionTrackTx(t, tx)
				if _, err = tx.Exec(ctx, "UPDATE page_sections SET title=title WHERE id=$1", section.ID); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				t.Log("actual section API uses SERIALIZABLE; explicit unchanged same-ID probe used requested isolation")
			})
			t.Run("actualProgressSnapshot", func(t *testing.T) {
				for _, id := range locals {
					if err := store.SetProgressAt(ctx, profile, id, 12, 100, false, writerCorrectionStamp); err != nil {
						t.Fatal(err)
					}
				}
				trace.start()
				first, err := snapshots.CreateSnapshot(ctx, actor, uuid.NewString(), 1)
				events := trace.stop()
				if err != nil || first.Next == nil || len(first.Items) != 1 {
					t.Fatal("actual authorized progress snapshot creation", err)
				}
				last, err := snapshots.ReadSnapshot(ctx, actor, *first.Next)
				if err != nil || len(last.Items) != 1 {
					t.Fatal("actual authorized snapshot continuation", err)
				}
				for _, e := range events {
					if strings.HasPrefix(strings.ToLower(e.SQL), "begin") {
						t.Logf("actual progress snapshot transaction: %s", e.SQL)
					}
				}
			})
			t.Run("ordinaryProfileAccountDelete", func(t *testing.T) {
				account, p, selected := metadataCurrentProfile(t, pool)
				if err := selected.SetProgressAt(t.Context(), p, native, 12, 100, false, writerCorrectionStamp); err != nil {
					t.Fatal(err)
				}
				if err := selected.DeleteProfile(t.Context(), p); err != nil {
					t.Fatal("actual profile cleanup refused", err)
				}
				if err := auth.NewUserRepository(pool).Delete(t.Context(), account); err != nil {
					t.Fatal("actual account cleanup refused", err)
				}
				var rows int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_watch_progress WHERE user_id=$1", account).Scan(&rows); err != nil || rows != 0 {
					t.Fatal("actual cleanup left progress", err)
				}
			})
			if err := pool.QueryRow(t.Context(), "SHOW transaction_isolation").Scan(&actual); err != nil || actual != iso.sql {
				t.Fatal("same-ID APIs changed caller session isolation", err)
			}
		})
	}
}

// This oracle applies the declared age/predicate rule to complete captured rows;
// it does not repeat the product's DELETE/UPDATE SQL or drop payload columns.
func writerCorrectionExpectedProgress(t *testing.T, before, after json.RawMessage, from, to string, user int, profile string, sourceWins, sourceStays bool) {
	t.Helper()
	var original, actual []map[string]any
	if err := json.Unmarshal(before, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after, &actual); err != nil {
		t.Fatal(err)
	}
	want := []string{}
	for _, row := range original {
		id, _ := row["media_item_id"].(string)
		if id == from {
			if !sourceStays {
				if !sourceWins {
					continue
				}
				row["media_item_id"] = to
				// Existing progress_stamp advances its server cursor on every
				// UPDATE. Assert advancement and compare every other column exactly.
				var moved map[string]any
				for _, candidate := range actual {
					if candidate["media_item_id"] == to && candidate["user_id"] == row["user_id"] && candidate["profile_id"] == row["profile_id"] {
						moved = candidate
						break
					}
				}
				oldSeq, oldOK := row["synced_seq"].(float64)
				newSeq, newOK := moved["synced_seq"].(float64)
				if !oldOK || !newOK || newSeq <= oldSeq {
					t.Fatal("actual moved progress server cursor did not advance")
				}
				row["synced_seq"] = newSeq
			}
		} else if id == to && sourceWins && row["user_id"] == float64(user) && row["profile_id"] == profile {
			continue
		}
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, string(b))
	}
	got := []string{}
	for _, row := range actual {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("complete progress rows/payloads or unrelated keys differ from declared age/predicate policy")
	}
}
