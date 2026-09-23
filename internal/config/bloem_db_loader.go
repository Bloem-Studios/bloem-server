package config

import "strconv"

// loadBloemLANConfig reads the LAN service advertisement toggle. Read at
// startup only: the mDNS service is registered when the process starts and
// deregistered when it stops.
func loadBloemLANConfig(m map[string]string, cfg *Config) error {
	lanAdvertise, err := boolOr(m, "lan.advertisement_enabled", false)
	if err != nil {
		return err
	}
	cfg.LAN.AdvertisementEnabled = lanAdvertise
	return nil
}

// loadBloemLiveTVConfig reads the Live TV settings.
func loadBloemLiveTVConfig(m map[string]string, cfg *Config) error {
	cfg.LiveTV.DVRPath = stringOr(m, "livetv.dvr_path", DefaultLiveTVDVRPath)
	liveTVMaxTranscodes, err := intOr(m, "livetv.max_transcodes", DefaultLiveTVMaxTranscodes)
	if err != nil {
		return err
	}
	cfg.LiveTV.MaxTranscodes = liveTVMaxTranscodes
	cfg.LiveTV.HWAccel = stringOr(m, "livetv.hw_accel", DefaultLiveTVHWAccel)
	cfg.LiveTV.HWDecode = stringOr(m, "livetv.hw_decode", DefaultLiveTVHWDecode)
	cfg.LiveTV.EncoderPreset = stringOr(m, "livetv.encoder_preset", DefaultLiveTVEncoderPreset)
	cfg.LiveTV.FrameRateCap = stringOr(m, "livetv.framerate_cap", DefaultLiveTVFrameRateCap)
	cfg.LiveTV.MaxResolution = stringOr(m, "livetv.max_resolution", DefaultLiveTVMaxResolution)
	cfg.LiveTV.PlayMethod = stringOr(m, "livetv.play_method", DefaultLiveTVPlayMethod)
	return nil
}

// setBloemLiveTVSettings maps the YAML livetv block onto settings keys.
func setBloemLiveTVSettings(m map[string]string, liveTV LiveTVConfig) {
	setIfNonEmpty(m, "livetv.dvr_path", liveTV.DVRPath)
	if liveTV.MaxTranscodes != 0 {
		m["livetv.max_transcodes"] = strconv.Itoa(liveTV.MaxTranscodes)
	}
	setIfNonEmpty(m, "livetv.hw_accel", liveTV.HWAccel)
	setIfNonEmpty(m, "livetv.hw_decode", liveTV.HWDecode)
	setIfNonEmpty(m, "livetv.encoder_preset", liveTV.EncoderPreset)
	setIfNonEmpty(m, "livetv.framerate_cap", liveTV.FrameRateCap)
	setIfNonEmpty(m, "livetv.max_resolution", liveTV.MaxResolution)
	setIfNonEmpty(m, "livetv.play_method", liveTV.PlayMethod)
}
