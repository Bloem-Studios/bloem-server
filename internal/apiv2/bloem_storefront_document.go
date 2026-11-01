package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// The platform administration a storefront (Bloem Garden) drives with its API
// key: reseller organizations (tenants) and their members, an account's
// profiles, devices and sign-in sessions, and announcements
// (api.mountBloemPlatformStorefront). The handlers are the ones behind the
// legacy /api/v1/admin routes; these are their native addresses.

// bloemPlatformCredential401 describes a refused Platform context or API key.
const bloemPlatformCredential401 = "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled."

const bloemStorefrontAuthority = "Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys, organization contexts and account sessions are refused, and the account is checked again on every request. "

type BloemTenantExternalRef struct {
	OperatorID string `json:"operator_id,omitempty" doc:"The storefront operator's reference."`
	ServiceID  string `json:"service_id" doc:"The storefront's service reference. Creation is idempotent on it."`
}

type BloemTenantLimits struct {
	Slots      int `json:"slots" minimum:"1" doc:"Seats: the most members the organization may have."`
	Transcodes int `json:"transcodes" minimum:"0" doc:"Video conversions shared by all members."`
}

type BloemTenantCreateBody struct {
	Name                        string                 `json:"name" minLength:"1" doc:"Display name."`
	ExternalRef                 BloemTenantExternalRef `json:"external_ref"`
	Limits                      BloemTenantLimits      `json:"limits"`
	EntitlementTemplateKey      string                 `json:"entitlement_template_key,omitempty" doc:"Plan to apply to the organization."`
	EntitlementTemplateRevision int64                  `json:"entitlement_template_revision,omitempty" doc:"Exact revision of that plan."`
}

type BloemTenantPathInput struct {
	ID string `path:"id" doc:"The tenant's organization ID (UUID). A malformed or unknown ID is not_found."`
}
type BloemTenantCreateInput struct{ Body BloemTenantCreateBody }
type BloemTenantLimitsInput struct {
	ID   string `path:"id" doc:"The tenant's organization ID (UUID)."`
	Body BloemTenantLimits
}
type BloemTenantOutput struct{ Body handlers.BloemTenantWire }
type BloemTenantListOutput struct{ Body []handlers.BloemTenantWire }

type BloemTenantMemberCreateBody struct {
	Username string `json:"username" doc:"Sign-in name."`
	Email    string `json:"email" doc:"Email address."`
	Password string `json:"password" doc:"Initial password."`
}
type BloemTenantMemberUpdateBody struct {
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
}
type BloemTenantMemberPasswordBody struct {
	Password string `json:"password" doc:"New password."`
}

type BloemTenantMembersInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
}
type BloemTenantMemberCreateInput struct {
	TenantID       string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	IdempotencyKey string `header:"Idempotency-Key" required:"true" doc:"Required. A replay of the same key and body returns the member with 200; the same key for another body is idempotency_conflict."`
	Body           BloemTenantMemberCreateBody
}
type BloemTenantMemberInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID   string `path:"user_id" doc:"The member's account ID."`
}
type BloemTenantMemberUpdateInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID   string `path:"user_id" doc:"The member's account ID."`
	Body     BloemTenantMemberUpdateBody
}
type BloemTenantMemberPasswordInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID   string `path:"user_id" doc:"The member's account ID."`
	Body     BloemTenantMemberPasswordBody
}
type BloemTenantMemberOutput struct {
	Body handlers.BloemTenantMemberWire
}
type BloemTenantMemberListOutput struct {
	Body []handlers.BloemTenantMemberWire
}

// Account resources, reached through a tenant member or directly by account.
type BloemTenantMemberProfileCreateInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID   string `path:"user_id" doc:"The member's account ID."`
	Body     handlers.ProfileCreateRequest
}
type BloemTenantMemberProfileInput struct {
	TenantID  string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID    string `path:"user_id" doc:"The member's account ID."`
	ProfileID string `path:"profile_id"`
}
type BloemTenantMemberProfileUpdateInput struct {
	TenantID  string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID    string `path:"user_id" doc:"The member's account ID."`
	ProfileID string `path:"profile_id"`
	Body      handlers.ProfileUpdateRequest
}
type BloemTenantMemberDeviceInput struct {
	TenantID string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID   string `path:"user_id" doc:"The member's account ID."`
	DeviceID string `path:"device_id"`
}
type BloemTenantMemberSessionInput struct {
	TenantID  string `path:"tenant_id" doc:"The tenant's organization ID (UUID)."`
	UserID    string `path:"user_id" doc:"The member's account ID."`
	SessionID string `path:"session_id"`
}

type BloemPlatformAccountInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
}
type BloemPlatformAccountProfileCreateInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	Body      handlers.ProfileCreateRequest
}
type BloemPlatformAccountProfileInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	ProfileID string `path:"profile_id"`
}
type BloemPlatformAccountProfileUpdateInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	ProfileID string `path:"profile_id"`
	Body      handlers.ProfileUpdateRequest
}
type BloemPlatformAccountDeviceInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	DeviceID  string `path:"device_id"`
}
type BloemPlatformAccountSessionInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	SessionID string `path:"session_id"`
}

type BloemAccountProfileOutput struct{ Body handlers.ProfileView }
type BloemAccountProfileListOutput struct{ Body []handlers.ProfileView }
type BloemAccountDeviceListOutput struct {
	Body []handlers.BloemAdminDeviceWire
}
type BloemAccountSessionListOutput struct {
	Body []handlers.BloemAdminAuthSessionWire
}

type BloemAnnouncementList struct {
	Announcements []notifications.Announcement `json:"announcements" nullable:"false"`
}
type BloemAnnouncementListOutput struct{ Body BloemAnnouncementList }
type BloemAnnouncementCreateInput struct {
	Body notifications.AnnouncementInput
}
type BloemAnnouncementOutput struct{ Body notifications.Announcement }
type BloemAnnouncementInput struct {
	ID string `path:"id" doc:"The announcement ID."`
}

func bloemStorefrontOp(reg *Registry, method, path, id, summary string, status int, errors map[int]string) huma.Operation {
	op := bloemChiDocumentOp(reg, method, "/admin/platform"+path, id, "administration", summary, "bloemPlatformContext", status)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = bloemStorefrontAuthority
	all := map[int]string{
		401: bloemPlatformCredential401,
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator.",
		500: "internal_error: the operation failed; details are withheld.",
		503: "tenant_unavailable or service_unavailable: the credential or the account could not be checked.",
	}
	for code, text := range errors {
		all[code] = text
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, all)
	return op
}

func registerBloemStorefrontDocument(reg *Registry) {
	registerBloemTenantDocument(reg)
	registerBloemTenantMemberDocument(reg)
	registerBloemPlatformAccountDocument(reg)
	registerBloemPlatformAccountResourceDocument(reg)
	registerBloemAnnouncementDocument(reg)
	registerBloemAmbienceAssetDocument(reg)
}

func registerBloemTenantDocument(reg *Registry) {
	notFound := map[int]string{404: "not_found: no such tenant (a malformed ID answers the same)."}

	list := bloemStorefrontOp(reg, "GET", "/tenants", "listBloemPlatformTenants", "List reseller organizations with live usage.", http.StatusOK, nil)
	list.Description += "A bare JSON array. A tenant is an organization created through this API, with seats (limits.slots), shared video conversions (limits.transcodes) and frozen when suspended: frozen_reason is quota (more members than seats; lifts by itself) or admin (lifts only on thaw)."
	registerBloemChiDocument[struct{}, BloemTenantListOutput](reg, list)

	create := bloemStorefrontOp(reg, "POST", "/tenants", "createBloemPlatformTenant", "Create a reseller organization.", http.StatusCreated, map[int]string{
		400: "invalid_json: the body is not valid JSON.",
		422: "validation: a tenant needs a name, an external service reference and at least one seat.",
	})
	create.Description += "Idempotent on external_ref.service_id: a replay returns the same organization instead of creating a second. The plan, when named, is applied to the new organization."
	registerBloemChiDocument[BloemTenantCreateInput, BloemTenantOutput](reg, create)

	get := bloemStorefrontOp(reg, "GET", "/tenants/{id}", "getBloemPlatformTenant", "Read one reseller organization.", http.StatusOK, notFound)
	registerBloemChiDocument[BloemTenantPathInput, BloemTenantOutput](reg, get)

	retire := bloemStorefrontOp(reg, "DELETE", "/tenants/{id}", "retireBloemPlatformTenant", "Retire a reseller organization.", http.StatusNoContent, map[int]string{
		404: notFound[404],
		503: "tenant_unavailable: retirement is not available, or the credential could not be checked.",
	})
	retire.Description += "The organization stays on record, suspended, with its external reference cleared. Its memberships are removed; member accounts with no other organization and no platform obligation are deleted."
	registerBloemChiDocument[BloemTenantPathInput, struct{}](reg, retire)

	limits := bloemStorefrontOp(reg, "PATCH", "/tenants/{id}/limits", "updateBloemPlatformTenantLimits", "Change seats and shared conversions.", http.StatusOK, map[int]string{
		400: "invalid_json: the body is not valid JSON.",
		404: notFound[404],
		422: "validation: slots must be at least 1 and transcodes 0 or more.",
	})
	limits.Description += "Applied in place. Dropping seats below the members in use suspends the organization at once with reason quota, until members are removed."
	registerBloemChiDocument[BloemTenantLimitsInput, BloemTenantOutput](reg, limits)

	freeze := bloemStorefrontOp(reg, "POST", "/tenants/{id}/freeze", "freezeBloemPlatformTenant", "Suspend a reseller organization.", http.StatusNoContent, notFound)
	freeze.Description += "Suspends with reason admin, for example for non-payment: members are refused new sessions and playback; data is kept."
	registerBloemChiDocument[BloemTenantPathInput, struct{}](reg, freeze)

	thaw := bloemStorefrontOp(reg, "POST", "/tenants/{id}/thaw", "thawBloemPlatformTenant", "Lift an administrative suspension.", http.StatusNoContent, notFound)
	thaw.Description += "An organization still over its seats stays suspended, with reason quota."
	registerBloemChiDocument[BloemTenantPathInput, struct{}](reg, thaw)
}

func registerBloemTenantMemberDocument(reg *Registry) {
	memberErrors := map[int]string{
		400: "bad_request: the body is not valid JSON or the request is invalid.",
		404: "not_found: no such tenant, or the account is not a member of it (malformed IDs answer the same).",
		409: "username_conflict: the username or email is already in use.",
	}
	const base = "/tenants/{tenant_id}/members"

	list := bloemStorefrontOp(reg, "GET", base, "listBloemPlatformTenantMembers", "List a reseller organization's members.", http.StatusOK, memberErrors)
	list.Description += "A bare JSON array. status is active, or suspended when the account is disabled."
	registerBloemChiDocument[BloemTenantMembersInput, BloemTenantMemberListOutput](reg, list)

	createErrors := map[int]string{
		400: "bad_request: the body is not valid JSON. idempotency_key_required: the Idempotency-Key header is missing.",
		404: memberErrors[404],
		409: "username_conflict: the username or email is already in use. idempotency_conflict: the key was used for a different command.",
		422: "slot_quota_exceeded: every seat is taken.",
	}
	create := bloemStorefrontOp(reg, "POST", base, "createBloemPlatformTenantMember", "Add a member account to a reseller organization.", http.StatusCreated, createErrors)
	create.Description += "Creates the account in the organization and takes one seat. A replay of the same Idempotency-Key and body returns the member with 200."
	create.Responses["200"] = &huma.Response{Description: "The member, for a replay of the same key and body."}
	registerBloemChiDocument[BloemTenantMemberCreateInput, BloemTenantMemberOutput](reg, create)

	get := bloemStorefrontOp(reg, "GET", base+"/{user_id}", "getBloemPlatformTenantMember", "Read one member.", http.StatusOK, memberErrors)
	registerBloemChiDocument[BloemTenantMemberInput, BloemTenantMemberOutput](reg, get)

	update := bloemStorefrontOp(reg, "PUT", base+"/{user_id}", "updateBloemPlatformTenantMember", "Change a member's username or email.", http.StatusOK, memberErrors)
	registerBloemChiDocument[BloemTenantMemberUpdateInput, BloemTenantMemberOutput](reg, update)

	remove := bloemStorefrontOp(reg, "DELETE", base+"/{user_id}", "deleteBloemPlatformTenantMember", "Remove a member.", http.StatusNoContent, memberErrors)
	remove.Description += "Removes the membership and frees its seat; an account with no other organization is deleted."
	registerBloemChiDocument[BloemTenantMemberInput, struct{}](reg, remove)

	suspend := bloemStorefrontOp(reg, "POST", base+"/{user_id}/suspend", "suspendBloemPlatformTenantMember", "Suspend a member's account.", http.StatusOK, memberErrors)
	registerBloemChiDocument[BloemTenantMemberInput, BloemTenantMemberOutput](reg, suspend)
	resume := bloemStorefrontOp(reg, "POST", base+"/{user_id}/resume", "resumeBloemPlatformTenantMember", "Resume a suspended member's account.", http.StatusOK, memberErrors)
	registerBloemChiDocument[BloemTenantMemberInput, BloemTenantMemberOutput](reg, resume)
	reset := bloemStorefrontOp(reg, "POST", base+"/{user_id}/reset-password", "resetBloemPlatformTenantMemberPassword", "Set a member's password.", http.StatusOK, memberErrors)
	registerBloemChiDocument[BloemTenantMemberPasswordInput, BloemTenantMemberOutput](reg, reset)

	scoped := "Checks the membership, then serves the account's resource as the account routes do, limited to this organization's profiles. "
	profileErrors := map[int]string{
		400: "bad_request: an invalid ID or body, a missing profile name, or an invalid max_playback_quality.",
		404: memberErrors[404] + " Also a profile, device or session the account does not have.",
		409: "name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted.",
		422: "profile_limit_reached: the account's plan allows no more profiles.",
	}
	op := bloemStorefrontOp(reg, "GET", base+"/{user_id}/profiles", "listBloemPlatformTenantMemberProfiles", "List a member's profiles.", http.StatusOK, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberInput, BloemAccountProfileListOutput](reg, op)
	op = bloemStorefrontOp(reg, "POST", base+"/{user_id}/profiles", "createBloemPlatformTenantMemberProfile", "Create a profile for a member.", http.StatusCreated, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberProfileCreateInput, BloemAccountProfileOutput](reg, op)
	op = bloemStorefrontOp(reg, "PUT", base+"/{user_id}/profiles/{profile_id}", "updateBloemPlatformTenantMemberProfile", "Change a member's profile.", http.StatusOK, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberProfileUpdateInput, BloemAccountProfileOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/{user_id}/profiles/{profile_id}", "deleteBloemPlatformTenantMemberProfile", "Delete a member's profile.", http.StatusNoContent, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberProfileInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "GET", base+"/{user_id}/devices", "listBloemPlatformTenantMemberDevices", "List a member's devices.", http.StatusOK, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberInput, BloemAccountDeviceListOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/{user_id}/devices/{device_id}", "deleteBloemPlatformTenantMemberDevice", "Remove a member's device.", http.StatusNoContent, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberDeviceInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "GET", base+"/{user_id}/auth-sessions", "listBloemPlatformTenantMemberAuthSessions", "List a member's sign-in sessions.", http.StatusOK, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberInput, BloemAccountSessionListOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/{user_id}/auth-sessions/{session_id}", "revokeBloemPlatformTenantMemberAuthSession", "End one of a member's sign-in sessions.", http.StatusNoContent, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberSessionInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/{user_id}/auth-sessions", "revokeAllBloemPlatformTenantMemberAuthSessions", "End every sign-in session of a member.", http.StatusNoContent, profileErrors)
	op.Description += scoped
	registerBloemChiDocument[BloemTenantMemberInput, struct{}](reg, op)
}

type BloemPlatformAccountCreateInput struct {
	Body handlers.BloemPlatformAccountCreateRequest
}

// BloemPlatformAccountUpdateBody documents handlers.BloemPlatformAccountUpdateRequest,
// whose nullable policy fields decode a tri-state the schema reflector cannot
// read; TestBloemPlatformAccountUpdateBodyMatchesTheHandler keeps the two alike.
type BloemPlatformAccountUpdateBody struct {
	Username                    *string   `json:"username,omitempty"`
	Email                       *string   `json:"email,omitempty"`
	Password                    *string   `json:"password,omitempty"`
	Role                        *string   `json:"role,omitempty" doc:"user or admin."`
	Enabled                     *bool     `json:"enabled,omitempty"`
	Permissions                 *[]string `json:"permissions,omitempty"`
	LibraryIDs                  []int     `json:"library_ids,omitempty" nullable:"true" doc:"null: every library."`
	AccessGroupID               *int64    `json:"access_group_id,omitempty" nullable:"true" doc:"null: no access group."`
	MaxProfiles                 *int      `json:"max_profiles,omitempty" nullable:"true" doc:"null clears the account's own value."`
	MaxPlaybackQuality          *string   `json:"max_playback_quality,omitempty" nullable:"true"`
	MaxStreams                  *int      `json:"max_streams,omitempty" nullable:"true"`
	MaxTranscodes               *int      `json:"max_transcodes,omitempty" nullable:"true"`
	TranscodeAllowed            *bool     `json:"transcode_allowed,omitempty" nullable:"true"`
	AudioTranscodeAllowed       *bool     `json:"audio_transcode_allowed,omitempty" nullable:"true"`
	DownloadAllowed             *bool     `json:"download_allowed,omitempty" nullable:"true"`
	DownloadTranscodeAllowed    *bool     `json:"download_transcode_allowed,omitempty" nullable:"true"`
	RequestsAllowed             *bool     `json:"requests_allowed,omitempty" nullable:"true"`
	EntitlementTemplateKey      string    `json:"entitlement_template_key,omitempty" doc:"With entitlement_template_revision, the plan to move the account to."`
	EntitlementTemplateRevision int64     `json:"entitlement_template_revision,omitempty" doc:"The exact plan revision."`
}

type BloemPlatformAccountUpdateInput struct {
	AccountID string `path:"account_id" doc:"The account ID."`
	Body      BloemPlatformAccountUpdateBody
}
type BloemPlatformAccountOutput struct{ Body handlers.AdminUserView }

// registerBloemPlatformAccountDocument documents creating and updating an
// account on a plan, on top of the account service behind Silo's
// /api/v2/admin/users, which takes no plan.
func registerBloemPlatformAccountDocument(reg *Registry) {
	errors := map[int]string{
		400: "bad_request: the body is not valid JSON or exceeds 64 KiB, a field is invalid (the message names it), or entitlement_template_key and a positive entitlement_template_revision are not given together.",
		409: "duplicate: the username or email is already in use. Silo's account rules can also answer 409, such as break_glass_required.",
		422: "entitlement_template_unavailable: that plan revision cannot be applied.",
		501: "capability_unsupported: this server's account store cannot manage accounts transactionally.",
		503: "entitlements_unavailable: plans are not configured. service_unavailable: the server is too busy to hash the password; retry after Retry-After. tenant_unavailable: the credential could not be checked.",
	}
	const plan = "With entitlement_template_key and entitlement_template_revision, the account is put on exactly that plan revision in the same request, and applied_entitlement_revision in the response reports it. Without organization_id the account is a direct account in the default organization. "
	create := bloemStorefrontOp(reg, "POST", "/accounts", "createBloemPlatformAccount", "Create an account, optionally on a plan.", http.StatusCreated, errors)
	create.Description += plan + "It runs Silo's own account creation (as POST /api/v2/admin/users does), then puts the account on the plan; if the plan cannot be applied, the account is deleted again. Unknown fields are ignored."
	registerBloemChiDocument[BloemPlatformAccountCreateInput, BloemPlatformAccountOutput](reg, create)
	update := bloemStorefrontOp(reg, "PUT", "/accounts/{account_id}", "updateBloemPlatformAccount", "Update an account, optionally moving it to a plan.", http.StatusOK, map[int]string{
		400: errors[400],
		404: "not_found: no such account.",
		409: errors[409],
		422: errors[422],
		501: errors[501],
		503: errors[503],
	})
	update.Description += plan + "It runs Silo's own account update (as PUT /api/v2/admin/users does, without its If-Match), then applies the plan. Fields left out are not changed; a nullable policy field set to null clears it."
	registerBloemChiDocument[BloemPlatformAccountUpdateInput, BloemPlatformAccountOutput](reg, update)
}

func registerBloemPlatformAccountResourceDocument(reg *Registry) {
	errors := map[int]string{
		400: "bad_request: an invalid account ID or body, a missing profile name, or an invalid max_playback_quality.",
		404: "not_found: no such account, or a profile, device or session the account does not have.",
		409: "name_conflict: another profile has this name. primary_profile_protected: the account's main profile cannot be deleted.",
		422: "profile_limit_reached: the account's plan allows no more profiles.",
	}
	const scope = "Platform scope: the whole account, whatever its organization. "
	const base = "/accounts/{account_id}"
	op := bloemStorefrontOp(reg, "GET", base+"/profiles", "listBloemPlatformAccountProfiles", "List an account's profiles.", http.StatusOK, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountInput, BloemAccountProfileListOutput](reg, op)
	op = bloemStorefrontOp(reg, "POST", base+"/profiles", "createBloemPlatformAccountProfile", "Create a profile for an account.", http.StatusCreated, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountProfileCreateInput, BloemAccountProfileOutput](reg, op)
	op = bloemStorefrontOp(reg, "PUT", base+"/profiles/{profile_id}", "updateBloemPlatformAccountProfile", "Change an account's profile.", http.StatusOK, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountProfileUpdateInput, BloemAccountProfileOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/profiles/{profile_id}", "deleteBloemPlatformAccountProfile", "Delete an account's profile.", http.StatusNoContent, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountProfileInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "GET", base+"/devices", "listBloemPlatformAccountDevices", "List an account's devices.", http.StatusOK, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountInput, BloemAccountDeviceListOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/devices/{device_id}", "deleteBloemPlatformAccountDevice", "Remove an account's device.", http.StatusNoContent, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountDeviceInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "GET", base+"/auth-sessions", "listBloemPlatformAccountAuthSessions", "List an account's sign-in sessions.", http.StatusOK, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountInput, BloemAccountSessionListOutput](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/auth-sessions/{session_id}", "revokeBloemPlatformAccountAuthSession", "End one of an account's sign-in sessions.", http.StatusNoContent, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountSessionInput, struct{}](reg, op)
	op = bloemStorefrontOp(reg, "DELETE", base+"/auth-sessions", "revokeAllBloemPlatformAccountAuthSessions", "End every sign-in session of an account.", http.StatusNoContent, errors)
	op.Description += scope
	registerBloemChiDocument[BloemPlatformAccountInput, struct{}](reg, op)
}

func registerBloemAnnouncementDocument(reg *Registry) {
	unavailable := map[int]string{503: "unavailable: announcements are not configured. tenant_unavailable or service_unavailable: the credential could not be checked."}
	list := bloemStorefrontOp(reg, "GET", "/announcements", "listBloemPlatformAnnouncements", "List published announcements, including withdrawn ones.", http.StatusOK, unavailable)
	registerBloemChiDocument[struct{}, BloemAnnouncementListOutput](reg, list)

	create := bloemStorefrontOp(reg, "POST", "/announcements", "createBloemPlatformAnnouncement", "Publish an announcement to its audience's inboxes.", http.StatusCreated, map[int]string{
		400: "bad_request: the body is not valid JSON, exceeds 64 KiB, or a field fails validation (title up to 120 characters, body up to 2,000, button label up to 40, links up to 2,048; a critical message cannot be dismissible).",
		422: "no_recipients: the targeting matches no one.",
		503: unavailable[503],
	})
	create.Description += "Publishes at once: the audience is resolved and every recipient's inbox receives it. type is system.announcement (informational) or system.alert (operational). targeting.audience is all, role, organization, library or explicit. There is no draft, schedule or edit."
	registerBloemChiDocument[BloemAnnouncementCreateInput, BloemAnnouncementOutput](reg, create)

	withdraw := bloemStorefrontOp(reg, "DELETE", "/announcements/{id}", "withdrawBloemPlatformAnnouncement", "Withdraw an announcement.", http.StatusNoContent, map[int]string{
		404: "not_found: no such announcement.",
		503: unavailable[503],
	})
	withdraw.Description += "The record stays, with withdrawn_at set. Idempotent."
	registerBloemChiDocument[BloemAnnouncementInput, struct{}](reg, withdraw)
}

type BloemAmbienceAssetInput struct {
	Ref         string `path:"ref" doc:"The content-addressed reference from an upload's url."`
	IfNoneMatch string `header:"If-None-Match" required:"false" doc:"The ETag of a copy already held; a match answers 304."`
}

// registerBloemAmbienceAssetDocument documents the public artwork route that
// uploads point at (ambience.AssetURLBase).
func registerBloemAmbienceAssetDocument(reg *Registry) {
	op := bloemOp("GET", "/ambience/assets/{ref}", "getBloemAmbienceAsset", "engagement", "Serve uploaded seasonal and campaign artwork.")
	op.Description = "Public: no credential, because artwork shows on the sign-in page. Uploads under /api/bloem/v1/admin/platform/ambience return paths here. The bytes are a PNG, WebP, JPEG or GIF image with its sniffed Content-Type; refs are content-addressed, so responses are immutable (Cache-Control public, max-age one year) and carry the ref as ETag. Paths under /api/v1/ambience/assets/ serve the same files."
	op.DefaultStatus = http.StatusOK
	op.Responses = map[string]*huma.Response{
		"200": {Description: "The image bytes.", Content: map[string]*huma.MediaType{"image/*": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}},
		"304": {Description: "Not modified: If-None-Match matched the ETag."},
	}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		404: "not_found: no artwork has this ref.",
		500: "internal_error: the artwork could not be read.",
		503: "unavailable: seasonal packs or their asset storage are not configured.",
	})
	registerBloemChiDocument[BloemAmbienceAssetInput, struct{}](reg, op)
}
