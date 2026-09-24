package notifications

import "strings"

// applySystemAlertDisplay renders system.alert / system.announcement rows
// from their stored AlertBody.
func applySystemAlertDisplay(display *NotificationDisplay, row DeliveryRow) {
	display.Category = strings.ReplaceAll(row.Type, ".", "_")
	if body, ok := ParseAlertBody(row.Body); ok {
		display.Title = truncateDisplayText(body.Title, displayBodyMaxLen)
		display.Body = truncateDisplayText(body.Body, displayBodyMaxLen)
		if body.Deeplink != "" {
			display.URL = body.Deeplink
		}
	}
	if row.AnnouncementID != nil && *row.AnnouncementID != "" {
		display.ThreadID = "announcement:" + *row.AnnouncementID
	}
}

// systemAlertWebPushIcon: system alerts carry their own image instead of a
// poster; every other type keeps the poster URL the payload builder resolved.
func systemAlertWebPushIcon(row DeliveryRow) string {
	if body, ok := ParseAlertBody(row.Body); ok {
		return body.ImageURL
	}
	return ""
}
