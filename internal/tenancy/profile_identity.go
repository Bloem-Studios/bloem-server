package tenancy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LegacyProfileIdentity is the organization and legacy account-level access
// group copied onto a newly-created v1 profile during the expand phase.
type LegacyProfileIdentity struct {
	OrganizationID uuid.UUID
	AccessGroupID  *int64
}

type profileIdentityQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// ProfileIdentityResolver selects the single default-organization membership
// used by Silo-compatible profile creation.
type ProfileIdentityResolver struct {
	list func(context.Context, int) ([]LegacyProfileIdentity, error)
}

func NewProfileIdentityResolver(query profileIdentityQuerier) *ProfileIdentityResolver {
	resolver := &ProfileIdentityResolver{}
	if query != nil {
		resolver.list = func(ctx context.Context, accountID int) ([]LegacyProfileIdentity, error) {
			rows, err := query.Query(ctx, `
				SELECT memberships.organization_id, users.access_group_id
				FROM organization_memberships AS memberships
				JOIN organizations ON organizations.id = memberships.organization_id
				JOIN users ON users.id = memberships.account_id
				WHERE memberships.account_id = $1
				  AND memberships.status = 'active'
				  AND organizations.is_default
				  AND organizations.status <> 'suspended'
				ORDER BY memberships.id`, accountID)
			if err != nil {
				return nil, fmt.Errorf("query legacy profile identity: %w", err)
			}
			defer rows.Close()

			identities := []LegacyProfileIdentity{}
			for rows.Next() {
				var identity LegacyProfileIdentity
				if err := rows.Scan(&identity.OrganizationID, &identity.AccessGroupID); err != nil {
					return nil, fmt.Errorf("scan legacy profile identity: %w", err)
				}
				identities = append(identities, identity)
			}
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("iterate legacy profile identities: %w", err)
			}
			return identities, nil
		}
	}
	return resolver
}

// ResolveLegacyProfileIdentity resolves only the default organization. It
// rejects missing or ambiguous state instead of choosing an arbitrary tenant.
func (r *ProfileIdentityResolver) ResolveLegacyProfileIdentity(ctx context.Context, accountID int) (uuid.UUID, *int64, error) {
	if r == nil || r.list == nil {
		return uuid.Nil, nil, ErrTenantUnavailable
	}
	identities, err := r.list(ctx, accountID)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("resolve legacy profile identity: %w", err)
	}
	switch len(identities) {
	case 0:
		return uuid.Nil, nil, ErrTenantNotFoundOrHidden
	case 1:
		identity := identities[0]
		if identity.OrganizationID == uuid.Nil {
			return uuid.Nil, nil, ErrTenantUnavailable
		}
		return identity.OrganizationID, identity.AccessGroupID, nil
	default:
		return uuid.Nil, nil, ErrOwnershipResolutionRequired
	}
}
