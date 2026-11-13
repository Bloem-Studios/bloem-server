package passwordreset

import (
	"fmt"
	"html"
	"strings"

	"github.com/Silo-Server/silo-server/internal/mail"
)

// composeResetNoticeEmail renders the message that tells an account holder an
// administrator of their organization issued a reset link for their account
// and handed it over out of band. It carries no link: whoever holds the link
// can use it, and this message only lets the account holder know.
func composeResetNoticeEmail(brand mail.Brand, username, organizationName, serverName string) emailContent {
	product := strings.TrimSpace(serverName)
	if product == "" {
		product = "Silo"
	}
	who := "An administrator of your organization"
	if name := strings.TrimSpace(organizationName); name != "" {
		who = fmt.Sprintf("An administrator of %s", name)
	}
	intro := fmt.Sprintf("%s issued a password reset for your %s account.", who, product)
	detail := "They may give you a link to choose a new password. Using it signs you out on every device. " +
		"If you did not expect this, tell your organization's administrator. Your password has not changed."

	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\n%s\n\nAccount: %s\n", intro, detail, username)

	var body strings.Builder
	body.WriteString(mail.EmailParagraph(intro))
	body.WriteString(mail.EmailParagraph(detail))
	body.WriteString(mail.EmailFacts(
		mail.EmailFact{Label: "Account", ValueHTML: html.EscapeString(username), Mono: true},
	))

	return emailContent{
		Subject: fmt.Sprintf("A password reset was issued for your %s account", product),
		Text:    text.String(),
		HTML: mail.RenderLayout(mail.LayoutOptions{
			Brand:      brand,
			Preheader:  "Your password has not changed.",
			Title:      "Password reset issued",
			BodyHTML:   body.String(),
			FooterHTML: html.EscapeString("You are getting this because you hold an account in this organization."),
		}),
	}
}
