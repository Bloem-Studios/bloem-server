//go:build integration

package scanqueue

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

// IntegrationNativeQueueCommit forwards the real domain authorizer to the same
// Service and real enqueue invocation, exposing only its existing commit hook.
// No service-global state or replacement queue algorithm is installed.
type IntegrationNativeQueueCommit struct {
	Service *Service
	Commit  func(context.Context, pgx.Tx) error
}

func (q IntegrationNativeQueueCommit) EnqueueNativeLibraryAuthorized(ctx context.Context, id int, authorize NativeLibraryAuthorizeTx) (*models.ScanRun, bool, error) {
	return (&nativeLibraryEnqueue{service: q.Service, commit: q.Commit}).enqueue(ctx, id, authorize)
}
