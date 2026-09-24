package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Visibility predicates shared by every client-facing read. Expired rows
// are never served (AMENDMENT 2); dismissed rows are hidden unless the
// caller opts in. Digest and outbox readers apply the expiry rule too so a
// lapsed alert is not mailed after the fact.
const (
	deliveryNotExpired   = "(d.expires_at IS NULL OR d.expires_at > now())"
	deliveryNotDismissed = "d.dismissed_at IS NULL"
)

func deliveryVisibility(includeDismissed bool) string {
	if includeDismissed {
		return deliveryNotExpired
	}
	return deliveryNotExpired + " AND " + deliveryNotDismissed
}

// ErrDeliveryNotDismissible reports a dismiss attempt on a row whose body
// carries dismissible=false (every critical alert).
var ErrDeliveryNotDismissible = errors.New("notification is not dismissible")

// Dismiss hides one delivery from the profile's feeds without marking it
// read. Idempotent; reports whether the row transitioned. Rows whose body
// says dismissible=false are refused with ErrDeliveryNotDismissible.
func (r *DeliveryRepository) Dismiss(ctx context.Context, profileID, id string) (bool, error) {
	var dismissible *bool
	var alreadyDismissed, expired bool
	err := r.pool.QueryRow(ctx, `
		SELECT (body->>'dismissible')::boolean, dismissed_at IS NOT NULL,
		       expires_at IS NOT NULL AND expires_at <= now()
		FROM notification_deliveries WHERE profile_id = $1 AND id = $2`,
		profileID, id).Scan(&dismissible, &alreadyDismissed, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load delivery for dismiss: %w", err)
	}
	if dismissible != nil && !*dismissible {
		return false, ErrDeliveryNotDismissible
	}
	if alreadyDismissed || expired {
		// Expired rows are invisible everywhere else; dismissing one is a
		// no-op rather than a state change.
		return false, nil
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET dismissed_at = now()
		WHERE profile_id = $1 AND id = $2 AND dismissed_at IS NULL`,
		profileID, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetRowsByIDs loads deliveries without profile scoping, for post-commit
// dispatch of a batch. Order is unspecified.
func (r *DeliveryRepository) GetRowsByIDs(ctx context.Context, ids []string) ([]DeliveryRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, deliveryRowSelect+` WHERE d.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("get delivery rows: %w", err)
	}
	return scanDeliveryRows(rows)
}

// WithdrawnDelivery identifies a row removed or expired by an announcement
// withdrawal so realtime clients can drop it.
type WithdrawnDelivery struct {
	ID        string
	UserID    int
	ProfileID string
}

// WithdrawAnnouncement removes the announcement's unread rows (their pending
// outbox attempts cascade away, so undelivered pushes are cancelled) and
// expires the already-read ones so every feed stops showing them. Returns
// both sets for realtime notification.
func (r *DeliveryRepository) WithdrawAnnouncement(ctx context.Context, tx pgx.Tx, announcementID string) ([]WithdrawnDelivery, error) {
	rows, err := tx.Query(ctx, `
		WITH removed AS (
			DELETE FROM notification_deliveries
			WHERE announcement_id = $1 AND read_at IS NULL
			RETURNING id, user_id, profile_id
		), expired AS (
			UPDATE notification_deliveries
			SET expires_at = now()
			WHERE announcement_id = $1 AND read_at IS NOT NULL
			  AND (expires_at IS NULL OR expires_at > now())
			RETURNING id, user_id, profile_id
		)
		SELECT id, user_id, profile_id FROM removed
		UNION ALL
		SELECT id, user_id, profile_id FROM expired`, announcementID)
	if err != nil {
		return nil, fmt.Errorf("withdraw announcement deliveries: %w", err)
	}
	defer rows.Close()
	out := make([]WithdrawnDelivery, 0, 16)
	for rows.Next() {
		var row WithdrawnDelivery
		if err := rows.Scan(&row.ID, &row.UserID, &row.ProfileID); err != nil {
			return nil, fmt.Errorf("scan withdrawn delivery: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
