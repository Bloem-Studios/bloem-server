package pluginhost_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

type boundHealth struct {
	healthv1.UnimplementedHealthServer
	pluginID string
	observed chan string
}

func (s *boundHealth) Check(context.Context, *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	s.observed <- s.pluginID
	return &healthv1.HealthCheckResponse{Status: healthv1.HealthCheckResponse_SERVING}, nil
}
func TestBrokerRegistrarSharesHostConnectionAndBindsIdentity(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "brokerextension")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/brokerextension").CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name, want          string
		extension, hostInfo bool
	}{
		{"ordinary", "unbound", false, false},
		{"extension only", "extension-ok runtime-unimplemented", true, false},
		{"existing runtime host", "extension-ok Main host", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := make(chan string, 1)
			bound := make(chan int, 1)
			cfg := pluginhost.Config{}
			if tc.extension {
				cfg.BrokerRegistrar = func(s *grpc.Server, id string, installation int) {
					bound <- installation
					healthv1.RegisterHealthServer(s, &boundHealth{pluginID: id, observed: observed})
				}
			}
			if tc.hostInfo {
				cfg.HostInfo = func(context.Context) (pluginhost.HostInfo, error) {
					return pluginhost.HostInfo{Name: "Main host", Role: pluginhost.HostRoleAPI}, nil
				}
			}
			host := pluginhost.NewHost(cfg)
			t.Cleanup(func() { _ = host.Shutdown(context.Background()) })
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			manifest := &pluginv1.PluginManifest{PluginId: "bloem.test.broker", Capabilities: []*pluginv1.CapabilityDescriptor{{Type: "metadata_provider.v1", Id: "test"}}}
			client, err := host.Start(ctx, pluginhost.StartRequest{InstallationID: 42, BinaryPath: bin, Manifest: manifest})
			if err != nil {
				t.Fatal(err)
			}
			provider, err := client.MetadataProvider("test")
			if err != nil {
				t.Fatal(err)
			}
			result, err := provider.Search(ctx, &pluginv1.SearchMetadataRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.GetResults()) != 1 || result.GetResults()[0].GetTitle() != tc.want {
				t.Fatalf("callback result: %v; want %q", result, tc.want)
			}
			if tc.extension {
				select {
				case id := <-bound:
					if id != 42 {
						t.Fatalf("bound installation %d", id)
					}
				case <-ctx.Done():
					t.Fatal("registrar not called")
				}
				select {
				case id := <-observed:
					if id != "bloem.test.broker" {
						t.Fatalf("bound plugin %q", id)
					}
				case <-ctx.Done():
					t.Fatal("extension not called")
				}
			}
		})
	}
}
