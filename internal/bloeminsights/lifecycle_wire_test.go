package bloeminsights_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
)

// The observer decodes the lifecycle events the events hub carries into its
// own types (importing apiv2 would close an import cycle through the
// handlers). This pins that the two shapes agree on every field it reads.
func TestLifecycleEventDecodesTheAdminWireShape(t *testing.T) {
	at := time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)
	node, egress := apiv2.ID("4"), apiv2.ID("9")
	ip, client, version, platform, video, audio := "198.51.100.7", "Bloem TV", "2.1", "android_tv", "transcode", "copy"
	started := apiv2.AdminPlaybackLifecycleEvent{
		SessionID: "s1", Seq: 3, At: apiv2.Instant{Time: at}, State: "playing",
		Session: &apiv2.AdminPlaybackSession{
			SessionID: "s1", UserID: "7", ProfileID: "p1", StartedAt: apiv2.Instant{Time: at}, UpdatedAt: apiv2.Instant{Time: at},
			ClientIP: ip, ClientName: client, ClientVersion: version, DevicePlatform: platform,
			PlayMethod: "transcode", EffectivePlayMethod: "transcode", VideoDecision: video, AudioDecision: audio,
			AdaptationReasons: []string{"video_codec_not_supported"}, NodeDisplayName: "API",
			RoutingExecutionNodeID: &node, RoutingExecutionNodeName: "a310", RoutingEgressNodeID: &egress, RoutingEgressNodeName: "edge",
		},
	}
	raw, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	got, err := bloeminsights.DecodeLifecycleEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s1" || got.Seq != 3 || !got.At.Equal(at) || got.State != "playing" || got.Session == nil {
		t.Fatalf("event = %+v", got)
	}
	want := bloeminsights.LifecycleSession{UserID: "7", ProfileID: "p1", StartedAt: at, ClientIP: ip, ClientName: client,
		ClientVersion: version, DevicePlatform: platform, PlayMethod: "transcode", EffectivePlayMethod: "transcode",
		VideoDecision: video, AudioDecision: audio, AdaptationReasons: []string{"video_codec_not_supported"}, NodeDisplayName: "API",
		RoutingExecutionNodeID: stringPtr("4"), RoutingExecutionNodeName: "a310", RoutingEgressNodeID: stringPtr("9"), RoutingEgressNodeName: "edge"}
	if !reflect.DeepEqual(*got.Session, want) {
		t.Fatalf("session = %+v\nwant      %+v", *got.Session, want)
	}

	end := at.Add(time.Hour)
	stopped := apiv2.AdminPlaybackLifecycleEvent{
		SessionID: "s1", Seq: 4, At: apiv2.Instant{Time: end}, State: "stopped", EndReason: "stopped",
		History: &apiv2.AdminPlaybackHistoryEntry{
			SessionID: "s1", UserID: "7", ProfileID: "p1", PlayMethod: "transcode",
			StartedAt: apiv2.Instant{Time: at}, EndedAt: apiv2.Instant{Time: end},
			ClientIP: &ip, ClientName: &client, ClientVersion: &version, DevicePlatform: &platform,
			VideoDecision: &video, AudioDecision: &audio,
		},
	}
	raw, err = json.Marshal(stopped)
	if err != nil {
		t.Fatal(err)
	}
	got, err = bloeminsights.DecodeLifecycleEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	wantHistory := bloeminsights.LifecycleHistory{UserID: "7", ProfileID: "p1", PlayMethod: "transcode", StartedAt: at, EndedAt: end,
		ClientIP: &ip, ClientName: &client, ClientVersion: &version, DevicePlatform: &platform, VideoDecision: &video, AudioDecision: &audio}
	if got.History == nil || !reflect.DeepEqual(*got.History, wantHistory) {
		t.Fatalf("history = %+v", got.History)
	}
}

func stringPtr(s string) *string { return &s }
