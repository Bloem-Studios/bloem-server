package playback

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/procgroup"
)

// AirPlay-aware planning (Bloem).
//
// An AirPlay screen receiver is not the device the client runs on. It is often
// a television with its own HLS implementation, and the stream has to suit it.
// A Blu-ray-class remux that plays on the client's own speaker has stalled and
// failed there.
//
// The client reports the active route in output.sink_type. Silo carries that
// value but plans the same stream whatever it says. This file narrows what the
// planner is shown while an AirPlay route is active, and picks the surround
// audio codec a receiver decodes; the planner needs no AirPlay knowledge of its
// own.
//
// The Silo-side hooks are one line each, marked "Bloem seam" and listed in
// contracts/seams.txt:
//
//   - the two planner calls in planPlaybackWithCapabilitiesV3
//     (internal/api/handlers/playback_v3.go) go through
//     PlanPlaybackAirPlayAwareV3. They run on every planning pass, so a replan
//     after a route change sees the new route: back on the device's own output
//     the request is unchanged and the normal plan returns;
//   - ProbeTransformationRegistryWithToneMapV3Result (transformations_v3.go)
//     defines the Dolby audio transformations;
//   - the "ac3" and "eac3" cases of appendAudioArgs (transcode.go) follow
//     Silo's encode line with bloemDolbyAudioTargetsV3.

const (
	// OutputSinkTypeAirPlayV3 is the output.sink_type Apple clients report while
	// the active route is an AirPlay receiver.
	OutputSinkTypeAirPlayV3 = "AirPlay"

	// AirPlayBitrateCapKbpsV3 is the ceiling on a stream's video bitrate, in
	// kbps, while the output is an AirPlay receiver. Audio adds up to a few
	// hundred kbps on top of it.
	//
	// A source whose video is above the ceiling is encoded instead of copied,
	// and a 4K source is scaled down to 1080p. Automatic quality then encodes
	// the 1080p class at the class's own rate, 6 Mbps, which is the primary
	// 1080p H.264 variant of Apple's HLS authoring specification (it lists 6
	// and 7.8 Mbps). A menu rung the viewer picked, up to "1080p High", is
	// still honored. A source at or below the ceiling plans exactly as before.
	//
	// 10 Mbps is the planner's own top 1080p rung, so the ceiling matches a
	// step on the existing ladder. It is also below the 11.6 Mbps Apple lists
	// for its lowest 4K HEVC variant, so a receiver is never asked for more
	// than Apple's smallest 4K stream.
	AirPlayBitrateCapKbpsV3 = 10_000
)

// IsAirPlayOutputV3 reports whether the active output is an AirPlay receiver.
// The match ignores case and surrounding space; any other sink type, and a
// client that sends none, is not AirPlay.
func IsAirPlayOutputV3(output OutputContextV3) bool {
	return strings.EqualFold(strings.TrimSpace(output.SinkType), OutputSinkTypeAirPlayV3)
}

// AirPlayAwareRequestV3 returns the request the planner should see for the
// active output. For an AirPlay receiver it lowers the bandwidth cap to
// AirPlayBitrateCapKbpsV3, so a source above the cap is transcoded or scaled
// down to it and a source within it plans exactly as before. It never raises a
// cap: a client that asked for less keeps its own limit. For every other output
// it returns the request unchanged.
//
// The input is never modified. Callers keep the client's own request as the
// durable record and plan from the returned copy.
func AirPlayAwareRequestV3(request StartRequestV3) StartRequestV3 {
	if !IsAirPlayOutputV3(request.ClientPlaybackContext.Output) {
		return request
	}
	return ApplyPlanOverridesV3(request, PlanOverridesV3{MaxBitrateKbps: AirPlayBitrateCapKbpsV3})
}

// PlanPlaybackAirPlayAwareV3 is PlanPlaybackV3 for the active output. Any
// output but an AirPlay receiver is planned exactly as PlanPlaybackV3 plans it.
//
// For an AirPlay receiver the cap is a preference: the capped request is
// planned first, and when that finds no route the request is planned as it
// came. That happens when the cap leaves only an encode the client's HLS
// delivery cannot decode, and no other version of the title can play instead.
// The receiver then gets the plan it got before this cap existed, and a refusal
// that would stand anyway is reported unchanged, so the cap can only replace a
// plan, never turn a playable start into a refusal. Where the server cannot
// encode at all, the planner already keeps the copy and says so in a warning.
//
// A title with a lower version keeps the capped refusal: the handler's own
// fallback then plays that version, which the cap does not refuse. If that
// fails too, the handler plans the title once more without the lower version,
// and this function falls back then.
//
// A plan that converts surround audio HLS cannot carry as it is, such as TrueHD
// or DTS, is then moved from AAC to AC-3 or E-AC-3 when the client decodes it
// (see adaptAirPlayAudioV3).
func PlanPlaybackAirPlayAwareV3(input PlannerInputV3) PlannerResultV3 {
	if !IsAirPlayOutputV3(input.Request.ClientPlaybackContext.Output) {
		return PlanPlaybackV3(input)
	}
	capped := input
	capped.Request = AirPlayAwareRequestV3(input.Request)
	result := PlanPlaybackV3(capped)
	if result.Terminal != nil && input.LowerVersion == nil {
		return PlanPlaybackV3(input)
	}
	return adaptAirPlayAudioV3(capped, result)
}

const (
	// TransformationAudioToAC3V3 and TransformationAudioToEAC3V3 name the
	// server's Dolby surround encodes. A plan lists one in place of
	// audio_to_aac when it converts surround audio for an AirPlay receiver.
	TransformationAudioToAC3V3  = "audio_to_ac3"
	TransformationAudioToEAC3V3 = "audio_to_eac3"

	// TransformationAudioToDolbyRecipeVersionV3 versions both Dolby recipes:
	// the source's channels mapped to 5.1 by FFmpeg's own downmix, encoded at
	// airPlaySurroundAudioKbpsV3.
	TransformationAudioToDolbyRecipeVersionV3 = "1"

	// airPlaySurroundAudioKbpsV3 is the 5.1 rate Apple's HLS authoring
	// specification recommends for Dolby Digital, and the rate the planner
	// already reserves for 5.1 AAC, so the stream's total does not change.
	airPlaySurroundAudioKbpsV3 = 384
	airPlaySurroundChannelsV3  = 6
)

// airPlaySurroundCodecsV3 is the order a surround conversion tries. AC-3
// first: Apple's specification asks for Dolby Digital alongside Dolby Digital
// Plus because it is the one every Dolby-capable receiver decodes, and a plan
// carries one audio codec.
var airPlaySurroundCodecsV3 = []struct{ codec, transformation, name string }{
	{audioCodecAC3, TransformationAudioToAC3V3, "AC-3"},
	{audioCodecEAC3, TransformationAudioToEAC3V3, "E-AC-3"},
}

// adaptAirPlayAudioV3 replaces a plan's 5.1 AAC conversion with AC-3, or with
// E-AC-3 when the client lists only that. AirPlay receivers and the televisions
// behind them decode Dolby surround natively, and the planner's only conversion
// target is otherwise AAC.
//
// Only a conversion the planner made is touched, so a plan that copies its
// audio, a source that is already AAC, AC-3 or E-AC-3, stereo and mono output,
// and every other delivery come back as they were. A surround source that
// HLS cannot carry as it is (TrueHD, DTS, FLAC, PCM) is the case it exists for.
//
// The new plan is only used when the client's HLS delivery accepts it, the
// executor pool offers the transformation, and the plan has not already been
// attempted. The last check matters: a client that fails the AC-3 plan reports
// its key, and each replan then moves on, to E-AC-3 when the client lists it
// and then to the AAC plan the planner chose, instead of offering the same plan
// again. The AAC plan is never touched, so it can fail on its own key.
func adaptAirPlayAudioV3(input PlannerInputV3, result PlannerResultV3) PlannerResultV3 {
	plan := result.Plan
	if plan == nil || !result.TranscodeAudio || result.TargetAudioCodec != audioCodecAACV3 ||
		result.TargetAudioChannels != airPlaySurroundChannelsV3 || hlsNativeAudioCodecV3(plan.Source.AudioCodec) {
		return result
	}
	// A tight budget reserved less than the Dolby rate; keep what it sized.
	if result.TargetAudioBitrateKbps > 0 && result.TargetAudioBitrateKbps < airPlaySurroundAudioKbpsV3 {
		return result
	}
	var registry *TransformationRegistryV3
	switch plan.Delivery {
	case DeliveryRemuxHLSV3:
		registry = input.hlsRemuxRegistry()
	case DeliveryTranscodeHLSV3:
		registry = input.hlsVideoRegistry()
	default:
		return result
	}
	request := input.Request
	output := request.ClientPlaybackContext.Output.OutputContextID
	for _, choice := range airPlaySurroundCodecsV3 {
		if !registry.Available(choice.transformation) || !clientDecodesHLSAudioV3(request, choice.codec, airPlaySurroundChannelsV3) {
			continue
		}
		adapted := *plan
		adapted.EffectiveRecipe.AudioCodec = choice.codec
		adapted.Claims.Audio.Codec = choice.codec
		adapted.Transformations = slices.Clone(plan.Transformations)
		for i, transformation := range adapted.Transformations {
			if transformation.Name == TransformationAudioToAACV3 {
				adapted.Transformations[i] = TransformationV3{Name: choice.transformation, Executor: ExecutorServerV3, RecipeVersion: TransformationAudioToDolbyRecipeVersionV3, ValidatedClaims: []string{ClaimAudioDecodeV3}}
			}
		}
		adapted.DegradationWarnings = slices.Clone(plan.DegradationWarnings)
		for i, warning := range adapted.DegradationWarnings {
			if warning.Code == degradationAudioConvertedV3 {
				adapted.DegradationWarnings[i].Message = fmt.Sprintf("The selected audio track is converted to %s 5.1 for this AirPlay receiver.", choice.name)
			}
		}
		finalizePlanIdentityV3(&adapted, request.PlaybackAttemptID, output)
		if planAttemptedV3(adapted, output, input.AttemptedKeys) || !deliverySupportsPlanV3(request, DeliveryClassHLSV3, adapted) {
			continue
		}
		result.Plan = &adapted
		result.TargetAudioCodec = choice.codec
		result.TargetAudioBitrateKbps = airPlaySurroundAudioKbpsV3
		return result
	}
	return result
}

// clientDecodesHLSAudioV3 reports whether the client lists codec as decodable,
// on the device and in its HLS delivery, at channels.
func clientDecodesHLSAudioV3(request StartRequestV3, codec string, channels int) bool {
	listed := slices.ContainsFunc(request.Capabilities.CodecsAudio, func(value string) bool {
		return normalizeCodecV3(value) == codec
	})
	return listed && deliverySupportsAudioClaimV3(request, DeliveryClassHLSV3, codec, AudioClaimsV3{Codec: codec}, true) &&
		!deliveryExceedsMaxChannelsV3(request, DeliveryClassHLSV3, codec, channels)
}

// bloemDolbyAudioTargetsV3 follows Silo's AC-3 or E-AC-3 encode in
// appendAudioArgs (its codec and fixed rate, the last two arguments) with the
// plan's rate when it froze one, and the channel count it froze. A zero
// channel count leaves FFmpeg to keep the source's layout, as Silo's encode
// does.
func bloemDolbyAudioTargetsV3(args []string, opts TranscodeOpts) []string {
	if n := len(args); opts.TargetAudioBitrateKbps > 0 && n >= 2 && args[n-2] == "-b:a" {
		args[n-1] = strconv.Itoa(opts.TargetAudioBitrateKbps) + "k"
	}
	if opts.TargetAudioChannels > 0 {
		args = append(args, "-ac", strconv.Itoa(opts.TargetAudioChannels))
	}
	return args
}

// addDolbyAudioV3 defines the AC-3 and E-AC-3 transformations on a probed
// registry. Each needs its encoder and a one-frame encode of a 7.1 input to 5.1,
// the conversion the plan names. A probe that could not run, as opposed to one
// FFmpeg rejected, leaves the registry to be probed again soon, as the HEVC
// probe does, rather than caching "unavailable".
func (r *TransformationRegistryV3) addDolbyAudioV3(ctx context.Context, ffmpegPath string, encoders []byte, ffmpegFound bool) {
	for _, choice := range airPlaySurroundCodecsV3 {
		available := false
		if ffmpegFound && bytes.Contains(encoders, []byte(" "+choice.codec+" ")) {
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := procgroup.CommandContext(probeCtx, ffmpegPath,
				"-hide_banner", "-loglevel", "error",
				"-f", "lavfi", "-i", "anullsrc=r=48000:cl=7.1",
				"-frames:a", "1", "-c:a", choice.codec, "-ac", strconv.Itoa(airPlaySurroundChannelsV3), "-b:a", strconv.Itoa(airPlaySurroundAudioKbpsV3)+"k",
				"-f", "null", "-",
			).Run()
			timedOut := probeCtx.Err() != nil
			cancel()
			available = err == nil
			if timedOut || audioRecipeProbeInfrastructureError(err) != nil {
				r.refreshAfter = time.Now().Add(transformationProbeRetryDelayV3)
			}
		}
		r.entries[choice.transformation] = TransformationSpecV3{
			Name: choice.transformation, RecipeVersion: TransformationAudioToDolbyRecipeVersionV3, Available: available,
			RequiredCapability: "ffmpeg_encoder:" + choice.codec + "+ffmpeg_encode_smoke:surround_5_1_v1",
			ValidatedClaims:    []string{ClaimAudioDecodeV3}, TerminalReason: TerminalAudioConversionUnsupportedV3,
		}
	}
}
