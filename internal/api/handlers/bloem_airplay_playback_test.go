package handlers

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// airPlayHandlerRequestV3 is an Apple-style player on the given output: H.264
// decode evidence, MP4 only, HLS as its remux delivery.
func airPlayHandlerRequestV3(sinkType, routeID string) playback.StartRequestV3 {
	req := v3HandlerStartRequest()
	req.QualityPreference = playback.QualityAutoV3
	req.Capabilities.CodecsAudio = []string{"aac", "ac3", "eac3"}
	req.Capabilities.VideoDecode[0].Levels = []int{51}
	req.Capabilities.VideoDecode[0].MaxBitrateKbps = 100_000
	req.ClientPlaybackContext.Output = playback.OutputContextV3{SinkType: sinkType, CurrentSink: "sink-screen", OutputContextID: routeID}
	req.ClientPlaybackContext.Deliveries[playback.DeliveryClassHLSV3] = playback.DeliveryCapabilityV3{
		Enabled: true, SupportedOnDevice: true, VideoCodecs: []string{"h264"}, AudioDecodeCodecs: []string{"aac", "ac3", "eac3"},
	}
	return req
}

// airPlayHandlerFileV3 is a 1080p H.264 Matroska remux at 31 Mbps with TrueHD
// 7.1: the file that played as an uncapped remux over AirPlay.
func airPlayHandlerFileV3() *models.MediaFile {
	return &models.MediaFile{
		ID: 42, ContentID: "movie-1", FilePath: "/media/movie.mkv", Container: "mkv", CodecVideo: "h264", CodecAudio: "truehd",
		Resolution: "1080p", Bitrate: 31_000, AudioChannels: 8, Duration: 7200,
		VideoTracks: []models.VideoTrack{{Codec: "h264", Profile: "high", Level: 41, Width: 1920, Height: 1080, FrameRate: "24000/1001", Bitrate: 27_000, BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR"}},
		AudioTracks: []models.AudioTrack{{Codec: "truehd", Channels: 8, Layout: "7.1", Language: "eng", Default: true}},
	}
}

// The handler plans every start and replan through planPlaybackWithCapabilitiesV3,
// so this is the path a route change takes: each pass plans from the output the
// request names.
func TestPlanPlaybackWithCapabilitiesV3FollowsTheActiveOutput(t *testing.T) {
	handler := NewPlaybackHandler(playback.NewSessionManager(0, 0))
	presetLocalRegistryV3(handler, playback.NewTransformationRegistryV3([]playback.TransformationSpecV3{
		{Name: playback.TransformationAudioToAACV3, Available: true},
		{Name: playback.TransformationVideoToH264V3, Available: true},
	}))
	file := airPlayHandlerFileV3()
	planning := planningV3{plannerRequestedFile: file, settings: playback.PlannerSettingsV3{TranscodeEnabled: true}}
	plan := func(sinkType, routeID string, attempted ...string) playback.PlannerResultV3 {
		t.Helper()
		planning := planning
		planning.attemptedKeys = attempted
		input := handler.plannerInputV3(context.Background(), planning, airPlayHandlerRequestV3(sinkType, routeID), file, 0)
		result, err := handler.planPlaybackWithCapabilitiesV3(context.Background(), input)
		if err != nil || result.Plan == nil {
			t.Fatalf("%s: err=%v result=%s", sinkType, err, playback.ExplainPlannerResultV3(result))
		}
		return result
	}

	device := plan("Speaker", "route-device")
	if device.Plan.Delivery != playback.DeliveryRemuxHLSV3 {
		t.Fatalf("device plan = %s, want the HLS remux", playback.ExplainPlannerResultV3(device))
	}

	// Device -> AirPlay: the same file is encoded within the AirPlay cap.
	cast := plan("AirPlay", "route-airplay", device.Plan.PlanAttemptKey)
	if cast.Plan.Delivery != playback.DeliveryTranscodeHLSV3 {
		t.Fatalf("AirPlay plan = %s, want an encode", playback.ExplainPlannerResultV3(cast))
	}
	if video := intValueV3(cast.Plan.EffectiveRecipe.BitrateKbps); video <= 0 || video > playback.AirPlayBitrateCapKbpsV3 {
		t.Fatalf("AirPlay video bitrate = %d, want within %d", video, playback.AirPlayBitrateCapKbpsV3)
	}

	// AirPlay -> device: the normal plan returns, though the AirPlay plan is
	// recorded as attempted.
	back := plan("Speaker", "route-device", cast.Plan.PlanAttemptKey)
	if back.Plan.Delivery != device.Plan.Delivery || back.Plan.PlanAttemptKey != device.Plan.PlanAttemptKey {
		t.Fatalf("returning to the device = %s, want %s", playback.ExplainPlannerResultV3(back), playback.ExplainPlannerResultV3(device))
	}
}

func intValueV3(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
