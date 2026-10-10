package requests

import (
	"context"
	"errors"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TenantGroupLimitRepository bounds group-limit capabilities to the validated
// request organization. Embedding preserves the repository's other optional
// capabilities; ordinary Silo repositories retain their account-wide behavior.
type TenantGroupLimitRepository struct{ *Repository }

func NewTenantGroupLimitRepository(repository *Repository) *TenantGroupLimitRepository {
	return &TenantGroupLimitRepository{Repository: repository}
}

// enableTenantGroupLimitRepository installs the owned storage adapter.
// It is idempotent and rejects unsupported stores instead of leaving production
// group-limit administration silently unscoped.
func (s *Service) enableTenantGroupLimitRepository() error {
	switch repository := s.store.(type) {
	case *TenantGroupLimitRepository:
		return nil
	case *Repository:
		s.store = NewTenantGroupLimitRepository(repository)
		return nil
	default:
		return errors.New("tenant request group limits require the PostgreSQL request repository")
	}
}

func groupLimitOrganization(ctx context.Context) (uuid.UUID, error) {
	if ctx != nil {
		if tenant, ok := tenancy.FromContext(ctx); ok {
			if _, err := policy.TenantFactsFromContext(ctx, tenant.AccountID); err != nil {
				return uuid.Nil, fmt.Errorf("%w: request group tenant authorization: %w", ErrForbidden, err)
			}
			return tenant.OrganizationID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("%w: request group limits require tenant authorization", ErrForbidden)
}

// TenantGroupLimitService binds administrative group-limit operations to their
// viewer as well as the organization. Other request operations keep the shared
// service implementation, including all optional guarded capabilities.
type TenantGroupLimitService struct{ *Service }

func NewTenantGroupLimitService(service *Service) (*TenantGroupLimitService, error) {
	if service == nil {
		return nil, errors.New("tenant request group limits require a request service")
	}
	if err := service.enableTenantGroupLimitRepository(); err != nil {
		return nil, err
	}
	return &TenantGroupLimitService{Service: service}, nil
}

func groupLimitViewer(ctx context.Context, viewer Viewer) error {
	if _, err := policy.TenantFactsFromContext(ctx, viewer.UserID); err != nil {
		return fmt.Errorf("%w: request group viewer authorization: %w", ErrForbidden, err)
	}
	return nil
}

func (s *TenantGroupLimitService) GetGroupLimit(ctx context.Context, viewer Viewer, groupID int64) (*GroupLimit, error) {
	if err := groupLimitViewer(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.GetGroupLimit(ctx, viewer, groupID)
}

func (s *TenantGroupLimitService) UpsertGroupLimitConditional(ctx context.Context, viewer Viewer, in GroupLimit, expected int64) (*GroupLimit, error) {
	if err := groupLimitViewer(ctx, viewer); err != nil {
		return nil, err
	}
	return s.Service.UpsertGroupLimitConditional(ctx, viewer, in, expected)
}

func (r *TenantGroupLimitRepository) GroupExists(ctx context.Context, groupID int64) (bool, error) {
	org, err := groupLimitOrganization(ctx)
	if err != nil {
		return false, err
	}
	var exists bool
	err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_groups WHERE id=$1 AND organization_id=$2)`, groupID, org).Scan(&exists)
	return exists, err
}

func (r *TenantGroupLimitRepository) GetGroupLimit(ctx context.Context, groupID int64) (*GroupLimit, error) {
	org, err := groupLimitOrganization(ctx)
	if err != nil {
		return nil, err
	}
	limit, err := scanGroupLimit(r.pool.QueryRow(ctx, `
		SELECT g.id, COALESCE(l.limit_mode, 'inherit'), l.max_requests, l.window_days,
		       COALESCE(l.approval_mode, 'inherit'), COALESCE(l.updated_at, g.updated_at), COALESCE(l.revision, 0)
		FROM access_groups g LEFT JOIN request_group_limits l ON l.group_id=g.id
		WHERE g.id=$1 AND g.organization_id=$2`, groupID, org))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant request group limit: %w", err)
	}
	if limit.Revision == 0 {
		return nil, nil // An owned group with no saved limit inherits.
	}
	return limit, nil
}

func (r *TenantGroupLimitRepository) UpsertGroupLimitConditional(ctx context.Context, in GroupLimit, expected int64) (*GroupLimit, error) {
	org, err := groupLimitOrganization(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var groupID int64
	// Hold ownership through the write. FOR SHARE also blocks organization
	// reassignment, so a separate preflight cannot race into a foreign group.
	if err = tx.QueryRow(ctx, `SELECT id FROM access_groups WHERE id=$1 AND organization_id=$2 FOR SHARE`, in.GroupID, org).Scan(&groupID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = lockRevision(ctx, tx, `SELECT revision FROM request_group_limits WHERE group_id=$1 FOR UPDATE`, []any{in.GroupID}, expected, true); err != nil {
		return nil, err
	}
	out, err := scanGroupLimit(tx.QueryRow(ctx, `
		INSERT INTO request_group_limits (group_id, limit_mode, max_requests, window_days, approval_mode, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (group_id) DO UPDATE SET
			limit_mode = EXCLUDED.limit_mode,
			max_requests = EXCLUDED.max_requests,
			window_days = EXCLUDED.window_days,
			approval_mode = EXCLUDED.approval_mode,
			updated_at = now()
		WHERE $6::bigint = -1 OR request_group_limits.revision = $6
		RETURNING group_id, limit_mode, max_requests, window_days, approval_mode, updated_at, revision`,
		in.GroupID, in.LimitMode, in.MaxRequests, in.WindowDays, in.ApprovalMode, expected))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaleRevision
	}
	if err != nil {
		return nil, fmt.Errorf("upsert tenant request group limit: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

var (
	_ Store                       = (*TenantGroupLimitRepository)(nil)
	_ GroupLimitStore             = (*TenantGroupLimitRepository)(nil)
	_ tenantBoundedStore          = (*TenantGroupLimitRepository)(nil)
	_ requestOrganizationStore    = (*TenantGroupLimitRepository)(nil)
	_ transactionalUserLimitStore = (*TenantGroupLimitRepository)(nil)
)
