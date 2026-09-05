package playback

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupOrphanedTranscodeDirsPreservesExecutorNamespace(t *testing.T) {
	root := t.TempDir()
	namespace := executorFixture()
	output, err := namespace.OutputDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := claimExecutorOutput(output, namespace); err != nil {
		t.Fatal(err)
	}
	segment := filepath.Join(output, "seg_00000.m4s")
	if err := os.WriteFile(segment, []byte("live output"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "legacy-orphan")
	if err := os.Mkdir(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := CleanupOrphanedTranscodeDirs(root, nil, 0)
	if err != nil || removed != 1 {
		t.Fatalf("cleanup = %d, %v; want one legacy orphan", removed, err)
	}
	if data, err := os.ReadFile(segment); err != nil || string(data) != "live output" {
		t.Fatalf("authority output changed: %q, %v", data, err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy orphan remains: %v", err)
	}
	if err := removeExecutorOutput(output, namespace); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupOrphanedTranscodeDirs(root, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := claimExecutorOutput(output, namespace); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("cleanup lost consumed executor claim: %v", err)
	}
}

func TestCleanupOrphanedTranscodeDirsPreservesPlanScopedActiveDirectory(t *testing.T) {
	root := t.TempDir()
	active := "session-1-plan-abc-generation"
	orphan := "session-2-plan-def-generation"
	for _, name := range []string{active, orphan} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := CleanupOrphanedTranscodeDirs(root, map[string]struct{}{"session-1": {}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(root, active)); err != nil {
		t.Fatalf("active plan directory was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, orphan)); !os.IsNotExist(err) {
		t.Fatalf("orphan directory still exists: %v", err)
	}
}

// An active session owns a dir only when the name is the session id exactly or
// the id followed by the generation separator ('-', see transportGenerationV3).
// A foreign session whose id merely shares the active id as a raw string
// prefix must not have its dirs retained.
func TestCleanupOrphanedTranscodeDirsRequiresSeparatorBoundary(t *testing.T) {
	root := t.TempDir()
	keep := []string{"session-1", "session-1-plan-abc"}
	reap := []string{"session-10", "session-10-plan-def", "session-1extra"}
	for _, name := range append(append([]string{}, keep...), reap...) {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := CleanupOrphanedTranscodeDirs(root, map[string]struct{}{"session-1": {}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if removed != len(reap) {
		t.Fatalf("removed = %d, want %d", removed, len(reap))
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("active session directory %q was removed: %v", name, err)
		}
	}
	for _, name := range reap {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("foreign directory %q still exists: %v", name, err)
		}
	}
}
