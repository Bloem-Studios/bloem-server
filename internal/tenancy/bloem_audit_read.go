package tenancy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// OrganizationAuditEvent deliberately excludes before/after JSON documents and
// credential/account data. Both underlying ledgers are filtered by tenant.
type OrganizationAuditEvent struct {
	ID             int64     `json:"id"`
	Source         string    `json:"source"`
	CreatedAt      time.Time `json:"created_at"`
	ActorAccountID *int      `json:"actor_account_id"`
	Action         string    `json:"action"`
	TargetID       string    `json:"target_id"`
	Outcome        string    `json:"outcome"`
}
type OrganizationAuditPage struct {
	Events     []OrganizationAuditEvent `json:"events"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}
type organizationAuditCursor struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	ID     int64     `json:"id"`
}

func (s *Store) ListOrganizationAudit(ctx context.Context, organizationID uuid.UUID, cursor string) (OrganizationAuditPage, error) {
	page := OrganizationAuditPage{Events: []OrganizationAuditEvent{}}
	if organizationID == uuid.Nil {
		return page, ErrOrganizationNotFound
	}
	var before *time.Time
	var after organizationAuditCursor
	if cursor != "" {
		if len(cursor) > 1024 {
			return page, ErrInvalidCursor
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.ID <= 0 || after.Time.IsZero() || (after.Source != "lifecycle" && after.Source != "entitlement") {
			return page, ErrInvalidCursor
		}
		before = &after.Time
	}
	const limit = 50
	rows, err := s.pool.Query(ctx, `SELECT id,source,created_at,actor_account_id,action,target_id,outcome FROM (
  SELECT id,'lifecycle'::text AS source,created_at,actor_account_id,action,target_id,outcome
    FROM admin_audit_events WHERE organization_id=$1
  UNION ALL
  SELECT id,'entitlement'::text AS source,created_at,actor_account_id,action,
    COALESCE(target_account_id::text,organization_id::text) AS target_id,'success'::text AS outcome
    FROM entitlement_audit_events WHERE organization_id=$1
 ) events WHERE ($2::timestamptz IS NULL OR (created_at,source,id)<($2::timestamptz,$3::text,$4::bigint))
 ORDER BY created_at DESC,source DESC,id DESC LIMIT 51`, organizationID, before, after.Source, after.ID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var event OrganizationAuditEvent
		if err := rows.Scan(&event.ID, &event.Source, &event.CreatedAt, &event.ActorAccountID, &event.Action, &event.TargetID, &event.Outcome); err != nil {
			return page, err
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		last := page.Events[limit-1]
		raw, err := json.Marshal(organizationAuditCursor{Time: last.CreatedAt, Source: last.Source, ID: last.ID})
		if err != nil {
			return page, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}
