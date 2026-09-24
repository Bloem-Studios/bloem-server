package notifications

const (
	// Severity colors for system alert embeds.
	discordAlertColorWarning  = 16753920 // orange
	discordAlertColorCritical = 15158332 // red
)

// applyDiscordAlertEmbed shapes a system alert/announcement embed. No
// provider links or catalog fields for system rows: the body text, the
// author's link, and a severity color are the whole embed.
func applyDiscordAlertEmbed(embed *discordEmbed, alertBody *AlertBody) {
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
