//go:build integration

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This callback reuses the sealed login/install/create/initialize/bind/actual
// queue claim/consumer/catalog fixture without rerunning its settled reader cases.
type nativeOnboardingLifecycleFixture struct {
	support                                                      nativeOnboardingTestSupport
	pool                                                         *pgxpool.Pool
	host                                                         *nativestorage.Host
	server                                                       *httptest.Server
	journal                                                      string
	source                                                       nativestorage.SourceView
	bound                                                        nativestorage.BindResult
	created                                                      nativestorage.LibraryStatus
	account                                                      int
	readerToken, platformToken, organizationToken, completedScan string
	request                                                      func(string, string, string, string, map[string]string) (int, http.Header, []byte)
	command                                                      func(string, string, string, string, io.Reader, int, any)
	body                                                         func(any) io.Reader
}

func TestNativeOnboardingHTTPDisableLifecycleDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success", func(t *testing.T, f nativeOnboardingLifecycleFixture) { verifyNativeHTTPLifecycle(t, f, false) })
}
func TestNativeOnboardingHTTPUninstallLifecycleDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success", func(t *testing.T, f nativeOnboardingLifecycleFixture) { verifyNativeHTTPLifecycle(t, f, true) })
}

// Receipts, rather than elapsed time, are the only successful wait condition.
func nativeLifecycleReceipt(t *testing.T, journal, receipt string) bool {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(journal)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal("read finite provider receipt")
		}
		if strings.Contains(string(data), receipt) {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Errorf("actual provider receipt absent: %s", receipt)
			return false
		case <-t.Context().Done():
			t.Fatal("lifecycle receipt canceled")
		}
	}
}

func verifyNativeHTTPLifecycle(t *testing.T, f nativeOnboardingLifecycleFixture, uninstall bool) {
	const platform = "/api/bloem/v1/admin/platform/native-storage"
	const organization = "/api/bloem/v1/admin/organization/native-storage"
	id := int(*f.source.InstallationID)
	key, folder := f.source.SourceKey, f.bound.FolderID
	queueRepo := scanqueue.NewRepository(f.pool)
	// Keep a real accepted, unclaimed run alongside the already completed ingest.
	// Service.Start is deliberately outside this bounded component gate.
	var queued nativestorage.ScanResult
	scanPath := fmt.Sprintf("%s/libraries/%d/scan", platform, folder)
	f.command("POST", scanPath, f.platformToken, "application/json", f.body(map[string]any{"expected_source_revision": 1, "expected_library_revision": 3}), 202, &queued)
	queuedRow, err := queueRepo.GetByID(t.Context(), queued.ScanRunID)
	if err != nil || !queued.Created || queuedRow.Status != "accepted" || queuedRow.MediaFolderID != folder {
		t.Fatal("second scan lacks real accepted queue receipt")
	}
	completed, err := queueRepo.GetByID(t.Context(), f.completedScan)
	if err != nil || completed.Status != "completed" {
		t.Fatal("original ingest is not terminal")
	}
	var running int
	if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$1 AND state='running'", key).Scan(&running); err != nil || running != 0 {
		t.Fatal("unexpected active native scan lease")
	}
	t.Logf("queue before: completed=%s accepted=%s; native running=0; live activity is provider Read", completed.ID, queuedRow.ID)

	// Exact affected durable rows only. Lifecycle-mutated source/installation
	// columns are asserted separately; every retained row is compared verbatim.
	retained := func() string {
		t.Helper()
		var value string
		err := f.pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
   'source_identity',(SELECT to_jsonb(s)-ARRAY['enabled','installation_id','latest_installation_id','configuration_revision','updated_at'] FROM bloem_storage_sources s WHERE key=$1),
   'folder',(SELECT to_jsonb(f) FROM media_folders f WHERE id=$2),
   'library',(SELECT to_jsonb(n) FROM bloem_native_libraries n WHERE folder_id=$2),
   'bindings',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM bloem_storage_bindings b WHERE source_key=$1),
   'entries',(SELECT jsonb_agg(to_jsonb(e) ORDER BY entry_id) FROM bloem_storage_entries e WHERE source_key=$1),
   'refs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY media_file_id) FROM bloem_storage_file_refs r WHERE binding_id=$3),
   'files',(SELECT jsonb_agg(to_jsonb(f) ORDER BY id) FROM media_files f WHERE media_folder_id=$2),
   'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY content_id) FROM media_items i WHERE content_id IN(SELECT content_id FROM media_files WHERE media_folder_id=$2)),
   'progress',(SELECT jsonb_agg(to_jsonb(p) ORDER BY content_id) FROM ebook_reader_progress p WHERE user_id=$4 AND profile_id='http-reader' AND content_id IN(SELECT content_id FROM media_files WHERE media_folder_id=$2)),
   'native_runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM bloem_storage_scan_runs r WHERE source_key=$1),
   'queue_runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM scan_runs r WHERE media_folder_id=$2)
  )::text`, key, folder, f.bound.BindingID, f.account).Scan(&value)
		if err != nil {
			t.Fatal("read affected lifecycle rows", err)
		}
		return value
	}
	before := retained()
	// Synthetic fixture rows/IDs are useful evidence and contain no credentials.
	t.Logf("affected retained rows BEFORE=%s", before)
	var file int
	var content, entry string
	if err = f.pool.QueryRow(t.Context(), "SELECT f.id,f.content_id,r.entry_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id WHERE r.binding_id=$1 ORDER BY f.id LIMIT 1", f.bound.BindingID).Scan(&file, &content, &entry); err != nil {
		t.Fatal(err)
	}
	readerPath := fmt.Sprintf("/api/v1/ebooks/%s/files/%d/read", content, file)
	snapshot, err := f.host.Registry.Snapshot(t.Context(), key, mustNativeLifecycleOwner(t, f))
	if err != nil {
		t.Fatal("snapshot of actual legally published provider")
	}
	runtimeSnapshot := storageplugin.Snapshot{InstallationID: id, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true}
	session, err := f.host.Manager.Ensure(t.Context(), runtimeSnapshot)
	if err != nil {
		t.Fatal("observe actual shared manager session")
	}
	select {
	case <-session.Done():
		t.Fatal("provider already reaped before lifecycle")
	default:
	}

	journalBefore, err := os.ReadFile(f.journal)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.journal+".arm", []byte("one legal reader"), 0600); err != nil {
		t.Fatal(err)
	}
	type readResult struct {
		status int
		data   []byte
		err    error
	}
	result := make(chan readResult, 1)
	joined := make(chan struct{})
	readCtx, cancelRead := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancelRead()
		select {
		case <-joined:
			t.Log("in-flight HTTP reader joined before server/host/pool cleanup")
		case <-time.After(15 * time.Second):
			t.Error("in-flight HTTP reader failed to join")
		}
	})
	// A fresh connection prevents net/http's automatic retry of an idempotent
	// GET on an already-used connection from hiding the blocked request outcome.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	readerClient := &http.Client{Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)
	go func() {
		defer close(joined)
		req, e := http.NewRequestWithContext(readCtx, "GET", f.server.URL+readerPath, nil)
		if e != nil {
			result <- readResult{err: e}
			return
		}
		req.Header.Set("Authorization", "Bearer "+f.readerToken)
		req.Header.Set("X-Profile-Id", "http-reader")
		response, e := readerClient.Do(req)
		if e != nil {
			result <- readResult{err: e}
			return
		}
		defer response.Body.Close()
		data, e := io.ReadAll(response.Body)
		result <- readResult{status: response.StatusCode, data: data, err: e}
	}()
	if !nativeLifecycleReceipt(t, f.journal, "read-started "+entry+" ") {
		t.Fatal("no live provider Read to fence")
	}
	select {
	case <-joined:
		t.Fatal("controlled reader exited before lifecycle")
	default:
	}
	pidBytes, err := os.ReadFile(f.journal + ".pid")
	if err != nil {
		t.Fatal("provider PID receipt missing")
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil || pid <= 1 || syscall.Kill(pid, 0) != nil {
		t.Fatal("provider process not live at Read receipt")
	}
	t.Logf("real provider Read started: file=%d entry=%s pid=%d generation=%d", file, entry, pid, snapshot.Generation)

	method, mutationPath, wantState := "POST", fmt.Sprintf("%s/installations/%d/disable", platform, id), "disabled_detached"
	if uninstall {
		method, mutationPath, wantState = "DELETE", fmt.Sprintf("%s/installations/%d", platform, id), "uninstalled_detached"
	}
	var outcome struct {
		InstallationID int    `json:"installation_id"`
		State          string `json:"state"`
		Retained       bool   `json:"retained"`
	}
	f.command(method, mutationPath, f.platformToken, "application/json", f.body(map[string]any{"source_key": key, "expected_revision": 1}), 200, &outcome)
	if outcome.InstallationID != id || outcome.State != wantState || !outcome.Retained {
		t.Fatal("HTTP lifecycle acknowledgement wrong")
	}
	t.Logf("actual HTTP mutation returned200 state=%s retained=true installation=%d", outcome.State, id)
	if session.Context().Err() != context.Canceled {
		t.Fatal("actual shared manager lifetime was not canceled")
	}
	select {
	case <-session.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("actual manager worker reap receipt missing")
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("provider worker receipt did not reap real process")
	}
	t.Log("actual session.Context cancellation, session.Done and process-reap receipts verified")
	select {
	case <-joined:
	case <-time.After(15 * time.Second):
		t.Fatal("HTTP reader terminal receipt missing")
	}
	read := <-result
	// The real handler aborts a failed stream; it must not return successful bytes.
	if read.err == nil || (!errors.Is(read.err, io.EOF) && !errors.Is(read.err, io.ErrUnexpectedEOF)) || len(read.data) != 0 {
		t.Fatalf("canceled read status=%d bytes=%d err=%T", read.status, len(read.data), read.err)
	}
	t.Logf("joined HTTP reader terminal: status=%d bytes=%d EOF=%t unexpectedEOF=%t", read.status, len(read.data), errors.Is(read.err, io.EOF), errors.Is(read.err, io.ErrUnexpectedEOF))
	if _, err = f.host.Manager.Ensure(t.Context(), runtimeSnapshot); !errors.Is(err, storageplugin.ErrDisabled) {
		t.Fatal("same-generation actual manager snapshot resurrected fenced provider")
	}

	var attached, latest *int64
	var enabled bool
	var revision int64
	if err = f.pool.QueryRow(t.Context(), "SELECT installation_id,latest_installation_id,enabled,configuration_revision FROM bloem_storage_sources WHERE key=$1", key).Scan(&attached, &latest, &enabled, &revision); err != nil || attached != nil || latest == nil || *latest != int64(id) || enabled || revision != 2 {
		t.Fatal("durable source did not retain disabled detached S2 lineage")
	}
	var installationCount, grantCount int
	if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM plugin_installations WHERE id=$1", id).Scan(&installationCount); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), "SELECT count(*) FROM organization_entitlements WHERE plugin_installation_id=$1", id).Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if uninstall {
		if installationCount != 0 || grantCount != 0 {
			t.Fatal("uninstall retained installation or availability grants")
		}
	} else {
		var installationEnabled bool
		var generation uint64
		if installationCount != 1 || grantCount != 1 {
			t.Fatal("disable removed retained installation or grant")
		}
		if err = f.pool.QueryRow(t.Context(), "SELECT enabled,runtime_generation FROM plugin_installations WHERE id=$1", id).Scan(&installationEnabled, &generation); err != nil || installationEnabled || generation != snapshot.Generation+1 {
			t.Fatal("disable durable installation generation wrong")
		}
	}
	var sourceEnvelope struct {
		Source nativestorage.SourceView `json:"source"`
	}
	f.command("GET", platform+"/sources/"+key.String(), f.platformToken, "", nil, 200, &sourceEnvelope)
	if sourceEnvelope.Source.SourceKey != key || sourceEnvelope.Source.InstallationID != nil || sourceEnvelope.Source.Enabled || sourceEnvelope.Source.State != "detached" || sourceEnvelope.Source.ConfigurationRevision != 2 {
		t.Fatal("HTTP retained source projection wrong")
	}
	for _, scope := range []struct{ prefix, token string }{{platform, f.platformToken}, {organization, f.organizationToken}} {
		var envelope struct {
			Library nativestorage.LibraryStatus `json:"library"`
		}
		f.command("GET", fmt.Sprintf("%s/libraries/%d", scope.prefix, folder), scope.token, "", nil, 200, &envelope)
		library := envelope.Library
		if library.LibraryID != folder || library.CreationKey != f.created.CreationKey || library.LibraryRevision != 3 || !library.Initialized || library.State != "source_unavailable" || library.ReadyToQueue || library.SupportedOperations["full_scan"] {
			t.Fatal("retained L3 unavailable projection wrong")
		}
		if scope.prefix == platform && (library.BindingID == nil || *library.BindingID != f.bound.BindingID || library.SourceKey == nil || *library.SourceKey != key || library.SourceRevision == nil || *library.SourceRevision != 2 || library.SourceAvailability != "detached") {
			t.Fatal("platform detached source/library identity projection wrong")
		}
	}
	after := retained()
	t.Logf("affected retained rows AFTER=%s", after)
	if before != after {
		t.Fatal("lifecycle changed retained library/binding/item/file/ref/progress/scan rows")
	}
	t.Logf("durable source revision=2 detached disabled latest_installation=%d; L3 retained; installation_count=%d grants=%d", id, installationCount, grantCount)
	journalAfter, err := os.ReadFile(f.journal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(journalAfter, journalBefore) {
		t.Fatal("finite provider journal truncated or replaced")
	}
	delta := string(journalAfter[len(journalBefore):])
	startedCount := strings.Count(delta, "read-started ")
	canceledCount := strings.Count(delta, "read-canceled ")
	// Hard process termination may preempt the provider handler's cancellation
	// journal write. It is diagnostic; host cancellation/read/reap stay mandatory.
	t.Logf("final provider journal after reap: read-started=%d read-canceled=%d (optional diagnostic)", startedCount, canceledCount)
	if startedCount != 1 || canceledCount > 1 {
		t.Error("finite read receipt count wrong")
	}
	// Both actual readers must refuse all published formats without provider I/O.
	rows, err := f.pool.Query(t.Context(), "SELECT id,content_id FROM media_files WHERE media_folder_id=$1 ORDER BY id", folder)
	if err != nil {
		t.Fatal(err)
	}
	type publishedFile struct {
		id      int
		content string
	}
	var published []publishedFile
	for rows.Next() {
		var p publishedFile
		if err = rows.Scan(&p.id, &p.content); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		published = append(published, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(published) != 2 {
		t.Fatal("retained published formats missing")
	}
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		for _, p := range published {
			status, _, _ := f.request("GET", fmt.Sprintf("%s/ebooks/%s/files/%d/read", prefix, p.content, p.id), f.readerToken, "http-reader", nil)
			if status != 503 {
				t.Fatalf("post-lifecycle reader status=%d", status)
			}
		}
	}
	// Current source revision prevents an obsolete-revision error from standing
	// in for unavailable-source admission. Platform sees unavailable503; the
	// organization receives hidden404 for the detached platform-owned source.
	f.command("POST", scanPath, f.platformToken, "application/json", f.body(map[string]any{"expected_source_revision": 2, "expected_library_revision": 3}), 503, nil)
	f.command("POST", fmt.Sprintf("%s/libraries/%d/scan", organization, folder), f.organizationToken, "application/json", f.body(map[string]any{"expected_source_revision": 2, "expected_library_revision": 3}), 404, nil)
	refusalJournal, err := os.ReadFile(f.journal)
	if err != nil || !bytes.Equal(journalAfter, refusalJournal) {
		t.Fatal("post-command read or scan reached provider")
	}
	if retained() != after {
		t.Fatal("refused read or scan mutated retained rows or queued another scan")
	}
	acceptedAfter, err := queueRepo.GetByID(t.Context(), queued.ScanRunID)
	if err != nil || acceptedAfter.Status != "accepted" {
		t.Fatal("unclaimed accepted queue run silently changed state")
	}
	t.Log("both reader versions and both admin scan scopes refuse; provider journal unchanged; prior completed and unclaimed accepted queue rows retained")
}

func mustNativeLifecycleOwner(t *testing.T, f nativeOnboardingLifecycleFixture) (owner uuid.UUID) {
	t.Helper()
	if err := f.pool.QueryRow(t.Context(), "SELECT owner_id FROM bloem_storage_sources WHERE key=$1", f.source.SourceKey).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return
}
