package ambience

import (
	"context"
	"github.com/google/uuid"
)

// ActiveForBloemViewer returns public packs plus active packs for the viewer's
// current organization. Content, target and membership share one database
// snapshot so a concurrent retarget cannot authorize an older pack's contents.
func (s *Service) ActiveForBloemViewer(ctx context.Context, accountID int, organizationID uuid.UUID) ([]Wire, error) {
	if accountID <= 0 || organizationID == uuid.Nil {
		return nil, invalid("a viewer account and organization are required")
	}
	now := s.Now()
	packs, err := s.queryPacks(ctx, `
  SELECT `+packColumns+` FROM ambience_packs
  WHERE starts_at <= $1 AND ($1 < ends_at OR repeat_yearly)
    AND (organization_id IS NULL OR (organization_id = $3 AND EXISTS (
      SELECT 1 FROM organization_memberships m
      JOIN organizations o ON o.id = m.organization_id
      WHERE m.organization_id = $3 AND m.account_id = $2
        AND m.status = 'active' AND o.status = 'active')))
  ORDER BY starts_at, id`, now, accountID, organizationID)
	return activeWire(packs, now), err
}
