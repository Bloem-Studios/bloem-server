package notifications

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Silo's inbox, as Silo's routes serve it on a Bloem server.
//
// Bloem's own notices (system.alert and system.announcement: announcements,
// upgrade prompts, book request notices, saved campaigns) are rows in
// notification_deliveries beside Silo's. Silo has no such type, so on a Silo
// server Silo's /api/v2 notification routes and events socket never show one.
// Silo's apps read those routes on a Bloem server too, and a notice there is
// a "System" card with no text that counts as unread.
//
// So Silo's routes read through the methods below: Silo's own queries
// (inbox_pages.go, delivery_repo.go) with Bloem's notices left out, and
// nothing else changed. Silo's methods stay as Silo wrote them and keep
// serving every row to Bloem's native routes (/api/bloem/v2/notifications).

// siloSocketDispatcher publishes notification.created on the events socket
// for Silo's rows only. The socket is Silo's (/api/v2/events/ws): a Silo
// server never publishes a Bloem notice there, and a Silo app would add it to
// its inbox and badge as a "System" row with no text.
type siloSocketDispatcher struct {
	socket *WebsocketDispatcher
}

func (d siloSocketDispatcher) Dispatch(ctx context.Context, delivery DeliveryRow) error {
	if !SiloInboxType(delivery.Type) {
		return nil
	}
	return d.socket.Dispatch(ctx, delivery)
}

// SiloInboxType reports whether a delivery type is one a Silo server writes,
// so Silo's routes and socket may show it.
func SiloInboxType(deliveryType string) bool {
	return !IsSystemDeliveryType(deliveryType)
}

// siloInboxTypes is the predicate that leaves Bloem's notices out of a query
// on notification_deliveries under alias.
func siloInboxTypes(alias string) string {
	return alias + ".type NOT IN ('" + DeliveryTypeSystemAlert + "', '" + DeliveryTypeSystemAnnouncement + "')"
}

// SiloInboxCutoff is InboxCutoff over Silo's rows.
func (r *DeliveryRepository) SiloInboxCutoff(ctx context.Context, userID int, profileID string) (Cursor, error) {
	var cursor Cursor
	err := r.pool.QueryRow(ctx, `SELECT d.created_at,d.id FROM notification_deliveries d WHERE d.user_id = $1 AND d.profile_id = $2 AND `+siloInboxTypes("d")+` ORDER BY d.created_at DESC,d.id DESC LIMIT 1`, userID, profileID).Scan(&cursor.CreatedAt, &cursor.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cursor{}, nil
	}
	return cursor, err
}

// SiloInboxWindow is ListInboxWindow over Silo's rows.
func (r *DeliveryRepository) SiloInboxWindow(ctx context.Context, userID int, profileID string, unread bool, limit int, before *Cursor, through Cursor) ([]DeliveryRow, bool, error) {
	limit = max(1, min(limit, 200))
	if through.ID == "" {
		return []DeliveryRow{}, false, nil
	}
	var beforeTime any
	beforeID := ""
	if before != nil {
		beforeTime = before.CreatedAt
		beforeID = before.ID
	}
	rows, err := r.pool.Query(ctx, deliveryRowSelect+` WHERE d.user_id = $1 AND d.profile_id = $2 AND (NOT $3 OR d.read_at IS NULL) AND (d.created_at,d.id)<=($4,$5) AND ($6::timestamptz IS NULL OR (d.created_at,d.id)<($6,$7)) AND `+siloInboxTypes("d")+` ORDER BY d.created_at DESC,d.id DESC LIMIT $8`, userID, profileID, unread, through.CreatedAt, through.ID, beforeTime, beforeID, limit+1)
	if err != nil {
		return nil, false, err
	}
	result, err := scanDeliveryRows(rows)
	if err != nil {
		return nil, false, err
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

// SiloMarkReadThrough is MarkReadThrough over Silo's rows: a Silo client's
// "mark all read" leaves the Bloem notices it never saw unread.
func (r *DeliveryRepository) SiloMarkReadThrough(ctx context.Context, userID int, profileID string, through Cursor) (int64, error) {
	if through.ID == "" {
		return 0, nil
	}
	tag, err := r.pool.Exec(ctx, `UPDATE notification_deliveries d SET read_at=now() WHERE d.user_id = $1 AND d.profile_id = $2 AND d.read_at IS NULL AND (d.created_at,d.id)<=($3,$4) AND `+siloInboxTypes("d"), userID, profileID, through.CreatedAt, through.ID)
	if err != nil {
		return 0, fmt.Errorf("mark notification cutoff read: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SiloListSync is ListSync over Silo's rows.
func (r *DeliveryRepository) SiloListSync(ctx context.Context, userID int, profileID string, since *Cursor, limit int) ([]DeliveryRow, error) {
	visibility := deliveryVisibility(false) + " AND " + siloInboxTypes("d")
	if since != nil {
		rows, err := r.pool.Query(ctx,
			deliveryRowSelect+`
			WHERE d.user_id = $1 AND d.profile_id = $2 AND (d.created_at, d.id) > ($3, $4) AND `+visibility+`
			ORDER BY d.created_at ASC, d.id ASC
			LIMIT $5`,
			userID, profileID, since.CreatedAt, since.ID, limit)
		if err != nil {
			return nil, fmt.Errorf("list sync: %w", err)
		}
		return scanDeliveryRows(rows)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT * FROM (`+deliveryRowSelect+`
			WHERE d.user_id = $1 AND d.profile_id = $2 AND `+visibility+`
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT $3
		) recent ORDER BY recent.created_at ASC, recent.id ASC`,
		userID, profileID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sync: %w", err)
	}
	return scanDeliveryRows(rows)
}

// SiloUnreadCount is UnreadCount over Silo's rows.
func (r *DeliveryRepository) SiloUnreadCount(ctx context.Context, userID int, profileID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM notification_deliveries d
		 WHERE d.user_id = $1 AND d.profile_id = $2 AND d.read_at IS NULL AND `+deliveryVisibility(false)+` AND `+siloInboxTypes("d"),
		userID, profileID,
	).Scan(&count)
	return count, err
}

// SiloGetByID is GetByID over Silo's rows: a Bloem notice is (nil, nil), as
// an absent row is.
func (r *DeliveryRepository) SiloGetByID(ctx context.Context, userID int, profileID, id string) (*DeliveryRow, error) {
	row, err := r.GetByID(ctx, userID, profileID, id)
	if err != nil || row == nil || !SiloInboxType(row.Type) {
		return nil, err
	}
	return row, nil
}

// DeliveryType returns the type of the profile's delivery id, or "" when the
// profile has no such delivery.
func (r *DeliveryRepository) DeliveryType(ctx context.Context, userID int, profileID, id string) (string, error) {
	var deliveryType string
	err := r.pool.QueryRow(ctx,
		`SELECT type FROM notification_deliveries WHERE user_id = $1 AND profile_id = $2 AND id = $3`,
		userID, profileID, id,
	).Scan(&deliveryType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("delivery type: %w", err)
	}
	return deliveryType, nil
}
