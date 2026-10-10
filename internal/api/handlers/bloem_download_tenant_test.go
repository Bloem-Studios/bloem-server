package handlers

import (
	"context"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemDownloadTenantContext lets download policy regressions reach the
// action under test instead of failing at Bloem's mandatory tenant gate.
func bloemDownloadTenantContext(ctx context.Context, accountID int) context.Context {
	return tenancy.WithContext(ctx, tenancy.Context{
		OrganizationID:     uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		MembershipID:       uuid.MustParse("20000000-0000-0000-0000-000000000001"),
		AccountID:          accountID,
		OrganizationStatus: tenancy.OrganizationActive,
		MembershipStatus:   tenancy.MembershipActive,
		PolicyRevision:     1,
		SecurityRevision:   1,
	})
}
