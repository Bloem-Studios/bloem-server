package managedtracking

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/watchsync"
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
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	type remoteProfile struct {
		ID, Name        string
		Active          bool
		Plays, Position int
	}
	state := func() map[string]remoteProfile {
		r, e := http.NewRequestWithContext(ctx, "GET", run.url+"/fixture/state", nil)
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
		provider, e := run.client.WatchSyncProvider("pastime")
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
	// The native playback path, through watch sync and the real plugin: a
	// resumed session and a completed non-confirmed stop both reach Pastime.
	native, e := run.client.WatchSyncProvider("pastime")
	if e != nil {
		t.Fatal(e)
	}
	notify := make(chan struct{}, 16)
	provider, e := watchsync.NewPluginProvider(watchsync.PluginProviderOptions{
		InstallationID: id, ProviderKey: watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), CapabilityID: "pastime", DisplayName: "Pastime",
		Descriptor: &pluginv1.WatchSyncProviderDescriptor{
			AuthMethods:         []pluginv1.WatchSyncAuthMethod{pluginv1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY},
			SupportedMediaTypes: []pluginv1.WatchSyncMediaType{pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE},
			MaxBatchSize:        100, ScrobblePlayback: true, ExportWatched: true,
		},
		ResolveClient: func(context.Context, int, string) (watchsync.WatchSyncPluginClient, error) {
			return notifyingClient{WatchSyncPluginClient: s.WrapClient(id, native), notify: notify}, nil
		},
		Repository: repo,
	})
	if e != nil {
		t.Fatal(e)
	}
	registry := watchsync.NewRegistry()
	if e = registry.Register(provider); e != nil {
		t.Fatal(e)
	}
	svc := watchsync.NewService(repo, registry)
	dispatched := func() {
		t.Helper()
		select {
		case <-notify:
		case <-ctx.Done():
			t.Fatal("native dispatch never reached the plugin")
		}
	}
	session := uuid.NewString()
	play := func(position float64) watchsync.ScrobbleEvent {
		return watchsync.ScrobbleEvent{PlaybackSessionID: session, UserID: account, ProfileID: second, MediaItemID: "native-media", Kind: "movie", TMDBID: "42", PositionSeconds: position, DurationSeconds: 100, OccurredAt: time.Now().UTC()}
	}
	for _, step := range []func() error{
		func() error { return svc.ScrobbleStart(ctx, play(0)) },
		func() error { return svc.ScrobblePause(ctx, play(30)) },
		func() error { return svc.ScrobbleStart(ctx, play(40)) },
	} {
		if e = step(); e != nil {
			t.Fatal(e)
		}
		dispatched()
	}
	if got = state(); got[second].Position != 40 || got[second].Plays != 0 {
		t.Fatalf("resumed session did not reach Pastime: %+v", got[second])
	}
	viewing := uuid.NewString()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,'native-media',100,true)`, viewing, account, second); err != nil {
		t.Fatal(err)
	}
	stop := play(97)
	stop.DurationSeconds = 101
	stop.HistoryID = viewing
	stop.Completed = true
	if e = svc.ScrobbleStop(ctx, stop); e != nil {
		t.Fatal(e)
	}
	dispatched()
	if got = state(); got[second].Plays != 1 || got[second].Position != 0 {
		t.Fatalf("completed native stop did not reach Pastime: %+v", got[second])
	}
	// Its history export is the same viewing, not a second play.
	if out, e := s.WrapClient(id, native).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: func() *pluginv1.WatchSyncCredentials {
		conn, _, _ := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, second)
		return &pluginv1.WatchSyncCredentials{AccessToken: conn.AccessToken, SecretAttributes: conn.SecretAttributes}
	}()}, Events: []*pluginv1.WatchSyncEvent{{EventId: viewing, WatchHistoryId: viewing, Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: timestamppb.Now(), DurationSeconds: 100, Media: &pluginv1.WatchSyncMedia{MediaItemId: "native-media", MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "42"}}}}}); e != nil || out.GetFault() != nil || len(out.GetResults()) != 1 || out.Results[0].GetFault() != nil {
		t.Fatalf("history export of the native viewing failed: %v %v", out, e)
	}
	if got = state(); got[second].Plays != 1 {
		t.Fatalf("history export duplicated the native viewing: %+v", got[second])
	}
	// Future profile creation, rename and deletion converge through the same connector.
	added := uuid.NewString()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES($1,$2,'Future child',$3,$4)`, added, account, info.Scope, group); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE user_profiles SET name='Renamed child' WHERE id=$1`, child); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
	got = state()
	if len(got) != 4 || got[child].Name != "Renamed child" || !got[added].Active {
		t.Fatal("future enrollment or rename failed")
	}
	if _, err = s.Pool.Exec(ctx, `DELETE FROM user_profiles WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
	if state()[second].Active {
		t.Fatal("deleted child remains active")
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
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
	run.reconcile()
	conn, found, e := repo.GetConnection(ctx, watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), account, child)
	if e != nil || !found || conn.AccessToken == "" || !conn.ScrobbleEnabled {
		t.Fatal("reactivation failed to issue fresh credentials")
	}
	if err = run.host.Stop(id); err != nil {
		t.Fatal(err)
	}
	run.start()
	run.reconcile()
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
	if err = run.host.Stop(id); err != nil {
		t.Fatal(err)
	}
	run.finish()
}

// pastimeAcceptance is one run of an independently built plugin, hosted by the
// real plugin host, against a Pastime fixture subprocess serving the real API.
type pastimeAcceptance struct {
	t        *testing.T
	ctx      context.Context
	id       int
	url      string
	binary   string
	manifest *pluginv1.PluginManifest
	host     *pluginhost.Host
	client   *pluginhost.Client
	finished chan error
	output   *bytes.Buffer
}

// startPastimeAcceptance starts the Pastime fixture for the installation's
// grant, with extra environment for the fixture, and prepares the plugin host.
func startPastimeAcceptance(t *testing.T, ctx context.Context, s *Service, id int, pluginBinary, fixtureBinary string, env ...string) *pastimeAcceptance {
	t.Helper()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := &pastimeAcceptance{t: t, ctx: ctx, id: id, binary: pluginBinary, finished: make(chan error, 1), output: &bytes.Buffer{}}
	settings := filepath.Join(t.TempDir(), "settings.json")
	fixture := exec.CommandContext(ctx, fixtureBinary, "-test.run=^TestNativeAcceptanceFixture$", "-test.timeout=110s")
	fixture.Env = append(append(os.Environ(), "TMPDIR="+filepath.Dir(settings), "PASTIME_NATIVE_FIXTURE_SETTINGS="+settings, "PASTIME_NATIVE_FIXTURE_INSTANCE="+info.InstanceID, "PASTIME_NATIVE_FIXTURE_SCOPE="+info.Scope), env...)
	fixture.Stdout = r.output
	fixture.Stderr = r.output
	if err = fixture.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { r.finished <- fixture.Wait() }()
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
		case e := <-r.finished:
			t.Fatalf("fixture startup: %v %s", e, r.output.String())
		case <-ctx.Done():
			t.Fatal("fixture startup timeout")
		case <-ticker.C:
		}
	}
	r.url = config.URL
	t.Setenv("BLOEM_PASTIME_CONFIG_FILE", settings)
	raw, err := exec.Command(pluginBinary, "manifest").Output()
	if err != nil {
		t.Fatal(err)
	}
	r.manifest = &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(raw, r.manifest); err != nil {
		t.Fatal(err)
	}
	r.host = pluginhost.NewHost(pluginhost.Config{BrokerRegistrar: s.RegisterBroker, InstanceState: plugins.NewInstanceStateStore(s.Pool, s.Cipher).ForScope("fixture")})
	t.Cleanup(func() { _ = r.host.Shutdown(context.Background()) })
	return r
}

func (r *pastimeAcceptance) start() {
	r.t.Helper()
	client, err := r.host.Start(r.ctx, pluginhost.StartRequest{InstallationID: r.id, BinaryPath: r.binary, Manifest: r.manifest})
	if err != nil {
		r.t.Fatal(err)
	}
	r.client = client
}

// reconcile runs the plugin's reconciliation once; it fails when any profile is
// left pending or the run aborts.
func (r *pastimeAcceptance) reconcile() {
	r.t.Helper()
	task, err := r.client.ScheduledTask("pastime.reconcile")
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err = task.Run(r.ctx, &pluginv1.RunScheduledTaskRequest{TaskKey: "pastime.reconcile"}); err != nil {
		r.t.Fatal(err)
	}
}

// call sends a controller request to the fixture. A JSON answer is decoded into
// out; the status and whether the answer was JSON are returned.
func (r *pastimeAcceptance) call(method, path string, in, out any) (int, bool) {
	r.t.Helper()
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			r.t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(r.ctx, method, r.url+path, &body)
	if err != nil {
		r.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	isJSON := strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json")
	if resp.StatusCode >= 400 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		r.t.Logf("%s %s: %d %s", method, path, resp.StatusCode, text)
	}
	if isJSON && out != nil {
		if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
			r.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode, isJSON
}

// finish lets the fixture exit and waits for it.
func (r *pastimeAcceptance) finish() {
	r.t.Helper()
	if status, _ := r.call("POST", "/fixture/finish", nil, nil); status != 200 {
		r.t.Fatalf("fixture finish: %d", status)
	}
	select {
	case err := <-r.finished:
		if err != nil {
			r.t.Fatalf("fixture: %v %s", err, r.output.String())
		}
	case <-r.ctx.Done():
		r.t.Fatal("fixture did not join")
	}
}

// TestRealPluginAndPastimeIdentityAcceptance carries verified sign-in
// identities from Bloem's sign-in links through the real plugin into a real
// Pastime, where a person's single sign-on claims the managed account holding
// their tracked history. Accounts without an unambiguous identity send none and
// cannot be claimed.
func TestRealPluginAndPastimeIdentityAcceptance(t *testing.T) {
	pluginBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY")
	fixtureBinary := os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	if !filepath.IsAbs(pluginBinary) || !filepath.IsAbs(fixtureBinary) {
		t.Fatal("absolute fixture binaries required")
	}
	s, id, p := enrollmentFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Second)
	defer cancel()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	oidc := authInstallation(t, s.Pool, "silo.auth.oidc", true)
	second := authInstallation(t, s.Pool, "silo.auth.oidc", true)
	type person struct {
		account int
		profile string
		subject string
	}
	owner := person{profile: p.ProfileID, subject: uuid.NewString()}
	owner.account, _ = strconv.Atoi(p.AccountID)
	add := func() person {
		var q person
		q.account, q.profile = tenantAccount(t, s.Pool, info.Scope)
		return q
	}
	explicit, none, twoSubjects, sharedA, sharedB, later := add(), add(), add(), add(), add(), add()
	// The owner's link predates the issuer column; the explicit one carries it.
	ownerLink := linkIdentity(t, s.Pool, oidc, owner.account, testIssuer+"|"+owner.subject, "")
	explicit.subject = uuid.NewString()
	linkIdentity(t, s.Pool, oidc, explicit.account, testIssuer+"|"+explicit.subject, testIssuer)
	// Two verified subjects for one account, and one subject held by two accounts,
	// are ambiguous: none of these accounts sends an identity.
	twoSubjects.subject = uuid.NewString()
	linkIdentity(t, s.Pool, oidc, twoSubjects.account, testIssuer+"|"+twoSubjects.subject, "")
	linkIdentity(t, s.Pool, oidc, twoSubjects.account, testIssuer+"|"+uuid.NewString(), testIssuer)
	sharedA.subject = uuid.NewString()
	sharedB.subject = sharedA.subject
	linkIdentity(t, s.Pool, oidc, sharedA.account, testIssuer+"|"+sharedA.subject, "")
	linkIdentity(t, s.Pool, second, sharedB.account, testIssuer+"|"+sharedB.subject, testIssuer)

	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary, "PASTIME_NATIVE_FIXTURE_IDENTITY_ISSUER="+testIssuer)
	type pastimeAccount struct {
		SourceAccountID string
		UserID          int64
		Active, Owns    bool
		LinkStatus      string
		Claimed         bool
		Issuer, Subject string
		Receipts        int
	}
	accounts := func() map[int]pastimeAccount {
		t.Helper()
		var list []pastimeAccount
		if status, isJSON := run.call("GET", "/fixture/accounts", nil, &list); status != 200 || !isJSON {
			t.Fatalf("fixture accounts: %d", status)
		}
		out := map[int]pastimeAccount{}
		for _, a := range list {
			n, _ := strconv.Atoi(a.SourceAccountID)
			out[n] = a
		}
		return out
	}
	if status, isJSON := run.call("GET", "/fixture/accounts", nil, nil); status != 200 || !isJSON {
		run.finish()
		t.Skip("the Pastime fixture predates managed identity claims")
	}
	run.start()
	run.reconcile()

	repo := watchsync.NewPostgresRepository(s.Pool, s.Cipher)
	key := watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false)
	everyone := []person{owner, explicit, none, twoSubjects, sharedA, sharedB, later}
	// Every account converged: an identity, or its absence, holds no profile back.
	for _, q := range everyone {
		conn, found, e := repo.GetConnection(ctx, key, q.account, q.profile)
		if e != nil || !found || conn.AccessToken == "" || !conn.ScrobbleEnabled {
			t.Fatalf("account %d has no managed connection", q.account)
		}
	}
	want := func(q person, issuer, subject, status string, claimed bool) pastimeAccount {
		t.Helper()
		a, ok := accounts()[q.account]
		if !ok || !a.Owns || a.Issuer != issuer || a.Subject != subject || a.LinkStatus != status || a.Claimed != claimed {
			t.Fatalf("account %d in Pastime = %+v (present %v), want identity (%q, %q) %s claimed=%v", q.account, a, ok, issuer, subject, status, claimed)
		}
		return a
	}
	if got := accounts(); len(got) != len(everyone) {
		t.Fatalf("Pastime holds %d accounts, want %d", len(got), len(everyone))
	}
	want(owner, testIssuer, owner.subject, "pending", false)
	want(explicit, testIssuer, explicit.subject, "pending", false)
	for _, q := range []person{none, twoSubjects, sharedA, sharedB, later} {
		want(q, "", "", "pending", false)
	}

	media := &pluginv1.WatchSyncMedia{MediaItemId: "synthetic-media", MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "42"}}
	watched := func(q person) {
		t.Helper()
		history := uuid.NewString()
		if _, e := s.Pool.Exec(ctx, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,'synthetic-media',100,true)`, history, q.account, q.profile); e != nil {
			t.Fatal(e)
		}
		conn, found, e := repo.GetConnection(ctx, key, q.account, q.profile)
		if e != nil || !found {
			t.Fatal("managed connection absent", e)
		}
		provider, e := run.client.WatchSyncProvider("pastime")
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.WrapClient(id, provider).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{
			Context: &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: conn.AccessToken, SecretAttributes: conn.SecretAttributes}},
			Events:  []*pluginv1.WatchSyncEvent{{EventId: history, WatchHistoryId: history, Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: timestamppb.New(time.Now().Add(-time.Minute)), DurationSeconds: 100, Media: media}},
		})
		if e != nil || out.GetFault() != nil || len(out.GetResults()) != 1 || out.Results[0].GetFault() != nil {
			t.Fatalf("history export failed: %v %v", out, e)
		}
	}
	watched(owner)
	if got := accounts()[owner.account].Receipts; got != 1 {
		t.Fatalf("owner history in Pastime = %d, want 1", got)
	}

	type signedIn struct {
		Location string
		UserID   int64
	}
	signIn := func(subject string) signedIn {
		t.Helper()
		var out signedIn
		if status, isJSON := run.call("POST", "/fixture/sign-in", map[string]string{"Subject": subject}, &out); status != 200 || !isJSON {
			t.Fatalf("fixture sign-in: %d", status)
		}
		return out
	}
	managedUsers := func() map[int64]bool {
		out := map[int64]bool{}
		for _, a := range accounts() {
			out[a.UserID] = true
		}
		return out
	}
	// The owner's sign-in claims the managed account and lands in it, with the
	// history Bloem exported; the next sign-in goes straight in.
	ownerUser := accounts()[owner.account].UserID
	for range 2 {
		if got := signIn(owner.subject); got.Location != "/library" || got.UserID != ownerUser {
			t.Fatalf("owner sign-in = %+v, want user %d", got, ownerUser)
		}
		if a := want(owner, testIssuer, owner.subject, "linked", true); a.Receipts != 1 {
			t.Fatalf("claimed account history = %d, want 1", a.Receipts)
		}
	}
	// Ambiguous and unknown subjects match no managed account: the sign-in
	// creates a separate empty account and claims nothing.
	for _, subject := range []string{twoSubjects.subject, sharedA.subject, uuid.NewString()} {
		got := signIn(subject)
		if got.Location != "/library" || got.UserID == 0 || managedUsers()[got.UserID] {
			t.Fatalf("non-matching sign-in = %+v", got)
		}
	}
	for _, q := range []person{none, twoSubjects, sharedA, sharedB, later} {
		want(q, "", "", "pending", false)
	}
	// A matching sign-in for an account Bloem disabled is refused, not created.
	if _, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, explicit.account); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
	if a := want(explicit, testIssuer, explicit.subject, "pending", false); a.Active {
		t.Fatal("disabled account still active in Pastime")
	}
	if got := signIn(explicit.subject); got.UserID != 0 || !strings.Contains(got.Location, "error=identity_claim_conflict") {
		t.Fatalf("sign-in for a disabled account = %+v", got)
	}
	if _, err = s.Pool.Exec(ctx, `UPDATE users SET enabled=true WHERE id=$1`, explicit.account); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
	explicitUser := want(explicit, testIssuer, explicit.subject, "pending", false).UserID
	if got := signIn(explicit.subject); got.Location != "/library" || got.UserID != explicitUser {
		t.Fatalf("explicit-issuer sign-in = %+v, want user %d", got, explicitUser)
	}
	want(explicit, testIssuer, explicit.subject, "linked", true)
	// A link added after enrollment attaches the identity to the existing account.
	later.subject = uuid.NewString()
	linkIdentity(t, s.Pool, oidc, later.account, testIssuer+"|"+later.subject, "")
	run.reconcile()
	laterUser := want(later, testIssuer, later.subject, "pending", false).UserID
	if got := signIn(later.subject); got.Location != "/library" || got.UserID != laterUser {
		t.Fatalf("later sign-in = %+v, want user %d", got, laterUser)
	}
	want(later, testIssuer, later.subject, "linked", true)
	// Unlinking clears the identity Pastime holds; the claim and its sign-in stay,
	// and tracking continues under the re-issued credentials.
	if _, err = s.Pool.Exec(ctx, `DELETE FROM plugin_auth_identities WHERE id=$1`, ownerLink); err != nil {
		t.Fatal(err)
	}
	run.reconcile()
	want(owner, "", "", "linked", true)
	if got := signIn(owner.subject); got.Location != "/library" || got.UserID != ownerUser {
		t.Fatalf("owner sign-in after unlink = %+v", got)
	}
	watched(owner)
	if got := accounts()[owner.account].Receipts; got != 2 {
		t.Fatalf("owner history after unlink = %d, want 2", got)
	}
	want(none, "", "", "pending", false)
	if err = run.host.Stop(id); err != nil {
		t.Fatal(err)
	}
	run.finish()
}
