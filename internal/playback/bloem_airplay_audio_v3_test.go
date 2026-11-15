package playback

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

// dolbyRegistryV3 is an executor pool that offers AAC and H.264 and, as
// flagged, the Dolby surround encodes.
func dolbyRegistryV3(ac3, eac3 bool) *TransformationRegistryV3 {
	return NewTransformationRegistryV3([]TransformationSpecV3{
		{Name: TransformationAudioToAACV3, Available: true},
		{Name: TransformationVideoToH264V3, Available: true},
		{Name: TransformationAudioToAC3V3, RecipeVersion: TransformationAudioToDolbyRecipeVersionV3, Available: ac3},
		{Name: TransformationAudioToEAC3V3, RecipeVersion: TransformationAudioToDolbyRecipeVersionV3, Available: eac3},
	})
}

func transformationNamesV3(plan *PlanV3) []string {
	names := make([]string, 0, len(plan.Transformations))
	for _, transformation := range plan.Transformations {
		names = append(names, transformation.Name)
	}
	return names
}

func warningMessageV3(plan *PlanV3, code string) string {
	for _, warning := range plan.DegradationWarnings {
		if warning.Code == code {
			return warning.Message
		}
	}
	return ""
}

// A surround source HLS cannot carry is converted to AC-3 5.1 for an AirPlay
// receiver, on the remux route (within the cap) and the encode route (above it).
func TestPlanPlaybackV3AirPlayConvertsSurroundToAC3(t *testing.T) {
	for _, test := range []struct {
		name         string
		audio        models.AudioTrack
		totalKbps    int
		wantDelivery DeliveryV3
	}{
		{"TrueHD 7.1 remux within the cap", trueHD71, 8_000, DeliveryRemuxHLSV3},
		{"DTS 5.1 remux within the cap", dts51, 8_000, DeliveryRemuxHLSV3},
		{"TrueHD 7.1 encode above the cap", trueHD71, airPlayHeavyKbps, DeliveryTranscodeHLSV3},
		{"DTS 5.1 encode above the cap", dts51, airPlayHeavyKbps, DeliveryTranscodeHLSV3},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := airPlayFileV3(test.totalKbps, test.audio)
			cast := planAirPlayWithV3(airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true))
			if cast.Plan == nil {
				t.Fatalf("no plan: %s", ExplainPlannerResultV3(cast))
			}
			plan := cast.Plan
			if plan.Delivery != test.wantDelivery {
				t.Fatalf("delivery = %s: %s", plan.Delivery, ExplainPlannerResultV3(cast))
			}
			recipe := plan.EffectiveRecipe
			if recipe.AudioCodec != "ac3" || optionalValueV3(recipe.AudioChannels) != 6 || recipe.AudioLayout != "5.1" {
				t.Fatalf("recipe audio = %s x%d %s, want ac3 x6 5.1", recipe.AudioCodec, optionalValueV3(recipe.AudioChannels), recipe.AudioLayout)
			}
			if plan.Claims.Audio.Codec != "ac3" {
				t.Fatalf("audio claim = %q, want ac3", plan.Claims.Audio.Codec)
			}
			if got := transformationNamesV3(plan); !slices.Contains(got, TransformationAudioToAC3V3) || slices.Contains(got, TransformationAudioToAACV3) {
				t.Fatalf("transformations = %v, want audio_to_ac3 in place of audio_to_aac", got)
			}
			for _, transformation := range plan.Transformations {
				if transformation.Name == TransformationAudioToAC3V3 && (transformation.Executor != ExecutorServerV3 ||
					transformation.RecipeVersion != TransformationAudioToDolbyRecipeVersionV3 || !slices.Contains(transformation.ValidatedClaims, ClaimAudioDecodeV3)) {
					t.Fatalf("transformation = %#v", transformation)
				}
			}
			if !cast.TranscodeAudio || cast.TargetAudioCodec != "ac3" || cast.TargetAudioChannels != 6 || cast.TargetAudioBitrateKbps != 384 {
				t.Fatalf("executor recipe = %s x%d at %d kbps, transcode=%v", cast.TargetAudioCodec, cast.TargetAudioChannels, cast.TargetAudioBitrateKbps, cast.TranscodeAudio)
			}
			// A remux says which codec it converted to; an encode re-encodes every
			// track and, as before, carries no audio warning at all.
			if test.wantDelivery == DeliveryRemuxHLSV3 && !strings.Contains(warningMessageV3(plan, degradationAudioConvertedV3), "AC-3 5.1") {
				t.Fatalf("warnings = %#v", plan.DegradationWarnings)
			}
			if plan.PlanID == "" || plan.PlanAttemptKey == "" {
				t.Fatalf("plan identity not finalized: %#v", plan)
			}
			if test.wantDelivery == DeliveryTranscodeHLSV3 {
				if video := optionalValueV3(recipe.BitrateKbps); video+cast.TargetAudioBitrateKbps > AirPlayBitrateCapKbpsV3 {
					t.Fatalf("stream %d + %d kbps audio exceeds the cap", video, cast.TargetAudioBitrateKbps)
				}
			}
		})
	}
}

// Where AC-3 cannot be planned the conversion stays AAC 5.1: the planner's own
// plan, untouched.
func TestPlanPlaybackV3AirPlayKeepsAACWhereDolbyCannotBePlanned(t *testing.T) {
	file := airPlayFileV3(8_000, trueHD71)
	limited := func(mutate func(*StartRequestV3)) StartRequestV3 {
		req := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
		mutate(&req)
		return req
	}
	for _, test := range []struct {
		name     string
		registry *TransformationRegistryV3
		request  StartRequestV3
	}{
		{"executors that predate the Dolby encodes", testTransformationRegistryV3(), airPlayClientV3(airPlaySinkV3, airPlayRouteV3)},
		{"executors without either encoder", dolbyRegistryV3(false, false), airPlayClientV3(airPlaySinkV3, airPlayRouteV3)},
		{"a client that lists neither codec", dolbyRegistryV3(true, true), limited(func(r *StartRequestV3) {
			r.Capabilities.CodecsAudio = []string{"aac"}
		})},
		{"an HLS delivery that lists neither codec", dolbyRegistryV3(true, true), limited(func(r *StartRequestV3) {
			hls := r.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3]
			hls.AudioDecodeCodecs = []string{"aac"}
			r.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3] = hls
		})},
		{"an HLS delivery limited to stereo", dolbyRegistryV3(true, true), limited(func(r *StartRequestV3) {
			hls := r.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3]
			hls.MaxChannels = new(2)
			r.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3] = hls
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := planAirPlayWithV3(test.request, file, airPlayPlannerSettingsV3, test.registry)
			if got.Plan == nil {
				t.Fatalf("no plan: %s", ExplainPlannerResultV3(got))
			}
			if got.TargetAudioCodec != audioCodecAACV3 || got.Plan.EffectiveRecipe.AudioCodec != audioCodecAACV3 ||
				!slices.Contains(transformationNamesV3(got.Plan), TransformationAudioToAACV3) {
				t.Fatalf("audio = %s, transformations %v, want the planner's AAC conversion", got.TargetAudioCodec, transformationNamesV3(got.Plan))
			}
		})
	}
}

// E-AC-3 is the fallback codec: used when the client lists it and not AC-3, or
// when only the E-AC-3 encoder is offered.
func TestPlanPlaybackV3AirPlayFallsBackToEAC3(t *testing.T) {
	file := airPlayFileV3(8_000, dts51)
	onlyEAC3 := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
	onlyEAC3.Capabilities.CodecsAudio = []string{"aac", "eac3"}
	hls := onlyEAC3.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3]
	hls.AudioDecodeCodecs = []string{"aac", "eac3"}
	onlyEAC3.ClientPlaybackContext.Deliveries[DeliveryClassHLSV3] = hls

	for name, got := range map[string]PlannerResultV3{
		"the client lists only E-AC-3":   planAirPlayWithV3(onlyEAC3, file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true)),
		"only the E-AC-3 encoder exists": planAirPlayWithV3(airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file, airPlayPlannerSettingsV3, dolbyRegistryV3(false, true)),
	} {
		if got.Plan == nil || got.TargetAudioCodec != "eac3" || got.Plan.EffectiveRecipe.AudioCodec != "eac3" ||
			!slices.Contains(transformationNamesV3(got.Plan), TransformationAudioToEAC3V3) ||
			!strings.Contains(warningMessageV3(got.Plan, degradationAudioConvertedV3), "E-AC-3 5.1") {
			t.Fatalf("%s: %s", name, ExplainPlannerResultV3(got))
		}
	}
}

// Only a conversion the planner made for surround is touched: stereo stays
// stereo AAC, sources HLS carries as they are stay copied, and a source that is
// already AAC, AC-3 or E-AC-3 is never re-encoded as something else.
func TestPlanPlaybackV3AirPlayLeavesOtherAudioAlone(t *testing.T) {
	trueHDStereo := models.AudioTrack{Codec: "truehd", Channels: 2, Layout: "stereo", Language: "eng"}
	eac351 := models.AudioTrack{Codec: "eac3", Channels: 6, Layout: "5.1", Language: "eng"}
	aac51 := models.AudioTrack{Codec: "aac", Channels: 6, Layout: "5.1", Language: "eng"}
	for _, test := range []struct {
		name         string
		audio        models.AudioTrack
		totalKbps    int
		wantCopy     bool
		wantChannels int
	}{
		{"stereo TrueHD converts to stereo AAC", trueHDStereo, 8_000, false, 2},
		{"AAC stereo is copied", aacSt, 8_000, true, 0},
		{"AAC 5.1 is copied", aac51, 8_000, true, 0},
		{"AC-3 5.1 is copied", ac351, 8_000, true, 0},
		{"E-AC-3 5.1 is copied", eac351, 8_000, true, 0},
		// The encode route re-encodes every source; a codec HLS carries as it is
		// stays AAC there, with the source's channels.
		{"AC-3 5.1 stays AAC in the encode", ac351, airPlayHeavyKbps, false, 6},
		{"AAC stereo stays AAC in the encode", aacSt, airPlayHeavyKbps, false, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := airPlayFileV3(test.totalKbps, test.audio)
			cast := planAirPlayWithV3(airPlayClientV3(airPlaySinkV3, airPlayRouteV3), file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true))
			if cast.Plan == nil {
				t.Fatalf("no plan: %s", ExplainPlannerResultV3(cast))
			}
			if names := transformationNamesV3(cast.Plan); slices.Contains(names, TransformationAudioToAC3V3) || slices.Contains(names, TransformationAudioToEAC3V3) {
				t.Fatalf("transformations = %v", names)
			}
			if test.wantCopy {
				if cast.TranscodeAudio || cast.Plan.EffectiveRecipe.AudioCodec != normalizeCodecV3(test.audio.Codec) {
					t.Fatalf("audio was converted: %s", ExplainPlannerResultV3(cast))
				}
				return
			}
			if cast.TargetAudioCodec != audioCodecAACV3 || cast.TargetAudioChannels != test.wantChannels {
				t.Fatalf("target audio = %s x%d, want aac x%d", cast.TargetAudioCodec, cast.TargetAudioChannels, test.wantChannels)
			}
		})
	}
}

// The Dolby encodes and a client that lists AC-3 change nothing for any other
// output: the plan is the AAC conversion it always was.
func TestPlanPlaybackV3NonAirPlayOutputsKeepAACWithDolbyAvailable(t *testing.T) {
	file := airPlayFileV3(8_000, trueHD71)
	baseline := planAirPlayWithV3(airPlayClientV3("Speaker", deviceRouteV3), file, airPlayPlannerSettingsV3, testTransformationRegistryV3())
	if baseline.Plan == nil {
		t.Fatalf("baseline: %s", ExplainPlannerResultV3(baseline))
	}
	for _, sink := range []string{"", "Speaker", "Bluetooth", "HDMI", "Cast", "AirPlay2"} {
		got := planAirPlayWithV3(airPlayClientV3(sink, deviceRouteV3), file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true))
		if got.Plan == nil {
			t.Fatalf("sink %q: %s", sink, ExplainPlannerResultV3(got))
		}
		sameShapeV3(t, got, baseline, "sink "+sink)
		if got.Plan.PlanAttemptKey != baseline.Plan.PlanAttemptKey || got.Plan.PlanID != baseline.Plan.PlanID {
			t.Fatalf("sink %q changed the plan identity", sink)
		}
	}
}

// A client that fails the AC-3 plan reports its key; each replan moves on, to
// E-AC-3 and then to the AAC plan, and never offers a plan already attempted.
func TestPlanPlaybackV3AirPlayDolbyFailuresMoveOnToAAC(t *testing.T) {
	file := airPlayFileV3(8_000, trueHD71)
	req := airPlayClientV3(airPlaySinkV3, airPlayRouteV3)
	var attempted, codecs []string
	for range 3 {
		got := planAirPlayWithV3(req, file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true), attempted...)
		if got.Plan == nil {
			t.Fatalf("after %v: %s", codecs, ExplainPlannerResultV3(got))
		}
		if slices.Contains(attempted, got.Plan.PlanAttemptKey) {
			t.Fatalf("offered an attempted plan again after %v", codecs)
		}
		attempted = append(attempted, got.Plan.PlanAttemptKey)
		codecs = append(codecs, got.TargetAudioCodec)
	}
	if want := []string{"ac3", "eac3", audioCodecAACV3}; !reflect.DeepEqual(codecs, want) {
		t.Fatalf("codecs across failures = %v, want %v", codecs, want)
	}
	// With all three attempted nothing repeats: another route or none.
	got := planAirPlayWithV3(req, file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true), attempted...)
	if got.Plan != nil && slices.Contains(attempted, got.Plan.PlanAttemptKey) {
		t.Fatalf("repeated an attempted plan: %s", ExplainPlannerResultV3(got))
	}
}

// The route change in both directions, now with the Dolby encodes on offer.
func TestPlanPlaybackV3RouteChangeSwitchesAudioWithTheOutput(t *testing.T) {
	file := airPlayFileV3(8_000, trueHD71)
	plan := func(sink, route string, attempted ...string) PlannerResultV3 {
		t.Helper()
		got := planAirPlayWithV3(airPlayClientV3(sink, route), file, airPlayPlannerSettingsV3, dolbyRegistryV3(true, true), attempted...)
		if got.Plan == nil {
			t.Fatalf("%s: %s", sink, ExplainPlannerResultV3(got))
		}
		return got
	}
	device := plan("Speaker", deviceRouteV3)
	if device.TargetAudioCodec != audioCodecAACV3 {
		t.Fatalf("device audio = %s, want aac", device.TargetAudioCodec)
	}
	cast := plan(airPlaySinkV3, airPlayRouteV3, device.Plan.PlanAttemptKey)
	if cast.TargetAudioCodec != "ac3" {
		t.Fatalf("AirPlay audio = %s, want ac3", cast.TargetAudioCodec)
	}
	back := plan("Speaker", deviceRouteV3, cast.Plan.PlanAttemptKey)
	sameShapeV3(t, back, device, "returning to the device")
	if back.Plan.PlanAttemptKey != device.Plan.PlanAttemptKey {
		t.Fatalf("plan attempt key = %s, want the device plan's %s", back.Plan.PlanAttemptKey, device.Plan.PlanAttemptKey)
	}
}

func TestAppendAudioArgsDolbyUsesThePlannedChannelsAndRate(t *testing.T) {
	for _, test := range []struct {
		name string
		opts TranscodeOpts
		want []string
	}{
		{"AC-3 surround plan", TranscodeOpts{TargetCodecAudio: "ac3", TargetAudioChannels: 6, TargetAudioBitrateKbps: 384}, []string{"-c:a", "ac3", "-b:a", "384k", "-ac", "6"}},
		{"E-AC-3 surround plan", TranscodeOpts{TargetCodecAudio: "eac3", TargetAudioChannels: 6, TargetAudioBitrateKbps: 384}, []string{"-c:a", "eac3", "-b:a", "384k", "-ac", "6"}},
		{"mixed-case codec", TranscodeOpts{TargetCodecAudio: " AC3 ", TargetAudioChannels: 6, TargetAudioBitrateKbps: 384}, []string{"-c:a", "ac3", "-b:a", "384k", "-ac", "6"}},
		// Encodes that name no channels or rate keep the arguments they had.
		{"AC-3 without a plan", TranscodeOpts{TargetCodecAudio: "ac3"}, []string{"-c:a", "ac3", "-b:a", "448k"}},
		{"E-AC-3 without a plan", TranscodeOpts{TargetCodecAudio: "eac3"}, []string{"-c:a", "eac3", "-b:a", "384k"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := appendAudioArgs(nil, test.opts); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args = %v, want %v", got, test.want)
			}
		})
	}
}

// The planned recipe runs on a real FFmpeg: 7.1 in, 5.1 AC-3 and E-AC-3 out,
// in the MPEG-TS segments an HLS remux of H.264 uses.
func TestAppendAudioArgsDolbyEncodesSurroundWithFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("real FFmpeg integration test")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobePath := ffprobePathFromFFmpeg(ffmpegPath)
	if _, err := exec.LookPath(ffprobePath); err != nil {
		t.Skip("ffprobe is not installed beside ffmpeg")
	}
	dir := t.TempDir()
	run := func(path string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", filepath.Base(path), args, err, out)
		}
		return out
	}
	source := filepath.Join(dir, "surround71.mkv")
	run(ffmpegPath, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2,aformat=channel_layouts=7.1",
		"-c:a", "flac", "-y", source)
	for _, codec := range []string{"ac3", "eac3"} {
		output := filepath.Join(dir, codec+".ts")
		args := []string{"-hide_banner", "-loglevel", "error", "-i", source, "-vn"}
		args = appendAudioArgs(args, TranscodeOpts{TargetCodecAudio: codec, TargetAudioChannels: 6, TargetAudioBitrateKbps: 384})
		run(ffmpegPath, append(args, "-f", "mpegts", "-y", output)...)
		// ffprobe lists an MPEG-TS stream again under its program; the first line
		// is the stream itself.
		probed, _, _ := strings.Cut(strings.TrimSpace(string(run(ffprobePath, "-v", "error", "-select_streams", "a:0",
			"-show_entries", "stream=codec_name,channels", "-of", "csv=p=0", output))), "\n")
		if probed != codec+",6" {
			t.Fatalf("%s output = %q, want %s,6", codec, probed, codec)
		}
		if info, err := os.Stat(output); err != nil || info.Size() == 0 {
			t.Fatalf("%s output missing or empty: %v", codec, err)
		}
	}
}

// probeDolbyScriptV3 is a fake FFmpeg that lists the encoders in listing, and
// fails any invocation whose arguments contain reject.
func probeDolbyScriptV3(listing, reject string) string {
	script := "#!/bin/sh\ncase \"$2\" in\n-bsfs) : ;;\n-encoders) printf '%s\\n' " + listing + " ;;\nesac\n"
	if reject != "" {
		script += "case \" $* \" in\n*\" " + reject + " \"*) exit 1 ;;\nesac\n"
	}
	return script
}

func TestProbeTransformationRegistryV3AdvertisesDolbyEncodes(t *testing.T) {
	both := "' A....D ac3 ATSC A/52A (AC-3)' ' A....D eac3 ATSC A/52 E-AC-3' ' A....D aac AAC'"
	registry := ProbeTransformationRegistryV3(context.Background(), writeProbeFFmpegV3(t, probeDolbyScriptV3(both, "")))
	names := map[string]TransformationV3{}
	for _, transformation := range registry.Advertised() {
		names[transformation.Name] = transformation
	}
	for _, name := range []string{TransformationAudioToAC3V3, TransformationAudioToEAC3V3} {
		got, ok := names[name]
		if !ok || got.RecipeVersion != TransformationAudioToDolbyRecipeVersionV3 || got.Executor != ExecutorServerV3 {
			t.Fatalf("%s advertised as %#v (found %v)", name, got, ok)
		}
	}

	// Only the encoder FFmpeg lists is offered; the other is defined but
	// unavailable, so a node that has it can still widen the registry.
	onlyAC3 := ProbeTransformationRegistryV3(context.Background(), writeProbeFFmpegV3(t, probeDolbyScriptV3("' A....D ac3 ATSC A/52A (AC-3)' ' A....D aac AAC'", "")))
	if !onlyAC3.Available(TransformationAudioToAC3V3) || onlyAC3.Available(TransformationAudioToEAC3V3) {
		t.Fatalf("ac3=%v eac3=%v, want only ac3", onlyAC3.Available(TransformationAudioToAC3V3), onlyAC3.Available(TransformationAudioToEAC3V3))
	}
	widened := onlyAC3.WithAdvertised([]TransformationV3{{Name: TransformationAudioToEAC3V3, Executor: ExecutorServerV3, RecipeVersion: TransformationAudioToDolbyRecipeVersionV3}})
	if !widened.Available(TransformationAudioToEAC3V3) {
		t.Fatal("a pooled node's E-AC-3 advertisement did not widen the registry")
	}
	stale := onlyAC3.WithAdvertised([]TransformationV3{{Name: TransformationAudioToEAC3V3, Executor: ExecutorServerV3, RecipeVersion: "0"}})
	if stale.Available(TransformationAudioToEAC3V3) {
		t.Fatal("a node on another recipe version widened the registry")
	}
}

// An FFmpeg that lists the encoder but rejects the 7.1 to 5.1 encode does not
// advertise it, and that is a result worth caching, not a failed probe.
func TestProbeTransformationRegistryV3RequiresTheSurroundEncodeToRun(t *testing.T) {
	listing := "' A....D ac3 ATSC A/52A (AC-3)' ' A....D eac3 ATSC A/52 E-AC-3' ' A....D aac AAC'"
	registry, err := ProbeTransformationRegistryWithToneMapV3Result(context.Background(), writeProbeFFmpegV3(t, probeDolbyScriptV3(listing, "-c:a ac3")), nil)
	if err != nil {
		t.Fatalf("a rejected encode is a capability result: %v", err)
	}
	if registry.Available(TransformationAudioToAC3V3) || !registry.Available(TransformationAudioToEAC3V3) {
		t.Fatalf("ac3=%v eac3=%v, want only eac3", registry.Available(TransformationAudioToAC3V3), registry.Available(TransformationAudioToEAC3V3))
	}
	if registry.NeedsRefresh(time.Now().Add(time.Hour)) {
		t.Fatal("a rejected encode marked the registry for another probe")
	}
}
