package mediasource

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	storagev1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1"
	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func launchStorage(t *testing.T) (storagev1.StorageProviderClient, *sdkruntime.Client, context.Context) {
	t.Helper()
	sdk := os.Getenv("BLOEM_STORAGE_SDK_WORKTREE")
	if sdk == "" {
		t.Fatal("BLOEM_STORAGE_SDK_WORKTREE must name the private SDK checkout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	binary := filepath.Join(t.TempDir(), "hello-storage")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./examples/hello-storage")
	build.Dir = sdk
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build storage fixture: %v\n%s", err, b)
	}
	t.Setenv("BLOEM_STORAGE_TEST_SENTINEL", "synthetic-parent-value")
	cmd := exec.Command(binary)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	process := plugin.NewClient(&plugin.ClientConfig{HandshakeConfig: sdkruntime.HandshakeConfig(), Plugins: sdkruntime.DefaultPluginSet(sdkruntime.CapabilityServers{}), Cmd: cmd, SkipHostEnv: true, AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC}, Logger: hclog.NewNullLogger(), StartTimeout: 10 * time.Second})
	t.Cleanup(func() {
		process.Kill()
		if cmd.Process == nil || cmd.ProcessState == nil || !process.Exited() {
			t.Error("fixture process was not reaped")
		}
	})
	rpc, err := process.Client()
	if err != nil {
		t.Fatal(err)
	}
	dispensed, err := rpc.Dispense(sdkruntime.PluginSetName)
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := dispensed.(*sdkruntime.Client)
	if !ok {
		t.Fatalf("unexpected legacy client: %T", dispensed)
	}
	m, err := legacy.Runtime().GetManifest(ctx, &publicv1.GetManifestRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if m.GetManifest().GetPluginId() != "bloem.example.hello-storage" {
		t.Fatal("wrong fixture identity")
	}
	f, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	if m.GetManifest().GetChecksum() != hex.EncodeToString(h.Sum(nil)) {
		t.Fatal("manifest checksum did not match executable")
	}
	if _, err = legacy.Runtime().Configure(ctx, &publicv1.ConfigureRequest{}); err != nil {
		t.Fatalf("legacy Configure: %v", err)
	}
	client := storagev1.NewStorageProviderClient(legacy.Conn())
	d, err := client.Describe(ctx, &storagev1.DescribeRequest{})
	if err != nil || d.GetRevision() != 1 {
		t.Fatalf("storage discovery: %v %v", d, err)
	}
	t.Logf("executable sha256=%s", hex.EncodeToString(h.Sum(nil)))
	return client, legacy, ctx
}

func TestExecutableStorageZIPAndFailures(t *testing.T) {
	client, _, ctx := launchStorage(t)
	ref := Ref{SourceID: "fixture", EntryID: "book", Revision: "v1"}
	f, err := Open(ctx, NewPluginSource(client), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	assertZIP(t, f)
	z, err := zip.NewReader(f, f.Info().Size)
	if err != nil {
		t.Fatal(err)
	}
	r, err := z.Open("OEBPS/payload.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// Check every byte without accumulating the whole member in the host.
	buf := make([]byte, 64<<10)
	var count int64
	for {
		n, err := r.Read(buf)
		for _, b := range buf[:n] {
			if b != 'x' {
				t.Fatal("payload corruption")
			}
		}
		count += int64(n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 5<<20 {
		t.Fatalf("large member length: %d", count)
	}
	for _, entry := range []string{"revision-conflict", "duplicate", "short"} {
		t.Run(entry, func(t *testing.T) {
			ref.EntryID = entry
			fault, err := Open(ctx, NewPluginSource(client), ref)
			if err != nil {
				t.Fatal(err)
			}
			defer fault.Close()
			if _, err = fault.ReadAt(make([]byte, 10), 0); err == nil {
				t.Fatal("faulty process read accepted")
			}
		})
	}
	ref.EntryID = "blocking"
	started := make(chan struct{})
	source := &observedSource{Source: NewPluginSource(client), started: started}
	blocked, err := Open(ctx, source, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	done := make(chan error, 1)
	go func() { _, err := blocked.ReadAt(make([]byte, 10), 0); done <- err }()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-started:
	case <-waitCtx.Done():
		t.Fatal("blocking provider did not deliver first chunk")
	}
	_ = blocked.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked process read succeeded")
		}
	case <-waitCtx.Done():
		t.Fatal("blocked process read not canceled")
	}
}

// Observe real received bytes so cancellation occurs during an active RPC.
type observedSource struct {
	Source
	started chan struct{}
}
type observedWriter struct {
	io.Writer
	started chan struct{}
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if w.started != nil {
		close(w.started)
		w.started = nil
	}
	return n, err
}
func (s *observedSource) ReadRange(ctx context.Context, ref Ref, offset, length int64, w io.Writer) error {
	return s.Source.ReadRange(ctx, ref, offset, length, &observedWriter{Writer: w, started: s.started})
}

func TestLegacySiloExecutableRemainsUsableWithoutStorage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "legacy-plugin")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./internal/pluginhost/testdata/legacyfakeplugin")
	build.Dir = filepath.Join("..", "..")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build legacy fixture: %v\n%s", err, b)
	}
	manifest := []byte(`{"plugin_id":"legacy.fixture","version":"0.1.0","checksum":"fixture","silo_api_version":"v1","capabilities":[{"type":"metadata_provider.v1","id":"stub","display_name":"Legacy metadata"}]}`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(binary), "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	process := plugin.NewClient(&plugin.ClientConfig{HandshakeConfig: sdkruntime.HandshakeConfig(), Plugins: sdkruntime.DefaultPluginSet(sdkruntime.CapabilityServers{}), Cmd: cmd, SkipHostEnv: true, AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC}, Logger: hclog.NewNullLogger(), StartTimeout: 10 * time.Second})
	t.Cleanup(func() {
		process.Kill()
		if cmd.ProcessState == nil || !process.Exited() {
			t.Error("legacy fixture was not reaped")
		}
	})
	rpc, err := process.Client()
	if err != nil {
		t.Fatal(err)
	}
	dispensed, err := rpc.Dispense(sdkruntime.PluginSetName)
	if err != nil {
		t.Fatal(err)
	}
	client := dispensed.(*sdkruntime.Client)
	m, err := client.Runtime().GetManifest(ctx, &publicv1.GetManifestRequest{})
	if err != nil || m.GetManifest().GetPluginId() != "legacy.fixture" {
		t.Fatalf("legacy manifest: %v %v", m, err)
	}
	result, err := publicv1.NewMetadataProviderClient(client.Conn()).Search(ctx, &publicv1.SearchMetadataRequest{})
	if err != nil || len(result.GetResults()) != 1 || result.GetResults()[0].GetTitle() != "Example Title" {
		t.Fatalf("legacy metadata: %v %v", result, err)
	}
	_, err = storagev1.NewStorageProviderClient(client.Conn()).Describe(ctx, &storagev1.DescribeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("legacy storage service should be optional: %v", err)
	}
}
