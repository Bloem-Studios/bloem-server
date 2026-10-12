// Command brokerextension exercises an optional service on the host callback broker.
package main

import (
	"context"
	"sync"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type fixture struct {
	pluginv1.UnimplementedRuntimeServer
	pluginv1.UnimplementedMetadataProviderServer
	mu     sync.Mutex
	broker *plugin.GRPCBroker
	conn   *grpc.ClientConn
}

func (f *fixture) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: &pluginv1.PluginManifest{PluginId: "bloem.test.broker", Capabilities: []*pluginv1.CapabilityDescriptor{{Type: "metadata_provider.v1", Id: "test"}}}}, nil
}
func (f *fixture) BindHostBroker(_ context.Context, req *pluginv1.BindHostBrokerRequest) (*pluginv1.BindHostBrokerResponse, error) {
	conn, err := f.broker.Dial(req.GetBrokerId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	return &pluginv1.BindHostBrokerResponse{}, nil
}
func (f *fixture) Search(ctx context.Context, _ *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	title := "unbound"
	if conn != nil {
		resp, err := healthv1.NewHealthClient(conn).Check(ctx, &healthv1.HealthCheckRequest{Service: "request-cannot-select-installation"})
		if err != nil {
			return nil, err
		}
		if resp.GetStatus() != healthv1.HealthCheckResponse_SERVING {
			return nil, status.Error(codes.Internal, "extension unavailable")
		}
		info, err := pluginv1.NewRuntimeHostClient(conn).GetHostInfo(ctx, &pluginv1.GetHostInfoRequest{})
		if status.Code(err) == codes.Unimplemented {
			title = "extension-ok runtime-unimplemented"
		} else if err != nil {
			return nil, err
		} else {
			title = "extension-ok " + info.GetHostName()
		}
	}
	return &pluginv1.SearchMetadataResponse{Results: []*pluginv1.ProviderSearchResult{{Title: title}}}, nil
}

type transport struct {
	*sdkruntime.GRPCPlugin
	f *fixture
}

func (p *transport) GRPCServer(b *plugin.GRPCBroker, s *grpc.Server) error {
	p.f.broker = b
	return p.GRPCPlugin.GRPCServer(b, s)
}
func main() {
	f := &fixture{}
	plugin.Serve(&plugin.ServeConfig{HandshakeConfig: sdkruntime.HandshakeConfig(), Plugins: plugin.PluginSet{sdkruntime.PluginSetName: &transport{GRPCPlugin: &sdkruntime.GRPCPlugin{Servers: sdkruntime.CapabilityServers{Runtime: f, MetadataProvider: f}}, f: f}}, GRPCServer: plugin.DefaultGRPCServer})
}
