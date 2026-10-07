package plugins

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// NativeManagementCommitHooksForTest exposes only per-instance acknowledgement
// fault injection to the external test package, which imports actual auth.
func NativeManagementCommitHooksForTest(r *NativeStorageRegistry, install, management func(context.Context, pgx.Tx) error) {
	r.commitInstall = install
	r.commitManagement = management
}
