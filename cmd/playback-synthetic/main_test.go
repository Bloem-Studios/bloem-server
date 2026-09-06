package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRequiresExplicitSyntheticFlag(t *testing.T) {
	if err := run(t.Context(), options{}); err == nil {
		t.Fatal("missing synthetic acknowledgment accepted")
	}
}
func TestRejectsUnboundedLifetimeBeforeConnecting(t *testing.T) {
	for _, duration := range []int{0, -1, 3601} {
		if err := run(t.Context(), options{syntheticOnly: true, duration: duration}); err == nil {
			t.Fatalf("accepted duration %d", duration)
		}
	}
}
func TestBootstrapIsPrivateAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	want := bootstrap{URL: "http://127.0.0.1:1234", Username: "synthetic", Password: "private", FileID: "42", ProfileID: "profile", InstallationID: "installation", ContentID: "content", MediaPath: "synthetic.mp4"}
	if err := writeBootstrap(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("bootstrap permissions = %o", info.Mode().Perm())
	}
	if err = writeBootstrap(path, bootstrap{Password: "replacement"}); err == nil {
		t.Fatal("overwrote existing bootstrap")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got bootstrap
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("bootstrap changed after rejected overwrite")
	}
}
