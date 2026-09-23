package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type xtreamGuideDBFixture struct {
	service      *Service
	store        *PgStore
	tuner        *Tuner
	source       *GuideSource
	channels     []Channel
	feed, lineup string
	now          time.Time
}

func newXtreamGuideDBFixture(t *testing.T, pool *pgxpool.Pool) *xtreamGuideDBFixture {
	t.Helper()
	cipher, err := secret.New([]byte(strings.Repeat("guide-fixture-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	f := &xtreamGuideDBFixture{service: NewService(pool), store: NewPgStore(pool), now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), lineup: `[{"stream_id":2147483648,"name":"Fixture news","epg_channel_id":"station-a"}]`}
	f.service.SetXtreamCipher(cipher)
	f.service.now = func() time.Time { return f.now }
	_, _, entry := xtreamGuideFixture()
	f.feed = "<tv>" + entry + "</tv>"
	f.service.xtreamTransport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/xmltv.php" {
			return xtreamFixtureResponse(200, f.feed), nil
		}
		if r.URL.Query().Get("action") == "get_live_streams" {
			return xtreamFixtureResponse(200, f.lineup), nil
		}
		return xtreamFixtureResponse(200, `{"user_info":{"auth":1,"status":"Active","max_connections":1}}`), nil
	})
	f.tuner, err = f.service.AddTuner(t.Context(), AddTunerInput{Type: TunerTypeXtream, URL: "https://guide-" + uuid.NewString() + ".invalid", Username: "guide-account", Password: "guide-private-credential"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM livetv_guide_sources WHERE type='xtream' AND config_json->>'tuner_id'=$1`, f.tuner.ID); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM livetv_tuners WHERE id=$1`, f.tuner.ID); err != nil {
			t.Error(err)
		}
	})
	f.channels, err = f.store.ListChannels(t.Context(), f.tuner.ID)
	if err != nil || len(f.channels) != 1 {
		t.Fatalf("fixture channels: %v", err)
	}
	f.source, err = f.service.CreateGuideSource(t.Context(), &GuideSource{Type: GuideSourceXtream, Enabled: true, DisplayName: "Guide fixture", Config: map[string]string{"tuner_id": f.tuner.ID, "username": "not-public", "password": "not-public", "url": "http://metadata"}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *xtreamGuideDBFixture) programs(t *testing.T) []Program {
	t.Helper()
	programs, _, err := parseXtreamGuide(strings.NewReader(f.feed), f.source.ID, f.channels, f.now.Add(-6*time.Hour), f.now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return programs
}

func TestBloemXtreamGuideAtomicRefreshAndRecordingLinks(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	if len(f.source.Config) != 1 || f.source.Config["tuner_id"] != f.tuner.ID {
		t.Fatal("guide configuration retained credentials or arbitrary URLs")
	}
	wire, _ := json.Marshal(f.source)
	if strings.Contains(string(wire), "not-public") || strings.Contains(string(wire), "guide-private-credential") {
		t.Fatal("guide response exposed credentials")
	}
	if err := f.service.SyncGuideSource(t.Context(), f.source.ID); err != nil {
		t.Fatal(err)
	}
	original := f.programs(t)[0]
	recording, err := f.store.CreateRecording(t.Context(), &Recording{ProgramID: original.ID, ChannelID: original.ChannelID, Start: original.Start, Stop: original.Stop, Title: original.Title})
	if err != nil {
		t.Fatal(err)
	}
	f.feed = strings.ReplaceAll(f.feed, "News &amp; Weather", "Corrected news")
	if err := f.service.SyncGuideSource(t.Context(), f.source.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := f.store.GetProgram(t.Context(), original.ID)
	if err != nil || updated == nil || updated.Title != "Corrected news" {
		t.Fatalf("programme correction not published in place: %v", err)
	}
	gotRecording, err := f.store.GetRecording(t.Context(), recording.ID)
	if err != nil || gotRecording == nil || gotRecording.ProgramID != original.ID || !gotRecording.Start.Equal(recording.Start) || gotRecording.Title != recording.Title {
		t.Fatalf("refresh changed the captured recording/link: %v", err)
	}
	f.feed = strings.TrimSuffix(f.feed, "</tv>")
	if err := f.service.SyncGuideSource(t.Context(), f.source.ID); err == nil {
		t.Fatal("partial feed accepted")
	}
	unchanged, err := f.store.GetProgram(t.Context(), original.ID)
	if err != nil || unchanged == nil || unchanged.Title != "Corrected news" {
		t.Fatal("failed refresh erased the working guide")
	}
	status, err := f.store.GetGuideSource(t.Context(), f.source.ID)
	if err != nil || status == nil || status.Status != "error" || status.LastSyncAt == nil {
		t.Fatal("failed refresh lost the previous success or failed to publish its error state")
	}
	f.lineup = `[]`
	if err := f.service.ScanTuner(t.Context(), f.tuner.ID); err == nil {
		t.Fatal("empty rescan should preserve the lineup and report failure")
	}
	f.lineup = `[{"stream_id":2,"name":"Other station"}]`
	if err := f.service.ScanTuner(t.Context(), f.tuner.ID); err != nil {
		t.Fatal(err)
	}
	channels, err := f.store.ListChannels(t.Context(), f.tuner.ID)
	if err != nil || len(channels) != 2 {
		t.Fatalf("missing channel was deleted instead of retained: %v", err)
	}
	for _, channel := range channels {
		if channel.ID == original.ChannelID && channel.Enabled {
			t.Fatal("missing provider channel remained enabled")
		}
	}
	gotRecording, err = f.store.GetRecording(t.Context(), recording.ID)
	if err != nil || gotRecording == nil || gotRecording.ProgramID != original.ID {
		t.Fatal("rescan cascaded away DVR history")
	}
	if err := f.service.DeleteTuner(t.Context(), f.tuner.ID); err != nil {
		t.Fatal(err)
	}
	if source, err := f.store.GetGuideSource(t.Context(), f.source.ID); err != nil || source != nil {
		t.Fatal("provider removal left an orphaned guide")
	}
}

func TestBloemXtreamGuideClaimsFencePublicationAndConfigurationABA(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	version, err := f.store.claimXtreamGuideSync(t.Context(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.claimXtreamGuideSync(t.Context(), f.source); err == nil {
		t.Fatal("a second refresh was admitted")
	}
	// Even restoring the same configuration invalidates the old MVCC owner.
	if _, err := pool.Exec(t.Context(), `UPDATE livetv_guide_sources SET config_json=jsonb_build_object('tuner_id','temporary-other-provider') WHERE id=$1`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE livetv_guide_sources SET config_json=jsonb_build_object('tuner_id',$2::text) WHERE id=$1`, f.source.ID, f.tuner.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, version, f.channels, f.programs(t), f.now, f.now.Add(48*time.Hour)); err == nil {
		t.Fatal("stale refresh published after a configuration edit")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE livetv_guide_sources SET status='syncing',updated_at=clock_timestamp()-interval '4 minutes' WHERE id=$1`, f.source.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.store.claimXtreamGuideSync(t.Context(), f.source)
	if err != nil || fresh == version {
		t.Fatalf("expired source claim was not replaceable: %v", err)
	}
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, fresh, f.channels, f.programs(t), f.now, f.now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, version, f.channels, f.programs(t), f.now, f.now.Add(48*time.Hour)); err == nil {
		t.Fatal("old owner overwrote the replacement's publication")
	}
}

func TestBloemXtreamGuideRejectsChangedChannelMappingAndForeignIdentity(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	other := newXtreamGuideDBFixture(t, pool)
	if err := other.service.SyncGuideSource(t.Context(), other.source.ID); err != nil {
		t.Fatal(err)
	}
	version, err := f.store.claimXtreamGuideSync(t.Context(), f.source)
	if err != nil {
		t.Fatal(err)
	}
	programs := f.programs(t)
	if _, err := pool.Exec(t.Context(), `UPDATE livetv_channels SET guide_station_id='replacement-station' WHERE id=$1`, f.channels[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, version, f.channels, programs, f.now, f.now.Add(48*time.Hour)); err == nil {
		t.Fatal("stale channel mapping accepted")
	}
	if _, err := pool.Exec(t.Context(), `UPDATE livetv_channels SET guide_station_id='station-a' WHERE id=$1`, f.channels[0].ID); err != nil {
		t.Fatal(err)
	}
	foreign := other.programs(t)[0]
	programs[0].ID = foreign.ID
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, version, f.channels, programs, f.now, f.now.Add(48*time.Hour)); err == nil || !strings.Contains(err.Error(), "programme identity") {
		t.Fatalf("foreign programme identity was not fenced: %v", err)
	}
	retained, err := other.store.GetProgram(t.Context(), foreign.ID)
	if err != nil || retained == nil || retained.SourceID != other.source.ID {
		t.Fatal("identity conflict damaged the other source")
	}
	programs = f.programs(t)
	programs[0].ChannelID = other.channels[0].ID
	if err := f.store.replaceXtreamPrograms(t.Context(), f.source, version, other.channels, programs, f.now, f.now.Add(48*time.Hour)); err == nil {
		t.Fatal("programme was published on another provider's channel")
	}
}

func TestBloemXtreamPostgresCreationRequiresCipherBeforeNetwork(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	s := NewService(pool)
	s.xtreamTransport = xtreamRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("unconfigured request contacted the provider")
		return nil, errors.New("unexpected provider request")
	})
	if _, err := s.AddTuner(t.Context(), AddTunerInput{Type: TunerTypeXtream, URL: "https://cipher-fixture.invalid", Username: "fixture", Password: "fixture"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing cipher = %v", err)
	}
}

func TestBloemXtreamGuideLateFailureDoesNotOverwriteNewConfiguration(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.service.xtreamTransport = xtreamRoundTrip(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-release:
			return xtreamFixtureResponse(200, "<tv>"), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	done := make(chan error, 1)
	go func() { done <- f.service.SyncGuideSource(t.Context(), f.source.ID) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("guide request did not start")
	}
	replacement := *f.source
	replacement.DisplayName, replacement.Status = "New configuration", "ready"
	if _, err := f.store.UpdateGuideSource(t.Context(), &replacement); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("malformed feed accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guide request did not finish")
	}
	got, err := f.store.GetGuideSource(t.Context(), f.source.ID)
	if err != nil || got == nil || got.DisplayName != replacement.DisplayName || got.Status != "ready" || got.LastError != "" {
		t.Fatalf("old failure overwrote the new source state: %v", err)
	}
}

func TestBloemXtreamGuideAdmissionAcrossPools(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	var existing int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM livetv_guide_sources WHERE enabled`).Scan(&existing); err != nil || existing != 1 {
		t.Fatal("guide admission fixture requires no unrelated enabled sources")
	}
	otherPool, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer otherPool.Close()
	other := NewPgStore(otherPool)
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, `DELETE FROM livetv_guide_sources WHERE id=ANY($1::text[])`, ids); err != nil {
			t.Error(err)
		}
	})
	start, results := make(chan struct{}), make(chan error, len(ids))
	for i, id := range ids {
		store := f.store
		if i%2 != 0 {
			store = other
		}
		go func() {
			<-start
			_, err := store.CreateGuideSource(t.Context(), &GuideSource{ID: id, Type: GuideSourceXMLSync, Enabled: true, Config: map[string]string{}})
			results <- err
		}()
	}
	close(start)
	accepted := 0
	for range ids {
		select {
		case err := <-results:
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrLimitExceeded) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cross-pool guide admission stalled")
		}
	}
	if accepted != MaxGuideSources-1 {
		t.Fatalf("admitted %d guides into %d remaining slots", accepted, MaxGuideSources-1)
	}
	if _, err := other.CreateGuideSource(t.Context(), &GuideSource{Type: GuideSourceXtream, Enabled: false, Config: map[string]string{"tuner_id": f.tuner.ID}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate provider guide accepted: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM livetv_guide_sources WHERE id=ANY($1::text[])`, ids); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteGuideSource(t.Context(), f.source.ID); err != nil {
		t.Fatal(err)
	}
	start, results = make(chan struct{}), make(chan error, 2)
	for _, store := range []*PgStore{f.store, other} {
		go func() {
			<-start
			_, err := store.CreateGuideSource(t.Context(), &GuideSource{Type: GuideSourceXtream, Enabled: true, Config: map[string]string{"tuner_id": f.tuner.ID}})
			results <- err
		}()
	}
	close(start)
	accepted = 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrInvalidArgument) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("duplicate provider admission stalled")
		}
	}
	if accepted != 1 {
		t.Fatalf("same provider acquired %d guide sources", accepted)
	}
}

// One bad programme must not keep the whole guide stale; a feed with nothing
// usable fails with counts only, never provider data.
func TestBloemXtreamGuideSyncSkipsInvalidProgrammes(t *testing.T) {
	pool := bloemXtreamTestPool(t)
	f := newXtreamGuideDBFixture(t, pool)
	_, _, entry := xtreamGuideFixture()
	zeroLength := strings.ReplaceAll(strings.ReplaceAll(entry, "20260919140000 +0200", "20260919130000 +0200"), "20260919150000 +0200", "20260919130000 +0200")
	badTitle := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(entry, "20260919140000 +0200", "20260919160000 +0200"), "20260919150000 +0200", "20260919170000 +0200"), "News &amp; Weather", "private-marker"+strings.Repeat("x", 1025))
	f.feed = "<tv>" + zeroLength + entry + badTitle + "</tv>"
	if err := f.service.SyncGuideSource(t.Context(), f.source.ID); err != nil {
		t.Fatalf("messy feed failed the sync: %v", err)
	}
	status, err := f.store.GetGuideSource(t.Context(), f.source.ID)
	if err != nil || status == nil || status.Status != "ready" {
		t.Fatalf("guide not published: %+v %v", status, err)
	}
	if programs := f.programs(t); len(programs) != 1 {
		t.Fatalf("published %d programmes, want 1", len(programs))
	} else if got, err := f.store.GetProgram(t.Context(), programs[0].ID); err != nil || got == nil {
		t.Fatalf("valid programme not stored: %v", err)
	}

	f.feed = "<tv>" + zeroLength + badTitle + "</tv>"
	err = f.service.SyncGuideSource(t.Context(), f.source.ID)
	if err == nil || !strings.Contains(err.Error(), "1 invalid duration, 1 oversized metadata") || strings.Contains(err.Error(), "private-marker") {
		t.Fatalf("all-invalid feed error = %v", err)
	}
}
