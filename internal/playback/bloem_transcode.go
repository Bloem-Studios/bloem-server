package playback

// Encoder latency presets for TranscodeOpts.EncoderPreset (Live TV).
const (
	EncoderPresetLowLatency = "low_latency"
	EncoderPresetBalanced   = "balanced"
	EncoderPresetQuality    = "quality"
)

// encoderPresetOverride maps an explicit EncoderPreset to the CPU encoder
// preset videoPreset returns. ok is false when upstream's choice applies.
func encoderPresetOverride(opts TranscodeOpts) (string, bool) {
	switch opts.EncoderPreset {
	case EncoderPresetLowLatency:
		return "ultrafast", true
	case EncoderPresetBalanced:
		return "veryfast", true
	}
	return "", false
}

func nvencPresetArgs(preset string) []string {
	switch preset {
	case EncoderPresetLowLatency:
		return []string{"-preset", "p2", "-tune", "ll"}
	case EncoderPresetBalanced:
		return []string{"-preset", "p4"}
	default:
		return nil
	}
}

func x264LatencyArgs(preset string) []string {
	if preset == EncoderPresetLowLatency {
		return []string{"-tune", "zerolatency"}
	}
	return nil
}
