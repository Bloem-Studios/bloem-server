package config

const (
	// PlaybackStrictReconstructAdmissionSettingKey makes playback-session
	// reconstruction fail CLOSED when the per-user limit provider cannot be
	// evaluated at all (as opposed to returning a genuine over-cap denial).
	//
	// Default "false" matches upstream Silo: a transient provider error admits
	// the session ungated rather than turning a recoverable dependency failure
	// into a permanent 404 mid-playback. Set "true" for the stricter posture,
	// where no session is ever admitted without its limits actually checked —
	// at the cost of refusing playback to within-limit users during a database
	// blip, which is exactly when a post-restart reconstruct wave happens.
	PlaybackStrictReconstructAdmissionSettingKey = "playback.strict_reconstruct_admission"
)

// bloemAdminSettingDefaults extends Silo's adminSettingDefaults.
var bloemAdminSettingDefaults = map[string]string{
	"playback.header_authenticated_media_mode":   "disabled",
	PlaybackStrictReconstructAdmissionSettingKey: "false",
	"livetv.dvr_path":       DefaultLiveTVDVRPath,
	"livetv.max_transcodes": "3",
	"livetv.hw_accel":       DefaultLiveTVHWAccel,
	"livetv.hw_decode":      DefaultLiveTVHWDecode,
	"livetv.encoder_preset": DefaultLiveTVEncoderPreset,
	"livetv.framerate_cap":  DefaultLiveTVFrameRateCap,
	"livetv.max_resolution": DefaultLiveTVMaxResolution,
	"livetv.play_method":    DefaultLiveTVPlayMethod,

	// LAN service advertisement (_bloem._tcp mDNS). Off by default: it needs
	// the host's L2 broadcast domain, and operators on shared networks may
	// not want the server announcing itself.
	"lan.advertisement_enabled": "false",
}

func init() {
	for key, value := range bloemAdminSettingDefaults {
		adminSettingDefaults[key] = value
	}
}

// normalizeBloemAdminSetting validates Bloem-owned admin settings before
// Silo's NormalizeAdminSetting switch. The per-user SQLite S3 replica keys
// were removed from Bloem, so they fall through unvalidated exactly like any
// other unknown key.
func normalizeBloemAdminSetting(key, raw, value string) (string, bool, error) {
	var (
		normalized string
		err        error
	)
	switch key {
	case "s3.user_db_path_style", "s3.user_db_endpoint":
		return raw, true, nil
	case PlaybackStrictReconstructAdmissionSettingKey, "lan.advertisement_enabled":
		normalized, err = normalizeAdminBool(key, value)
	case "livetv.max_transcodes":
		normalized, err = normalizeAdminInt(key, value, -1, 1024)
	case "livetv.hw_accel":
		normalized, err = normalizeAdminEnum(key, value, "auto", "qsv", "vaapi", "nvenc", "videotoolbox", "none")
	case "livetv.hw_decode":
		normalized, err = normalizeAdminEnum(key, value, "auto", "on", "off")
	case "livetv.encoder_preset":
		normalized, err = normalizeAdminEnum(key, value, "low_latency", "balanced", "quality")
	case "livetv.framerate_cap":
		normalized, err = normalizeAdminEnum(key, value, "source", "60", "30")
	case "livetv.max_resolution":
		normalized, err = normalizeAdminEnum(key, value, "source", "1080p", "720p")
	case "livetv.play_method":
		normalized, err = normalizeAdminEnum(key, value, "auto", "copy", "transcode")
	case "playback.header_authenticated_media_mode":
		if value == "" {
			value = "disabled"
		}
		normalized, err = normalizeAdminEnum(key, value, "disabled", "single_or_affine")
	default:
		return "", false, nil
	}
	return normalized, true, err
}
