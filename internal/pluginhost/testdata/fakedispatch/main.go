package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
)

type server struct {
	pluginv1.UnimplementedRuntimeServer
	pluginv1.UnimplementedScheduledTaskServer
	pluginv1.UnimplementedEventConsumerServer
	manifest *pluginv1.PluginManifest
}

func (s *server) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.manifest}, nil
}

func (s *server) BindHostBroker(_ context.Context, req *pluginv1.BindHostBrokerRequest) (*pluginv1.BindHostBrokerResponse, error) {
	runtime.SetHostBrokerID(req.GetBrokerId())
	return &pluginv1.BindHostBrokerResponse{}, nil
}

func (s *server) Configure(context.Context, *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	return &pluginv1.ConfigureResponse{}, nil
}

func (s *server) Run(ctx context.Context, req *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	host := runtime.Host()
	if host == nil {
		return nil, fmt.Errorf("host broker is not bound")
	}
	var err error
	switch req.GetTaskKey() {
	case "emit-once":
		err = host.PublishEvent(ctx, "submitted", nil)
	case "emit-targeted":
		err = host.PublishEventTo(ctx, "example.fake-target", "foo-event", nil)
	case "ping":
		err = host.PublishEvent(ctx, "ping", nil)
	default:
		err = fmt.Errorf("unknown task %q", req.GetTaskKey())
	}
	return &pluginv1.RunScheduledTaskResponse{}, err
}

func (s *server) HandleEvent(ctx context.Context, req *pluginv1.HandleEventRequest) (*pluginv1.HandleEventResponse, error) {
	host := runtime.Host()
	if host == nil {
		return nil, fmt.Errorf("host broker is not bound")
	}
	err := host.PublishEvent(ctx, "received."+req.GetEventName(), nil)
	return &pluginv1.HandleEventResponse{}, err
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(executable), "manifest.json"))
	if err != nil {
		panic(err)
	}
	m, err := manifest.Load(data)
	if err != nil {
		panic(err)
	}
	s := &server{manifest: m}
	runtime.Serve(runtime.ServeConfig{Servers: runtime.CapabilityServers{Runtime: s, ScheduledTask: s, EventConsumer: s}})
}
