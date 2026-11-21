package apiv2

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

type BloemHouseholdLoginMoveProfileInput struct {
	AccountID string `path:"account_id" doc:"The main account's ID."`
	Body      handlers.BloemHouseholdLoginMoveProfileRequest
}
type BloemHouseholdLoginMoveToMainInput struct {
	AccountID string `path:"account_id" doc:"The main account's ID."`
	LoginID   string `path:"login_id" doc:"The login's account ID."`
}
type BloemHouseholdLoginMoveToMainOutput struct {
	Body handlers.BloemHouseholdLoginMoveToMainResponse
}

// registerBloemHouseholdLoginMovesDocument documents moving a person between
// a profile of a main account and a household login of their own (feature
// token household_login_moves_v1).
func registerBloemHouseholdLoginMovesDocument(reg *Registry) {
	const about = "A move keeps the profile's ID and everything it holds: watch history and progress, lists, ratings, collections, settings and PIN. It ends the sign-ins and device registrations the account it leaves had as that profile, so the person signs in to the account it joins. It is one transaction: it completes, or both accounts stay as they were. It never changes either account's limits; the caller makes room first. "
	toLogin := bloemStorefrontOp(reg, "POST", "/accounts/{account_id}/logins/move-profile", "moveBloemPlatformProfileToHouseholdLogin", "Give one of an account's profiles a household login of its own.", http.StatusCreated, map[int]string{
		400: "bad_request: the body is not valid JSON or exceeds 16 KiB, or a field is invalid (the message names it): a profile_id, a username and a password of 8 to 72 bytes are required, and max_streams and max_profiles must be at least 1.",
		404: "not_found: no such main account. profile_not_found: the main account has no such profile.",
		409: "primary_profile: the main account's primary profile cannot move. restricted_profile: a profile with parental limits (a child profile, library limits, a rating or age limit, requests blocked) cannot have a login of its own, since as its login's main profile it would manage its own limits. household_nested: the account is itself a login. household_main_ineligible: an admin account cannot have logins. duplicate: the username or email is already in use. silo_switching_on: Silo switching is on, which refuses household logins. profile_conflict: the new login could not take the profile.",
		501: "capability_unsupported: this server's account store cannot create accounts and profiles transactionally.",
		503: "service_unavailable: the server is too busy to hash the password; retry after Retry-After. tenant_unavailable: the credential could not be checked.",
	})
	toLogin.Description += about + "The login is created as the create route creates one, with the profile as its only profile and its primary, and the profile leaves the main account. Under a disabled main account the login starts disabled, with household_disabled true."
	registerBloemChiDocument[BloemHouseholdLoginMoveProfileInput, BloemHouseholdLoginOutput](reg, toLogin)
	toMain := bloemStorefrontOp(reg, "POST", "/accounts/{account_id}/logins/{login_id}/move-to-main", "moveBloemPlatformHouseholdLoginToMain", "Make a household login's profiles the main account's again and delete the login.", http.StatusOK, map[int]string{
		400: "bad_request: an invalid account or login ID.",
		404: "login_not_found: the account has no such login.",
		409: "profile_limit: the main account's profile limit has no room for the login's profiles. profile_name_conflict: the main account already has a profile named like one of the login's (names are unique per account, ignoring case). profile_conflict: the main account already has one of the profiles.",
		501: "capability_unsupported: this server's account store cannot change accounts transactionally.",
	})
	toMain.Description += about + "Every profile of the login joins the main account as an ordinary profile (never its primary), then the login is deleted as any account is: its sign-ins revoked and its deletion audited."
	registerBloemChiDocument[BloemHouseholdLoginMoveToMainInput, BloemHouseholdLoginMoveToMainOutput](reg, toMain)
}
