package storageplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var fixturePath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "storage-runtime-tests")
	if err != nil {
		panic(err)
	}
	fixturePath = filepath.Join(dir, "fixture")
	cmd := exec.Command("go", "build", "-o", fixturePath, "-race", "./testdata/nativefixture")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func snapshot(t *testing.T) Snapshot {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	value, _ := structpb.NewStruct(map[string]any{"token": "synthetic-source-secret"})
	return Snapshot{InstallationID: 1, Generation: 1, Enabled: true, NativeOnly: true, BinaryPath: fixturePath, ExpectedChecksum: digest, Manifest: &publicv1.PluginManifest{PluginId: "bloem.runtime.fixture", Version: "0.1.0", Checksum: digest, SiloApiVersion: "v1", SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}, Config: []*publicv1.ConfigEntry{{Key: "source", Value: value}}}
}
func manager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(Config{StartTimeout: 5 * time.Second, RPCDeadline: time.Second, HealthInterval: 20 * time.Millisecond, HealthFailureLimit: 2, RestartBackoff: 20 * time.Millisecond, MaxStartsPerGeneration: 2})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return m
}
func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process not reaped")
	}
	if s.command.ProcessState == nil || !s.process.Exited() {
		t.Fatal("process exit not reaped")
	}
}
func TestIsolatedConfigureAndConcurrentSingleLaunch(t *testing.T) {
	t.Setenv("BLOEM_STORAGE_TEST_SENTINEL", "synthetic-parent-secret")
	m := manager(t)
	req := snapshot(t)
	var wg sync.WaitGroup
	results := make(chan *Session, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); s, err := m.Ensure(context.Background(), req); results <- s; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *Session
	for s := range results {
		if first == nil {
			first = s
		}
		if s != first {
			t.Fatal("installation launched twice")
		}
	}
	d, err := first.Provider().Describe(context.Background(), &storagev1.DescribeRequest{})
	if err != nil || d.GetRevision() != 1 {
		t.Fatalf("isolated configured provider: %v %v", d, err)
	}
	m.Disable(req.InstallationID)
	waitDone(t, first)
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled generation relaunched: %v", err)
	}
}
func TestReplacementFencesAndCopiesSnapshot(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	one, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	original := proto.CloneOf(req.Config[0])
	req.Config[0].Value.Fields["token"] = structpb.NewStringValue("mutated")
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("changed config same generation accepted: %v", err)
	}
	req.Config[0] = original
	if unchanged, err := m.Ensure(context.Background(), req); err != nil || unchanged != one {
		t.Fatalf("caller mutation changed retained snapshot: %v", err)
	}
	req.Manifest.Version = "0.2.0"
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrSnapshotChanged) {
		t.Fatalf("changed manifest same generation accepted: %v", err)
	}
	req.Manifest.Version = "0.1.0"
	req.Generation = 2
	two, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, one)
	if one == two || one.Context().Err() == nil {
		t.Fatal("old session not fenced")
	}
	req.Generation = 1
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	if _, err := one.Provider().Stat(context.Background(), &storagev1.StatRequest{}); err == nil {
		t.Fatal("old provider remains usable")
	}
}
func TestRejectUnapprovedAndTamperedArtifact(t *testing.T) {
	for _, kind := range []string{"disabled", "unmarked", "checksum", "manifest", "platform", "reserved", "capability"} {
		t.Run(kind, func(t *testing.T) {
			m := manager(t)
			req := snapshot(t)
			switch kind {
			case "disabled":
				req.Enabled = false
			case "unmarked":
				req.NativeOnly = false
			case "checksum":
				req.ExpectedChecksum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "manifest":
				req.Manifest.PluginId = "bloem.wrong.identity"
			case "platform":
				req.Manifest.SupportedPlatforms[0].Arch = "wrong"
			case "reserved":
				req.Manifest.PluginId = "silo.builtin"
			case "capability":
				req.Manifest.Capabilities = []*publicv1.CapabilityDescriptor{{Type: "metadata_provider.v1", Id: "metadata"}}
			}
			if _, err := m.Ensure(context.Background(), req); err == nil {
				t.Fatal("unapproved/tampered artifact launched")
			}
		})
	}
}
func TestCanceledStartupAndRPCDeadline(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	req.Config[0].Value.Fields["block"] = structpb.NewBoolValue(true)
	started := filepath.Join(t.TempDir(), "configure-started")
	req.Config[0].Value.Fields["notify"] = structpb.NewStringValue(started)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := m.Ensure(ctx, req); result <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
waiting:
	for {
		select {
		case <-poll.C:
			if _, err := os.Stat(started); err == nil {
				break waiting
			}
		case err := <-result:
			t.Fatalf("startup ended before Configure blocked: %v", err)
		case <-deadline.C:
			t.Fatal("Configure was never entered")
		}
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("blocked startup accepted")
		}
	case <-deadline.C:
		t.Fatal("startup cancellation did not return")
	}
	m.mu.Lock()
	canceled := m.slots[req.InstallationID]
	m.mu.Unlock()
	<-canceled.done
	m.mu.Lock()
	canceled.nextRestart = time.Now().Add(time.Hour)
	m.mu.Unlock()
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrRestartBackoff) {
		t.Fatalf("canceled startup did not retain bounded retry: %v", err)
	}
	req.Generation = 2
	delete(req.Config[0].Value.Fields, "block")
	delete(req.Config[0].Value.Fields, "notify")
	s, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Provider().Stat(context.Background(), &storagev1.StatRequest{EntryId: "block"})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("unbounded RPC: %v", err)
	}
	stream, err := s.Provider().Read(context.Background(), &storagev1.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	m.Disable(req.InstallationID)
	if _, err := stream.Recv(); err == nil {
		t.Fatal("stream not canceled by disable")
	}
	waitDone(t, s)
}
func TestCrashHealthAndBoundedRestart(t *testing.T) {
	for _, entry := range []string{"crash", "unhealthy", "bad-revision"} {
		t.Run(entry, func(t *testing.T) {
			m := manager(t)
			req := snapshot(t)
			s, err := m.Ensure(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.Provider().Stat(context.Background(), &storagev1.StatRequest{EntryId: entry})
			waitDone(t, s)
			// Wait for the observable restart deadline, not a guessed sleep.
			m.mu.Lock()
			allowed := m.slots[req.InstallationID].nextRestart
			m.mu.Unlock()
			timer := time.NewTimer(time.Until(allowed))
			defer timer.Stop()
			<-timer.C
			second, err := m.Ensure(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = second.Provider().Stat(context.Background(), &storagev1.StatRequest{EntryId: "crash"})
			waitDone(t, second)
			if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrRestartLimit) {
				t.Fatalf("restart budget bypassed: %v", err)
			}
		})
	}
}
func TestShutdownClosesManagerAndReaps(t *testing.T) {
	m := manager(t)
	s, err := m.Ensure(context.Background(), snapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	if _, err := m.Ensure(context.Background(), snapshot(t)); !errors.Is(err, ErrClosed) {
		t.Fatalf("shutdown manager restarted: %v", err)
	}
}

func TestDisabledSnapshotRetainsGenerationFence(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	req.Generation = 5
	req.Enabled = false
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	req.Enabled = true
	req.Generation = 4
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("disabled generation lost: %v", err)
	}
	req.Generation = 5
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled generation reopened: %v", err)
	}
	req.Generation = 6
	resident, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Generation = 7
	req.Enabled = false
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	m.mu.Lock()
	done := m.slots[req.InstallationID].done
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled snapshot did not reap resident")
	}
	waitDone(t, resident)
}

func TestTamperedExecutableFailsChecksumVerification(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	b, err := os.ReadFile(req.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	req.BinaryPath = filepath.Join(t.TempDir(), "tampered")
	if err := os.WriteFile(req.BinaryPath, append(b, []byte("tampered")...), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(context.Background(), req); err == nil {
		t.Fatal("tampered executable accepted")
	}
}

func TestBorrowerCancellationDoesNotStopResident(t *testing.T) {
	m := manager(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := m.Ensure(ctx, snapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Provider().Describe(context.Background(), &storagev1.DescribeRequest{}); err != nil {
		t.Fatalf("resident inherited caller cancellation: %v", err)
	}
}

func TestCanceledReplacementRetainsReapingChain(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	oldCtx, oldCancel := context.WithCancel(context.Background())
	oldDone := make(chan struct{})
	oldReady := make(chan struct{})
	close(oldReady)
	// Hold the predecessor's reaping completion to exercise the shutdown barrier.
	m.slots[req.InstallationID] = &slot{snapshot: req, ctx: oldCtx, cancel: oldCancel, ready: oldReady, done: oldDone}
	req.Generation++
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Ensure(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replacement wait: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shutdownCancel()
	err := m.Shutdown(shutdownCtx)
	close(oldDone)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown bypassed predecessor reaping: %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFailedStartupBoundedCallerTriggeredRetry(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	req.Config = nil
	if _, err := m.Ensure(context.Background(), req); err == nil {
		t.Fatal("missing configuration accepted")
	}
	m.mu.Lock()
	failed := m.slots[req.InstallationID]
	m.mu.Unlock()
	<-failed.done
	m.mu.Lock()
	failed.nextRestart = time.Now().Add(time.Hour)
	m.mu.Unlock()
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrRestartBackoff) {
		t.Fatalf("startup backoff bypassed: %v", err)
	}
	m.mu.Lock()
	failed.nextRestart = time.Now()
	m.mu.Unlock()
	if _, err := m.Ensure(context.Background(), req); err == nil {
		t.Fatal("invalid retry accepted")
	}
	m.mu.Lock()
	failed = m.slots[req.InstallationID]
	m.mu.Unlock()
	<-failed.done
	if _, err := m.Ensure(context.Background(), req); !errors.Is(err, ErrRestartLimit) {
		t.Fatalf("startup budget bypassed: %v", err)
	}
}

func TestExactPrivateProtocolNegotiation(t *testing.T) {
	m := manager(t)
	req := snapshot(t)
	req.Config[0].Value.Fields["revision"] = structpb.NewNumberValue(2)
	if _, err := m.Ensure(context.Background(), req); err == nil {
		t.Fatal("unsupported private protocol accepted")
	}
}

// A real provider must not receive the host's redirected stdin descriptor.
// No tests run in parallel while os.Stdin is replaced.
func TestChildStdinEOFWithHostSentinel(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "host-stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := input.WriteString("synthetic-host-stdin-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	original := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = original }()
	t.Setenv("BLOEM_STORAGE_TEST_SENTINEL", "synthetic-parent-secret")
	m := manager(t)
	req := snapshot(t)
	req.Config[0].Value.Fields["stdin_eof"] = structpb.NewBoolValue(true)
	child, err := m.Ensure(context.Background(), req)
	if err != nil {
		t.Fatalf("provider did not observe isolated stdin EOF: %v", err)
	}
	m.Disable(req.InstallationID)
	waitDone(t, child)
}

func TestChildLateStreamFailure(t *testing.T) {
	m := manager(t)
	child, err := m.Ensure(context.Background(), snapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := child.Provider().Read(context.Background(), &storagev1.ReadRequest{EntryId: "late-error"})
	if err != nil {
		t.Fatal(err)
	}
	if chunk, err := stream.Recv(); err != nil || string(chunk.GetData()) != "partial" {
		t.Fatalf("first chunk: %v %v", chunk, err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.Unavailable {
		t.Fatalf("late stream error lost: %v", err)
	}
	m.Disable(1)
	waitDone(t, child)
}
