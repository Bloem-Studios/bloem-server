package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"strings"
	"sync/atomic"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type server struct {
	publicv1.UnimplementedRuntimeServer
	storagev1.UnimplementedStorageProviderServer
	manifest        *publicv1.PluginManifest
	configured      atomic.Bool
	unhealthy       atomic.Bool
	revision        atomic.Uint32
	description     atomic.Pointer[storagev1.DescribeResponse]
	describeBlocked atomic.Bool
	describeNotify  string
}

func (s *server) GetManifest(context.Context, *publicv1.GetManifestRequest) (*publicv1.GetManifestResponse, error) {
	return &publicv1.GetManifestResponse{Manifest: s.manifest}, nil
}
func (s *server) Configure(ctx context.Context, r *publicv1.ConfigureRequest) (*publicv1.ConfigureResponse, error) {
	for _, e := range r.Config {
		if e.Key != "source" {
			return nil, status.Error(codes.InvalidArgument, "unrelated config")
		}
		if e.Value.GetFields()["token"].GetStringValue() != "synthetic-source-secret" {
			return nil, status.Error(codes.PermissionDenied, "config absent")
		}
		if e.Value.GetFields()["stdin_eof"].GetBoolValue() {
			data, err := io.ReadAll(io.LimitReader(os.Stdin, 1))
			if err != nil || len(data) != 0 {
				return nil, status.Error(codes.PermissionDenied, "host stdin exposed")
			}
		}
		if e.Value.GetFields()["revision"].GetNumberValue() != 0 {
			s.revision.Store(uint32(e.Value.GetFields()["revision"].GetNumberValue()))
		}
		s.describeNotify = e.Value.GetFields()["describe_notify"].GetStringValue()
		mode := e.Value.GetFields()["describe_mode"].GetStringValue()
		description := &storagev1.DescribeResponse{Sources: []*storagev1.Source{{Id: "fixture", Name: "Synthetic storage", RootEntryId: "root", RevisionPinnedReads: true}}}
		switch mode {
		case "empty":
			description.Sources = nil
		case "duplicate":
			description.Sources = append(description.Sources, description.Sources[0])
		case "empty-root":
			description.Sources[0].RootEntryId = ""
		case "nil-source":
			description.Sources = append(description.Sources, nil)
		case "long-source":
			description.Sources[0].Id = strings.Repeat("s", 1025)
		case "oversized":
			description.Sources[0].Name = strings.Repeat("n", 1<<20)
		case "block":
			s.describeBlocked.Store(true)
		}
		s.description.Store(description)
		if e.Value.GetFields()["block"].GetBoolValue() {
			if path := e.Value.GetFields()["notify"].GetStringValue(); path != "" {
				if err := os.WriteFile(path, []byte("configure started"), 0600); err != nil {
					return nil, status.Error(codes.Internal, "notification failed")
				}
			}
			<-ctx.Done()
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if len(r.Config) != 1 {
		return nil, status.Error(codes.InvalidArgument, "source config required")
	}
	s.configured.Store(true)
	return &publicv1.ConfigureResponse{}, nil
}
func (s *server) Describe(ctx context.Context, _ *storagev1.DescribeRequest) (*storagev1.DescribeResponse, error) {
	if !s.configured.Load() {
		return nil, status.Error(codes.FailedPrecondition, "not configured")
	}
	if s.unhealthy.Load() {
		return nil, status.Error(codes.Unavailable, "synthetic outage")
	}
	if _, ok := os.LookupEnv("BLOEM_STORAGE_TEST_SENTINEL"); ok {
		return nil, status.Error(codes.PermissionDenied, "parent environment")
	}
	allowed := map[string]bool{
		"LANG": true, "LC_ALL": true, "TZ": true,
		sdkruntime.HandshakeConfig().MagicCookieKey: true,
		"PLUGIN_MIN_PORT": true, "PLUGIN_MAX_PORT": true,
		"PLUGIN_PROTOCOL_VERSIONS": true, plugin.EnvUnixSocketDir: true,
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !allowed[key] {
			return nil, status.Error(codes.PermissionDenied, "unexpected environment")
		}
	}
	if os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" || os.Getenv("TZ") != "UTC" {
		return nil, status.Error(codes.PermissionDenied, "application environment differs")
	}
	if s.describeBlocked.Load() {
		if s.describeNotify != "" {
			if err := os.WriteFile(s.describeNotify, []byte("describe started"), 0600); err != nil {
				return nil, status.Error(codes.Internal, "notification failed")
			}
		}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return &storagev1.DescribeResponse{Revision: s.revision.Load(), Sources: s.description.Load().Sources}, nil
}
func (s *server) Stat(ctx context.Context, r *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	switch r.EntryId {
	case "crash":
		os.Exit(7)
	case "bad-revision":
		s.revision.Store(2)
	case "unhealthy":
		s.unhealthy.Store(true)
	case "malformed-description":
		s.description.Store(&storagev1.DescribeResponse{Sources: []*storagev1.Source{{Id: "fixture", RootEntryId: ""}}})
	case "block-description":
		s.describeBlocked.Store(true)
	case "change-source":
		s.description.Store(&storagev1.DescribeResponse{Sources: []*storagev1.Source{{Id: "changed", RootEntryId: "root", RevisionPinnedReads: true}}})
	case "change-root":
		s.description.Store(&storagev1.DescribeResponse{Sources: []*storagev1.Source{{Id: "fixture", RootEntryId: "changed", RevisionPinnedReads: true}}})
	case "unpinned":
		s.description.Store(&storagev1.DescribeResponse{Sources: []*storagev1.Source{{Id: "fixture", RootEntryId: "root"}}})
	case "block":
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return &storagev1.StatResponse{Entry: &storagev1.Entry{Id: r.EntryId, Revision: "v1"}}, nil
}
func (s *server) Read(r *storagev1.ReadRequest, out grpc.ServerStreamingServer[storagev1.ReadChunk]) error {
	chunk := &storagev1.ReadChunk{}
	if r.EntryId == "late-error" {
		chunk.Data = []byte("partial")
	}
	if err := out.Send(chunk); err != nil {
		return err
	}
	if r.EntryId == "late-error" {
		return status.Error(codes.Unavailable, "synthetic late failure")
	}
	<-out.Context().Done()
	return status.FromContextError(out.Context().Err()).Err()
}

type extension struct {
	*sdkruntime.GRPCPlugin
	storage storagev1.StorageProviderServer
}

func (e *extension) GRPCServer(b *plugin.GRPCBroker, s *grpc.Server) error {
	if err := e.GRPCPlugin.GRPCServer(b, s); err != nil {
		return err
	}
	storagev1.RegisterStorageProviderServer(s, e.storage)
	return nil
}
func main() {
	path, _ := os.Executable()
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(b)
	s := &server{manifest: &publicv1.PluginManifest{PluginId: "bloem.runtime.fixture", Version: "0.1.0", Checksum: hex.EncodeToString(sum[:]), SiloApiVersion: "v1", SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}}
	s.revision.Store(1)
	sdkruntime.Serve(sdkruntime.ServeConfig{Plugins: plugin.PluginSet{sdkruntime.PluginSetName: &extension{GRPCPlugin: &sdkruntime.GRPCPlugin{Servers: sdkruntime.CapabilityServers{Runtime: s}}, storage: s}}})
}
