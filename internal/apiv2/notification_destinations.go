package apiv2

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

const (
	listNotificationWebPushOperation        = "listNotificationWebPushSubscriptions"
	listNotificationWebhooksOperation       = "listNotificationWebhooks"
	listNotificationServerChannelsOperation = "listAdminNotificationServerChannels"
)

type NotificationDestinationService interface {
	UnsubscribeNotificationWebPush(context.Context, int, string, string) error
	DeleteNotificationWebPushSubscription(context.Context, int, string, string) error

	ListNotificationWebPushPage(context.Context, string, int, *notifications.Cursor) ([]notifications.WebPushSubscription, error)
	ListNotificationWebhookPage(context.Context, string, int, *notifications.Cursor) ([]notifications.Webhook, error)
	ListNotificationServerChannelPage(context.Context, int, *notifications.Cursor) ([]notifications.ServerChannel, error)
}

type NotificationDestinationListInput struct {
	LimitParam
	Cursor string `query:"cursor"`
}

func destinationInstant(t *time.Time) NullableInstant {
	if t == nil {
		return NullableInstant{}
	}
	return NullableInstant{Valid: true, Time: NewInstant(*t)}
}

type NotificationWebPushSubscription struct {
	ID            ID              `json:"id"`
	Endpoint      string          `json:"endpoint"`
	DeviceName    string          `json:"device_name,omitempty"`
	Enabled       bool            `json:"enabled"`
	CreatedAt     Instant         `json:"created_at"`
	LastSuccessAt NullableInstant `json:"last_success_at"`
	LastFailureAt NullableInstant `json:"last_failure_at"`
}

func notificationWebPushSubscriptionOf(row notifications.WebPushSubscription) NotificationWebPushSubscription {
	return NotificationWebPushSubscription{
		ID:            ID(row.ID),
		Endpoint:      row.Endpoint,
		DeviceName:    row.DeviceName,
		Enabled:       row.Enabled,
		CreatedAt:     NewInstant(row.CreatedAt),
		LastSuccessAt: destinationInstant(row.LastSuccessAt),
		LastFailureAt: destinationInstant(row.LastFailureAt),
	}
}

type NotificationWebPushSubscriptionListOutput struct {
	Body Collection[NotificationWebPushSubscription]
}

type NotificationWebhookDestination struct {
	ID                     ID              `json:"id"`
	Name                   string          `json:"name"`
	Type                   string          `json:"type" enum:"discord,generic"`
	URLHost                string          `json:"url_host"`
	Enabled                bool            `json:"enabled"`
	NotifyFavorites        bool            `json:"notify_favorites"`
	NotifyWatchlist        bool            `json:"notify_watchlist"`
	NotifyContinueWatching bool            `json:"notify_continue_watching"`
	NotifyNextUp           bool            `json:"notify_next_up"`
	NotifyRequests         bool            `json:"notify_requests"`
	ConsecutiveFailures    int             `json:"consecutive_failures"`
	DisabledReason         *string         `json:"disabled_reason"`
	LastSuccessAt          NullableInstant `json:"last_success_at"`
	LastFailureAt          NullableInstant `json:"last_failure_at"`
	LastFailureStatus      *int            `json:"last_failure_status"`
	LastFailureMessage     *string         `json:"last_failure_message"`
}

func notificationWebhookDestinationOf(row notifications.Webhook) NotificationWebhookDestination {
	return NotificationWebhookDestination{
		ID:                     ID(row.ID),
		Name:                   row.Name,
		Type:                   row.Type,
		URLHost:                row.URLHost,
		Enabled:                row.Enabled,
		NotifyFavorites:        row.NotifyFavorites,
		NotifyWatchlist:        row.NotifyWatchlist,
		NotifyContinueWatching: row.NotifyContinueWatching,
		NotifyNextUp:           row.NotifyNextUp,
		NotifyRequests:         row.NotifyRequests,
		ConsecutiveFailures:    row.ConsecutiveFailures,
		DisabledReason:         row.DisabledReason,
		LastSuccessAt:          destinationInstant(row.LastSuccessAt),
		LastFailureAt:          destinationInstant(row.LastFailureAt),
		LastFailureStatus:      row.LastFailureStatus,
		LastFailureMessage:     row.LastFailureMessage,
	}
}

type NotificationWebhookDestinationListOutput struct {
	Body Collection[NotificationWebhookDestination]
}

type NotificationServerChannel struct {
	ID                     ID              `json:"id"`
	Name                   string          `json:"name"`
	Type                   string          `json:"type" enum:"discord,generic"`
	URLHost                string          `json:"url_host"`
	Enabled                bool            `json:"enabled"`
	NotifyNewMovies        bool            `json:"notify_new_movies"`
	NotifyNewEpisodes      bool            `json:"notify_new_episodes"`
	NotifyNewAudiobooks    bool            `json:"notify_new_audiobooks"`
	NotifyNewEbooks        bool            `json:"notify_new_ebooks"`
	NotifyRequestSubmitted bool            `json:"notify_request_submitted"`
	NotifyRequestApproved  bool            `json:"notify_request_approved"`
	NotifyRequestDeclined  bool            `json:"notify_request_declined"`
	NotifyRequestFulfilled bool            `json:"notify_request_fulfilled"`
	ConsecutiveFailures    int             `json:"consecutive_failures"`
	DisabledReason         *string         `json:"disabled_reason"`
	LastSuccessAt          NullableInstant `json:"last_success_at"`
	LastFailureAt          NullableInstant `json:"last_failure_at"`
	LastFailureStatus      *int            `json:"last_failure_status"`
	LastFailureMessage     *string         `json:"last_failure_message"`
	CreatedAt              Instant         `json:"created_at"`
}

func notificationServerChannelOf(row notifications.ServerChannel) NotificationServerChannel {
	return NotificationServerChannel{
		ID:                     ID(row.ID),
		Name:                   row.Name,
		Type:                   row.Type,
		URLHost:                row.URLHost,
		Enabled:                row.Enabled,
		NotifyNewMovies:        row.NotifyNewMovies,
		NotifyNewEpisodes:      row.NotifyNewEpisodes,
		NotifyNewAudiobooks:    row.NotifyNewAudiobooks,
		NotifyNewEbooks:        row.NotifyNewEbooks,
		NotifyRequestSubmitted: row.NotifyRequestSubmitted,
		NotifyRequestApproved:  row.NotifyRequestApproved,
		NotifyRequestDeclined:  row.NotifyRequestDeclined,
		NotifyRequestFulfilled: row.NotifyRequestFulfilled,
		ConsecutiveFailures:    row.ConsecutiveFailures,
		DisabledReason:         row.DisabledReason,
		LastSuccessAt:          destinationInstant(row.LastSuccessAt),
		LastFailureAt:          destinationInstant(row.LastFailureAt),
		LastFailureStatus:      row.LastFailureStatus,
		LastFailureMessage:     row.LastFailureMessage,
		CreatedAt:              NewInstant(row.CreatedAt),
	}
}

type NotificationServerChannelListOutput struct {
	Body Collection[NotificationServerChannel]
}

type NotificationWebPushDeleteInput struct {
	ID string `path:"id"`
}

type NotificationWebPushUnsubscribeInput struct {
	Body struct {
		Endpoint string `json:"endpoint" minLength:"1" maxLength:"2048" writeOnly:"true"`
	}
}

func registerNotificationDestinations(reg *Registry) {
	unsubscribe := notificationOperation(http.MethodPost, "/web-push/unsubscribe", "unsubscribeNotificationWebPush")
	unsubscribe.DefaultStatus = http.StatusNoContent
	unsubscribe.RetrySafety = RetrySafetyNonRetryable
	unsubscribe.MaxBodyBytes = 4096
	unsubscribe.Summary = "Remove the account's current registration for a browser endpoint. Send once; a retry can delete a newer registration."
	Register(reg, unsubscribe, func(ctx context.Context, in *NotificationWebPushUnsubscribeInput) (*struct{}, error) {
		if reg.deps.NotificationDestinations == nil {
			return nil, unavailable("notification destinations")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		if err := reg.deps.NotificationDestinations.UnsubscribeNotificationWebPush(ctx, user, profile, in.Body.Endpoint); err != nil {
			return nil, serviceProblem(err)
		}
		return &struct{}{}, nil
	})

	op := notificationOperation(http.MethodDelete, "/web-push/subscriptions/{id}", "deleteNotificationWebPushSubscription")
	op.DefaultStatus = http.StatusNoContent
	op.RetrySafety = RetrySafetyNaturalIdempotent
	op.Summary = "Remove a server subscription belonging to the current profile. Does not unsubscribe the browser or order concurrent registration intents."
	Register(reg, op, func(ctx context.Context, in *NotificationWebPushDeleteInput) (*struct{}, error) {
		if reg.deps.NotificationDestinations == nil {
			return nil, unavailable("notification destinations")
		}
		user, profile, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		if err := reg.deps.NotificationDestinations.DeleteNotificationWebPushSubscription(ctx, user, profile, in.ID); err != nil {
			return nil, serviceProblem(err)
		}
		return &struct{}{}, nil
	})

	cursors := NewCursors(reg.deps.CursorSecret)

	Register(reg, notificationOperation(http.MethodGet, "/web-push/subscriptions", listNotificationWebPushOperation), func(ctx context.Context, in *NotificationDestinationListInput) (*NotificationWebPushSubscriptionListOutput, error) {
		svc := reg.deps.NotificationDestinations
		if svc == nil {
			return nil, unavailable("notification destinations")
		}
		scope := notificationCursorScope(ctx, listNotificationWebPushOperation, strconv.Itoa(in.Limit))
		var after *notifications.Cursor
		if in.Cursor != "" {
			after = new(notifications.Cursor)
			if p := cursors.Decode(scope, in.Cursor, after); p != nil {
				return nil, p
			}
		}
		rows, err := svc.ListNotificationWebPushPage(ctx, profileFrom(ctx), in.Limit+1, after)
		if err != nil {
			return nil, serviceProblem(err)
		}
		more := len(rows) > in.Limit
		if more {
			rows = rows[:in.Limit]
		}
		items := make([]NotificationWebPushSubscription, 0, len(rows))
		for _, row := range rows {
			items = append(items, notificationWebPushSubscriptionOf(row))
		}
		next := ""
		if more {
			last := rows[len(rows)-1]
			next, err = cursors.Encode(scope, notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			if err != nil {
				return nil, serviceProblem(err)
			}
		}
		return &NotificationWebPushSubscriptionListOutput{Body: Paginated(items, next)}, nil
	})

	Register(reg, notificationOperation(http.MethodGet, "/webhooks", listNotificationWebhooksOperation), func(ctx context.Context, in *NotificationDestinationListInput) (*NotificationWebhookDestinationListOutput, error) {
		svc := reg.deps.NotificationDestinations
		if svc == nil {
			return nil, unavailable("notification destinations")
		}
		scope := notificationCursorScope(ctx, listNotificationWebhooksOperation, strconv.Itoa(in.Limit))
		var after *notifications.Cursor
		if in.Cursor != "" {
			after = new(notifications.Cursor)
			if p := cursors.Decode(scope, in.Cursor, after); p != nil {
				return nil, p
			}
		}
		rows, err := svc.ListNotificationWebhookPage(ctx, profileFrom(ctx), in.Limit+1, after)
		if err != nil {
			return nil, serviceProblem(err)
		}
		more := len(rows) > in.Limit
		if more {
			rows = rows[:in.Limit]
		}
		items := make([]NotificationWebhookDestination, 0, len(rows))
		for _, row := range rows {
			items = append(items, notificationWebhookDestinationOf(row))
		}
		next := ""
		if more {
			last := rows[len(rows)-1]
			next, err = cursors.Encode(scope, notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			if err != nil {
				return nil, serviceProblem(err)
			}
		}
		return &NotificationWebhookDestinationListOutput{Body: Paginated(items, next)}, nil
	})

	Register(reg, Operation{Operation: humaOp(http.MethodGet, Prefix+"/admin/notifications/server-channels", listNotificationServerChannelsOperation, "admin", "List server notification channels."), Class: ClassActingAdmin, ServiceBacked: true, DemoRestricted: true}, func(ctx context.Context, in *NotificationDestinationListInput) (*NotificationServerChannelListOutput, error) {
		svc := reg.deps.NotificationDestinations
		if svc == nil {
			return nil, unavailable("notification destinations")
		}
		scope := notificationCursorScope(ctx, listNotificationServerChannelsOperation, strconv.Itoa(in.Limit))
		var after *notifications.Cursor
		if in.Cursor != "" {
			after = new(notifications.Cursor)
			if p := cursors.Decode(scope, in.Cursor, after); p != nil {
				return nil, p
			}
		}
		rows, err := svc.ListNotificationServerChannelPage(ctx, in.Limit+1, after)
		if err != nil {
			return nil, serviceProblem(err)
		}
		more := len(rows) > in.Limit
		if more {
			rows = rows[:in.Limit]
		}
		items := make([]NotificationServerChannel, 0, len(rows))
		for _, row := range rows {
			items = append(items, notificationServerChannelOf(row))
		}
		next := ""
		if more {
			last := rows[len(rows)-1]
			next, err = cursors.Encode(scope, notifications.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
			if err != nil {
				return nil, serviceProblem(err)
			}
		}
		return &NotificationServerChannelListOutput{Body: Paginated(items, next)}, nil
	})
}
