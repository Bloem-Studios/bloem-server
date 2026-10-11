package managedtracking

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRealPluginAndPastimeHouseholdAcceptance(t *testing.T) {
	pluginBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY")
	fixtureBinary := os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	if !filepath.IsAbs(pluginBinary) || !filepath.IsAbs(fixtureBinary) {
		t.Fatal("absolute fixture binaries required")
	}
	s, id, p := enrollmentFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	group := int64(0)
	if err = s.Pool.QueryRow(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND is_default`, info.Scope).Scan(&group); err != nil {
		t.Fatal(err)
	}
	child, second := uuid.NewString(), uuid.NewString()
	for i, profile := range []string{child, second} {
		if _, err = s.Pool.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES($1,$2,$3,$4,$5)`, profile, p.AccountID, []string{"Child", "Second"}[i], info.Scope, group); err != nil {
			t.Fatal(err)
		}
	}
	settings := filepath.Join(t.TempDir(), "settings.json")
	fixture := exec.CommandContext(ctx, fixtureBinary, "-test.run=^TestNativeAcceptanceFixture$", "-test.timeout=110s")
	fixture.Env = append(os.Environ(), "PASTIME_NATIVE_FIXTURE_SETTINGS="+settings, "PASTIME_NATIVE_FIXTURE_INSTANCE="+info.InstanceID, "PASTIME_NATIVE_FIXTURE_SCOPE="+info.Scope)
	var fixtureOutput bytes.Buffer
	fixture.Stdout = &fixtureOutput
	fixture.Stderr = &fixtureOutput
	if err = fixture.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- fixture.Wait() }()
	t.Cleanup(func() {
		if fixture.Process != nil {
			_ = fixture.Process.Kill()
		}
	})
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var config struct {
		URL string `json:"url"`
	}
	for {
		raw, e := os.ReadFile(settings)
		if e == nil {
			if json.Unmarshal(raw, &config) != nil {
				t.Fatal("fixture settings invalid")
			}
			break
		}
		select {
		case e := <-finished:
			t.Fatalf("fixture startup: %v %s", e, fixtureOutput.String())
		case <-ctx.Done():
			t.Fatal("fixture startup timeout")
		case <-ticker.C:
		}
	}
	t.Setenv("BLOEM_PASTIME_CONFIG_FILE", settings)
	raw, err := exec.Command(pluginBinary, "manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	manifest := &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(raw, manifest); err != nil {
		t.Fatal(err)
	}
	host := pluginhost.NewHost(pluginhost.Config{BrokerRegistrar: s.RegisterBroker, InstanceState: plugins.NewInstanceStateStore(s.Pool, s.Cipher).ForScope("fixture")})
	t.Cleanup(func() { host.Shutdown(context.Background()) })
	var client *pluginhost.Client
	start := func() {
		var e error
		client, e = host.Start(ctx, pluginhost.StartRequest{InstallationID: id, BinaryPath: pluginBinary, Manifest: manifest})
		if e != nil {
			t.Fatal(e)
		}
	}
	reconcile := func() {
		task, e := client.ScheduledTask("pastime.reconcile")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = task.Run(ctx, &pluginv1.RunScheduledTaskRequest{TaskKey: "pastime.reconcile"}); e != nil {
			t.Fatal(e)
		}
	}
	start()
	reconcile()
	type remoteProfile struct {
		ID, Name        string
		Active          bool
		Plays, Position int
	}
	state := func() map[string]remoteProfile {
		r, e := http.NewRequestWithContext(ctx, "GET", config.URL+"/fixture/state", nil)
		if e != nil {
			t.Fatal(e)
		}
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var data struct {
			Accounts int
			Profiles []remoteProfile
		}
		if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&data) != nil {
			t.Fatal("fixture state failed")
		}
		if data.Accounts != 1 {
			t.Fatalf("accounts=%d", data.Accounts)
		}
		out := map[string]remoteProfile{}
		for _, p := range data.Profiles {
			out[p.ID] = p
		}
		return out
	}
	if got := state(); len(got) != 3 {
		t.Fatalf("profiles=%d", len(got))
	}
	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	account, _ := strconv.Atoi(p.AccountID)
	apply := func(profile string, event *pluginv1.WatchSyncEvent, record bool) {
		conn, found, e := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, profile)
		if e != nil || !found {
			t.Fatal("managed connection absent", e)
		}
		if record {
			action := "pause"
			if event.Completed {
				action = "stop_confirming"
			}
			if e = repo.UpsertScrobbleSession(ctx, watchsync.ScrobbleEvent{PlaybackSessionID: event.PlaybackSessionId, MediaItemID: event.Media.MediaItemId, HistoryID: event.WatchHistoryId, PositionSeconds: event.PositionSeconds, DurationSeconds: event.DurationSeconds, Completed: event.Completed, OccurredAt: event.OccurredAt.AsTime()}, conn.ID, action); e != nil {
				t.Fatal(e)
			}
		}
		provider, e := client.WatchSyncProvider("pastime")
		if e != nil {
			t.Fatal(e)
		}
		request := &pluginv1.WatchSyncApplyEventsRequest{Context: &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: conn.AccessToken, SecretAttributes: conn.SecretAttributes}}, Events: []*pluginv1.WatchSyncEvent{event}}
		out, e := s.WrapClient(id, provider).ApplyEvents(ctx, request)
		if e != nil || out.GetFault() != nil || len(out.GetResults()) != 1 || out.Results[0].GetFault() != nil {
			t.Fatalf("playback failed: %v %v", out, e)
		}
	}
	media := &pluginv1.WatchSyncMedia{MediaItemId: "synthetic-media", MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "42"}}
	history := "fixture-viewing"
	if _, err = s.Pool.Exec(ctx, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,'synthetic-media',100,true)`, history, account, child); err != nil {
		t.Fatal(err)
	}
	completion := &pluginv1.WatchSyncEvent{EventId: "completed-viewing", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP, OccurredAt: timestamppb.New(time.Now().Add(-time.Minute)), PlaybackSessionId: "fixture-session", WatchHistoryId: history, Completed: true, PositionSeconds: 95, DurationSeconds: 100, Media: media}
	apply(child, completion, true)
	apply(child, completion, false)
	newer := &pluginv1.WatchSyncEvent{EventId: "newer-resume", Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_PAUSE, OccurredAt: timestamppb.New(time.Now().Add(-time.Minute)), PlaybackSessionId: "second-session", PositionSeconds: 20, DurationSeconds: 100, Media: media}
	apply(child, newer, true)
	imported := &pluginv1.WatchSyncEvent{EventId: history, WatchHistoryId: history, Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: completion.OccurredAt, DurationSeconds: 100, Media: media}
	apply(child, imported, false)
	got := state()
	if got[child].Plays != 1 || got[child].Position != 20 || got[p.ProfileID].Plays != 0 || got[second].Plays != 0 {
		t.Fatalf("profile isolation/history overlap: %+v", got)
	}
	// Future profile creation, rename and deletion converge through the same connector.
	added := uuid.NewString()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES($1,$2,'Future child',$3,$4)`, added, account, info.Scope, group); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE user_profiles SET name='Renamed child' WHERE id=$1`, child); err != nil {
		t.Fatal(err)
	}
	reconcile()
	got = state()
	if len(got) != 4 || got[child].Name != "Renamed child" || !got[added].Active {
		t.Fatal("future enrollment or rename failed")
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM user_profiles WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	reconcile()
	if state()[second].Active {
		t.Fatal("deleted child remains active")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	reconcile()
	got = state()
	for _, p := range got {
		if p.Active { // profile activity remains distinct; source account revocation must block its credentials
			conn, found, e := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, p.ID)
			if e != nil || found && (conn.AccessToken != "" || conn.ScrobbleEnabled) {
				t.Fatal("source cancellation retained tracking credentials")
			}
		}
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=true WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	reconcile()
	conn, found, e := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, child)
	if e != nil || !found || conn.AccessToken == "" || !conn.ScrobbleEnabled {
		t.Fatal("reactivation failed to issue fresh credentials")
	}
	if err = host.Stop(id); err != nil {
		t.Fatal(err)
	}
	start()
	reconcile()
	got = state()
	if len(got) != 4 || got[child].Plays != 1 {
		t.Fatal("restart duplicated destination")
	}
	if err = s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetInfo(ctx, id); err == nil {
		t.Fatal("revoked authority survives")
	}
	if err = host.Stop(id); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", config.URL+"/fixture/finish", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("fixture: %v %s", err, fixtureOutput.String())
		}
	case <-ctx.Done():
		t.Fatal("fixture did not join")
	}
}
