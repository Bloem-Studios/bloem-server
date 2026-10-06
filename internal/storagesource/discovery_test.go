package storagesource

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testProvider struct {
	storagev1.UnimplementedStorageProviderServer
	list func(context.Context, *storagev1.ListRequest) (*storagev1.ListResponse, error)
}

func (p testProvider) List(ctx context.Context, r *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	return p.list(ctx, r)
}

func providerClient(t *testing.T, list func(context.Context, *storagev1.ListRequest) (*storagev1.ListResponse, error)) storagev1.StorageProviderClient {
	t.Helper()
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	storagev1.RegisterStorageProviderServer(server, testProvider{list: list})
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); server.Stop(); _ = listener.Close() })
	return storagev1.NewStorageProviderClient(conn)
}

func TestDiscoveryRequestBoundsAndDeadline(t *testing.T) {
	client := providerClient(t, func(ctx context.Context, r *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 31*time.Second || r.GetMaxEntries() != 512 || r.GetSourceId() != "books" || r.GetDirectoryId() != "root" || r.GetCursor() != "next" {
			return nil, status.Error(codes.InvalidArgument, "unbounded or wrong request")
		}
		return &storagev1.ListResponse{Complete: true}, nil
	})
	if _, err := fetchPage(context.Background(), client, "books", Checkpoint{DirectoryID: "root", Cursor: "next"}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryRejectsOversizedResponse(t *testing.T) {
	client := providerClient(t, func(context.Context, *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		return &storagev1.ListResponse{NextCursor: strings.Repeat("x", (1<<20)+1)}, nil
	})
	if _, err := fetchPage(context.Background(), client, "books", Checkpoint{DirectoryID: "root"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("receive bound absent: %v", err)
	}
}

func TestDiscoveryPreservesProviderFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.FailedPrecondition, codes.NotFound, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			client := providerClient(t, func(context.Context, *storagev1.ListRequest) (*storagev1.ListResponse, error) {
				return nil, status.Error(code, "fixture")
			})
			if _, err := fetchPage(context.Background(), client, "books", Checkpoint{DirectoryID: "root"}); status.Code(err) != code {
				t.Fatalf("provider status changed: %v", err)
			}
		})
	}
}
