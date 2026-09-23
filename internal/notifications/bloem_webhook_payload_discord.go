package notifications

const (
	// Severity colors for system alert embeds.
	discordAlertColorWarning  = 16753920 // orange
	discordAlertColorCritical = 15158332 // red
)

// applyDiscordAlertEmbed shapes a system alert/announcement embed. No
// provider links or catalog fields for system rows: the body text, the
// author's link, and a severity color are the whole embed.
// A nil alertBody (every non-system row) leaves the embed untouched.
func applyDiscordAlertEmbed(embed *discordEmbed, alertBody *AlertBody) {
	if alertBody == nil {
		return
	}
	embed.URL = ""
	if validAlertHTTPURL(alertBody.Deeplink) {
		embed.URL = alertBody.Deeplink
	}
	embed.Description = truncateWithEllipsis(alertBody.Body, discordDescriptionLimit)
	if alertBody.ImageURL != "" {
		embed.Thumbnail = &discordEmbedMedia{URL: alertBody.ImageURL}
	}
	switch alertBody.Severity {
	case SeverityCritical:
		embed.Color = discordAlertColorCritical
	case SeverityWarning:
		embed.Color = discordAlertColorWarning
	}
}

// discordSystemAlert returns the stored AlertBody for system rows (nil
// otherwise) and the overview to render: system alerts carry their own text
// and (optional) link; no catalog join.
func discordSystemAlert(row DeliveryRow, overview string) (*AlertBody, string) {
	if IsSystemDeliveryType(row.Type) {
		if body, ok := ParseAlertBody(row.Body); ok {
			return body, body.Body
		}
	}
	return nil, overview
}

// discordAlertTitle is the embed title for system alert/announcement rows.
func discordAlertTitle(row DeliveryRow) string {
	if body, ok := ParseAlertBody(row.Body); ok && body.Title != "" {
		return body.Title
	}
	return genericNotificationTitle
}
