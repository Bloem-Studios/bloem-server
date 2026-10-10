package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const schema = `{"type":"object","required":["source_id"],"properties":{"source_id":{"type":"string"}}}`
const root = " /opaque:S3-root%2f/ "

type server struct {
	publicv1.UnimplementedRuntimeServer
	storagev1.UnimplementedStorageProviderServer
	manifest                             *publicv1.PluginManifest
	source, mode, state, notify, release string
	calls                                atomic.Int32
}

func (s *server) GetManifest(context.Context, *publicv1.GetManifestRequest) (*publicv1.GetManifestResponse, error) {
	return &publicv1.GetManifestResponse{Manifest: s.manifest}, nil
}
func (s *server) Configure(ctx context.Context, req *publicv1.ConfigureRequest) (*publicv1.ConfigureResponse, error) {
	if len(req.Config) != 1 || req.Config[0].GetKey() != "connection" {
		return nil, status.Error(codes.InvalidArgument, "connection required")
	}
	fields := req.Config[0].GetValue().GetFields()
	s.source = fields["source_id"].GetStringValue()
	s.mode = fields["mode"].GetStringValue()
	s.state = fields["state_path"].GetStringValue()
	s.notify = fields["notify"].GetStringValue()
	s.release = fields["release"].GetStringValue()
	if s.source == "" || s.state == "" {
		return nil, status.Error(codes.InvalidArgument, "host state required")
	}
	data, err := os.ReadFile(s.state)
	if os.IsNotExist(err) {
		err = os.WriteFile(s.state, []byte(root), 0600)
	} else if err == nil && string(data) != root {
		return nil, status.Error(codes.FailedPrecondition, "identity state rotated")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "state unavailable")
	}
	if s.mode == "block-configure" {
		if err := s.gate(ctx); err != nil {
			return nil, err
		}
	}
	return &publicv1.ConfigureResponse{}, nil
}
func (s *server) gate(ctx context.Context) error {
	if err := os.WriteFile(s.notify, []byte(strconv.Itoa(os.Getpid())+"\n"+s.state), 0600); err != nil {
		return status.Error(codes.Internal, "notify failed")
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-ticker.C:
			if _, err := os.Stat(s.release); err == nil {
				return nil
			}
		}
	}
}
func (s *server) Describe(ctx context.Context, _ *storagev1.DescribeRequest) (*storagev1.DescribeResponse, error) {
	call := s.calls.Add(1)
	if (s.mode == "block-startup" && call == 1) || (s.mode == "gate-fresh" && call == 2) {
		if err := s.gate(ctx); err != nil {
			return nil, err
		}
	}
	selected := &storagev1.Source{Id: s.source, RootEntryId: root, RevisionPinnedReads: true}
	description := &storagev1.DescribeResponse{Revision: 1, Sources: []*storagev1.Source{{Id: "other-source", RootEntryId: "other-root", RevisionPinnedReads: true}, selected}}
	switch s.mode {
	case "empty":
		description.Sources = nil
	case "unpinned":
		selected.RevisionPinnedReads = false
	case "malformed":
		selected.RootEntryId = ""
	case "duplicate":
		description.Sources = append(description.Sources, selected)
	case "missing":
		selected.Id = "unrequested"
	case "fresh-root":
		if call == 1 {
			selected.RootEntryId = "startup-old-root"
		}
	}
	return description, nil
}

// List, Stat, Read remain unimplemented: discovery must only Describe.
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
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "startup"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		panic(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(binary)
	s := &server{manifest: &publicv1.PluginManifest{PluginId: "bloem.storage.s3", Version: "0.1.0", Checksum: hex.EncodeToString(sum[:]), SiloApiVersion: "v1", SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "connection", Required: true, JsonSchema: schema}}}}
	sdkruntime.Serve(sdkruntime.ServeConfig{Plugins: plugin.PluginSet{sdkruntime.PluginSetName: &extension{GRPCPlugin: &sdkruntime.GRPCPlugin{Servers: sdkruntime.CapabilityServers{Runtime: s}}, storage: s}}})
}
