package notifications

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/userstore"
)

func (p *interestTrackingProvider) AcquireLegacyPlaybackAdmission(ctx context.Context, accountID int) (context.Context, func(), error) {
	return userstore.AcquireLegacyPlaybackAdmission(ctx, p.inner, accountID)
}
