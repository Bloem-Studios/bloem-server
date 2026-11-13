package apiv2

import (
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
)

// The organization support routes: what an organization administrator uses to
// give support to their own organization. Handlers are in
// internal/api/handlers/bloem_admin_support*.go.

const (
	bloemSupportAuthority = bloemOrganizationAuthorityDescription + " Who may act on whom is judged under row locks in the same transaction as the write: an organization administrator acts on ordinary members only; the organization owner may also act on the organization's other administrators; nobody acts on themselves; only a platform administrator acts on the organization owner; a platform-level account is touched only where the server Owner rule allows. A refusal is 409 target_protected with a reason, never 403. Ids outside the organization are indistinguishable from ids that do not exist (404). Every call writes its own audit event, and the support rate limits count refused calls too."

	bloemSupportProtected   = "target_protected (reason self, platform_account, organization_owner or organization_admin): the person is protected from this actor. membership_not_active: the membership is invited or suspended. account_shared: the account belongs to more than one organization, so an account-wide effect is refused. authorization_state_changed: the stale revision; current_revision carries the current value."
	bloemSupportRateLimited = "rate_limited: a support rate limit is spent; Retry-After says when the oldest counted action leaves its window."
)

// BloemSupportConflict is the 409 of the support routes. reason accompanies
// target_protected, current_revision a stale revision, capability a delivery
// the server cannot make.
type BloemSupportConflict struct {
	Error           string `json:"error"`
	Message         string `json:"message"`
	Reason          string `json:"reason,omitempty" enum:"self,platform_account,organization_owner,organization_admin" doc:"With target_protected: why the person is protected from this actor."`
	CurrentRevision int64  `json:"current_revision,omitempty" doc:"With authorization_state_changed: the person's security_revision, or the profile's revision for a profile write."`
	Capability      string `json:"capability,omitempty" enum:"email,link" doc:"With capability_not_configured: the delivery this server cannot make."`
}

type BloemSupportResetRequest struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"The person's security_revision. It is checked, not raised: issuing a link does not sign the person out."`
	Delivery         string `json:"delivery" enum:"email,link" doc:"email sends the link to the account's own address (valid 24 hours); link returns it once in this response for the administrator to hand over (valid 4 hours)."`
}

type BloemSupportReset struct {
	Delivery       string    `json:"delivery" enum:"email,link"`
	DeliveryStatus string    `json:"delivery_status" enum:"not_requested,sent,failed_or_unknown" doc:"not_requested for link. For email, sent or failed_or_unknown: the link is live either way, and issuing again replaces it."`
	ResetURL       string    `json:"reset_url,omitempty" readOnly:"true" doc:"link delivery only. A bearer credential: shown once, never stored in audit or logs. The host is the target organization's primary domain."`
	ExpiresAt      time.Time `json:"expires_at"`
}

type BloemSupportResetResponse struct {
	Reset  BloemSupportReset         `json:"reset"`
	Person adminpeople.PersonSummary `json:"person"`
}

type BloemSupportSessionsResponse struct {
	Sessions []adminpeople.SessionView `json:"sessions" doc:"Live sessions, newest first, at most 50. No IP address and no impersonation sessions."`
}

type BloemSupportDevicesResponse struct {
	Devices []adminpeople.DeviceView `json:"devices"`
}

type BloemSupportRevoked struct {
	Sessions int `json:"sessions"`
}

type BloemSupportSignOutResponse struct {
	Person  adminpeople.PersonSummary `json:"person" doc:"With the raised security_revision."`
	Revoked BloemSupportRevoked       `json:"revoked"`
}

type BloemSupportProfileCreate struct {
	Name    string `json:"name" minLength:"1" maxLength:"100" doc:"Unique among the account's profiles, case-insensitively. Surrounding space is trimmed."`
	GroupID *int   `json:"group_id,omitempty" minimum:"1" doc:"An access group of this organization. Defaults to the group the account's other profiles inherit."`
}

type BloemSupportProfileRename struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"The profile's own revision (profiles[].revision), not the person's security_revision."`
	Name             string `json:"name" minLength:"1" maxLength:"100"`
}

type BloemSupportProfileRevision struct {
	ExpectedRevision int64 `json:"expected_revision" minimum:"1" doc:"The profile's own revision (profiles[].revision), not the person's security_revision."`
}

type BloemSupportProfileResponse struct {
	Profile adminpeople.ProfileSummary `json:"profile"`
	Person  adminpeople.PersonSummary  `json:"person"`
}

type BloemSupportProfileOnlyResponse struct {
	Profile adminpeople.ProfileSummary `json:"profile"`
}

type bloemSupportSessionInput struct {
	SessionID string `path:"session_id" maxLength:"256"`
}

type bloemSupportDeviceInput struct {
	DeviceID string `path:"device_id" maxLength:"256"`
}

// bloemSupportOp starts a support operation with the answers every one shares.
func bloemSupportOp(reg *Registry, method, path, id, summary string, status int) huma.Operation {
	op := bloemAdminDocumentOp(reg, method, path, id, summary, "bloemOrganizationContext", status)
	op.Description = bloemSupportAuthority
	bloemDocumentErrors[BloemSupportConflict](reg, &op, map[int]string{409: bloemSupportProtected})
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found"})
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: bloemSupportRateLimited})
	op.Responses["429"].Headers = map[string]*huma.Header{
		"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds to wait before retrying."},
	}
	return op
}

func registerBloemOrganizationSupportDocument(reg *Registry) {
	const person = "/admin/organization/people/{account_id}"
	const notFound = "not_found: account_id is not a positive integer or not a member of the organization; the session, device, profile or group is not this person's or this organization's."

	{
		op := bloemSupportOp(reg, "POST", person+"/password-reset", "createBloemOrganizationPersonPasswordReset", "Issue a single-use password reset link for a member.", 201)
		op.Description += " The administrator never types or sees a password. The link is Silo's reset link (one live link per account; issuing replaces it) and is completed on Silo's public reset route, which sets the password, clears the temporary flag and signs the person out everywhere. The response is Cache-Control: no-store. With delivery=link the person also receives an email notice, without the link. The server refuses before replacing a live link when the account cannot take the delivery."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: notFound,
			409: bloemSupportProtected + " password_login_disabled: the account signs in through an external provider. account_disabled: the account is disabled. email_unavailable: delivery=email and the account has no valid address. capability_not_configured (capability email or link): no mail server or no public URL.",
			422: "validation_failed: expected_revision is not positive or delivery is not email or link.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemSupportResetRequest], bloemJSONOutput[BloemSupportResetResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "GET", person+"/sessions", "listBloemOrganizationPersonSessions", "List a member's live sessions.", 200)
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemSupportSessionsResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "DELETE", person+"/sessions", "revokeBloemOrganizationPersonSessions", "Sign a member out everywhere.", 200)
		op.Description += " Raises the person's security_revision, which ends membership-bound tokens and older account-login sessions, and revokes every session of the account, impersonation sessions included. The person's personal API keys survive."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected, 422: "validation_failed: expected_revision is not positive."})
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{503: "tenant_unavailable: the store is unavailable. dependency_unavailable (Retry-After: 30): the sign-out took effect but a connected service could not be told; retrying is safe."})
		registerBloemChiDocument[bloemJSONInput[BloemSupportProfileRevision], bloemJSONOutput[BloemSupportSignOutResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "DELETE", person+"/sessions/{session_id}", "revokeBloemOrganizationPersonSession", "Sign a member out of one session.", 204)
		op.Description += " Does not raise the security revision. Succeeds when the session already ended. An impersonation session, or one that is not this person's, is 404."
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected})
		registerBloemChiDocument[bloemSupportSessionInput, struct{}](reg, op)
	}
	{
		op := bloemSupportOp(reg, "GET", person+"/devices", "listBloemOrganizationPersonDevices", "List the devices a member's profiles have used.", 200)
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemSupportDevicesResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "DELETE", person+"/devices/{device_id}", "signOutBloemOrganizationPersonDevice", "Sign a member out of one device.", 204)
		op.Description += " Ends the person's sessions opened from the device and forgets its registration and per-device settings for their profiles in this organization. Does not raise the security revision and does not stop a stream that is playing. A device the person does not have is 404."
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected})
		registerBloemChiDocument[bloemSupportDeviceInput, struct{}](reg, op)
	}
	{
		op := bloemSupportOp(reg, "POST", person+"/profiles", "createBloemOrganizationPersonProfile", "Add a profile to a member.", 201)
		op.Description += " The account's profile limit applies. The administrator cannot set a PIN or parental fields."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: notFound,
			409: bloemSupportProtected + " name_conflict: the account already has a profile with this name.",
			422: "validation_failed: name is empty, too long or has control characters, or group_id is not a group of this organization. profile_limit_reached: the account is at its profile limit.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemSupportProfileCreate], bloemJSONOutput[BloemSupportProfileResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "PATCH", person+"/profiles/{profile_id}/details", "renameBloemOrganizationPersonProfile", "Rename a member's profile.", 200)
		op.Description += " Uses the profile's own revision and does not sign the person out."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: notFound,
			409: bloemSupportProtected + " name_conflict: another profile of the account has this name. For a stale profile revision, authorization_state_changed with current_revision.",
			422: "validation_failed: expected_revision is not positive or name is invalid.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemSupportProfileRename], bloemJSONOutput[BloemSupportProfileResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "DELETE", person+"/profiles/{profile_id}/pin", "clearBloemOrganizationPersonProfilePin", "Clear a member's forgotten profile PIN.", 200)
		op.Description += " The administrator never sets a PIN: the member or the household's main profile chooses a new one. Clearing the primary profile's PIN is allowed and audited. Clearing a profile that has no PIN succeeds. A profile locked by wrong guesses works at once."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{404: notFound, 409: bloemSupportProtected, 422: "validation_failed: expected_revision is not positive."})
		registerBloemChiDocument[bloemJSONInput[BloemSupportProfileRevision], bloemJSONOutput[BloemSupportProfileOnlyResponse]](reg, op)
	}
	{
		op := bloemSupportOp(reg, "DELETE", person+"/profiles/{profile_id}", "removeBloemOrganizationPersonProfile", "Remove a member's profile.", 204)
		op.Description += " Removes the profile's watch history and personal collections; this cannot be undone. The primary profile cannot be removed. Sessions bound to the profile end."
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: notFound,
			409: bloemSupportProtected + " primary_profile_protected: the primary profile cannot be removed.",
			422: "validation_failed: expected_revision is not positive.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemSupportProfileRevision], struct{}](reg, op)
	}
}
