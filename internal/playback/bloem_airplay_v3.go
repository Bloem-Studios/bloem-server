package playback

import "strings"

// AirPlay-aware planning (Bloem).
//
// An AirPlay screen receiver is not the device the client runs on. It is often
// a television with its own HLS implementation, and the stream has to suit it.
// A Blu-ray-class remux that plays on the client's own speaker has stalled and
// failed there.
//
// The client reports the active route in output.sink_type. Silo carries that
// value but plans the same stream whatever it says. This file narrows what the
// planner is shown while an AirPlay route is active; the planner needs no
// AirPlay knowledge of its own.
//
// The only Silo-side hook is the two planner calls in
// planPlaybackWithCapabilitiesV3 (internal/api/handlers/playback_v3.go), which
// go through PlanPlaybackAirPlayAwareV3. They run on every planning pass, so a
// replan after a route change sees the new route: back on the device's own
// output the request is unchanged and the normal plan returns.
//
// Audio is not changed here. The plan contract converts audio only to AAC (the
// audio_to_aac transformation), so surround that HLS cannot carry as it is
// still becomes AAC 5.1.

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
func PlanPlaybackAirPlayAwareV3(input PlannerInputV3) PlannerResultV3 {
	if !IsAirPlayOutputV3(input.Request.ClientPlaybackContext.Output) {
		return PlanPlaybackV3(input)
	}
	capped := input
	capped.Request = AirPlayAwareRequestV3(input.Request)
	result := PlanPlaybackV3(capped)
	if result.Terminal == nil || input.LowerVersion != nil {
		return result
	}
	return PlanPlaybackV3(input)
}
