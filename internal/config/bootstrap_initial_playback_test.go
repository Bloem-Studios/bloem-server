package config

import (
	"reflect"
	"testing"
)

func TestInitialPlaybackAPIOrigin(t *testing.T) {
	for _, raw := range []string{"", "https://api.example.test", "http://api.example.test:8080/"} {
		if _, err := initialPlaybackAPIOrigin(raw); err != nil {
			t.Fatalf("origin %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"api.example.test", "//api.example.test", "ftp://api.example.test", "https://user:secret@api.example.test", "https://api.example.test/prefix", "https://api.example.test/?q=x", "https://api.example.test/?", "https://api.example.test/#", "https://api.example.test/%2f", " https://api.example.test"} {
		if _, err := initialPlaybackAPIOrigin(raw); err == nil {
			t.Fatalf("invalid origin accepted: %q", raw)
		}
	}
}

func TestInitialPlaybackBootstrap(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, accounts string
		want                    bool
		ids                     []int
		invalid                 bool
	}{
		{name: "default off"}, {name: "explicit off", enabled: "false"},
		{name: "on", enabled: "true", accounts: "9, 12", want: true, ids: []int{9, 12}},
		{name: "typo", enabled: "TRUE", invalid: true}, {name: "numeric", enabled: "1", invalid: true},
		{name: "missing scope", enabled: "true", invalid: true},
		{name: "off scope", accounts: "9", invalid: true},
		{name: "duplicate", enabled: "true", accounts: "9,9", invalid: true},
		{name: "zero", enabled: "true", accounts: "0", invalid: true},
		{name: "negative", enabled: "true", accounts: "-1", invalid: true},
		{name: "trailing comma", enabled: "true", accounts: "9,", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, ids, err := initialPlaybackBootstrap(tc.enabled, tc.accounts)
			if (err != nil) != tc.invalid {
				t.Fatalf("error=%v, invalid=%v", err, tc.invalid)
			}
			if err == nil && (enabled != tc.want || !reflect.DeepEqual(ids, tc.ids)) {
				t.Fatalf("enabled=%v ids=%v", enabled, ids)
			}
		})
	}
}

func TestLoadBootstrapInitialPlayback(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example.invalid/test")
	t.Setenv("SECRET_KEY", "01234567890123456789012345678901")
	t.Setenv("SILO_INITIAL_PLAYBACK_ENABLED", "true")
	t.Setenv("SILO_INITIAL_PLAYBACK_RECONCILE_ACCOUNTS", "9")
	t.Setenv("MODE", "proxy")
	if _, err := LoadBootstrap(""); err == nil {
		t.Fatal("worker mode accepted account reconciliation")
	}
	t.Setenv("MODE", "integrated")
	cfg, err := LoadBootstrap("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InitialPlaybackEnabled || !reflect.DeepEqual(cfg.InitialPlaybackReconcileAccounts, []int{9}) {
		t.Fatalf("bootstrap=%+v", cfg.InitialPlaybackReconcileAccounts)
	}
}

func TestInitialNodePlaybackBootstrap(t *testing.T) {
	for _, mode := range []string{"proxy", "transcode"} {
		enabled, accounts, err := initialPlaybackBootstrapForMode(mode, "true", "")
		if err != nil || !enabled || len(accounts) != 0 {
			t.Fatalf("mode=%s enabled=%v accounts=%v err=%v", mode, enabled, accounts, err)
		}
		if enabled, _, err := initialPlaybackBootstrapForMode(mode, "", ""); err != nil || enabled {
			t.Fatalf("mode=%s opted in by default", mode)
		}
		if _, _, err := initialPlaybackBootstrapForMode(mode, "true", "1"); err == nil {
			t.Fatalf("mode=%s accepted reconciliation scope", mode)
		}
	}
	for _, mode := range []string{"api", "integrated", "invalid"} {
		if _, _, err := initialPlaybackBootstrapForMode(mode, "true", ""); err == nil {
			t.Fatalf("mode=%s accepted missing scope", mode)
		}
	}
}
