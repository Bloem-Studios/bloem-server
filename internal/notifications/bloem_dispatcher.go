package notifications

const (
	// EventNotificationDismissed carries {id, profile_id} when a profile
	// dismisses a row; EventNotificationWithdrawn carries the same shape when
	// an admin withdraws the announcement behind it. Both are additive: older
	// clients ignore unknown event names.
	EventNotificationDismissed = "notification.dismissed"
	EventNotificationWithdrawn = "notification.withdrawn"
)
