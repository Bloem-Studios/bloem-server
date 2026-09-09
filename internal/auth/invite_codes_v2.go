package auth

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/models"
)

var ErrInviteCodeConflict = errors.New("invite code already exists with different configuration")

// CreateNamed uses the caller's code as the unique creation identity. A retry
// never changes an existing code's configuration or replenishes its uses.
func (r *InviteCodeRepository) CreateNamed(ctx context.Context, input models.CreateInviteCodeInput) (*models.InviteCode, error) {
	if input.Code == "" || input.MaxUses <= 0 || input.CreatedBy <= 0 {
		return nil, ErrInviteCodeInvalid
	}
	row, err := scanInviteCode(r.pool.QueryRow(ctx, `INSERT INTO invite_codes (code,label,max_uses,created_by,organization_id) VALUES ($1,$2,$3,$4,COALESCE(NULLIF($5,0),default_organization_id())) ON CONFLICT (code) DO NOTHING RETURNING `+inviteCodeColumns, input.Code, input.Label, input.MaxUses, input.CreatedBy, input.OrganizationID))
	if !errors.Is(err, ErrInviteCodeNotFound) {
		return row, err
	}
	row, err = scanInviteCode(r.pool.QueryRow(ctx, `SELECT `+inviteCodeColumns+` FROM invite_codes WHERE code=$1 AND organization_id=COALESCE(NULLIF($2,0),default_organization_id())`, input.Code, input.OrganizationID))
	if errors.Is(err, ErrInviteCodeNotFound) {
		return nil, ErrInviteCodeConflict
	}
	if err != nil {
		return nil, err
	}
	if row.CreatedBy != input.CreatedBy || row.Label != input.Label || row.MaxUses != input.MaxUses {
		return nil, ErrInviteCodeConflict
	}
	return row, nil
}

// ListPage uses a descending immutable ID continuation; no unbounded read is
// needed to build an administrator page.
func (r *InviteCodeRepository) ListPage(ctx context.Context, before int, limit int) ([]*models.InviteCode, bool, error) {
	limit = max(1, min(limit, 200))
	rows, err := r.pool.Query(ctx, `SELECT `+inviteCodeColumns+` FROM invite_codes WHERE ($1=0 OR id<$1) ORDER BY id DESC LIMIT $2`, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	codes := make([]*models.InviteCode, 0, limit+1)
	for rows.Next() {
		row, err := scanInviteCode(rows)
		if err != nil {
			return nil, false, err
		}
		codes = append(codes, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(codes) > limit
	if more {
		codes = codes[:limit]
	}
	return codes, more, nil
}
