package progresssync

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
)

// bloemTenancy is the Bloem option state on Service. The zero value keeps
// Silo's single-tenant snapshot reads.
type bloemTenancy struct{ enabled bool }

// WithBloemTenancy makes snapshot reads require the request's validated
// tenant: the account is read through its active organization membership and
// the group through the (organization, account, profile) subject, failing
// closed without a tenant. Without it the service keeps Silo's account-level
// reads. Production wiring always sets it
// (TestProductionProgressSyncServicesUseBloemTenancy).
func (s *Service) WithBloemTenancy() *Service {
	s.bloem.enabled = true
	return s
}

func (s *Service) userInTransaction(ctx context.Context, tx pgx.Tx, userID int) (*models.User, error) {
	if s.bloem.enabled {
		return auth.TenantUserInTransaction(ctx, tx, userID)
	}
	return auth.UserInTransaction(ctx, tx, userID)
}

// groupApplies keeps Silo's account-group gate in Silo mode. Bloem access
// groups attach to the validated profile subject, so the group is always
// resolved and ApplyGroupPolicy decides what applies.
func (s *Service) groupApplies(user *models.User) bool {
	if s.bloem.enabled {
		return true
	}
	return access.GroupApplies(user)
}

func (s *Service) groupPolicyInTransaction(ctx context.Context, tx pgx.Tx, input access.ResolveInput) (*access.GroupPolicy, error) {
	if !s.bloem.enabled {
		return access.GroupPolicyInTransaction(ctx, tx, input.UserID)
	}
	subject, err := access.GroupSubjectFromContext(ctx, input.UserID, input.ProfileID)
	if err != nil {
		return nil, err
	}
	return access.TenantGroupPolicyInTransaction(ctx, tx, subject)
}
