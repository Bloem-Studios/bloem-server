package config

import "os"

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

// defaultLiveTVConfig is setDefaults' Live TV block.
func defaultLiveTVConfig() LiveTVConfig {
	return LiveTVConfig{
		DVRPath:       DefaultLiveTVDVRPath,
		MaxTranscodes: DefaultLiveTVMaxTranscodes,
		HWAccel:       DefaultLiveTVHWAccel,
		HWDecode:      DefaultLiveTVHWDecode,
		EncoderPreset: DefaultLiveTVEncoderPreset,
		FrameRateCap:  DefaultLiveTVFrameRateCap,
		MaxResolution: DefaultLiveTVMaxResolution,
		PlayMethod:    DefaultLiveTVPlayMethod,
	}
}

// bloemJellyfinCompatListenDefault is the jellyfin_compat.listen default.
// The Jellyfin-compatible API is reached through the compatibility gateway
// on the server's own address by default. A dedicated listener on :8096 is
// still available, but is an explicit opt-in via jellyfin_compat.listen
// rather than the default.
const bloemJellyfinCompatListenDefault = ""

// bloemABSCompatListen returns the Audiobookshelf-compatible listener
// address. The ABS-compatible API is reached through the compatibility
// gateway on the server's own address by default; a dedicated listener is
// an explicit opt-in (audiobookshelf_compat.listen), so this defaults to
// empty rather than :13378 even when the feature itself is enabled.
func bloemABSCompatListen(m map[string]string) string {
	return stringOr(m, "audiobookshelf_compat.listen", "")
}

// bloemBootstrapJFListen returns BootstrapConfig.JFListen. JF_PORT is an
// explicit opt-in to a dedicated Jellyfin-compatibility listener. Unset by
// default: the Jellyfin-compatible API is reached through the compatibility
// gateway on the server's own address, and an empty JFListen means "no
// bootstrap override" (see its use in cmd/silo/main.go), leaving the
// DB-configured jellyfin_compat.listen value — itself empty by default — in
// place. jfPort is the upstream-defaulted port and is ignored when JF_PORT
// is unset.
func bloemBootstrapJFListen(jfPort string) string {
	if os.Getenv("JF_PORT") == "" {
		return ""
	}
	return ":" + jfPort
}

func init() {
	// LAN advertisement registers and deregisters with the process: the
	// service is announced at startup and gets its goodbye packet at
	// shutdown, so a toggle needs a restart to take effect.
	restartRequiredKeys["lan.advertisement_enabled"] = true
	// The dedicated ABS-compatible listener is bound at startup.
	restartRequiredKeys["audiobookshelf_compat.listen"] = true
}
