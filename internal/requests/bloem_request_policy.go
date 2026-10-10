package requests

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requestAccessResolver is an owned capability: shared request policy keeps
// Silo's account-group resolution unless Bloem installs this provider.
type requestAccessResolver interface {
	resolveRequestAccess(context.Context, int, *string) (requestAccess, error)
}

type tenantRequestPolicyProvider struct {
	access.GroupPolicyProvider
	pool *pgxpool.Pool
}

// EnableTenantRequestPolicy installs profile-bound permission and quota
// resolution on the underlying service, including its internal policy calls.
func (s *Service) EnableTenantRequestPolicy() error {
	if _, ok := s.groupProvider.(*tenantRequestPolicyProvider); ok {
		return nil
	}
	if _, ok := s.groupProvider.(*access.TenantGroupStore); !ok || s.users == nil {
		return errors.New("tenant request policy requires the tenant group store and user repository")
	}
	var pool *pgxpool.Pool
	switch repository := s.store.(type) {
	case *Repository:
		pool = repository.pool
	case *TenantGroupLimitRepository:
		pool = repository.pool
	default:
		return errors.New("tenant request policy requires the PostgreSQL request repository")
	}
	s.groupProvider = &tenantRequestPolicyProvider{GroupPolicyProvider: s.groupProvider, pool: pool}
	return nil
}

func (s *Service) requestAccessForAccount(ctx context.Context, accountID int) (requestAccess, error) {
	if resolver, ok := s.groupProvider.(requestAccessResolver); ok {
		return resolver.resolveRequestAccess(ctx, accountID, nil)
	}
	return s.viewerRequestAccess(ctx, accountID)
}

func (p *tenantRequestPolicyProvider) resolveRequestAccess(ctx context.Context, accountID int, viewerProfile *string) (requestAccess, error) {
	if _, err := policy.TenantFactsFromContext(ctx, accountID); err != nil {
		return requestAccess{}, fmt.Errorf("%w: request policy tenant authorization: %w", ErrForbidden, err)
	}
	tenant, _ := tenancy.FromContext(ctx)
	profileID := ""
	if scope, ok := access.GetScope(ctx); ok {
		if scope.UserID != accountID || !scope.ProfileVerified {
			return requestAccess{}, fmt.Errorf("%w: request policy requires a verified viewer", ErrForbidden)
		}
		profileID = scope.ProfileID
	}
	if profileID == "" && (!tenant.Legacy || !tenant.OrganizationDefault) {
		return requestAccess{}, fmt.Errorf("%w: request policy requires a verified profile", ErrForbidden)
	}
	if viewerProfile != nil && *viewerProfile != profileID {
		return requestAccess{}, fmt.Errorf("%w: request policy profile does not match the viewer", ErrForbidden)
	}

	// Permission, membership override, and quota use the same group from one
	// live snapshot. The account repository's default/oldest membership and
	// account-level group never select a profile request's policy.
	var member models.User
	member.ID = accountID
	groupPolicy := access.NoGroupPolicy()
	var groupID *int64
	var limit GroupLimit
	err := p.pool.QueryRow(ctx, `
		SELECT m.legacy_role, m.requests_allowed, COALESCE(g.requests_allowed,true), g.id,
		       COALESCE(l.limit_mode,'inherit'), l.max_requests, l.window_days,
		       COALESCE(l.approval_mode,'inherit'), COALESCE(l.updated_at,g.updated_at,now()), COALESCE(l.revision,0)
		FROM organization_memberships m
		JOIN organizations o ON o.id=m.organization_id
		JOIN users u ON u.id=m.account_id
		LEFT JOIN user_profiles p ON p.organization_id=m.organization_id AND p.user_id=m.account_id AND p.id=$6
		LEFT JOIN access_groups g ON g.organization_id=m.organization_id
		  AND g.id=CASE WHEN $6='' THEN m.access_group_id ELSE p.access_group_id END
		LEFT JOIN request_group_limits l ON l.group_id=g.id
		WHERE m.account_id=$1 AND m.organization_id=$2 AND m.id=$3
		  AND m.status='active' AND u.enabled
		  AND m.security_revision=$4 AND o.policy_revision=$5
		  AND (o.status='active' OR ($7 AND o.is_default AND o.status='initializing'))
		  AND ($6<>'' OR ($7 AND o.is_default))
		  AND ($6='' OR p.id IS NOT NULL)
		  AND (CASE WHEN $6='' THEN m.access_group_id ELSE p.access_group_id END IS NULL OR g.id IS NOT NULL)`,
		accountID, tenant.OrganizationID, tenant.MembershipID, tenant.SecurityRevision, tenant.PolicyRevision, profileID, tenant.Legacy).Scan(
		&member.Role, &member.RequestsAllowed, &groupPolicy.RequestsAllowed, &groupID,
		&limit.LimitMode, &limit.MaxRequests, &limit.WindowDays, &limit.ApprovalMode, &limit.UpdatedAt, &limit.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return requestAccess{}, fmt.Errorf("%w: request policy subject is unavailable or inactive", ErrForbidden)
	}
	if err != nil {
		return requestAccess{}, fmt.Errorf("resolve tenant request policy: %w", err)
	}
	out := requestAccess{allowed: access.ApplyGroupPolicy(&member, &groupPolicy).RequestsAllowed}
	if groupID != nil && member.Role != models.RoleAdmin && limit.Revision > 0 {
		limit.GroupID = *groupID
		out.group = &limit
	}
	return out, nil
}

var _ requestAccessResolver = (*tenantRequestPolicyProvider)(nil)
