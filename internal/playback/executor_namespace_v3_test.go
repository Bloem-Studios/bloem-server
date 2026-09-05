package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

func executorFixture() ExecutorNamespaceV3 {
	return ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
}
func TestExecutorNamespaceValidation(t *testing.T) {
	valid := executorFixture()
	for name, mutate := range map[string]func(*ExecutorNamespaceV3){
		"zero epoch":     func(n *ExecutorNamespaceV3) { n.Epoch = 0 },
		"negative epoch": func(n *ExecutorNamespaceV3) { n.Epoch = -1 },
		"path":           func(n *ExecutorNamespaceV3) { n.ExecutorID = "../escape" },
		"uppercase":      func(n *ExecutorNamespaceV3) { n.Incarnation = strings.ToUpper(n.Incarnation) },
		"nil UUID":       func(n *ExecutorNamespaceV3) { n.ExecutorID = uuid.Nil.String() },
		"URN":            func(n *ExecutorNamespaceV3) { n.ExecutorID = "urn:uuid:" + n.ExecutorID },
	} {
		t.Run(name, func(t *testing.T) {
			n := valid
			mutate(&n)
			if _, err := n.OutputDir(t.TempDir()); !errors.Is(err, ErrInvalidExecutorNamespace) {
				t.Fatalf("invalid namespace accepted: %v", err)
			}
		})
	}
	subdir, err := valid.OutputSubdir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("_authority", valid.Incarnation, "1", valid.ExecutorID)
	if subdir != want {
		t.Fatalf("subdir=%s", subdir)
	}
}
func TestExecutorNamespaceRecipeAndClaims(t *testing.T) {
	namespace := executorFixture()
	card := NewRecipeCard(1, "profile", 3, "", TranscodeOpts{SessionID: "session", TargetCodecVideo: "libx264", Executor: &namespace})
	claims := card.ToClaims()
	if !claims.ExecutorBound {
		t.Fatal("binding marker lost")
	}
	token, err := streamtoken.Sign(claims, "test-secret", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := streamtoken.Verify(token, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	roundtrip := RecipeCardFromClaims(verified)
	if !reflect.DeepEqual(roundtrip.Executor, &namespace) || !reflect.DeepEqual(roundtrip.TranscodeOpts("output", "ffmpeg", nil).Executor, &namespace) {
		t.Fatal("executor binding lost")
	}
	for _, claims := range []streamtoken.Claims{{ExecutorBound: true}, {ExecutorID: namespace.ExecutorID}, {ExecutorBound: true, ExecutorIncarnation: namespace.Incarnation, ExecutorEpoch: -1, ExecutorID: namespace.ExecutorID}} {
		decoded := RecipeCardFromClaims(&claims)
		if decoded.Executor == nil || ValidateCopyFMP4RecipeCard(decoded) == nil {
			t.Fatal("malformed binding downgraded")
		}
		if _, err := streamtoken.Sign(claims, "test-secret", time.Minute); err == nil {
			t.Fatal("signed malformed binding")
		}
	}
}
func executorTestBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	// Real subprocess, kept alive by its stdin pipe until session cancellation.
	script := `#!/bin/sh
printf '#EXTM3U
#EXT-X-TARGETDURATION:6
#EXTINF:6,
seg_00000.ts
#EXTINF:6,
seg_00001.ts
#EXTINF:6,
seg_00002.ts
' > stream.m3u8
printf 'segment' > seg_00000.ts
printf 'segment' > seg_00001.ts
printf 'segment' > seg_00002.ts
exec cat >/dev/null
`

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestExecutorNamespaceExclusiveSpawnAndStaleCleanup(t *testing.T) {
	root := t.TempDir()
	binary := executorTestBinary(t)
	first := executorFixture()
	second := first
	second.ExecutorID = uuid.NewString()
	dir1, _ := first.OutputDir(root)
	dir2, _ := second.OutputDir(root)
	opts := TranscodeOpts{ExecuteGrants: executorGrantTestProvider(nil), SessionID: "session", OutputDir: dir1, FFmpegPath: binary, TargetCodecVideo: "libx264", FastStart: true, Executor: &first}
	session1, err := StartTranscode(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session1.Close() })
	if _, err := session1.WaitForManifest(time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := StartTranscode(t.Context(), opts); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("duplicate spawn=%v", err)
	}
	if err := session1.Restart(t.Context(), 10, 2); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("bound restart=%v", err)
	}
	if !session1.IsRunning() {
		t.Fatal("rejected restart stopped live process")
	}
	opts.Executor = &second
	opts.OutputDir = dir2
	session2, err := StartTranscode(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session2.Close() })
	if _, err := session2.WaitForManifest(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := session1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session1.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "seg_00000.ts")); err != nil {
		t.Fatalf("stale cleanup removed successor: %v", err)
	}
	opts.Executor = &first
	opts.OutputDir = dir1
	if _, err := StartTranscode(t.Context(), opts); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("consumed namespace reused=%v", err)
	}
	if err := session2.CheckExecutorNamespace(nil); !errors.Is(err, ErrExecutorNamespaceMismatch) {
		t.Fatalf("legacy downgrade=%v", err)
	}
	returned := session2.Opts()
	returned.Executor.Epoch++
	if err := session2.CheckExecutorNamespace(&second); err != nil {
		t.Fatalf("Opts leaked mutable binding: %v", err)
	}
}
func TestExecutorNamespaceConcurrentClaims(t *testing.T) {
	n := executorFixture()
	dir, _ := n.OutputDir(t.TempDir())
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { results <- claimExecutorOutput(dir, n) })
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrExecutorReplacementRequired) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("claim winners=%d", wins)
	}
}
func TestExecutorNamespaceReconstructionExactAndFenced(t *testing.T) {
	root := t.TempDir()
	namespace := executorFixture()
	manager := NewTranscodeManager()
	manager.ExecuteGrants = executorGrantTestProvider(nil)
	manager.Config = func() TranscodeRuntimeConfig {
		return TranscodeRuntimeConfig{TranscodeDir: root, FFmpegPath: executorTestBinary(t)}
	}
	card := NewRecipeCard(1, "profile", 3, "", TranscodeOpts{SessionID: "session", TargetCodecVideo: "libx264", Executor: &namespace})
	card.OutputSubdir = "session/legacy"
	manager.ResolveExecutorRecipe = func(context.Context, string, ExecutorNamespaceV3) (*RecipeCard, error) {
		return &card, nil
	}
	session, err := manager.ReconstructTranscodeWithError(t.Context(), "session", -1, card)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("missing reconstructed session")
	}
	t.Cleanup(func() { _ = session.Close() })
	exact, _ := namespace.OutputDir(root)
	if session.Opts().OutputDir != exact {
		t.Fatalf("reconstructed path=%s", session.Opts().OutputDir)
	}
	legacy := card
	legacy.Executor = nil
	if _, err := manager.ReconstructTranscodeWithError(t.Context(), "session", -1, legacy); !errors.Is(err, ErrExecutorNamespaceMismatch) {
		t.Fatalf("legacy reuse=%v", err)
	}
	other := card
	replacement := namespace
	replacement.ExecutorID = uuid.NewString()
	other.Executor = &replacement
	if _, err := manager.ReconstructTranscodeWithError(t.Context(), "session", -1, other); !errors.Is(err, ErrExecutorNamespaceMismatch) {
		t.Fatalf("wrong executor reuse=%v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	otherManager := NewTranscodeManager()
	otherManager.Config = manager.Config
	otherManager.ExecuteGrants = manager.ExecuteGrants
	otherManager.ResolveExecutorRecipe = manager.ResolveExecutorRecipe
	if _, err := otherManager.ReconstructTranscodeWithError(t.Context(), "session", -1, card); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("crash namespace reuse=%v", err)
	}
}
