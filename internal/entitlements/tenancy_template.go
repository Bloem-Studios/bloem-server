package entitlements

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// An organization created from a template gets it applied in its creation
// transaction. tenancy cannot import this package (see tenancy.TemplateApplier).
func init() {
	tenancy.RegisterTemplateApplier(func(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64) error {
		_, err := ApplyTemplateInTx(ctx, tx, organizationID, key, revision, false)
		return err
	})
}
