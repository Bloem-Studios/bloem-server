package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// Bloem: every storefront route that sets a password applies the server's
// password policy (Silo #5124, Settings → Sign-in), as Silo's own password
// writes do, and a refusal says which rule the password broke. The account
// and household-login routes check it with validateAdminPassword, the tenant
// member routes through tenancy.MemberService.SetPasswordPolicy with
// BloemCheckNewPassword. See docs/architecture/bloem-household-logins.md.

// BloemCheckNewPassword applies the server's password policy to a password a
// Bloem route sets, with the settings validateAdminPassword reads, and
// returns the policy's own error (auth.PasswordRuleMessage reads it).
func (h *AdminHandler) BloemCheckNewPassword(ctx context.Context, password string) error {
	var settings auth.SettingsGetter
	if h != nil && h.SettingsRepo != nil {
		settings = h.SettingsRepo
	}
	return auth.CheckNewPassword(ctx, settings, password)
}

// BloemPasswordRuleCode names the rule of the password policy err refused a
// password for, with the field-error codes Silo's v2 API gives the same
// refusal (Silo #5356). ok is false when err is not such a refusal.
func BloemPasswordRuleCode(err error) (code string, ok bool) {
	if _, ok := auth.PasswordRuleMessage(err); !ok {
		return "", false
	}
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort):
		return "password_too_short", true
	case errors.Is(err, auth.ErrPasswordTooLong):
		return "password_too_long", true
	case errors.Is(err, auth.ErrPasswordNeedsUppercase):
		return "password_needs_uppercase", true
	case errors.Is(err, auth.ErrPasswordNeedsSpecial):
		return "password_needs_special", true
	}
	return "invalid", true
}

// bloemPasswordRuleResponse is the 400 of a password the policy refused: the
// usual bad_request, with code naming the rule and message its English
// sentence (BloemNativeError).
type bloemPasswordRuleResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeBloemPasswordRuleError writes err as that response when the password
// policy refused the password, and reports whether it did.
func writeBloemPasswordRuleError(w http.ResponseWriter, err error) bool {
	code, ok := BloemPasswordRuleCode(err)
	if !ok {
		return false
	}
	message, _ := auth.PasswordRuleMessage(err)
	writeJSON(w, http.StatusBadRequest, bloemPasswordRuleResponse{Error: "bad_request", Code: code, Message: message})
	return true
}
