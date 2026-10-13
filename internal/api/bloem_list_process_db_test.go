//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func TestListFollowRealPluginAndPastimeProcess(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	d, subject, native, requestCtx := listNativeRequestFixture(t)
	d.source.prepare = func(context.Context, managedtracking.Profile) (context.Context, error) { return requestCtx, nil }
	service := &listcollections.Service{Pool: d.source.pool, Cipher: d.managed.Cipher, Domain: d, Requests: &listCollectionRequests{domain: d, movies: native, books: bookrequests.NewService(d.source.pool, nil)}}
	// The unit fixture pre-enrolls synthetic destination IDs. A new source
	// process must enroll itself instead of retargeting that pinned identity.
	tx, err := d.source.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('bloem.managed_tracking_writer','v1',true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM watch_provider_connections WHERE provider=$1`, watchsync.PluginProviderKey(subject.InstallationID, "bloem.pastime", "pastime", false)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_managed_enrollments WHERE installation_id=$1`, subject.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	d.managed.SetListBackend(&listCollectionBackend{service: service})
	settings := filepath.Join(t.TempDir(), "settings.json")
	fixture := exec.CommandContext(ctx, fixtureBinary, "-test.run=^TestNativeAcceptanceFixture$", "-test.timeout=170s")
	info, err := d.managed.GetInfo(ctx, subject.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Env = append(os.Environ(), "TMPDIR="+filepath.Dir(settings), "PASTIME_NATIVE_FIXTURE_SETTINGS="+settings, "PASTIME_NATIVE_FIXTURE_INSTANCE="+subject.InstanceID, "PASTIME_NATIVE_FIXTURE_SCOPE="+info.Scope)
	output := filepath.Join(t.TempDir(), "fixture.log")
	log, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	fixture.Stdout = log
	fixture.Stderr = log
	if err = fixture.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- fixture.Wait() }()
	defer func() {
		_ = fixture.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()
	var config struct {
		URL string `json:"url"`
	}
	listProcessWait(t, ctx, func() bool {
		raw, err := os.ReadFile(settings)
		return err == nil && json.Unmarshal(raw, &config) == nil && config.URL != ""
	})
	t.Setenv("BLOEM_PASTIME_CONFIG_FILE", settings)
	raw, err := exec.Command(pluginBinary, "manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	manifest := &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(raw, manifest); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.NewHost(pluginhost.Config{BrokerRegistrar: d.managed.RegisterBroker, InstanceState: plugins.NewInstanceStateStore(d.source.pool, d.managed.Cipher).ForScope("fixture")})
	defer func() { _ = host.Shutdown(context.Background()) }()
	client, err := host.Start(ctx, pluginhost.StartRequest{InstallationID: subject.InstallationID, BinaryPath: pluginBinary, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.ScheduledTask("pastime.reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = task.Run(ctx, &pluginv1.RunScheduledTaskRequest{TaskKey: "pastime.reconcile"}); err != nil {
		t.Fatal(err)
	}
	control := func(ids []int, id int64, remove bool) (int64, string) {
		in := map[string]any{"Profile": subject.ProfileID, "ListID": id, "IDs": ids, "Delete": remove}
		body, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(ctx, "POST", config.URL+"/fixture/list", bytes.NewReader(body))
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out struct {
			ListID int64  `json:"list_id"`
			URL    string `json:"url"`
		}
		if response.StatusCode != 200 {
			raw, _ := io.ReadAll(io.LimitReader(response.Body, 512))
			t.Fatalf("source control: %d %s", response.StatusCode, raw)
		}
		if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out.ListID, out.URL
	}
	var folder int
	if err = d.source.pool.QueryRow(ctx, `SELECT id FROM media_folders ORDER BY id DESC LIMIT 1`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	catalog := map[int]string{}
	ingest := func(id int) {
		content := uuid.NewString()
		catalog[id] = content
		if _, err = d.source.pool.Exec(ctx, `INSERT INTO media_items(content_id,type,title,tmdb_id)VALUES($1,'movie','Synthetic process title',$2)`, content, strconv.Itoa(id)); err != nil {
			t.Fatal(err)
		}
		if _, err = d.source.pool.Exec(ctx, `INSERT INTO media_item_libraries(content_id,media_folder_id)VALUES($1,$2)`, content, folder); err != nil {
			t.Fatal(err)
		}
	}
	ingest(42)
	ingest(43)
	sourceID, url := control([]int{42, 99}, 0, false)
	preview, err := service.QueuePreview(ctx, subject, listcollections.PreviewInput{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	listProcessWait(t, ctx, func() bool {
		status, e := service.GetPreview(ctx, subject, preview.ID)
		if e != nil {
			t.Fatal(e)
		}
		if status.Status == "failed" {
			t.Fatal("source resolve failed", status.Failure)
		}
		return status.Status == "resolved"
	})
	binding, err := service.CreateBinding(ctx, subject, listcollections.CreateInput{PreviewID: preview.ID, Scope: "personal", Name: "Native linked collection"})
	if err != nil {
		t.Fatal(err)
	}
	waitReady := func(generation uint64, matched int) {
		listProcessWait(t, ctx, func() bool {
			b, _, e := service.GetBinding(ctx, subject, binding.ID)
			if e != nil {
				t.Fatal(e)
			}
			if b.Status == "failed" || b.Status == "paused" {
				t.Fatal("source refresh failed", b.Failure)
			}
			return b.Generation == generation && b.Status == "ready" && b.Matched == matched
		})
	}
	members := func(want []int) {
		rows, e := d.source.pool.Query(ctx, `SELECT media_item_id FROM user_personal_collection_items WHERE collection_id=$1 ORDER BY position`, binding.TargetID)
		if e != nil {
			t.Fatal(e)
		}
		actual, e := pgx.CollectRows(rows, pgx.RowTo[string])
		if e != nil {
			t.Fatal(e)
		}
		expected := []string{}
		for _, id := range want {
			expected = append(expected, catalog[id])
		}
		if !slices.Equal(actual, expected) {
			t.Fatalf("source order/membership changed: %v want %v", actual, expected)
		}
	}
	refresh := func(matched int) {
		job, e := service.Refresh(ctx, subject, binding.ID)
		if e != nil {
			t.Fatal(e)
		}
		waitReady(job.Generation, matched)
	}
	waitReady(binding.Generation, 1)
	members([]int{42})
	delivered, err := service.DeliverRequests(ctx, 10)
	if err != nil || delivered.Delivered != 1 {
		t.Fatalf("native missing request: %+v %v", delivered, err)
	}
	ingest(99)
	refresh(2)
	members([]int{42, 99}) // Identical digest still rematches newly ingested media.
	control([]int{43, 42}, sourceID, false)
	refresh(2)
	members([]int{43, 42})
	control([]int{}, sourceID, false)
	refresh(0)
	members(nil)
	control([]int{43}, sourceID, false)
	refresh(1)
	members([]int{43})
	control(nil, sourceID, true)
	if _, err = service.Refresh(ctx, subject, binding.ID); err != nil {
		t.Fatal(err)
	}
	listProcessWait(t, ctx, func() bool {
		b, _, e := service.GetBinding(ctx, subject, binding.ID)
		if e != nil {
			t.Fatal(e)
		}
		return b.Status == "failed" || b.Status == "paused"
	})
	members([]int{43})
	if err = service.Disconnect(ctx, subject, binding.ID); err != nil {
		t.Fatal(err)
	}
	members([]int{43})
	var count int
	if err = d.source.pool.QueryRow(ctx, `SELECT count(*) FROM media_requests WHERE requested_by_user_id=$1`, subject.UserID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("issued requests changed after source removal: %d %v", count, err)
	}
	t.Log("Real host, plugin and Pastime: resolve, signed publication, native request, ingest rematch, reorder/remove, empty clear, failed-source retention and disconnect passed")
}
func listProcessWait(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("observable source state timeout", ctx.Err())
		case <-tick.C:
		}
	}
}
