package handlers

import (
	"context"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// BloemSiloInboxView is the notification inbox Silo's /api/v2 routes serve on
// a Bloem server: Silo's inbox service with Bloem's own notices
// (system.alert, system.announcement) left out of every read and every read
// effect, so a Silo app sees the inbox, unread count and 404s a Silo server
// would give it (internal/notifications/bloem_silo_inbox.go).
//
// Each method is Silo's method of the same name (notification_inbox_service.go)
// reading through the repository's Silo-view queries. Every other method is
// Silo's, promoted from the embedded handler. Bloem's native routes
// (bloem_notifications.go) call the embedded handler directly and serve
// every row.
type BloemSiloInboxView struct {
	*NotificationsHandler
}

func (v BloemSiloInboxView) ListNotificationInbox(ctx context.Context, userID int, profile string, unread bool, limit int, before, through *notifications.Cursor) (NotificationInboxPageView, error) {
	var boundary notifications.Cursor
	var err error
	if through == nil {
		boundary, err = v.system.Deliveries.SiloInboxCutoff(ctx, userID, profile)
		if err != nil {
			return NotificationInboxPageView{}, err
		}
	} else {
		boundary = *through
	}
	rows, more, err := v.system.Deliveries.SiloInboxWindow(ctx, userID, profile, unread, limit, before, boundary)
	if err != nil {
		return NotificationInboxPageView{}, err
	}
	return NotificationInboxPageView{Items: v.system.PayloadsForRows(ctx, rows), Through: boundary, More: more}, nil
}

func (v BloemSiloInboxView) SyncNotificationInbox(ctx context.Context, userID int, profile string, limit int, since *notifications.Cursor) ([]notifications.DeliveryRowPayload, bool, int, error) {
	requested := limit
	if since != nil {
		requested++
	}
	rows, err := v.system.Deliveries.SiloListSync(ctx, userID, profile, since, requested)
	if err != nil {
		return nil, false, 0, err
	}
	more := since != nil && len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	count, err := v.system.Deliveries.SiloUnreadCount(ctx, userID, profile)
	if err != nil {
		return nil, false, 0, err
	}
	return v.system.PayloadsForRows(ctx, rows), more, count, nil
}

func (v BloemSiloInboxView) GetNotificationInboxItem(ctx context.Context, userID int, profile, id string) (notifications.DeliveryRowPayload, error) {
	row, err := v.system.Deliveries.SiloGetByID(ctx, userID, profile, id)
	if err != nil {
		return notifications.DeliveryRowPayload{}, err
	}
	if row == nil {
		return notifications.DeliveryRowPayload{}, apiError(404, "not_found", "Notification not found")
	}
	return v.system.PayloadForRow(ctx, *row), nil
}

func (v BloemSiloInboxView) NotificationUnreadCount(ctx context.Context, userID int, profile string) (int, error) {
	return v.system.Deliveries.SiloUnreadCount(ctx, userID, profile)
}

// MarkNotificationRead answers a Bloem notice as Silo answers an ID it does
// not have, and otherwise is Silo's.
func (v BloemSiloInboxView) MarkNotificationRead(ctx context.Context, userID int, profile, id string) error {
	kind, err := v.system.Deliveries.DeliveryType(ctx, userID, profile, id)
	if err != nil {
		return err
	}
	if kind != "" && !notifications.SiloInboxType(kind) {
		return apiError(404, "not_found", "Notification not found")
	}
	return v.NotificationsHandler.MarkNotificationRead(ctx, userID, profile, id)
}

func (v BloemSiloInboxView) MarkNotificationInboxThrough(ctx context.Context, userID int, profile string, through notifications.Cursor) error {
	changed, err := v.system.Deliveries.SiloMarkReadThrough(ctx, userID, profile, through)
	if err != nil {
		return err
	}
	if changed > 0 && v.hub != nil {
		_ = v.hub.PublishJSON(ctx, evt.ChannelNotifications, notifications.EventNotificationRead, map[string]any{settingFieldProfileID: profile, "through_created_at": through.CreatedAt.UTC().Format(time.RFC3339Nano), "through_id": through.ID}, evt.PublishOptions{UserID: userID, ProfileID: profile})
	}
	return nil
}

func (v BloemSiloInboxView) NotificationPushDisplay(ctx context.Context, userID int, profile, id string) (notifications.NotificationDisplay, error) {
	row, err := v.system.Deliveries.SiloGetByID(ctx, userID, profile, id)
	if err != nil {
		return notifications.NotificationDisplay{}, err
	}
	if row == nil {
		return notifications.NotificationDisplay{}, apiError(404, "not_found", "Notification not found")
	}
	return notifications.BuildNotificationDisplay(*row), nil
}
