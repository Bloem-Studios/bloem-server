//go:build integration

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Test-only fixture wiring; every rebuilt handler still enters NewRouter's
// actual login-session and administrative authority stack.
type nativeOnboardingTestSupport struct {
	deps      Dependencies
	queue     *scanqueue.Service
	stopQueue context.CancelFunc
	observed  <-chan events.Envelope
	rebuild   func(Dependencies, func(http.Handler) http.Handler)
}

type nativeRecoveryError struct {
	Error           string     `json:"error"`
	Operation       string     `json:"operation"`
	OperationID     uuid.UUID  `json:"operation_id"`
	LibraryID       int        `json:"library_id"`
	CreationKey     uuid.UUID  `json:"creation_key"`
	SourceKey       *uuid.UUID `json:"source_key"`
	ScanRunID       *string    `json:"scan_run_id"`
	LibraryRevision int64      `json:"library_revision"`
	State           string     `json:"state"`
	Created         *bool      `json:"created"`
}

func nativeRecoveryNoEvents(t *testing.T, f nativeOnboardingLifecycleFixture) {
	t.Helper()
	for {
		select {
		case e := <-f.support.observed:
			if strings.HasPrefix(e.Event, "scan.") {
				t.Fatalf("unacknowledged mutation emitted %s", e.Event)
			}
		default:
			return
		}
	}
}

func nativeRecoveryInstall(f nativeOnboardingLifecycleFixture, libraries *nativestorage.LibraryManagement, wrap func(http.Handler) http.Handler) {
	next := f.support.deps
	handler := *next.NativeStorageManagement
	handler.Libraries = libraries
	next.NativeStorageManagement = &handler
	f.support.rebuild(next, wrap)
}

func nativeRecoveryGet(t *testing.T, f nativeOnboardingLifecycleFixture, id int) nativestorage.LibraryStatus {
	t.Helper()
	var result struct {
		Library nativestorage.LibraryStatus `json:"library"`
	}
	f.command("GET", fmt.Sprintf("/api/bloem/v1/admin/organization/native-storage/libraries/%d", id), f.organizationToken, "", nil, 200, &result)
	return result.Library
}

func nativeRecoveryUnknown(t *testing.T, got nativeRecoveryError, operation string, l nativestorage.LibraryStatus, key uuid.UUID) {
	t.Helper()
	if got.Error != "mutation_outcome_unknown" || got.Operation != operation || got.OperationID == uuid.Nil || got.LibraryID != l.LibraryID || got.CreationKey != l.CreationKey || got.Created != nil || got.State != "" {
		t.Fatalf("%s lost authorized unknown-outcome envelope", operation)
	}
	if key != uuid.Nil && (got.SourceKey == nil || *got.SourceKey != key) {
		t.Fatal("unknown response lost authorized source")
	}
	if key == uuid.Nil && got.SourceKey != nil {
		t.Fatal("unknown response disclosed an unrelated source")
	}
}

// The tracer is armed ONLY by middleware on one actual HTTP request. Its
// cancellation derives from r.Context; no authorization context is fabricated.
type nativeSectionAttempt struct {
	cancel    context.CancelFunc
	before    bool
	sawInsert atomic.Bool
	fired     atomic.Bool
	committed atomic.Bool
}
type nativeSectionAttemptKey struct{}
type nativeSectionCommitKey struct{}
type nativeSectionTrace struct{}

func (nativeSectionTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	a, _ := ctx.Value(nativeSectionAttemptKey{}).(*nativeSectionAttempt)
	if a == nil {
		return ctx
	}
	if strings.Contains(d.SQL, "INSERT INTO page_sections") {
		a.sawInsert.Store(true)
	}
	selected := a.sawInsert.Load() && strings.EqualFold(strings.TrimSpace(d.SQL), "commit") && !a.fired.Load()
	if selected && a.before && a.fired.CompareAndSwap(false, true) {
		a.cancel()
	}
	return context.WithValue(ctx, nativeSectionCommitKey{}, selected)
}
func (nativeSectionTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	a, _ := ctx.Value(nativeSectionAttemptKey{}).(*nativeSectionAttempt)
	selected, _ := ctx.Value(nativeSectionCommitKey{}).(bool)
	if a != nil && selected && d.Err == nil {
		a.committed.Store(true)
		if !a.before && a.fired.CompareAndSwap(false, true) {
			a.cancel()
		}
	}
}

// Capture is a test observer only. The client below discards the whole create
// response and obtains its recovery address exclusively from authorized LIST.
type nativeRecoveryCapture struct {
	http.ResponseWriter
	status int
	data   []byte
}

func (w *nativeRecoveryCapture) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *nativeRecoveryCapture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.data = append(w.data, b...)
	return w.ResponseWriter.Write(b)
}

func TestNativeOnboardingHTTPRecoveryCommitDB(t *testing.T) {
	runNativeOnboardingHTTPWithTracer(t, "success", nativeSectionTrace{}, func(t *testing.T, f nativeOnboardingLifecycleFixture) {
		const org = "/api/bloem/v1/admin/organization/native-storage"
		const platform = "/api/bloem/v1/admin/platform/native-storage"
		base := f.support.deps.NativeStorageManagement.Libraries
		nativeRecoveryNoEvents(t, f)
		var rollbackReceipts, commitReceipts atomic.Int32
		hook := func(want string, rollback bool) func(context.Context, pgx.Tx, string) error {
			return func(ctx context.Context, tx pgx.Tx, operation string) error {
				if operation != want {
					return tx.Commit(ctx)
				}
				if rollback {
					if err := tx.Rollback(ctx); err != nil {
						return err
					}
					rollbackReceipts.Add(1)
					return pgx.ErrTxCommitRollback
				}
				if err := tx.Commit(ctx); err != nil {
					return err
				}
				commitReceipts.Add(1)
				return errors.New("finite acknowledgement loss after actual COMMIT")
			}
		}
		count := func(query string, args ...any) int {
			t.Helper()
			var n int
			if err := f.pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		initialCount := count("SELECT count(*) FROM bloem_native_libraries")
		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("create", true)), nil)
		var failure nativeRecoveryError
		f.command("POST", org+"/libraries", f.organizationToken, "application/json", f.body(map[string]any{"name": "Rolled back recovery"}), 503, &failure)
		if failure.Error != "native_storage_unavailable" || failure.OperationID != uuid.Nil || count("SELECT count(*) FROM bloem_native_libraries") != initialCount || count("SELECT count(*) FROM media_folders WHERE name='Rolled back recovery'") != 0 {
			t.Fatal("definite Create rollback retained rows or reported unknown")
		}
		nativeRecoveryNoEvents(t, f)

		observed := make(chan nativeRecoveryCapture, 1)
		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("create", false)), func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture := &nativeRecoveryCapture{ResponseWriter: w}
				next.ServeHTTP(capture, r)
				if r.Method == "POST" && r.URL.Path == org+"/libraries" {
					observed <- *capture
				}
			})
		})
		req, err := http.NewRequestWithContext(t.Context(), "POST", f.server.URL+org+"/libraries", f.body(map[string]any{"name": "Recovered HTTP identity", "metadata_language": "en"}))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.organizationToken)
		req.Header.Set("Content-Type", "application/json")
		// The outer server observer sees wire bytes, before Transport decompression.
		req.Header.Set("Accept-Encoding", "identity")
		response, err := f.server.Client().Do(req)
		if err != nil {
			t.Fatal("actual lost-response create transport failed")
		}
		// Deliberately consume no status, header, ID, key or body for recovery.
		if _, err = io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		nativeRecoveryInstall(f, base, nil)
		var page nativestorage.LibraryPage
		f.command("GET", org+"/libraries", f.organizationToken, "", nil, 200, &page)
		var l1 nativestorage.LibraryStatus
		for _, l := range page.Libraries {
			if l.LibraryID != f.created.LibraryID {
				if l1.LibraryID != 0 {
					t.Fatal("list allocated duplicate recovery identities")
				}
				l1 = l
			}
		}
		if len(page.Libraries) != initialCount+1 || l1.LibraryID <= 0 || l1.CreationKey == uuid.Nil || l1.LibraryRevision != 1 || l1.Initialized || count("SELECT count(*) FROM media_folder_paths WHERE media_folder_id=$1", l1.LibraryID) != 0 {
			t.Fatal("discarded Create response did not recover one pathless L1")
		}
		witness := <-observed
		var unknown nativeRecoveryError
		if witness.status != 503 {
			t.Fatalf("actual committed Create status=%d", witness.status)
		}
		if err := json.Unmarshal(witness.data, &unknown); err != nil {
			t.Fatal("decode observed identity-encoded Create response", err)
		}
		nativeRecoveryUnknown(t, unknown, "create", l1, uuid.Nil)
		var keyed struct {
			Library nativestorage.LibraryStatus `json:"library"`
		}
		f.command("GET", org+"/libraries/creation/"+l1.CreationKey.String(), f.organizationToken, "", nil, 200, &keyed)
		if keyed.Library.LibraryID != l1.LibraryID || nativeRecoveryGet(t, f, l1.LibraryID).CreationKey != l1.CreationKey {
			t.Fatal("create ID/key/list reconciliation disagreed")
		}
		nativeRecoveryNoEvents(t, f)
		t.Logf("Create: real rollback left no folder/marker; real COMMIT+lost acknowledgement emitted unknown503; discarded response recovered by authorized LIST: library=%d key=%s", l1.LibraryID, l1.CreationKey)

		initialize := fmt.Sprintf("%s/libraries/%d/initialize", org, l1.LibraryID)
		sectionCounts := func() (int, int) {
			return count("SELECT count(*) FROM page_sections WHERE scope='library' AND library_id=$1", l1.LibraryID), count("SELECT count(*) FROM page_sections WHERE scope='home' AND config->>'filter_library_id'=$1", fmt.Sprint(l1.LibraryID))
		}
		sectionSnapshot := func(scope string) string {
			var rows string
			if err := f.pool.QueryRow(t.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(p) ORDER BY id),'[]'::jsonb)::text FROM page_sections p WHERE (scope='library' AND library_id=$1 AND $2 IN ('library','all')) OR (scope='home' AND config->>'filter_library_id'=$3 AND $2='all')`, l1.LibraryID, scope, fmt.Sprint(l1.LibraryID)).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			return rows
		}
		partial := func(before bool) {
			a := &nativeSectionAttempt{before: before}
			nativeRecoveryInstall(f, base, func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "POST" && r.URL.Path == initialize {
						ctx, cancel := context.WithCancel(r.Context())
						defer cancel()
						a.cancel = cancel
						r = r.WithContext(context.WithValue(ctx, nativeSectionAttemptKey{}, a))
					}
					next.ServeHTTP(w, r)
				})
			})
			var result nativeRecoveryError
			f.command("POST", initialize, f.organizationToken, "application/json", f.body(map[string]any{"expected_library_revision": 1}), 503, &result)
			nativeRecoveryInstall(f, base, nil)
			if !a.fired.Load() || a.committed.Load() == before || result.Error != "initialization_incomplete" || result.LibraryID != l1.LibraryID || result.CreationKey != l1.CreationKey || result.LibraryRevision != 1 || result.State != "initialization_required" {
				t.Fatalf("section interruption lacks actual selected commit/rollback + authorized reconciliation (before=%t fired=%t committed=%t code=%s)", before, a.fired.Load(), a.committed.Load(), result.Error)
			}
			current := nativeRecoveryGet(t, f, l1.LibraryID)
			if current.Initialized || current.LibraryRevision != 1 {
				t.Fatal("partial section stage promoted L2")
			}
			nativeRecoveryNoEvents(t, f)
		}
		partial(true)
		libraryRows, homeRows := sectionCounts()
		if libraryRows != 0 || homeRows != 0 {
			t.Fatal("before-COMMIT library stage retained sections")
		}
		partial(false)
		libraryRows, homeRows = sectionCounts()
		if libraryRows == 0 || homeRows != 0 {
			t.Fatal("after-COMMIT library stage did not retain only earlier stage")
		}
		custom, err := sections.NewRepository(f.pool).Create(t.Context(), &sections.PageSection{Scope: "library", LibraryID: &l1.LibraryID, SectionType: sections.SectionRecentlyAdded, Title: "Retained custom recovery section", Position: 47, Featured: true, ItemLimit: 29, Enabled: false, Config: json.RawMessage(`{"custom":"recovery"}`)})
		if err != nil || custom == nil {
			t.Fatal("create real custom section", err)
		}
		librarySnapshot := sectionSnapshot("library")
		partial(true)
		_, homeRows = sectionCounts()
		if homeRows != 0 || sectionSnapshot("library") != librarySnapshot {
			t.Fatal("home rollback changed earlier committed defaults/custom rows")
		}
		partial(false)
		_, homeRows = sectionCounts()
		if homeRows != 2 || sectionSnapshot("library") != librarySnapshot {
			t.Fatal("home commit did not preserve earlier rows or duplicated defaults")
		}
		allSections := sectionSnapshot("all")
		t.Log("Section stages: actual library and home COMMIT boundaries each exercised before/after; authorized incomplete503 retained earlier rows and custom section; final L1 unchanged")

		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("initialize", true)), nil)
		failure = nativeRecoveryError{}
		f.command("POST", initialize, f.organizationToken, "application/json", f.body(map[string]any{"expected_library_revision": 1}), 503, &failure)
		if failure.Error != "native_storage_unavailable" || nativeRecoveryGet(t, f, l1.LibraryID).LibraryRevision != 1 || sectionSnapshot("all") != allSections {
			t.Fatal("final Initialize rollback mutated retained stage rows")
		}
		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("initialize", false)), nil)
		unknown = nativeRecoveryError{}
		f.command("POST", initialize, f.organizationToken, "application/json", f.body(map[string]any{"expected_library_revision": 1}), 503, &unknown)
		nativeRecoveryUnknown(t, unknown, "initialize", l1, uuid.Nil)
		nativeRecoveryNoEvents(t, f)
		nativeRecoveryInstall(f, base, nil)
		l2 := nativeRecoveryGet(t, f, l1.LibraryID)
		if !l2.Initialized || l2.LibraryRevision != 2 || l2.CreationKey != l1.CreationKey {
			t.Fatal("final init unknown did not retain L2")
		}
		var retried nativestorage.LibraryStatus
		f.command("POST", initialize, f.organizationToken, "application/json", f.body(map[string]any{"expected_library_revision": l2.LibraryRevision}), 200, &retried)
		if retried.LibraryID != l1.LibraryID || retried.LibraryRevision != 2 || sectionSnapshot("all") != allSections {
			t.Fatal("same-ID/current-revision init retry duplicated or rewrote sections")
		}

		bind := fmt.Sprintf("%s/sources/%s/bindings/%d", platform, f.source.SourceKey, l1.LibraryID)
		revisions := map[string]any{"expected_source_revision": f.source.ConfigurationRevision, "expected_library_revision": l2.LibraryRevision}
		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("bind", true)), nil)
		failure = nativeRecoveryError{}
		f.command("PUT", bind, f.platformToken, "application/json", f.body(revisions), 503, &failure)
		if failure.Error != "native_storage_unavailable" || nativeRecoveryGet(t, f, l1.LibraryID).LibraryRevision != 2 || count("SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=$1", l1.LibraryID) != 0 {
			t.Fatal("Bind definite rollback retained L3")
		}
		nativeRecoveryInstall(f, nativestorage.IntegrationLibraryCommit(base, hook("bind", false)), nil)
		unknown = nativeRecoveryError{}
		f.command("PUT", bind, f.platformToken, "application/json", f.body(revisions), 503, &unknown)
		nativeRecoveryUnknown(t, unknown, "bind", l1, f.source.SourceKey)
		nativeRecoveryNoEvents(t, f)
		nativeRecoveryInstall(f, base, nil)
		l3 := nativeRecoveryGet(t, f, l1.LibraryID)
		if l3.LibraryRevision != 3 || l3.BindingID == nil || l3.SourceKey == nil || *l3.SourceKey != f.source.SourceKey {
			t.Fatal("HTTP GET did not recover committed L3 binding")
		}
		var bindings struct {
			Bindings []struct {
				BindingID uuid.UUID `json:"binding_id"`
				FolderID  int       `json:"folder_id"`
			} `json:"bindings"`
		}
		f.command("GET", fmt.Sprintf("%s/sources/%s/bindings", platform, f.source.SourceKey), f.platformToken, "", nil, 200, &bindings)
		found := 0
		for _, b := range bindings.Bindings {
			if b.FolderID == l1.LibraryID && b.BindingID == *l3.BindingID {
				found++
			}
		}
		if found != 1 {
			t.Fatal("authorized current binding list omitted unique recovered binding")
		}
		revisions["expected_library_revision"] = l3.LibraryRevision
		var repeat nativestorage.BindResult
		f.command("PUT", bind, f.platformToken, "application/json", f.body(revisions), 200, &repeat)
		if !repeat.Repeated || repeat.BindingID != *l3.BindingID || count("SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=$1", l1.LibraryID) != 1 {
			t.Fatal("same-ID/current-revision bind retry duplicated binding")
		}
		t.Log("Final Initialize/Bind: rollback and real COMMIT+lost acknowledgement distinguished; unknown503 IDs reconciled by GET/current binding list; same-ID/current-revision retries preserve L2/L3 and exact custom/default rows")

		queueHook := func(rollback bool) func(context.Context, pgx.Tx) error {
			return func(ctx context.Context, tx pgx.Tx) error { return hook("scan", rollback)(ctx, tx, "scan") }
		}
		queueLibraries := func(rollback bool) *nativestorage.LibraryManagement {
			return nativestorage.NewLibraryManagement(f.pool, catalog.NewFolderRepository(f.pool), sections.NewRepository(f.pool), resourcetenancy.NewStore(f.pool), scanqueue.IntegrationNativeQueueCommit{Service: f.support.queue, Commit: queueHook(rollback)})
		}
		scan := fmt.Sprintf("%s/libraries/%d/scan", platform, l1.LibraryID)
		nativeRecoveryInstall(f, queueLibraries(true), nil)
		failure = nativeRecoveryError{}
		f.command("POST", scan, f.platformToken, "application/json", f.body(revisions), 503, &failure)
		if failure.Error != "native_storage_unavailable" || count("SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", l1.LibraryID) != 0 {
			t.Fatal("queue definite rollback retained run")
		}
		nativeRecoveryNoEvents(t, f)
		nativeRecoveryInstall(f, queueLibraries(false), nil)
		unknown = nativeRecoveryError{}
		f.command("POST", scan, f.platformToken, "application/json", f.body(revisions), 503, &unknown)
		nativeRecoveryUnknown(t, unknown, "scan", l1, f.source.SourceKey)
		if unknown.ScanRunID == nil || *unknown.ScanRunID == "" {
			t.Fatal("queue unknown omitted actual selected run")
		}
		selected := *unknown.ScanRunID
		run, err := scanqueue.NewRepository(f.pool).GetByID(t.Context(), selected)
		if err != nil || run.Status != "accepted" || run.MediaFolderID != l1.LibraryID || count("SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", l1.LibraryID) != 1 {
			t.Fatal("queue unknown lacks durable selected run")
		}
		nativeRecoveryNoEvents(t, f)
		nativeRecoveryInstall(f, base, nil)
		current := nativeRecoveryGet(t, f, l1.LibraryID)
		revisions["expected_library_revision"] = current.LibraryRevision
		revisions["expected_source_revision"] = *current.SourceRevision
		var coalesced nativestorage.ScanResult
		f.command("POST", scan, f.platformToken, "application/json", f.body(revisions), 202, &coalesced)
		if coalesced.Created || coalesced.ScanRunID != selected || count("SELECT count(*) FROM scan_runs WHERE media_folder_id=$1", l1.LibraryID) != 1 {
			t.Fatal("unknown queue reconciliation created second run")
		}
		nativeRecoveryNoEvents(t, f)
		if rollbackReceipts.Load() != 4 || commitReceipts.Load() != 4 || count("SELECT count(*) FROM bloem_native_libraries") != initialCount+1 || sectionSnapshot("all") != allSections {
			t.Fatal("recovery operation receipts or retained identities changed")
		}
		t.Logf("Queue: real rollback left zero rows; actual service COMMIT+lost acknowledgement unknown503 selected run=%s; no scan.accepted; authorized GET/current-revision explicit retry coalesced SAME run; four rollback and four real-COMMIT hook receipts", selected)
	})
}

// This serial integration test runs without any other Service.Start instance.
// Go1.26.8 stack symbols provide an independent terminal observation because the
// production Stop method intentionally has no join API. Timeout is a failure.
func nativeQueueFrames(t *testing.T) int {
	t.Helper()
	b := make([]byte, 2<<20)
	n := runtime.Stack(b, true)
	if n == len(b) {
		t.Fatal("queue stack census truncated")
	}
	total := 0
	for _, frame := range []string{"github.com/Silo-Server/silo-server/internal/scanqueue.(*Service).workerLoop(", "github.com/Silo-Server/silo-server/internal/scanqueue.(*Service).maintenanceLoop(", "github.com/Silo-Server/silo-server/internal/scanqueue.(*Service).heartbeatLoop("} {
		total += strings.Count(string(b[:n]), frame)
	}
	return total
}

func TestNativeOnboardingHTTPRealQueueWorkerDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success", func(t *testing.T, f nativeOnboardingLifecycleFixture) {
		if runtime.Version() != "go1.26.8" {
			t.Fatal("queue stack observation requires pinned Go1.26.8")
		}
		if nativeQueueFrames(t) != 0 {
			t.Fatal("queue baseline not isolated")
		}
		snapshot := func() string {
			var s string
			err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
 'library',(SELECT to_jsonb(n) FROM bloem_native_libraries n WHERE folder_id=$1),
 'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM bloem_storage_bindings b WHERE folder_id=$1),
 'files',(SELECT jsonb_agg(jsonb_build_array(f.id,f.content_id,f.container,r.binding_id,r.entry_id) ORDER BY f.id) FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE f.media_folder_id=$1),
 'progress',(SELECT jsonb_agg(to_jsonb(p) ORDER BY content_id) FROM ebook_reader_progress p WHERE user_id=$2 AND profile_id='http-reader'))::text`, f.bound.FolderID, f.account).Scan(&s)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		before := snapshot()
		nativeRecoveryNoEvents(t, f)
		var queued nativestorage.ScanResult
		f.command("POST", fmt.Sprintf("/api/bloem/v1/admin/platform/native-storage/libraries/%d/scan", f.bound.FolderID), f.platformToken, "application/json", f.body(map[string]any{"expected_source_revision": 1, "expected_library_revision": 3}), 202, &queued)
		if !queued.Created || queued.ScanRunID == "" || queued.ScanRunID == f.completedScan {
			t.Fatal("next run not newly accepted")
		}
		deadline := time.NewTimer(45 * time.Second)
		defer deadline.Stop()
		await := func(name, status string) {
			t.Helper()
			for {
				select {
				case e := <-f.support.observed:
					if !strings.HasPrefix(e.Event, "scan.") {
						continue
					}
					var r events.ScanRun
					if json.Unmarshal(e.Data, &r) != nil {
						t.Fatal("decode actual queue event")
					}
					if r.ID != queued.ScanRunID {
						t.Fatal("queue event named unrelated run")
					}
					if e.Event == "scan.progress" {
						continue
					}
					if e.Event != name || r.Status != status {
						t.Fatalf("actual queue event=%s status=%s want=%s/%s", e.Event, r.Status, name, status)
					}
					if name == "scan.completed" && (r.Result == nil || r.Result.Errors != 0) {
						t.Fatal("actual worker completed without a clean consumer result")
					}
					t.Logf("actual Service event=%s run=%s status=%s", e.Event, r.ID, r.Status)
					return
				case <-deadline.C:
					t.Fatalf("actual queue %s receipt absent", name)
				case <-t.Context().Done():
					t.Fatal("queue event observation canceled")
				}
			}
		}
		await("scan.accepted", "accepted")
		// Register teardown before Start; even a failed receipt must stop/cancel and
		// independently observe every queue loop gone before fixture cleanup.
		stopped := false
		quiesce := func() {
			if stopped {
				return
			}
			stopped = true
			f.support.queue.Stop()
			f.support.stopQueue()
			limit := time.NewTimer(15 * time.Second)
			defer limit.Stop()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				if nativeQueueFrames(t) == 0 && f.pool.Stat().AcquiredConns() == 0 {
					t.Log("Service.Stop + app cancellation: workerLoop/maintenanceLoop/heartbeatLoop terminal census=0; acquired pool connections=0 before host/pool cleanup")
					return
				}
				select {
				case <-tick.C:
				case <-limit.C:
					t.Error("queue quiescence NOT observed; timeout is not a join")
					return
				}
			}
		}
		t.Cleanup(quiesce)
		f.support.queue.Start()
		await("scan.started", "running")
		await("scan.completed", "completed")
		run, err := scanqueue.NewRepository(f.pool).GetByID(t.Context(), queued.ScanRunID)
		if err != nil || run.Status != "completed" || run.MediaFolderID != f.bound.FolderID || run.StartedAt == nil || run.CompletedAt == nil || run.CompletedAt.Before(*run.StartedAt) {
			t.Fatal("real Service completion lacks durable run")
		}
		quiesce()
		if snapshot() != before {
			t.Fatal("real next-run worker changed library/binding/file/content identities or progress")
		}
		var formats, files, running int
		if err = f.pool.QueryRow(t.Context(), `SELECT count(*),count(DISTINCT f.container),(SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$2 AND state='running') FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1`, f.bound.BindingID, f.source.SourceKey).Scan(&files, &formats, &running); err != nil || files != 2 || formats != 2 || running != 0 {
			t.Fatal("real worker lost legal EPUB/PDF rows or left native run active")
		}
		t.Log("Same real Service.Start claimed next accepted run -> actual folder store/Executor/NativeConsumer -> durable completed and scan.completed; legal EPUB/PDF identities/progress unchanged; no process-restart or independent heartbeat acceptance claimed")
	})
}
