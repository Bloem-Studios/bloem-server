package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDBReadsBloemLANAndLiveTV(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{
		"lan.advertisement_enabled": "true",
		"livetv.dvr_path":           "/srv/dvr",
		"livetv.max_transcodes":     "7",
		"livetv.hw_accel":           "vaapi",
		"livetv.play_method":        "copy",
	})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if !cfg.LAN.AdvertisementEnabled {
		t.Error("lan.advertisement_enabled not loaded")
	}
	if cfg.LiveTV.DVRPath != "/srv/dvr" || cfg.LiveTV.MaxTranscodes != 7 ||
		cfg.LiveTV.HWAccel != "vaapi" || cfg.LiveTV.PlayMethod != "copy" {
		t.Errorf("live TV settings not loaded: %+v", cfg.LiveTV)
	}
	if cfg.LiveTV.EncoderPreset != DefaultLiveTVEncoderPreset {
		t.Errorf("encoder preset default = %q", cfg.LiveTV.EncoderPreset)
	}

	if _, err := LoadFromDB(map[string]string{"livetv.max_transcodes": "many"}); err == nil {
		t.Error("invalid livetv.max_transcodes accepted")
	}
	if _, err := LoadFromDB(map[string]string{"lan.advertisement_enabled": "maybe"}); err == nil {
		t.Error("invalid lan.advertisement_enabled accepted")
	}
}

func TestLoadFromDBBloemDefaults(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if cfg.LAN.AdvertisementEnabled {
		t.Error("LAN advertisement must default off")
	}
	if cfg.LiveTV != defaultLiveTVConfig() {
		t.Errorf("live TV defaults = %+v, want %+v", cfg.LiveTV, defaultLiveTVConfig())
	}
}

func TestYAMLToSettingsMapImportsLiveTV(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	m, err := YAMLToSettingsMap(write("with.yaml", "livetv:\n  hw_accel: nvenc\n  max_transcodes: 5\n  dvr_path: /data/dvr\n"))
	if err != nil {
		t.Fatalf("YAMLToSettingsMap: %v", err)
	}
	for key, want := range map[string]string{
		"livetv.hw_accel":       "nvenc",
		"livetv.max_transcodes": "5",
		"livetv.dvr_path":       "/data/dvr",
		"livetv.play_method":    DefaultLiveTVPlayMethod,
	} {
		if m[key] != want {
			t.Errorf("%s = %q, want %q", key, m[key], want)
		}
	}

	m, err = YAMLToSettingsMap(write("without.yaml", "server:\n  listen: \":8080\"\n"))
	if err != nil {
		t.Fatalf("YAMLToSettingsMap: %v", err)
	}
	if m["livetv.hw_accel"] != DefaultLiveTVHWAccel || m["livetv.max_transcodes"] != "3" {
		t.Errorf("live TV defaults not imported: hw_accel=%q max_transcodes=%q", m["livetv.hw_accel"], m["livetv.max_transcodes"])
	}

	if _, err := YAMLToSettingsMap(write("bad.yaml", "livetv:\n  max_transcodes: lots\n")); err == nil {
		t.Error("invalid livetv.max_transcodes accepted")
	}
}

func TestNormalizeAdminSettingValidatesUserDBKeysLikeSilo(t *testing.T) {
	if _, err := NormalizeAdminSetting("s3.user_db_path_style", "sideways"); err == nil {
		t.Error("non-boolean s3.user_db_path_style accepted; LoadFromDB would then fail at startup")
	}
	if got, err := NormalizeAdminSetting("s3.user_db_path_style", "TRUE"); err != nil || got != "true" {
		t.Errorf("s3.user_db_path_style TRUE = %q, %v", got, err)
	}
}
