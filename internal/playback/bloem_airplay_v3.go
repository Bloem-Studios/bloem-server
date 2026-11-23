package playback

import (
	"fmt"
	"slices"
	"strings"
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
//
// The Dolby surround conversion itself is Silo's: its audio_to_ac3 and
// audio_to_eac3 transformations, their registry probe and their encode
// (surround_conversion_v3.go). An AirPlay plan names the same transformation
// and runs the same 48 kHz 5.1 encode as Silo's own surround conversion.

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
// delivery cannot decode, or the server cannot encode the source at all (no
// encoder, transcoding off, or a 4K source with 4K transcoding off), and no
// other version of the title can play instead. The receiver then gets the plan
// it got before this cap existed, and a refusal that would stand anyway is
// reported unchanged, so the cap can only replace a plan, never turn a playable
// start into a refusal.
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
	return AirPlayAwarePlanV3(input, PlanPlaybackV3(input))
}

// AirPlayAwarePlanV3 is PlanPlaybackAirPlayAwareV3 for a caller that already
// has planned, PlanPlaybackV3's result for input: the playback handler keeps
// Silo's planner line and passes its result here on Bloem's own line. Any
// output but an AirPlay receiver keeps planned as it is.
func AirPlayAwarePlanV3(input PlannerInputV3, planned PlannerResultV3) PlannerResultV3 {
	if !IsAirPlayOutputV3(input.Request.ClientPlaybackContext.Output) {
		return planned
	}
	capped := input
	capped.Request = AirPlayAwareRequestV3(input.Request)
	result := PlanPlaybackV3(capped)
	if result.Terminal != nil && input.LowerVersion == nil {
		// The uncapped plan still goes to an AirPlay receiver, so its surround
		// conversion gets the receiver's Dolby codec too.
		return adaptAirPlayAudioV3(input, planned)
	}
	return adaptAirPlayAudioV3(capped, result)
}

const (
	// airPlaySurroundBudgetKbpsV3 is the rate the planner reserves for 5.1
	// AAC. A plan whose budget reserved less keeps its AAC; one that reserved
	// it moves to Silo's Dolby encode, which runs at
	// surroundBitstreamBitrateKbpsV3.
	airPlaySurroundBudgetKbpsV3 = 384
	airPlaySurroundChannelsV3   = surroundBitstreamChannelsV3
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
	if result.TargetAudioBitrateKbps > 0 && result.TargetAudioBitrateKbps < airPlaySurroundBudgetKbpsV3 {
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
				_, version := surroundBitstreamTransformationV3(choice.codec)
				adapted.Transformations[i] = TransformationV3{Name: choice.transformation, Executor: ExecutorServerV3, RecipeVersion: version, ValidatedClaims: []string{ClaimAudioDecodeV3}}
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
		result.TargetAudioBitrateKbps = surroundBitstreamBitrateKbpsV3
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
