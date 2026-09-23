package config

// LiveTVConfig holds Live TV / OTA / DVR delivery settings.
type LiveTVConfig struct {
	DVRPath       string `yaml:"dvr_path"`
	MaxTranscodes int    `yaml:"max_transcodes"`
	HWAccel       string `yaml:"hw_accel"`
	HWDecode      string `yaml:"hw_decode"`
	EncoderPreset string `yaml:"encoder_preset"`
	FrameRateCap  string `yaml:"framerate_cap"`
	MaxResolution string `yaml:"max_resolution"`
	PlayMethod    string `yaml:"play_method"`
}

const (
	DefaultLiveTVDVRPath       = "/var/lib/bloem/dvr"
	DefaultLiveTVMaxTranscodes = 3
	DefaultLiveTVHWAccel       = "auto"
	DefaultLiveTVHWDecode      = "auto"
	DefaultLiveTVEncoderPreset = "low_latency"
	DefaultLiveTVFrameRateCap  = "source"
	DefaultLiveTVMaxResolution = "source"
	DefaultLiveTVPlayMethod    = "auto"
)

// LANConfig holds local-network advertisement settings. mDNS registration
// needs the host's L2 broadcast domain, which is why the advertiser is
// opt-in: bridge-networked compose and most LXC deployments cannot reach it.
type LANConfig struct {
	// AdvertisementEnabled gates the _bloem._tcp mDNS advertiser. Off by
	// default; an operator on a shared or hostile network keeps the
	// server silent on the LAN.
	AdvertisementEnabled bool `yaml:"-"`
}
