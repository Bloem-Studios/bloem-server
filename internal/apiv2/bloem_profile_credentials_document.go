package apiv2

import "github.com/Silo-Server/silo-server/internal/auth"

type BloemProfileCredentialInput struct {
	ID           string `path:"id" doc:"Target profile in the authenticated account; other accounts' profiles return 404."`
	ProfileID    string `header:"X-Profile-Id" doc:"The acting household primary profile. Required unless the account is a server administrator; it is separate from the target path id."`
	ProfileToken string `header:"X-Profile-Token" doc:"Verification proof for a PIN-locked acting primary profile; required for household management unless the account is a server administrator."`
}

type BloemProfileCredentialOutput struct {
	Body auth.ProfileCredentialStatus
}

type BloemProfileCredentialSetBody struct {
	CurrentPassword  string `json:"current_password" writeOnly:"true" doc:"Current account password, verified again before the change."`
	LoginEmail       string `json:"login_email" format:"email" doc:"Direct-profile login email; surrounding whitespace is trimmed."`
	Password         string `json:"password" writeOnly:"true" minLength:"8" doc:"New direct-profile password: at least 8 characters and at most 72 UTF-8 bytes."`
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"Positive credential revision from the reviewed status response's credential_revision; this is not an organization policy revision."`
}

type BloemProfileCredentialClearBody struct {
	CurrentPassword  string `json:"current_password" writeOnly:"true" doc:"Current account password, verified again before clearing credentials."`
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"Positive credential revision from the reviewed status response's credential_revision; this is not an organization policy revision."`
	LoginEmail       string `json:"login_email,omitempty" doc:"Accepted but ignored by DELETE."`
	Password         string `json:"password,omitempty" writeOnly:"true" doc:"Accepted but ignored by DELETE."`
}

type BloemProfileCredentialSetInput struct {
	BloemProfileCredentialInput
	Body BloemProfileCredentialSetBody
}

type BloemProfileCredentialClearInput struct {
	BloemProfileCredentialInput
	Body BloemProfileCredentialClearBody
}

func registerBloemProfileCredentialDocument(reg *Registry) {
	const path = "/profile-credentials/{id}"
	const authority = "Requires a non-impersonated account login access token and household management authority: a server administrator, or the account's selected primary profile with PIN verification when locked. Ownership is always restricted to the caller's account. Direct-profile sessions, API keys and administrative contexts are refused."
	get := bloemChiDocumentOp(reg, "GET", path, "getBloemProfileCredentials", "profiles", "Read direct-profile credential status without exposing a password or hash.", "bloemAccountSession", 200)
	get.Description = authority
	bloemDocumentErrors[BloemNativeError](reg, &get, map[int]string{
		404: "not_found: profile does not belong to this account or does not exist.",
		500: "internal_error: household management permissions could not be checked.",
	})
	bloemDocumentRateLimit(reg, &get)
	registerBloemChiDocument[BloemProfileCredentialInput, BloemProfileCredentialOutput](reg, get)
	for _, method := range []string{"PUT", "DELETE"} {
		id, summary := "setBloemProfileCredentials", "Set or replace direct-profile login credentials."
		if method == "DELETE" {
			id, summary = "clearBloemProfileCredentials", "Clear direct-profile login credentials."
		}
		op := bloemChiDocumentOp(reg, method, path, id, "profiles", summary, "bloemAccountSession", 204)
		op.Description = authority + " The JSON body is required even on DELETE. The current account password is rechecked. expected_revision compares the target credential revision in the transaction; success increments it and revokes the target's direct-profile sessions. A stale revision is 409 credential_revision_conflict. Reload and review status after an uncertain result; do not automatically replay the mutation."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			403: "account_session_required, forbidden or reauthentication_failed: use an eligible account session, verify the primary profile when required, and supply the current account password.",
			404: "not_found: profile does not belong to this account or does not exist.",
			409: "credential_revision_conflict: reload the reviewed status; credential_email_in_use: the login email is already registered (PUT only).",
			500: "internal_error: household management permissions could not be checked.",
		})
		bloemDocumentRateLimit(reg, &op)
		if method == "PUT" {
			registerBloemChiDocument[BloemProfileCredentialSetInput, struct{}](reg, op)
		} else {
			registerBloemChiDocument[BloemProfileCredentialClearInput, struct{}](reg, op)
		}
	}
}
