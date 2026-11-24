package bloeminsights

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemgeo"
)

var lifecycleBase = time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)

func startedEvent(session string, seq int64) LifecycleEvent {
	execNode := "4"
	return LifecycleEvent{
		SessionID: session, Seq: seq, At: lifecycleBase, State: "playing",
		Session: &LifecycleSession{
			UserID: "7", ProfileID: "p1", StartedAt: lifecycleBase,
			ClientName: "Bloem TV", ClientVersion: "2.1", DevicePlatform: "android_tv",
			PlayMethod: "transcode", EffectivePlayMethod: "transcode", VideoDecision: "transcode", AudioDecision: "copy",
			AdaptationReasons: []string{"video_codec_not_supported"}, ClientIP: "198.51.100.7",
			RoutingExecutionNodeID: &execNode, RoutingExecutionNodeName: "a310",
		},
	}
}

func stoppedEvent(session string, seq int64, at time.Time) LifecycleEvent {
	video, audio := "transcode", "copy"
	return LifecycleEvent{
		SessionID: session, Seq: seq, At: at, State: "stopped", EndReason: "stopped",
		History: &LifecycleHistory{
			UserID: "7", ProfileID: "p1", PlayMethod: "transcode",
			StartedAt: lifecycleBase, EndedAt: at, VideoDecision: &video, AudioDecision: &audio,
		},
	}
}

type recordRow struct {
	userID                        int
	profileID                     string
	endedAt                       *time.Time
	lastSeq                       int64
	clientName, videoDecision     string
	nodeID                        *int
	nodeName                      string
	viewerCity, viewerCountryCode string
	viewerLat                     *float64
	viewerTransport, viewerProto  string
	serverProto, edgeName, cfColo string
	transportChanged              bool
	adaptationReasons             []string
}

func readRecord(t *testing.T, store *Store, session string) (recordRow, bool) {
	t.Helper()
	var row recordRow
	err := store.pool.QueryRow(context.Background(), `
		SELECT user_id, profile_id, ended_at, last_seq, client_name, video_decision, node_id, node_name,
		       viewer_city, viewer_country_code, viewer_lat, viewer_transport, viewer_proto, server_proto,
		       edge_name, cf_colo, transport_changed, adaptation_reasons
		FROM bloem_stream_records WHERE session_id = $1`, session).Scan(
		&row.userID, &row.profileID, &row.endedAt, &row.lastSeq, &row.clientName, &row.videoDecision, &row.nodeID, &row.nodeName,
		&row.viewerCity, &row.viewerCountryCode, &row.viewerLat, &row.viewerTransport, &row.viewerProto, &row.serverProto,
		&row.edgeName, &row.cfColo, &row.transportChanged, &row.adaptationReasons)
	if err != nil {
		return recordRow{}, false
	}
	return row, true
}

func TestStoreStartThenStop(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	geo := &bloemgeo.Location{City: "Leiden", CountryCode: "NL", Country: "Netherlands", Latitude: 52.16, Longitude: 4.49}
	if err := store.ApplyLifecycle(ctx, startedEvent("s1", 1), geo, nil); err != nil {
		t.Fatal(err)
	}
	row, ok := readRecord(t, store, "s1")
	if !ok || row.userID != 7 || row.profileID != "p1" || row.endedAt != nil || row.clientName != "Bloem TV" {
		t.Fatalf("started row = %+v ok = %v", row, ok)
	}
	if row.nodeID == nil || *row.nodeID != 4 || row.nodeName != "a310" || row.viewerCity != "Leiden" || row.viewerCountryCode != "NL" {
		t.Fatalf("started row node/viewer = %+v", row)
	}
	summary := &FactsSummary{
		Latest:  RequestFacts{Proto: "HTTP/1.1", Edge: &EdgeHop{Name: "ams-1", Latitude: 52.37, Longitude: 4.89, ViewerProto: "h3"}, CFColo: "AMS"},
		Counts:  map[Transport]int64{TransportTCP: 2, TransportUDP: 9},
		Changed: true,
	}
	end := lifecycleBase.Add(42 * time.Minute)
	if err := store.ApplyLifecycle(ctx, stoppedEvent("s1", 5, end), nil, summary); err != nil {
		t.Fatal(err)
	}
	row, _ = readRecord(t, store, "s1")
	if row.endedAt == nil || !row.endedAt.Equal(end) || row.lastSeq != 5 || row.videoDecision != "transcode" {
		t.Fatalf("stopped row = %+v", row)
	}
	if row.viewerTransport != "udp" || !row.transportChanged || row.viewerProto != "h3" || row.serverProto != "HTTP/1.1" || row.edgeName != "ams-1" || row.cfColo != "AMS" {
		t.Fatalf("stopped transport = %+v, want the udp majority through ams-1", row)
	}
	if row.viewerCity != "Leiden" {
		t.Fatalf("a stop without a location cleared the viewer: %+v", row)
	}
	if len(row.adaptationReasons) != 1 || row.adaptationReasons[0] != "video_codec_not_supported" {
		t.Fatalf("adaptation reasons = %v", row.adaptationReasons)
	}
}

func TestStoreIgnoresOlderEvents(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	if err := store.ApplyLifecycle(ctx, startedEvent("s2", 1), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, stoppedEvent("s2", 5, lifecycleBase.Add(time.Hour)), nil, nil); err != nil {
		t.Fatal(err)
	}
	paused := startedEvent("s2", 3)
	paused.State = "paused"
	if err := store.ApplyLifecycle(ctx, paused, nil, nil); err != nil {
		t.Fatal(err)
	}
	row, _ := readRecord(t, store, "s2")
	if row.endedAt == nil || row.lastSeq != 5 {
		t.Fatalf("an older paused event reopened the stopped row: %+v", row)
	}
}

func TestStoreWithoutLocation(t *testing.T) {
	store := NewStore(testPool(t))
	if err := store.ApplyLifecycle(context.Background(), startedEvent("s3", 1), nil, nil); err != nil {
		t.Fatal(err)
	}
	row, _ := readRecord(t, store, "s3")
	if row.viewerLat != nil || row.viewerCity != "" || row.viewerTransport != "unknown" {
		t.Fatalf("row without a location = %+v, want empty viewer columns", row)
	}
}

func TestStorePrune(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	for _, id := range []string{"old", "recent", "live"} {
		if err := store.ApplyLifecycle(ctx, startedEvent(id, 1), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ApplyLifecycle(ctx, stoppedEvent("old", 2, lifecycleBase.Add(time.Hour)), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyLifecycle(ctx, stoppedEvent("recent", 2, lifecycleBase.Add(48*time.Hour)), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bloem_stream_records SET updated_at = $1 WHERE session_id = 'live'`, lifecycleBase.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bloem_limit_refusals (user_id, kind, scope, refused_at) VALUES (7, 'streams', 'person', $1), (7, 'streams', 'person', $2)`,
		lifecycleBase, lifecycleBase.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Prune(ctx, lifecycleBase.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("pruned %d rows, want the old record and the old refusal", deleted)
	}
	for id, want := range map[string]bool{"old": false, "recent": true, "live": true} {
		if _, ok := readRecord(t, store, id); ok != want {
			t.Fatalf("record %s kept = %v, want %v", id, ok, want)
		}
	}
}
