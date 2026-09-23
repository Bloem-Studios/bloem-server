package config

// Shared server-setting keys used by playback and prepared-download policy
// readers. Keep them here with the effective admin-setting defaults.
const (
	Allow4KTranscodeSettingKey = "allow_4k_transcode"

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
	DownloadLocalTranscodeFallbackSettingKey     = "download.local_transcode_fallback"
)
