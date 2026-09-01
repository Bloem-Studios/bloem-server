package playback

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// A directly playable 1080p HEVC source on a client that decodes it; the
// baseline plan is direct so every override below is visible as a change.
func directPlayableOverrideFixtureV3(t *testing.T) (*models.MediaFile, StartRequestV3) {
	t.Helper()
	file := detailedFixtureFileV3()
	file.Resolution = "1080p"
	file.Bitrate = 12_000
	file.VideoTracks[0] = models.VideoTrack{Codec: "hevc", Profile: "Main 10", Level: 120, Width: 1920, Height: 1080, FrameRate: "24000/1001", Bitrate: 12_000, BitDepth: 10, VideoRange: "SDR", VideoRangeType: "SDR", ColorRange: "tv", ColorTransfer: "bt709"}
	req := validStartRequestV3()
	req.Capabilities.CodecsVideo = []string{"hevc", "h264"}
	req.Capabilities.VideoDecode = []VideoDecodeCapabilityV3{{
		Codec: "hevc", Profiles: []string{"main 10"}, Levels: []int{153}, BitDepths: []int{10},
		MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60, MaxBitrateKbps: 80_000, Hardware: true,
	}}
	direct := req.ClientPlaybackContext.Deliveries[DeliveryClassOriginalHTTPV3]
	direct.Containers = []string{"mkv"}
	direct.VideoCodecs = []string{"hevc"}
	direct.AudioDecodeCodecs = []string{"aac"}
	req.ClientPlaybackContext.Deliveries[DeliveryClassOriginalHTTPV3] = direct
	for _, delivery := range []string{DeliveryClassProgressiveV3, DeliveryClassHLSV3} {
		packaged := req.ClientPlaybackContext.Deliveries[delivery]
		packaged.VideoCodecs = []string{"h264"}
		packaged.AudioDecodeCodecs = []string{"aac"}
		req.ClientPlaybackContext.Deliveries[delivery] = packaged
	}
	return file, req
}

func planWithOverridesV3(file *models.MediaFile, req StartRequestV3, o PlanOverridesV3) PlannerResultV3 {
	return PlanPlaybackV3(PlannerInputV3{
		Request: ApplyPlanOverridesV3(req, o), RequestedFile: file, EffectiveFile: file, AudioTrackIndex: 0,
		Settings: PlannerSettingsV3{TranscodeEnabled: true, Allow4KTranscode: true}, Registry: testTransformationRegistryV3(),
		ForceTranscode: o.ForceTranscode(),
	})
}

func TestPlanOverridesV3(t *testing.T) {
	file, req := directPlayableOverrideFixtureV3(t)

	baseline := planWithOverridesV3(file, req, PlanOverridesV3{})
	if baseline.Plan == nil || baseline.Plan.Delivery != DeliveryOriginalHTTPV3 {
		t.Fatalf("baseline should direct play: %s", ExplainPlannerResultV3(baseline))
	}

	forced := planWithOverridesV3(file, req, PlanOverridesV3{Transcode: PlanOverrideTranscodeForce})
	if forced.Plan == nil || forced.Plan.Delivery != DeliveryTranscodeHLSV3 || forced.PlayMethod != PlayTranscode {
		t.Fatalf("transcode=force did not take the transcode route: %s", ExplainPlannerResultV3(forced))
	}
	if forced.Plan.DecisionReason != "admin_transcode_forced" {
		t.Fatalf("decision reason = %q", forced.Plan.DecisionReason)
	}

	capped := planWithOverridesV3(file, req, PlanOverridesV3{Transcode: PlanOverrideTranscodeAuto, MaxBitrateKbps: 4_000})
	if capped.Plan == nil || capped.Plan.Delivery != DeliveryTranscodeHLSV3 {
		t.Fatalf("a 12 Mbps source over a 4 Mbps cap must transcode: %s", ExplainPlannerResultV3(capped))
	}
	if capped.TargetBitrateKbps <= 0 || capped.TargetBitrateKbps > 4_000 {
		t.Fatalf("target bitrate %d does not honor the 4000 kbps cap", capped.TargetBitrateKbps)
	}
	if capped.Plan.EffectiveRecipe.BitrateKbps == nil || *capped.Plan.EffectiveRecipe.BitrateKbps > 4_000 {
		t.Fatalf("effective recipe bitrate %v exceeds the cap", capped.Plan.EffectiveRecipe.BitrateKbps)
	}

	// The cap never raises a tighter client cap.
	clientCap := 2_000
	tighter := req
	tighter.BandwidthCapKbps = &clientCap
	narrowed := ApplyPlanOverridesV3(tighter, PlanOverridesV3{MaxBitrateKbps: 4_000})
	if narrowed.BandwidthCapKbps == nil || *narrowed.BandwidthCapKbps != 2_000 {
		t.Fatalf("override raised the client's cap: %v", narrowed.BandwidthCapKbps)
	}

	// direct clears the cap the client sent and asks for original quality.
	direct := ApplyPlanOverridesV3(tighter, PlanOverridesV3{Transcode: PlanOverrideTranscodeDirect})
	if direct.BandwidthCapKbps != nil || direct.QualityPreference != QualityOriginalV3 {
		t.Fatalf("direct override kept cap/quality: %+v %q", direct.BandwidthCapKbps, direct.QualityPreference)
	}
	directPlan := planWithOverridesV3(file, tighter, PlanOverridesV3{Transcode: PlanOverrideTranscodeDirect})
	if directPlan.Plan == nil || directPlan.Plan.Delivery != DeliveryOriginalHTTPV3 {
		t.Fatalf("transcode=direct should lift the client cap: %s", ExplainPlannerResultV3(directPlan))
	}

	// A codec pin narrows what counts as directly playable.
	pinned := planWithOverridesV3(file, req, PlanOverridesV3{Transcode: PlanOverrideTranscodeAuto, VideoCodec: "h264"})
	if pinned.Plan == nil || pinned.Plan.Delivery == DeliveryOriginalHTTPV3 {
		t.Fatalf("video_codec=h264 pin still direct-played an HEVC source: %s", ExplainPlannerResultV3(pinned))
	}
	pinnedReq := ApplyPlanOverridesV3(req, PlanOverridesV3{VideoCodec: "h264", AudioCodec: "aac", Container: "mp4"})
	if len(pinnedReq.Capabilities.VideoDecode) != 0 || len(pinnedReq.Capabilities.CodecsVideo) != 1 || pinnedReq.Capabilities.CodecsVideo[0] != "h264" {
		t.Fatalf("video pin not applied: %+v", pinnedReq.Capabilities)
	}
	if len(req.Capabilities.VideoDecode) != 1 || len(req.Capabilities.CodecsVideo) != 2 {
		t.Fatalf("ApplyPlanOverridesV3 mutated its input: %+v", req.Capabilities)
	}
	for _, container := range pinnedReq.Capabilities.Containers {
		if container != "mp4" {
			t.Fatalf("container pin kept %q", container)
		}
	}
}
