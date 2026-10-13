package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadFromDBMetadataPresignExpiry(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB() returned error: %v", err)
	}
	if cfg.S3.MetadataPresignExpiry != 4*time.Hour {
		t.Fatalf("default metadata presign expiry = %s, want 4h", cfg.S3.MetadataPresignExpiry)
	}

	cfg, err = LoadFromDB(map[string]string{"s3.metadata_presign_expiry": "90m"})
	if err != nil {
		t.Fatalf("LoadFromDB() with metadata expiry returned error: %v", err)
	}
	if cfg.S3.MetadataPresignExpiry != 90*time.Minute {
		t.Fatalf("configured metadata presign expiry = %s, want 90m", cfg.S3.MetadataPresignExpiry)
	}
}

func TestLoadFromDBMetadataPresignExpiryRejectsInvalidDuration(t *testing.T) {
	_, err := LoadFromDB(map[string]string{"s3.metadata_presign_expiry": "soon"})
	if err == nil {
		t.Fatal("LoadFromDB() error = nil, want invalid duration error")
	}
	if !strings.Contains(err.Error(), "s3.metadata_presign_expiry") {
		t.Fatalf("LoadFromDB() error = %v, want key name", err)
	}
}

func TestLoadFromDBRejectsInvalidSegmentRetention(t *testing.T) {
	for _, value := range []string{"-1", "119", "86401"} {
		t.Run(value, func(t *testing.T) {
			_, err := LoadFromDB(map[string]string{playbackSegmentRetentionSettingKey: value})
			if err == nil || !strings.Contains(err.Error(), playbackSegmentRetentionSettingKey) {
				t.Fatalf("LoadFromDB() error = %v, want retention bounds error", err)
			}
		})
	}
}

func TestLoadFromDBDownloadArtifactDirRequiresAbsolutePath(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{downloadArtifactDirSettingKey: "/mnt/silo-downloads"})
	if err != nil {
		t.Fatalf("LoadFromDB() with absolute artifact dir: %v", err)
	}
	if cfg.Download.ArtifactDir != "/mnt/silo-downloads" {
		t.Fatalf("artifact dir = %q", cfg.Download.ArtifactDir)
	}

	_, err = LoadFromDB(map[string]string{downloadArtifactDirSettingKey: "relative/downloads"})
	if err == nil || !strings.Contains(err.Error(), downloadArtifactDirSettingKey) {
		t.Fatalf("relative artifact dir error = %v", err)
	}
}

func TestLoadFromDBPolicyEditorEnabledDefaultsFalse(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB() returned error: %v", err)
	}
	if cfg.Policy.EditorEnabled {
		t.Fatal("Policy.EditorEnabled = true, want default false")
	}

	cfg, err = LoadFromDB(map[string]string{"policy.editor_enabled": "true"})
	if err != nil {
		t.Fatalf("LoadFromDB() returned error: %v", err)
	}
	if !cfg.Policy.EditorEnabled {
		t.Fatal("Policy.EditorEnabled = false, want configured true")
	}

	_, err = LoadFromDB(map[string]string{"policy.editor_enabled": "maybe"})
	if err == nil {
		t.Fatal("LoadFromDB() error = nil, want invalid bool error")
	}
	if !strings.Contains(err.Error(), "policy.editor_enabled") {
		t.Fatalf("LoadFromDB() error = %v, want key name", err)
	}
}

func TestLoadFromDBScannerRealtimeMonitoringDefaultsTrue(t *testing.T) {
	cfg, err := LoadFromDB(map[string]string{})
	if err != nil {
		t.Fatalf("LoadFromDB() returned error: %v", err)
	}
	if !cfg.Scanner.RealtimeMonitoring {
		t.Fatal("Scanner.RealtimeMonitoring = false, want default true")
	}

	cfg, err = LoadFromDB(map[string]string{"scanner.realtime_monitoring": "false"})
	if err != nil {
		t.Fatalf("LoadFromDB() returned error: %v", err)
	}
	if cfg.Scanner.RealtimeMonitoring {
		t.Fatal("Scanner.RealtimeMonitoring = true, want configured false")
	}

	_, err = LoadFromDB(map[string]string{"scanner.realtime_monitoring": "maybe"})
	if err == nil {
		t.Fatal("LoadFromDB() error = nil, want invalid bool error")
	}
	if !strings.Contains(err.Error(), "scanner.realtime_monitoring") {
		t.Fatalf("LoadFromDB() error = %v, want key name", err)
	}
}

// The Audiobookshelf-compatible API is reached through the compatibility
// gateway on the server's own address by default now, so the dedicated
// :13378 listener is no longer implied by the feature being enabled — it
// stays empty unless an operator opts in with an explicit
// audiobookshelf_compat.listen value.
// The Jellyfin-compatible API is reached through the compatibility gateway
// on the server's own address by default now, so the dedicated :8096
// listener is no longer implied by the feature being enabled — Enabled
// still legacy-defaults to true (the compat API itself is on), but Listen
// stays empty unless an operator opts in with an explicit
// jellyfin_compat.listen value.
func TestYAMLToSettingsMapPreservesExplicitlyDisabledSegmentRetention(t *testing.T) {
	m := yamlSettingsMapFromString(t, `
playback:
  segment_retention_seconds: 0
`)
	if got := m[playbackSegmentRetentionSettingKey]; got != "0" {
		t.Fatalf("segment retention = %q, want explicit disable", got)
	}
}

func TestYAMLToSettingsMapScannerRealtimeMonitoring(t *testing.T) {
	m := yamlSettingsMapFromString(t, `server:
  mode: integrated
`)
	if got := m["scanner.realtime_monitoring"]; got != "true" {
		t.Fatalf("scanner.realtime_monitoring = %q, want default true", got)
	}

	m = yamlSettingsMapFromString(t, `
scanner:
  realtime_monitoring: false
`)
	if got := m["scanner.realtime_monitoring"]; got != "false" {
		t.Fatalf("scanner.realtime_monitoring = %q, want explicit false", got)
	}
}

func yamlSettingsMapFromString(t *testing.T, body string) map[string]string {
	t.Helper()
	path := t.TempDir() + "/silo.yaml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() returned error: %v", err)
	}
	m, err := YAMLToSettingsMap(path)
	if err != nil {
		t.Fatalf("YAMLToSettingsMap() returned error: %v", err)
	}
	return m
}
