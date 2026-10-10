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

// TenantAdminRequestUsageService reports the explicitly addressed account's
// membership policy in the actor's organization. It never selects a target
// profile; viewer request operations remain profile-bound on Service.
type TenantAdminRequestUsageService struct {
	service *Service
	pool    *pgxpool.Pool
}

func NewTenantAdminRequestUsageService(service *Service) (*TenantAdminRequestUsageService, error) {
	if service == nil {
		return nil, errors.New("tenant admin request usage requires a request service")
	}
	provider, ok := service.groupProvider.(*tenantRequestPolicyProvider)
	if !ok {
		return nil, errors.New("tenant admin request usage requires tenant request policy")
	}
	return &TenantAdminRequestUsageService{service: service, pool: provider.pool}, nil
}

func (s *TenantAdminRequestUsageService) EffectivePolicy(ctx context.Context, targetAccountID int) (EffectivePolicy, error) {
	if ctx == nil {
		return EffectivePolicy{}, ErrForbidden
	}
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return EffectivePolicy{}, ErrForbidden
	}
	if _, err := policy.TenantFactsFromContext(ctx, tenant.AccountID); err != nil {
		return EffectivePolicy{}, fmt.Errorf("%w: admin request usage tenant authorization: %w", ErrForbidden, err)
	}
	var target models.User
	target.ID = targetAccountID
	group := access.NoGroupPolicy()
	var groupID *int64
	var limit GroupLimit
	// Current actor authority, exact target membership, and that membership's
	// group policy come from one snapshot. Neither users.role nor the account
	// repository's default/oldest membership projection grants tenant authority.
	err := s.pool.QueryRow(ctx, `
		SELECT target.legacy_role, target.requests_allowed, COALESCE(g.requests_allowed,true), g.id,
		       COALESCE(l.limit_mode,'inherit'), l.max_requests, l.window_days,
		       COALESCE(l.approval_mode,'inherit'), COALESCE(l.updated_at,g.updated_at,now()), COALESCE(l.revision,0)
		FROM organization_memberships actor
		JOIN organizations o ON o.id=actor.organization_id
		JOIN users actor_account ON actor_account.id=actor.account_id
		JOIN organization_memberships target ON target.organization_id=actor.organization_id AND target.account_id=$6
		JOIN users target_account ON target_account.id=target.account_id
		LEFT JOIN access_groups g ON g.organization_id=target.organization_id AND g.id=target.access_group_id
		LEFT JOIN request_group_limits l ON l.group_id=g.id
		WHERE actor.account_id=$1 AND actor.organization_id=$2 AND actor.id=$3
		  AND actor.status='active' AND actor.legacy_role='admin' AND actor_account.enabled
		  AND actor.security_revision=$4 AND o.policy_revision=$5
		  AND (o.status='active' OR ($7 AND o.is_default AND o.status='initializing'))
		  AND target.status='active' AND target_account.enabled
		  AND (target.access_group_id IS NULL OR g.id IS NOT NULL)`,
		tenant.AccountID, tenant.OrganizationID, tenant.MembershipID, tenant.SecurityRevision, tenant.PolicyRevision, targetAccountID, tenant.Legacy).Scan(
		&target.Role, &target.RequestsAllowed, &group.RequestsAllowed, &groupID,
		&limit.LimitMode, &limit.MaxRequests, &limit.WindowDays, &limit.ApprovalMode, &limit.UpdatedAt, &limit.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return EffectivePolicy{}, fmt.Errorf("%w: admin request usage actor or target is unavailable or inactive", ErrForbidden)
	}
	if err != nil {
		return EffectivePolicy{}, fmt.Errorf("resolve tenant admin request usage: %w", err)
	}
	resolved := requestAccess{allowed: access.ApplyGroupPolicy(&target, &group).RequestsAllowed}
	if groupID != nil && target.Role != models.RoleAdmin && limit.Revision > 0 {
		limit.GroupID = *groupID
		resolved.group = &limit
	}
	// Reuse the shared settings/group/user-limit precedence and account-wide
	// consumption calculation. This call-local evaluator holds only the policy
	// dependencies and cannot install account authority on the viewer service.
	evaluator := Service{
		store: s.service.store,
		Now:   s.service.Now,
		groupProvider: &adminRequestAccessProvider{
			GroupPolicyProvider: s.service.groupProvider, accountID: targetAccountID, resolved: resolved,
		},
	}
	return evaluator.resolvePolicy(ctx, targetAccountID)
}

type adminRequestAccessProvider struct {
	access.GroupPolicyProvider
	accountID int
	resolved  requestAccess
}

func (p *adminRequestAccessProvider) resolveRequestAccess(_ context.Context, accountID int, viewerProfile *string) (requestAccess, error) {
	if accountID != p.accountID || viewerProfile != nil {
		return requestAccess{}, ErrForbidden
	}
	return p.resolved, nil
}

var _ requestAccessResolver = (*adminRequestAccessProvider)(nil)
