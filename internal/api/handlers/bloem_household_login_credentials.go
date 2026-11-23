package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A login's credentials (Bloem household logins, household_login_credentials_v1).
// The storefront gives every login an email address of its own and a
// temporary password the person replaces at their first sign-in
// (BloemHouseholdLoginCreateRequest.RequirePasswordChange). An address another
// account already uses, the main account's included, is refused before the
// login is made as email_in_use rather than the duplicate that also covers a
// taken username, so the storefront can say which. Changing a login's address
// later is the ordinary account update on the login's own id.

// checkBloemHouseholdLoginEmail refuses an email address another account
// already uses, as its email or as a sign-in identifier, in any case. No
// address is not checked: a login may have none.
func checkBloemHouseholdLoginEmail(ctx context.Context, tx pgx.Tx, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	var inUse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1))
		    OR EXISTS (SELECT 1 FROM user_login_identifiers WHERE lower(identifier) = lower($1))`, email).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return apiError(http.StatusConflict, "email_in_use", "That email address is already used by another account")
	}
	return nil
}
