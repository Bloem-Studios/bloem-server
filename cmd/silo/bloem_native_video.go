package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// configureStandaloneNativeVideo installs this worker's own approved runtime.
// Shared descriptors contain no provider/tool URL; every open resolves fresh
// pinned bytes through this host and current delegated viewer/source policy.
func configureStandaloneNativeVideo(server *transcodenode.Server, deps api.Dependencies, nodeID int, root, approvalPath string) (http.Handler, func(context.Context) error, error) {
	if server == nil {
		return nil, nil, errors.New("native video requires a transcode server")
	}
	ordinary := server.Handler()
	if deps.RedisClient == nil || approvalPath == "" {
		return ordinary, server.Shutdown, nil
	}
	approved, err := nativestorage.LoadApprovals(approvalPath)
	if err != nil {
		return nil, nil, err
	}
	if len(approved) == 0 {
		return ordinary, server.Shutdown, nil
	}
	if deps.DB == nil || deps.SecretCipher == nil || nodeID <= 0 {
		return nil, nil, errors.New("native video requires database, cipher and registered worker identity")
	}
	host, err := nativestorage.NewHost(deps.DB, deps.SecretCipher, root, approvalPath)
	if err != nil {
		return nil, nil, err
	}
	deps.NativeStorage = host
	if deps.FileRepo == nil {
		deps.FileRepo = scanner.NewFileRepository(deps.DB)
	}
	if deps.UserStoreProvider == nil {
		deps.UserStoreProvider = pgstore.NewPostgresProvider(deps.DB)
	}
	coordinator := &nativestorage.Coordinator{References: storagesource.NewRepository(deps.DB), Registry: host.Registry, Runtime: nativestorage.ManagedRuntime{Manager: host.Manager}, AcquireOpen: host.AcquireOpen}
	service, err := api.NewNativeVideoDelegateService(deps, coordinator)
	if err != nil {
		_ = host.Shutdown(context.Background())
		return nil, nil, err
	}
	server.SetInputPathAuthorizer(transcodenode.NewNativeInputPathAuthorizer(transcodenode.NewCatalogPathAuthorizer(deps.FileRepo)))
	wrapped := server.WrapNativeExecution(ordinary, transcodenode.NativeExecutionConfig{NodeID: nodeID, Resolve: func(ctx context.Context, request transcodenode.NativeExecutionRequest) (*transcodenode.NativeExecutionGrant, error) {
		return service.WorkerGrants.Resolve(ctx, request, service)
	}})
	return wrapped, nativeVideoShutdown(server.Shutdown, host.Shutdown), nil
}

func nativeVideoShutdown(executor, host func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := executor(ctx); err != nil {
			return err
		}
		return host(ctx)
	}
}
