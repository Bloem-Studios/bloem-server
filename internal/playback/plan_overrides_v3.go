package playback

import "strings"

// PlanOverridesV3 pins planner inputs on behalf of an admin replan command
// (docs/specs/admin-remote-control.md §C, S-5a). Overrides never widen what a
// client advertised: they narrow the durable start request the planner sees,
// so every plan they produce is one the client could have negotiated itself.
//
//   - Transcode "force" makes the planner take the adapted (transcode) route
//     even when a source-preserving route validates; "direct" drops the
//     bandwidth cap, estimate and metered flag and asks for original quality
//     so a validated direct route wins when one exists; "auto" changes
//     nothing.
//   - MaxBitrateKbps lowers the request's bandwidth cap (never raises it).
//   - VideoCodec / AudioCodec / Container restrict the advertised decode
//     capabilities to the named codec or container, which decides what counts
//     as directly playable. The transcode recipe itself stays the server's.
type PlanOverridesV3 struct {
	Transcode      string
	MaxBitrateKbps int
	VideoCodec     string
	AudioCodec     string
	Container      string
}

const (
	PlanOverrideTranscodeAuto   = "auto"
	PlanOverrideTranscodeForce  = "force"
	PlanOverrideTranscodeDirect = "direct"
)

// IsZero reports whether the overrides change nothing.
func (o PlanOverridesV3) IsZero() bool {
	return (o.Transcode == "" || o.Transcode == PlanOverrideTranscodeAuto) &&
		o.MaxBitrateKbps <= 0 && o.VideoCodec == "" && o.AudioCodec == "" && o.Container == ""
}

// ForceTranscode reports whether the planner must take the adapted route.
func (o PlanOverridesV3) ForceTranscode() bool {
	return o.Transcode == PlanOverrideTranscodeForce
}

// ApplyPlanOverridesV3 returns a copy of request narrowed by the overrides.
// The input is never mutated: the durable attempt record keeps the client's
// own request.
func ApplyPlanOverridesV3(request StartRequestV3, o PlanOverridesV3) StartRequestV3 {
	if o.IsZero() {
		return request
	}
	out := request
	caps := &out.Capabilities
	caps.CodecsVideo = append([]string(nil), request.Capabilities.CodecsVideo...)
	caps.CodecsVideoHardware = append([]string(nil), request.Capabilities.CodecsVideoHardware...)
	caps.CodecsAudio = append([]string(nil), request.Capabilities.CodecsAudio...)
	caps.Containers = append([]string(nil), request.Capabilities.Containers...)
	caps.VideoDecode = append([]VideoDecodeCapabilityV3(nil), request.Capabilities.VideoDecode...)

	if o.Transcode == PlanOverrideTranscodeDirect {
		out.BandwidthCapKbps = nil
		out.BandwidthEstimateKbps = nil
		out.Metered = false
		out.QualityPreference = QualityOriginalV3
	}
	if o.MaxBitrateKbps > 0 {
		capKbps := o.MaxBitrateKbps
		if existing := optionalValueV3(request.BandwidthCapKbps); existing > 0 && existing < capKbps {
			capKbps = existing
		}
		out.BandwidthCapKbps = &capKbps
	}
	if codec := strings.ToLower(strings.TrimSpace(o.VideoCodec)); codec != "" {
		caps.CodecsVideo = keepFoldV3(caps.CodecsVideo, codec)
		caps.CodecsVideoHardware = keepFoldV3(caps.CodecsVideoHardware, codec)
		kept := caps.VideoDecode[:0]
		for _, entry := range caps.VideoDecode {
			if strings.EqualFold(strings.TrimSpace(entry.Codec), codec) {
				kept = append(kept, entry)
			}
		}
		caps.VideoDecode = kept
	}
	if codec := strings.ToLower(strings.TrimSpace(o.AudioCodec)); codec != "" {
		caps.CodecsAudio = keepFoldV3(caps.CodecsAudio, codec)
	}
	if container := strings.ToLower(strings.TrimSpace(o.Container)); container != "" {
		caps.Containers = keepFoldV3(caps.Containers, container)
	}
	return out
}

func keepFoldV3(values []string, wanted string) []string {
	kept := values[:0]
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			kept = append(kept, value)
		}
	}
	return kept
}
