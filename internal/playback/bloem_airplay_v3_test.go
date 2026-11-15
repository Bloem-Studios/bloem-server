package playback

import (
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

const (
	airPlaySinkV3     = "AirPlay"
	airPlayRouteV3    = "route-airplay"
	deviceRouteV3     = "route-device"
	airPlayHeavyKbps  = 31_000
	airPlayEpisodeBPS = 4_500
)

// airPlayClientV3 is an Apple-style player: attested H.264 decode, AAC with
// AC-3 and E-AC-3 decode, MP4 only and HLS as its only remux delivery, so a
// Matroska source needs an HLS remux or an encode.
func airPlayClientV3(sinkType, routeID string) StartRequestV3 {
	req := validStartRequestV3()
	req.QualityPreference = QualityAutoV3
	req.Capabilities.CodecsVideo = []string{"h264"}
	req.Capabilities.CodecsVideoHardware = []string{"h264"}
	req.Capabilities.CodecsAudio = []string{"aac", "ac3", "eac3"}
	req.Capabilities.Containers = []string{"mp4"}
	req.Capabilities.MaxResolution = "2160p"
	req.Capabilities.VideoDecode = []VideoDecodeCapabilityV3{{Codec: "h264", Profiles: []string{"high"}, Levels: []int{51}, BitDepths: []int{8}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60, MaxBitrateKbps: 100_000, Hardware: true}}
	req.ClientPlaybackContext.Device = DeviceContextV3{Platform: "ios", OSVersion: "26"}
	req.ClientPlaybackContext.Output = OutputContextV3{SinkType: sinkType, CurrentSink: "sink-screen", OutputContextID: routeID}
	req.ClientPlaybackContext.Deliveries[DeliveryClassProgressiveV3] = DeliveryCapabilityV3{}
	for class, capability := range req.ClientPlaybackContext.Deliveries {
		if capability.Enabled {
			capability.AudioDecodeCodecs = []string{"aac", "ac3", "eac3"}
			req.ClientPlaybackContext.Deliveries[class] = capability
		}
	}
	return req
}

// airPlayFileV3 is a 1080p H.264 Matroska file with one audio track.
func airPlayFileV3(totalKbps int, audio models.AudioTrack) *models.MediaFile {
	return &models.MediaFile{
		ID: 42, FilePath: "/media/movie.mkv", Container: "mkv", CodecVideo: "h264", CodecAudio: audio.Codec,
		Resolution: "1080p", Bitrate: totalKbps, AudioChannels: audio.Channels,
		VideoTracks: []models.VideoTrack{{Codec: "h264", Profile: "High", Level: 41, Width: 1920, Height: 1080, FrameRate: "24000/1001", Bitrate: totalKbps - 4_000, BitDepth: 8, VideoRange: "SDR", VideoRangeType: "SDR", ColorRange: "tv"}},
		AudioTracks: []models.AudioTrack{audio},
	}
}

var airPlayPlannerSettingsV3 = PlannerSettingsV3{TranscodeEnabled: true, Allow4KTranscode: true}

var (
	trueHD71 = models.AudioTrack{Codec: "truehd", Channels: 8, Layout: "7.1", Language: "eng"}
	dts51    = models.AudioTrack{Codec: "dts", Channels: 6, Layout: "5.1", Language: "eng"}
	ac351    = models.AudioTrack{Codec: "ac3", Channels: 6, Layout: "5.1", Language: "eng"}
	aacSt    = models.AudioTrack{Codec: "aac", Channels: 2, Layout: "stereo", Language: "eng"}
)

// planAirPlayV3 plans file for req the way the handler does, through
// PlanPlaybackAirPlayAwareV3.
func planAirPlayV3(t *testing.T, req StartRequestV3, file *models.MediaFile, attempted ...string) PlannerResultV3 {
	t.Helper()
	result := planAirPlayWithV3(req, file, airPlayPlannerSettingsV3, testTransformationRegistryV3(), attempted...)
	if result.Plan == nil {
		t.Fatalf("no plan: %s", ExplainPlannerResultV3(result))
	}
	return result
}

func planAirPlayWithV3(req StartRequestV3, file *models.MediaFile, settings PlannerSettingsV3, registry *TransformationRegistryV3, attempted ...string) PlannerResultV3 {
	return PlanPlaybackAirPlayAwareV3(PlannerInputV3{
		Request: req, RequestedFile: file, EffectiveFile: file, AudioTrackIndex: 0,
		Settings: settings, Registry: registry, AttemptedKeys: attempted,
	})
}

// sameShapeV3 compares what a client would execute and what it is told about
// it, ignoring ids that depend on the output route.
func sameShapeV3(t *testing.T, got, want PlannerResultV3, what string) {
	t.Helper()
	if !sameRouteV3(got, want) || !reflect.DeepEqual(got.Plan.DegradationWarnings, want.Plan.DegradationWarnings) {
		t.Fatalf("%s changed the plan:\n got  %s\n want %s", what, ExplainPlannerResultV3(got), ExplainPlannerResultV3(want))
	}
}

// sameRouteV3 compares the route and recipe a plan executes, not the warnings
// that explain it.
func sameRouteV3(got, want PlannerResultV3) bool {
	g, w := got.Plan, want.Plan
	return g.Delivery == w.Delivery && g.DecisionReason == w.DecisionReason &&
		reflect.DeepEqual(g.EffectiveRecipe, w.EffectiveRecipe) &&
		reflect.DeepEqual(g.Transformations, w.Transformations) &&
		got.PlayMethod == want.PlayMethod && got.TranscodeAudio == want.TranscodeAudio &&
		got.TargetAudioCodec == want.TargetAudioCodec && got.TargetAudioChannels == want.TargetAudioChannels
}

func TestAirPlayAwareRequestV3OnlyAirPlayChangesTheRequest(t *testing.T) {
	for _, sink := range []string{"", "Speaker", "Bluetooth", "HDMI", "AirPlay2", "Cast", "Wired"} {
		req := airPlayClientV3(sink, deviceRouteV3)
		if got := AirPlayAwareRequestV3(req); !reflect.DeepEqual(got, req) {
			t.Fatalf("sink %q changed the request", sink)
		}
	}
	for _, sink := range []string{"AirPlay", "airplay", " AirPlay "} {
		got := AirPlayAwareRequestV3(airPlayClientV3(sink, airPlayRouteV3))
		if optionalValueV3(got.BandwidthCapKbps) != AirPlayBitrateCapKbpsV3 {
			t.Fatalf("sink %q cap = %d, want %d", sink, optionalValueV3(got.BandwidthCapKbps), AirPlayBitrateCapKbpsV3)
		}
	}
}

func TestAirPlayAwareRequestV3NeverRaisesOrMutatesTheClientCap(t *testing.T) {
	for _, test := range []struct{ client, want int }{
		{0, AirPlayBitrateCapKbpsV3},
		{50_000, AirPlayBitrateCapKbpsV3},
		{AirPlayBitrateCapKbpsV3, AirPlayBitrateCapKbpsV3},
		{6_000, 6_000},
		{800, 800},
	} {
		req := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
		if test.client > 0 {
			client := test.client
			req.BandwidthCapKbps = &client
		}
		got := AirPlayAwareRequestV3(req)
		if optionalValueV3(got.BandwidthCapKbps) != test.want {
			t.Fatalf("client cap %d -> %d, want %d", test.client, optionalValueV3(got.BandwidthCapKbps), test.want)
		}
		// The durable request keeps what the client sent.
		if optionalValueV3(req.BandwidthCapKbps) != test.client {
			t.Fatalf("client cap %d was rewritten to %d", test.client, optionalValueV3(req.BandwidthCapKbps))
		}
	}
}

// The case that failed in support: a Blu-ray-class remux with lossless or DTS
// surround, planned as a 31 Mbps remux for the local player.
func TestPlanPlaybackV3AirPlayCapsHeavyRemux(t *testing.T) {
	for _, test := range []struct {
		name         string
		audio        models.AudioTrack
		wantChannels int
	}{
		{"TrueHD 7.1 keeps surround as 5.1", trueHD71, 6},
		{"DTS 5.1 keeps surround", dts51, 6},
		{"AC-3 5.1 keeps surround", ac351, 6},
		{"AAC stereo stays stereo", aacSt, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := airPlayFileV3(airPlayHeavyKbps, test.audio)

			local := planAirPlayV3(t, airPlayClientV3("Speaker", deviceRouteV3), file)
			if local.Plan.Delivery != DeliveryRemuxHLSV3 || local.PlayMethod != PlayRemux {
				t.Fatalf("baseline is not the heavy HLS remux: %s", ExplainPlannerResultV3(local))
			}

			cast := planAirPlayV3(t, airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file)
			plan := cast.Plan
			if plan.Delivery != DeliveryTranscodeHLSV3 || cast.PlayMethod != PlayTranscode {
				t.Fatalf("AirPlay plan is not an encode: %s", ExplainPlannerResultV3(cast))
			}
			if video := optionalValueV3(plan.EffectiveRecipe.BitrateKbps); video <= 0 || video+cast.TargetAudioBitrateKbps > AirPlayBitrateCapKbpsV3 {
				t.Fatalf("stream %d + %d kbps audio exceeds the %d kbps cap", video, cast.TargetAudioBitrateKbps, AirPlayBitrateCapKbpsV3)
			}
			if h := optionalValueV3(plan.EffectiveRecipe.Height); h > 1080 {
				t.Fatalf("height = %d, want at most 1080", h)
			}
			// The client is told why the quality is lower than the source's.
			capWarned := false
			for _, warning := range plan.DegradationWarnings {
				capWarned = capWarned || warning.Code == "bandwidth_cap_applied"
			}
			if !capWarned {
				t.Fatalf("no bandwidth_cap_applied warning: %#v", plan.DegradationWarnings)
			}
			// Audio. AAC is the only conversion the plan contract names today, so
			// surround keeps its 5.1 layout and stereo stays stereo; an AC-3 or
			// E-AC-3 encode needs a transformation of its own.
			if cast.TargetAudioCodec != audioCodecAACV3 || cast.TargetAudioChannels != test.wantChannels {
				t.Fatalf("audio = %s x%d, want aac x%d", cast.TargetAudioCodec, cast.TargetAudioChannels, test.wantChannels)
			}
		})
	}
}

func TestPlanPlaybackV3AirPlayScalesFourKDownToTheCap(t *testing.T) {
	file := airPlayFileV3(60_000, ac351)
	file.Resolution = "2160p"
	file.VideoTracks[0].Width, file.VideoTracks[0].Height = 3840, 2160
	cast := planAirPlayV3(t, airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file)
	if cast.Plan.Delivery != DeliveryTranscodeHLSV3 {
		t.Fatalf("4K source: %s", ExplainPlannerResultV3(cast))
	}
	if h := optionalValueV3(cast.Plan.EffectiveRecipe.Height); h <= 0 || h > 1080 {
		t.Fatalf("4K source encoded at %dp, want 1080p or lower", h)
	}
	if bitrate := optionalValueV3(cast.Plan.EffectiveRecipe.BitrateKbps); bitrate > AirPlayBitrateCapKbpsV3 {
		t.Fatalf("encode bitrate %d exceeds the cap", bitrate)
	}
}

// Within the cap nothing is transcoded: the plan is the one the local player
// gets, including a copied AC-3 track.
func TestPlanPlaybackV3AirPlayLeavesSourcesWithinTheCapAlone(t *testing.T) {
	for _, test := range []struct {
		name  string
		kbps  int
		audio models.AudioTrack
	}{
		{"episode with AAC stereo", airPlayEpisodeBPS, aacSt},
		{"movie with AC-3 5.1 under the cap", 8_000, ac351},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := airPlayFileV3(test.kbps, test.audio)
			local := planAirPlayV3(t, airPlayClientV3("Speaker", deviceRouteV3), file)
			cast := planAirPlayV3(t, airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file)
			sameShapeV3(t, cast, local, "AirPlay")
			if cast.Plan.Delivery == DeliveryTranscodeHLSV3 {
				t.Fatalf("a %d kbps source was transcoded: %s", test.kbps, ExplainPlannerResultV3(cast))
			}
			if len(cast.Plan.Transformations) != len(local.Plan.Transformations) {
				t.Fatalf("transformations %v, want %v", cast.Plan.Transformations, local.Plan.Transformations)
			}
		})
	}
}

// Every other output plans exactly as before, whatever the sink says.
func TestPlanPlaybackV3NonAirPlayOutputsPlanAsBefore(t *testing.T) {
	file := airPlayFileV3(airPlayHeavyKbps, trueHD71)
	baseline := PlanPlaybackV3(PlannerInputV3{
		Request: airPlayClientV3("", deviceRouteV3), RequestedFile: file, EffectiveFile: file,
		Settings: airPlayPlannerSettingsV3, Registry: testTransformationRegistryV3(),
	})
	if baseline.Plan == nil {
		t.Fatalf("baseline: %s", ExplainPlannerResultV3(baseline))
	}
	for _, sink := range []string{"Speaker", "Bluetooth", "HDMI", "Cast", "AirPlay2"} {
		got := planAirPlayV3(t, airPlayClientV3(sink, deviceRouteV3), file)
		sameShapeV3(t, got, baseline, "sink "+sink)
		if got.Plan.PlanAttemptKey != baseline.Plan.PlanAttemptKey {
			t.Fatalf("sink %s changed the plan attempt key", sink)
		}
	}
}

// The client's own maximum is a ceiling the plan never passes, above or below
// the AirPlay cap.
func TestPlanPlaybackV3AirPlayHonorsALowerClientMaximum(t *testing.T) {
	for _, clientKbps := range []int{6_000, 3_000, 2_000} {
		client := clientKbps
		req := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
		req.BandwidthCapKbps = &client
		cast := planAirPlayV3(t, req, airPlayFileV3(airPlayHeavyKbps, trueHD71))
		if cast.Plan.Delivery != DeliveryTranscodeHLSV3 {
			t.Fatalf("client cap %d: %s", clientKbps, ExplainPlannerResultV3(cast))
		}
		if video := optionalValueV3(cast.Plan.EffectiveRecipe.BitrateKbps); video+cast.TargetAudioBitrateKbps > clientKbps {
			t.Fatalf("client cap %d kbps: planned %d video + %d audio", clientKbps, video, cast.TargetAudioBitrateKbps)
		}
		// Against the same cap on a non-AirPlay output the plan is the same.
		localReq := airPlayClientV3("Speaker", deviceRouteV3)
		localReq.BandwidthCapKbps = &client
		sameShapeV3(t, cast, planAirPlayV3(t, localReq, airPlayFileV3(airPlayHeavyKbps, trueHD71)), "AirPlay under a lower client cap")
	}

	// A higher client maximum does not lift the AirPlay cap.
	high := 80_000
	req := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
	req.BandwidthCapKbps = &high
	cast := planAirPlayV3(t, req, airPlayFileV3(airPlayHeavyKbps, trueHD71))
	if video := optionalValueV3(cast.Plan.EffectiveRecipe.BitrateKbps); video+cast.TargetAudioBitrateKbps > AirPlayBitrateCapKbpsV3 {
		t.Fatalf("a client cap of %d lifted the AirPlay cap: %d + %d", high, video, cast.TargetAudioBitrateKbps)
	}
}

// The cap is a preference. Where no route can honor it, the receiver gets the
// plan it got before, never a refusal the device's own output would not meet.
func TestPlanPlaybackV3AirPlayNeverRefusesWhereTheDevicePlays(t *testing.T) {
	fourK := airPlayFileV3(60_000, ac351)
	fourK.Resolution = "2160p"
	fourK.VideoTracks[0].Width, fourK.VideoTracks[0].Height = 3840, 2160
	noEncoder := NewTransformationRegistryV3([]TransformationSpecV3{{Name: "audio_to_aac", Available: true}})
	// An HLS delivery that lists only HEVC, with HEVC encoding off: the capped
	// request has no encode to land on, and the copy still plays.
	hevcOnly := func(req *StartRequestV3) {
		hls := req.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3]
		hls.VideoCodecs = []string{"hevc"}
		req.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3] = hls
		req.Capabilities.CodecsVideo = []string{"hevc"}
		req.Capabilities.CodecsVideoHardware = []string{"hevc"}
		req.Capabilities.VideoDecode = []VideoDecodeCapabilityV3{{Codec: "hevc", Profiles: []string{"main"}, Levels: []int{153}, BitDepths: []int{8}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60, MaxBitrateKbps: 100_000, Hardware: true}}
	}
	hevcFile := airPlayFileV3(airPlayHeavyKbps, trueHD71)
	hevcFile.CodecVideo = "hevc"
	hevcFile.VideoTracks[0].Codec, hevcFile.VideoTracks[0].Profile, hevcFile.VideoTracks[0].Level = "hevc", "Main", 150
	for _, test := range []struct {
		name     string
		file     *models.MediaFile
		settings PlannerSettingsV3
		registry *TransformationRegistryV3
		client   func(*StartRequestV3)
		// refusedWhenCapped marks the cases where the capped request alone is
		// refused, so only the fallback keeps the start playable.
		refusedWhenCapped bool
	}{
		{"4K with 4K transcoding off", fourK, PlannerSettingsV3{TranscodeEnabled: true}, testTransformationRegistryV3(), nil, false},
		{"HLS lists only HEVC", hevcFile, airPlayPlannerSettingsV3, testTransformationRegistryV3(), hevcOnly, true},
		{"transcoding off", airPlayFileV3(airPlayHeavyKbps, trueHD71), PlannerSettingsV3{}, testTransformationRegistryV3(), nil, false},
		{"no video encoder", airPlayFileV3(airPlayHeavyKbps, trueHD71), airPlayPlannerSettingsV3, noEncoder, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			deviceReq, castReq := airPlayClientV3("Speaker", deviceRouteV3), airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
			if test.client != nil {
				test.client(&deviceReq)
				test.client(&castReq)
			}
			local := planAirPlayWithV3(deviceReq, test.file, test.settings, test.registry)
			if local.Plan == nil {
				t.Fatalf("the device does not play this source either: %s", ExplainPlannerResultV3(local))
			}
			if test.refusedWhenCapped {
				capped := PlanPlaybackV3(PlannerInputV3{Request: AirPlayAwareRequestV3(castReq), RequestedFile: test.file, EffectiveFile: test.file, Settings: test.settings, Registry: test.registry})
				if capped.Terminal == nil {
					t.Fatalf("the capped request alone is planned (%s), so this case no longer tests the fallback", ExplainPlannerResultV3(capped))
				}
			}
			cast := planAirPlayWithV3(castReq, test.file, test.settings, test.registry)
			if cast.Plan == nil {
				t.Fatalf("AirPlay was refused where the device plays: %s", ExplainPlannerResultV3(cast))
			}
			// The same route and recipe; where the cap could not be applied the
			// plan may carry warnings that say so.
			if !sameRouteV3(cast, local) {
				t.Fatalf("AirPlay changed the plan:\n got  %s\n want %s", ExplainPlannerResultV3(cast), ExplainPlannerResultV3(local))
			}
		})
	}

	// A title with a lower version keeps the capped refusal, so the handler's
	// own fallback plays that version instead of the heavy copy.
	hevcCast := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
	hevcOnly(&hevcCast)
	withLower := PlanPlaybackAirPlayAwareV3(PlannerInputV3{
		Request: hevcCast, RequestedFile: hevcFile, EffectiveFile: hevcFile, Settings: airPlayPlannerSettingsV3,
		Registry: testTransformationRegistryV3(), LowerVersion: &LowerVersionV3{},
	})
	if withLower.Terminal == nil {
		t.Fatalf("a title with a lower version was planned instead of left to the handler's fallback: %s", ExplainPlannerResultV3(withLower))
	}

	// A refusal the device would meet too is reported unchanged.
	failedAt := time.Now()
	refused := &models.MediaFile{ID: 42, FilePath: "/media/movie.mkv", ProbeFailedAt: &failedAt}
	local := planAirPlayWithV3(airPlayClientV3("Speaker", deviceRouteV3), refused, airPlayPlannerSettingsV3, testTransformationRegistryV3())
	cast := planAirPlayWithV3(airPlayClientV3(airPlaySinkV3, airPlayRouteV3), refused, airPlayPlannerSettingsV3, testTransformationRegistryV3())
	if local.Terminal == nil || cast.Terminal == nil || *cast.Terminal != *local.Terminal {
		t.Fatalf("shared refusal changed: AirPlay %s, device %s", ExplainPlannerResultV3(cast), ExplainPlannerResultV3(local))
	}
}

// A quality the viewer picked from the menu is a limit of their own, and is
// planned as it would be anywhere else. Only the requests that leave the choice
// to the server ("original", a plain resolution, auto) meet the AirPlay cap.
func TestPlanPlaybackV3AirPlayKeepsAMenuQualityTheViewerPicked(t *testing.T) {
	file := airPlayFileV3(airPlayHeavyKbps, trueHD71)
	for _, quality := range []string{QualityRung1080pHighV3, QualityRung1080pMediumV3, QualityRung720pHighV3, "720p"} {
		cast, local := airPlayClientV3(airPlaySinkV3, airPlayRouteV3), airPlayClientV3("Speaker", deviceRouteV3)
		cast.QualityPreference, local.QualityPreference = quality, quality
		got := planAirPlayV3(t, cast, file)
		sameShapeV3(t, got, planAirPlayV3(t, local, file), "AirPlay with "+quality)
		if video := optionalValueV3(got.Plan.EffectiveRecipe.BitrateKbps); video > AirPlayBitrateCapKbpsV3 {
			t.Fatalf("%s planned %d kbps, above the cap", quality, video)
		}
	}
	for _, quality := range []string{QualityOriginalV3, "1080p", QualityAutoV3} {
		cast, local := airPlayClientV3(airPlaySinkV3, airPlayRouteV3), airPlayClientV3("Speaker", deviceRouteV3)
		cast.QualityPreference, local.QualityPreference = quality, quality
		if got := planAirPlayV3(t, cast, file); got.Plan.Delivery != DeliveryTranscodeHLSV3 ||
			planAirPlayV3(t, local, file).Plan.Delivery != DeliveryRemuxHLSV3 {
			t.Fatalf("%s: AirPlay %s", quality, ExplainPlannerResultV3(got))
		}
	}
}

// Replans after a route change plan from the request in hand, so the cap
// follows the route in both directions and a failed AirPlay plan never blocks
// the device's own.
func TestPlanPlaybackV3RouteChangeRestoresTheNormalPlan(t *testing.T) {
	file := airPlayFileV3(airPlayHeavyKbps, trueHD71)

	device := planAirPlayV3(t, airPlayClientV3("Speaker", deviceRouteV3), file)
	if device.Plan.Delivery != DeliveryRemuxHLSV3 {
		t.Fatalf("device plan: %s", ExplainPlannerResultV3(device))
	}

	// Device -> AirPlay.
	cast := planAirPlayV3(t, airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file, device.Plan.PlanAttemptKey)
	if cast.Plan.Delivery != DeliveryTranscodeHLSV3 {
		t.Fatalf("AirPlay plan after the route change: %s", ExplainPlannerResultV3(cast))
	}

	// AirPlay -> device, with the AirPlay plan recorded as attempted.
	back := planAirPlayV3(t, airPlayClientV3("Speaker", deviceRouteV3), file, cast.Plan.PlanAttemptKey)
	sameShapeV3(t, back, device, "returning to the device")
	if back.Plan.PlanAttemptKey != device.Plan.PlanAttemptKey {
		t.Fatalf("plan attempt key = %s, want the device plan's %s", back.Plan.PlanAttemptKey, device.Plan.PlanAttemptKey)
	}

	// A new route id on the device is the normal plan too.
	other := planAirPlayV3(t, airPlayClientV3("Speaker", "route-device-2"), file, cast.Plan.PlanAttemptKey)
	sameShapeV3(t, other, device, "a different device route")
}
