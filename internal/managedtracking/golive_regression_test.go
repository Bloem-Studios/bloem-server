package managedtracking

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/watchsync"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// resolvingPlugin stands in for the out-of-process Pastime plugin: it resolves
// every received event through the host broker authority exactly like the
// real provider does, and records what it saw.
type resolvingPlugin struct {
	watchsync.WatchSyncPluginClient
	service      *Service
	installation int
	mu           sync.Mutex
	fail         map[pluginv1.WatchSyncOperation]int
	received     []*pluginv1.WatchSyncEvent
	resolved     map[string]EventAuthority
	errors       []error
	notify       chan struct{}
}

func newResolvingPlugin(s *Service, id int) *resolvingPlugin {
	return &resolvingPlugin{service: s, installation: id, fail: map[pluginv1.WatchSyncOperation]int{}, resolved: map[string]EventAuthority{}, notify: make(chan struct{}, 64)}
}

func (p *resolvingPlugin) ApplyEvents(ctx context.Context, r *pluginv1.WatchSyncApplyEventsRequest) (*pluginv1.WatchSyncApplyEventsResponse, error) {
	attrs := r.GetContext().GetCredentials().GetSecretAttributes()
	out := &pluginv1.WatchSyncApplyEventsResponse{}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, event := range r.GetEvents() {
		p.received = append(p.received, event)
		raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(event)
		digest := sha256.Sum256(raw)
		result := &pluginv1.WatchSyncApplyResult{EventId: event.GetEventId(), Status: pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED}
		authority, err := p.service.ResolveEvent(ctx, p.installation, attrs["instance_id"], attrs["account_id"], attrs["profile_id"], event.GetEventId(), digest[:])
		if err != nil {
			p.errors = append(p.errors, err)
			out.Fault = &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_PERMISSION_DENIED, SafeMessage: "denied"}
			return out, nil
		}
		p.resolved[event.GetEventId()] = authority
		if p.fail[event.GetOperation()] > 0 {
			p.fail[event.GetOperation()]--
			result.Status = pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_RETRY
			result.Fault = &pluginv1.WatchSyncFault{Code: pluginv1.WatchSyncFaultCode_WATCH_SYNC_FAULT_CODE_TEMPORARY, SafeMessage: "temporarily unavailable"}
		}
		out.Results = append(out.Results, result)
	}
	return out, nil
}

// notifyingClient signals every completed dispatch, whatever its outcome, so
// tests wait on observable delivery instead of sleeping.
type notifyingClient struct {
	watchsync.WatchSyncPluginClient
	notify chan struct{}
}

func (c notifyingClient) ApplyEvents(ctx context.Context, r *pluginv1.WatchSyncApplyEventsRequest) (*pluginv1.WatchSyncApplyEventsResponse, error) {
	defer func() { c.notify <- struct{}{} }()
	return c.WatchSyncPluginClient.ApplyEvents(ctx, r)
}

func (p *resolvingPlugin) wait(t *testing.T, calls int) {
	t.Helper()
	for i := 0; i < calls; i++ {
		select {
		case <-p.notify:
		case <-time.After(10 * time.Second):
			t.Fatalf("plugin call %d of %d never arrived", i+1, calls)
		}
	}
}

func (p *resolvingPlugin) snapshot() ([]*pluginv1.WatchSyncEvent, map[string]EventAuthority, []error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	resolved := make(map[string]EventAuthority, len(p.resolved))
	for k, v := range p.resolved {
		resolved[k] = v
	}
	return append([]*pluginv1.WatchSyncEvent(nil), p.received...), resolved, append([]error(nil), p.errors...)
}

type managedFixture struct {
	s       *Service
	id      int
	p       Profile
	info    Info
	e       Enrollment
	account int
	key     string
	repo    *watchsync.PostgresRepository
}

func enrolledFixture(t *testing.T) managedFixture {
	t.Helper()
	s, id, p := enrollmentFixture(t)
	ctx := t.Context()
	info, err := s.GetInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e := Enrollment{Profile: p, ProviderID: "pastime", DestinationAccountID: "10", DestinationProfileID: "20", Credential: "ptbs_fixture-" + uuid.NewString(), Active: true, Generation: info.Generation}
	e.ID = EnrollmentID(info.InstanceID, e)
	if err = s.EnsureConnection(ctx, id, e); err != nil {
		t.Fatal(err)
	}
	account, _ := strconv.Atoi(p.AccountID)
	return managedFixture{s: s, id: id, p: p, info: info, e: e, account: account, key: watchsync.PluginProviderKey(id, "bloem.pastime", "pastime", false), repo: watchsync.NewPostgresRepository(s.Pool, s.Cipher)}
}

func (f managedFixture) authContext() *pluginv1.WatchSyncAuthenticatedContext {
	return &pluginv1.WatchSyncAuthenticatedContext{CapabilityId: "pastime", Credentials: &pluginv1.WatchSyncCredentials{AccessToken: f.e.Credential, SecretAttributes: map[string]string{"instance_id": f.info.InstanceID, "account_id": f.p.AccountID, "profile_id": f.p.ProfileID}}}
}

func (f managedFixture) history(t *testing.T, id, media string, duration any) {
	t.Helper()
	if _, err := f.s.Pool.Exec(t.Context(), `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,$4,$5,true)`, id, f.account, f.p.ProfileID, media, duration); err != nil {
		t.Fatal(err)
	}
}

func movie(id string) *pluginv1.WatchSyncMedia {
	return &pluginv1.WatchSyncMedia{MediaItemId: id, MediaType: pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, ExternalIds: map[string]string{"tmdb": "42"}}
}

func markWatched(history, media string, duration float64) *pluginv1.WatchSyncEvent {
	return &pluginv1.WatchSyncEvent{EventId: history, WatchHistoryId: history, Operation: pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_MARK_WATCHED, OccurredAt: timestamppb.New(time.Now().Add(-time.Hour)), DurationSeconds: duration, Media: movie(media)}
}

func statusFor(t *testing.T, out *pluginv1.WatchSyncApplyEventsResponse, eventID string) pluginv1.WatchSyncApplyStatus {
	t.Helper()
	for _, r := range out.GetResults() {
		if r.GetEventId() == eventID {
			return r.GetStatus()
		}
	}
	t.Fatalf("no result for %q in %v", eventID, out)
	return 0
}

// PT-01: one unmatched or invalid event must not poison its whole batch.
func TestApplyEventsRejectsUnmatchedEventsIndividually(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	good, zero, missing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.history(t, good, "media-good", 100)
	f.history(t, zero, "media-zero", nil)
	plugin := newResolvingPlugin(f.s, f.id)
	out, err := f.s.WrapClient(f.id, plugin).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{
		markWatched(zero, "media-zero", 0),
		markWatched(good, "media-good", 100),
		markWatched(missing, "media-missing", 100),
	}})
	if err != nil || out.GetFault() != nil {
		t.Fatalf("one bad event failed the batch: %v %v", err, out.GetFault())
	}
	if statusFor(t, out, good) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatal("valid history event was not applied")
	}
	for _, id := range []string{zero, missing} {
		if statusFor(t, out, id) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED {
			t.Fatalf("invalid event %s was not rejected individually", id)
		}
	}
	received, resolved, errs := plugin.snapshot()
	if len(received) != 1 || len(resolved) != 1 || len(errs) != 0 {
		t.Fatalf("plugin saw %d events, resolved %d, errors %v", len(received), len(resolved), errs)
	}
	for _, a := range resolved {
		if a.Revision == 0 || a.ConsumptionID == "" || a.EntityID != stableTuple("media_", "media-good") {
			t.Fatalf("history authority incomplete: %+v", a)
		}
	}
}

func scrobbleService(t *testing.T, f managedFixture, plugin *resolvingPlugin) *watchsync.Service {
	t.Helper()
	provider, err := watchsync.NewPluginProvider(watchsync.PluginProviderOptions{
		InstallationID: f.id, ProviderKey: f.key, CapabilityID: "pastime", DisplayName: "Pastime",
		Descriptor: &pluginv1.WatchSyncProviderDescriptor{
			AuthMethods:         []pluginv1.WatchSyncAuthMethod{pluginv1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY},
			SupportedMediaTypes: []pluginv1.WatchSyncMediaType{pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_MOVIE, pluginv1.WatchSyncMediaType_WATCH_SYNC_MEDIA_TYPE_EPISODE},
			MaxBatchSize:        100, ScrobblePlayback: true, ExportWatched: true,
		},
		ResolveClient: func(context.Context, int, string) (watchsync.WatchSyncPluginClient, error) {
			return notifyingClient{WatchSyncPluginClient: f.s.WrapClient(f.id, plugin), notify: plugin.notify}, nil
		},
		Repository: f.repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := watchsync.NewRegistry()
	if err = registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	return watchsync.NewService(f.repo, registry)
}

// PT-02 and PT-03: the native playback path (ScrobbleStart, pause, resume and
// a completed non-confirmed ScrobbleStop) must reach the plugin with source
// authority, in source order, and a completed stop carries its consumption.
func TestNativeScrobbleFlowResumesAndCompletesThroughWatchsync(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	plugin := newResolvingPlugin(f.s, f.id)
	svc := scrobbleService(t, f, plugin)
	session := uuid.NewString()
	base := watchsync.ScrobbleEvent{PlaybackSessionID: session, UserID: f.account, ProfileID: f.p.ProfileID, MediaItemID: "media-native", Kind: "movie", TMDBID: "42", DurationSeconds: 100}
	at := func(position float64) watchsync.ScrobbleEvent {
		e := base
		e.PositionSeconds = position
		e.OccurredAt = time.Now().UTC()
		return e
	}
	if err := svc.ScrobbleStart(ctx, at(0)); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	if err := svc.ScrobblePause(ctx, at(40)); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	if err := svc.ScrobbleStart(ctx, at(40)); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	history := uuid.NewString()
	f.history(t, history, "media-native", 100)
	stop := at(98)
	stop.DurationSeconds = 101 // the stop reports the file runtime, not the start estimate
	stop.HistoryID = history
	stop.Completed = true
	if err := svc.ScrobbleStop(ctx, stop); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	received, resolved, errs := plugin.snapshot()
	if len(errs) != 0 || len(received) != 4 || len(resolved) != 4 {
		t.Fatalf("native flow rejected: received=%d resolved=%d errors=%v", len(received), len(resolved), errs)
	}
	var last uint64
	for i, event := range received {
		a := resolved[event.GetEventId()]
		if a.Revision <= last {
			t.Fatalf("event %d (%v) revision %d not after %d", i, event.GetOperation(), a.Revision, last)
		}
		last = a.Revision
	}
	completed := resolved[received[3].GetEventId()]
	if !received[3].GetCompleted() || completed.ConsumptionID != stableTuple("play_", f.p.AccountID, f.p.ProfileID, history) {
		t.Fatalf("completed stop lost its consumption: %+v", completed)
	}
	var open bool
	if err := f.s.Pool.QueryRow(ctx, `SELECT stop_sent_at IS NULL FROM watch_provider_scrobble_sessions WHERE playback_session_id=$1`, session).Scan(&open); err != nil || open {
		t.Fatalf("completed stop left the session open: %v %v", open, err)
	}
	// The history export of the same viewing is older than the live stop and
	// shares its stable consumption.
	out, err := f.s.WrapClient(f.id, plugin).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{markWatched(history, "media-native", 100)}})
	if err != nil || statusFor(t, out, history) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatalf("history export rejected: %v %v", out, err)
	}
	_, resolved, _ = plugin.snapshot()
	var exported EventAuthority
	for id, a := range resolved {
		if a.ConsumptionID == completed.ConsumptionID && id != received[3].GetEventId() {
			exported = a
		}
	}
	if exported.Revision == 0 || exported.Revision >= completed.Revision {
		t.Fatalf("history revision %d is not older than the live stop %d", exported.Revision, completed.Revision)
	}
}

// PT-03: a stop whose delivery failed is re-sent by the startup sweep with a
// fresh occurrence time; it must be accepted and close the session.
func TestSweptStopIsAcceptedAfterFailedDelivery(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	plugin := newResolvingPlugin(f.s, f.id)
	plugin.fail[pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP] = 1
	svc := scrobbleService(t, f, plugin)
	session := uuid.NewString()
	event := watchsync.ScrobbleEvent{PlaybackSessionID: session, UserID: f.account, ProfileID: f.p.ProfileID, MediaItemID: "media-swept", Kind: "movie", TMDBID: "42", DurationSeconds: 100, OccurredAt: time.Now().UTC()}
	if err := svc.ScrobbleStart(ctx, event); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	event.PositionSeconds = 55
	event.OccurredAt = time.Now().UTC()
	if err := svc.ScrobbleStop(ctx, event); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	if err := svc.SweepOpenScrobbles(ctx); err != nil {
		t.Fatal(err)
	}
	var open bool
	if err := f.s.Pool.QueryRow(ctx, `SELECT stop_sent_at IS NULL FROM watch_provider_scrobble_sessions WHERE playback_session_id=$1`, session).Scan(&open); err != nil || open {
		_, _, errs := plugin.snapshot()
		t.Fatalf("swept stop never closed the session: %v %v %v", open, err, errs)
	}
}

// PT-08: watch-history writes and event resolution must not serialize on one
// server-wide row.
func TestHistoryWritesAndResolutionDoNotWaitOnGlobalClock(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	holder, err := f.s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	history := uuid.NewString()
	if _, err = f.s.Pool.Exec(bounded, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,'media-lock',100,true)`, history, f.account, f.p.ProfileID); err != nil {
		t.Fatalf("history write waited on the global clock: %v", err)
	}
	event := markWatched(history, "media-lock", 100)
	plugin := newResolvingPlugin(f.s, f.id)
	out, err := f.s.WrapClient(f.id, plugin).ApplyEvents(bounded, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{event}})
	if err != nil || statusFor(t, out, history) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatalf("event recording or resolution waited on the global clock: %v %v", out, err)
	}
}

// PT-08: history of tenants without a managed grant produces no source rows.
func TestUngrantedTenantHistoryRecordsNoSources(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	other := uuid.NewString()
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO organizations(id,slug,name,status,owner_account_id) VALUES($1,$2,'Ungranted','active',$3)`, other, other, f.account); err != nil {
		t.Fatal(err)
	}
	var group int64
	if err := f.s.Pool.QueryRow(ctx, `INSERT INTO access_groups(name,organization_id,is_default) VALUES('Ungranted default',$1,true) RETURNING id`, other).Scan(&group); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role,access_group_id) VALUES($1,$2,'active','user',$3)`, other, f.account, group); err != nil {
		t.Fatal(err)
	}
	profile := uuid.NewString()
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id) VALUES($1,$2,'Elsewhere',$3,$4)`, profile, f.account, other, group); err != nil {
		t.Fatal(err)
	}
	history := uuid.NewString()
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO user_watch_history(id,user_id,profile_id,media_item_id,duration_seconds,completed) VALUES($1,$2,$3,'media-elsewhere',100,true)`, history, f.account, profile); err != nil {
		t.Fatal(err)
	}
	var sources int
	if err := f.s.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_managed_playback_sources WHERE history_id=$1`, history).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("ungranted history recorded %d sources: %v", sources, err)
	}
}

// PT-09: a history row moved to another media item exports for its current item.
func TestReattributedHistoryExportsForCurrentMediaItem(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	history := uuid.NewString()
	f.history(t, history, "media-before", 100)
	if _, err := f.s.Pool.Exec(ctx, `UPDATE user_watch_history SET media_item_id='media-after' WHERE id=$1`, history); err != nil {
		t.Fatal(err)
	}
	plugin := newResolvingPlugin(f.s, f.id)
	out, err := f.s.WrapClient(f.id, plugin).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{markWatched(history, "media-after", 100)}})
	if err != nil || statusFor(t, out, history) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatalf("reattributed history rejected: %v %v", out, err)
	}
	_, resolved, _ := plugin.snapshot()
	for _, a := range resolved {
		if a.EntityID != stableTuple("media_", "media-after") {
			t.Fatalf("entity follows the old item: %+v", a)
		}
	}
	stale, err := f.s.WrapClient(f.id, newResolvingPlugin(f.s, f.id)).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{markWatched(history, "media-before", 100)}})
	if err != nil || statusFor(t, stale, history) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_REJECTED {
		t.Fatalf("export for the old item accepted: %v %v", stale, err)
	}
}

// PT-06: a stale full-row UpsertConnection must not overwrite the managed
// credential, and a same-ID enrollment repairs a removed connection.
func TestManagedCredentialSurvivesStaleUpsertAndSameEnrollmentRepairs(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	conn, found, err := f.repo.GetConnection(ctx, f.key, f.account, f.p.ProfileID)
	if err != nil || !found {
		t.Fatal(err)
	}
	conn.AccessToken = "ptbs_stale-in-memory"
	conn.SecretAttributes = map[string]string{"instance_id": "forged"}
	conn.LastError = "sync finished"
	if _, err = f.repo.UpsertConnection(ctx, conn); err != nil {
		t.Fatalf("watchsync upsert of a managed row failed: %v", err)
	}
	conn, _, err = f.repo.GetConnection(ctx, f.key, f.account, f.p.ProfileID)
	if err != nil || conn.AccessToken != f.e.Credential || conn.SecretAttributes["instance_id"] != f.info.InstanceID || conn.LastError != "sync finished" {
		t.Fatalf("stale upsert replaced managed credentials: token-match=%v err=%v", conn.AccessToken == f.e.Credential, err)
	}
	if _, err = f.s.Pool.Exec(ctx, `DELETE FROM watch_provider_connections WHERE provider=$1 AND user_id=$2 AND profile_id=$3`, f.key, f.account, f.p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.EnsureConnection(ctx, f.id, f.e); err != nil {
		t.Fatal(err)
	}
	conn, found, err = f.repo.GetConnection(ctx, f.key, f.account, f.p.ProfileID)
	if err != nil || !found || conn.AccessToken != f.e.Credential || !conn.ScrobbleEnabled || !conn.ExportWatchedEnabled {
		t.Fatalf("same enrollment did not repair the connection: found=%v err=%v", found, err)
	}
}

// PT-14: renewal keeps current credentials usable until replacements exist,
// and the operator's read-only status never renews.
func TestRenewalKeepsCredentialsAndDescribeHasNoSideEffects(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	if _, err := f.s.Pool.Exec(ctx, `UPDATE bloem_managed_grants SET renewed_at=clock_timestamp()-interval '31 days' WHERE installation_id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	described, err := f.s.Describe(ctx, f.id)
	if err != nil || described.Generation != f.info.Generation {
		t.Fatalf("describe renewed or failed: %+v %v", described, err)
	}
	renewed, err := f.s.GetInfo(ctx, f.id)
	if err != nil || renewed.Generation != f.info.Generation+1 {
		t.Fatalf("renewal missing: %+v %v", renewed, err)
	}
	conn, found, err := f.repo.GetConnection(ctx, f.key, f.account, f.p.ProfileID)
	if err != nil || !found || conn.AccessToken != f.e.Credential || !conn.ScrobbleEnabled || !conn.ExportWatchedEnabled {
		t.Fatal("renewal wiped the connection before its replacement existed")
	}
	history := uuid.NewString()
	f.history(t, history, "media-renewal", 100)
	out, err := f.s.WrapClient(f.id, newResolvingPlugin(f.s, f.id)).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{markWatched(history, "media-renewal", 100)}})
	if err != nil || out.GetFault() != nil || statusFor(t, out, history) != pluginv1.WatchSyncApplyStatus_WATCH_SYNC_APPLY_STATUS_APPLIED {
		t.Fatalf("previous-generation enrollment lost authority on renewal: %v %v", out, err)
	}
	// Explicit rotation is a security action and still cuts old credentials.
	if err = f.s.Rotate(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.WrapClient(f.id, newResolvingPlugin(f.s, f.id)).ApplyEvents(ctx, &pluginv1.WatchSyncApplyEventsRequest{Context: f.authContext(), Events: []*pluginv1.WatchSyncEvent{markWatched(history, "media-renewal", 100)}}); !errors.Is(err, ErrAuthority) {
		t.Fatalf("rotation kept old credentials usable: %v", err)
	}
}

// PT-17: only granted bloem.pastime connections record scrobble sources.
func TestScrobbleSourcesIgnoreOtherPastimeCapabilities(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	var other int
	if err := f.s.Pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('third.party','test','fixture') RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	var connection string
	if err := f.s.Pool.QueryRow(ctx, `INSERT INTO watch_provider_connections(provider,user_id,profile_id,access_token) VALUES($1,$2,$3,'') RETURNING id::text`, watchsync.PluginProviderKey(other, "third.party", "pastime", false), f.account, f.p.ProfileID).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	session := uuid.NewString()
	if _, err := f.s.Pool.Exec(ctx, `INSERT INTO watch_provider_scrobble_sessions(playback_session_id,connection_id,media_item_id,last_action) VALUES($1,$2::uuid,'media-third','start')`, session, connection); err != nil {
		t.Fatal(err)
	}
	var sources int
	if err := f.s.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_managed_playback_sources WHERE session_id=$1`, session).Scan(&sources); err != nil || sources != 0 {
		t.Fatalf("foreign pastime capability recorded %d sources: %v", sources, err)
	}
}

// Inventory carries no email and only names Pastime accepts, so one profile
// cannot block a tenant's reconciliation.
func TestInventoryOmitsEmailAndNormalizesNames(t *testing.T) {
	s, pool, account, profile, install, _ := fixture(t)
	ctx := t.Context()
	long := "  Child\x07 with a very long name that keeps going well past the eighty rune limit of Pastime profiles  "
	if _, err := pool.Exec(ctx, `UPDATE user_profiles SET name=$1 WHERE id=$2`, long, profile); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListProfiles(ctx, install, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page.Profiles {
		if p.AccountID != strconv.Itoa(account) {
			continue
		}
		if p.Email != "" {
			t.Fatal("inventory exported an email address")
		}
		for _, name := range []string{p.Name, p.AccountName} {
			runes := []rune(name)
			if len(runes) == 0 || len(runes) > 80 || name[0] == ' ' || name[len(name)-1] == ' ' {
				t.Fatalf("name %q is not acceptable to Pastime", name)
			}
			for _, r := range runes {
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
					t.Fatalf("name %q carries a control character", name)
				}
			}
		}
	}
}

// PT-02: watch sync does not store the completed flag of a non-confirmed stop
// on its session, so the startup sweep re-sends it as a plain stop. The host
// recognizes the session's completed viewing and delivers the completion,
// instead of a near-end resume point for a title already watched.
func TestSweptCompletedStopKeepsItsCompletion(t *testing.T) {
	f := enrolledFixture(t)
	ctx := t.Context()
	plugin := newResolvingPlugin(f.s, f.id)
	plugin.fail[pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP] = 1
	svc := scrobbleService(t, f, plugin)
	session, history := uuid.NewString(), uuid.NewString()
	event := watchsync.ScrobbleEvent{PlaybackSessionID: session, UserID: f.account, ProfileID: f.p.ProfileID, MediaItemID: "media-sweep-done", Kind: "movie", TMDBID: "42", DurationSeconds: 100, OccurredAt: time.Now().UTC()}
	if err := svc.ScrobbleStart(ctx, event); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	f.history(t, history, "media-sweep-done", 100)
	event.PositionSeconds, event.HistoryID, event.Completed, event.OccurredAt = 97, history, true, time.Now().UTC()
	if err := svc.ScrobbleStop(ctx, event); err != nil {
		t.Fatal(err)
	}
	plugin.wait(t, 1)
	if err := svc.SweepOpenScrobbles(ctx); err != nil {
		t.Fatal(err)
	}
	received, resolved, errs := plugin.snapshot()
	if len(errs) != 0 || len(received) != 3 {
		t.Fatalf("sweep delivery failed: received=%d errors=%v", len(received), errs)
	}
	swept := received[2]
	a := resolved[swept.GetEventId()]
	if swept.GetOperation() != pluginv1.WatchSyncOperation_WATCH_SYNC_OPERATION_SCROBBLE_STOP || !swept.GetCompleted() || a.ConsumptionID != stableTuple("play_", f.p.AccountID, f.p.ProfileID, history) {
		t.Fatalf("swept stop lost its completion: completed=%v authority=%+v", swept.GetCompleted(), a)
	}
	if !swept.GetOccurredAt().AsTime().Equal(received[1].GetOccurredAt().AsTime()) {
		t.Fatal("two deliveries of one viewing carry different occurrence times")
	}
}
