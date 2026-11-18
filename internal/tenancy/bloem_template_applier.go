package tenancy

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TemplateApplier applies an entitlement template to an organization inside
// the transaction that creates it.
//
// internal/entitlements registers it (RegisterTemplateApplier in its init), so
// this package does not import entitlements. entitlements evaluates group
// policy with internal/access, and access imports tenancy, so the import would
// be a cycle.
type TemplateApplier func(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64) error

var templateApplier atomic.Pointer[TemplateApplier]

// RegisterTemplateApplier installs the applier CreateTenantOrganization uses
// for an organization created from a template.
func RegisterTemplateApplier(apply TemplateApplier) {
	templateApplier.Store(&apply)
}

// errNoTemplateApplier means the program never linked internal/entitlements.
var errNoTemplateApplier = errors.New("tenancy: entitlement templates are unavailable: internal/entitlements is not linked")

func applyEntitlementTemplate(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64) error {
	apply := templateApplier.Load()
	if apply == nil {
		return errNoTemplateApplier
	}
	return (*apply)(ctx, tx, organizationID, key, revision)
}
