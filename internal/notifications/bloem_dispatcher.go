package notifications

const (
	// EventNotificationDismissed carries {id, profile_id} when a profile
	// dismisses a row; EventNotificationWithdrawn carries the same shape when
	// an admin withdraws the announcement behind it. Both are additive: older
	// clients ignore unknown event names.
	EventNotificationDismissed = "notification.dismissed"
	EventNotificationWithdrawn = "notification.withdrawn"
)

// applyAlertPayload adds the S-1 alert fields to a release/request payload:
// expiry and dismissal state for every row, and the AlertBody fields for
// system.alert / system.announcement rows (docs/specs/client-engagement.md
// §A).
func applyAlertPayload(payload DeliveryRowPayload, row DeliveryRow) DeliveryRowPayload {
	payload.ExpiresAt = row.ExpiresAt
	payload.DismissedAt = row.DismissedAt
	if body, ok := ParseAlertBody(row.Body); ok {
		payload.Title = body.Title
		payload.Body = body.Body
		payload.Severity = body.Severity
		payload.Deeplink = body.Deeplink
		payload.ImageURL = body.ImageURL
		dismissible := body.Dismissible
		payload.Dismissible = &dismissible
		payload.CTA = body.CTA
		if payload.ExpiresAt == nil {
			payload.ExpiresAt = body.ExpiresAt
		}
	}
	return payload
}
