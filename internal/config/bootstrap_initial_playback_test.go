package config

import (
	"reflect"
	"testing"
)

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
		t.Fatal("worker mode accepted initial runtime")
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
