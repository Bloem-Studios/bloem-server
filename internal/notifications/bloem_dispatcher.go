package notifications

// EventNotificationDismissed carries {id, profile_id} when a profile
// dismisses a row.
const EventNotificationDismissed = "notification.dismissed"

// applyAlertPayload adds the S-1 alert fields to a release/request payload:
// expiry and dismissal state for every row, and the AlertBody fields for
// system.alert / system.announcement rows (docs/architecture/admin-announcements.md,
// "What viewers' apps receive").
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
