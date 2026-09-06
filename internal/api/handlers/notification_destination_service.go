package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

func (h *NotificationsHandler) ListNotificationWebPushPage(ctx context.Context, profile string, limit int, after *notifications.Cursor) ([]notifications.WebPushSubscription, error) {
	svc := h.webPush()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Web push is not available")
	}
	return svc.ListPage(ctx, profile, limit, after)
}
func (h *NotificationsHandler) ListNotificationWebhookPage(ctx context.Context, profile string, limit int, after *notifications.Cursor) ([]notifications.Webhook, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.ListPage(ctx, profile, limit, after)
}
func (h *NotificationsHandler) ListNotificationServerChannelPage(ctx context.Context, limit int, after *notifications.Cursor) ([]notifications.ServerChannel, error) {
	if h == nil || h.system == nil || h.system.ServerChannels == nil {
		return nil, apiError(503, "unavailable", "Server channels are not available")
	}
	return h.system.ServerChannels.ListPage(ctx, limit, after)
}

func (h *NotificationsHandler) TestNotificationWebhook(ctx context.Context, profile, id string) (*notifications.WebhookTestResult, error) {
	svc := h.webhooks()
	if svc == nil {
		return nil, apiError(503, "unavailable", "Webhooks are not available")
	}
	return svc.Test(ctx, profile, id)
}
func (h *NotificationsHandler) TestNotificationServerChannel(ctx context.Context, id string) (*notifications.WebhookTestResult, error) {
	if h == nil || h.system == nil || h.system.ServerChannels == nil {
		return nil, apiError(503, "unavailable", "Server channels are not available")
	}
	return h.system.ServerChannels.Test(ctx, id)
}
