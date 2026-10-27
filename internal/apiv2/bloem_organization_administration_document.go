package apiv2

import (
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

const (
	bloemOrganizationAuthorityDescription = "Requires the selected organization administrative context. Current account, login session, membership and policy/security revisions are revalidated; resource IDs cannot expand organization scope."
	bloemOrganizationUnavailable          = bloemOrganizationContextUnavailable
	bloemOrganizationRevisionInvalid      = "validation_failed: expected_revision is not positive."
	bloemOrganizationGroupInvalid         = "invalid_request: invalid JSON, unknown fields, trailing JSON or a body exceeding 32 KiB. bad_request: an invalid name, quality, library ID, permission or stream/transcode cap."
	bloemOrganizationGroupNotFound        = "not_found: the group ID is not a positive integer or the group is absent from the organization."
	bloemOrganizationPersonNotFound       = "not_found: account_id is not a positive integer or the account is not a member of the organization."
	bloemOrganizationSelectionNotFound    = "not_found: the selection token is invalid or belongs to another organization."
	bloemPersonRevisionConflict           = "authorization_state_changed: expected_revision is not the person's security_revision; current_revision carries the current value."
)

// Private handler wire records are mirrored here; exported records are reused.

type BloemOrganizationGroup struct {
	ID                       int64     `json:"id"`
	Name                     string    `json:"name"`
	Description              string    `json:"description"`
	LibraryIDs               []int     `json:"library_ids" nullable:"true"`
	MaxPlaybackQuality       string    `json:"max_playback_quality"`
	DownloadAllowed          bool      `json:"download_allowed"`
	DownloadTranscodeAllowed bool      `json:"download_transcode_allowed"`
	TranscodeAllowed         bool      `json:"transcode_allowed"`
	AudioTranscodeAllowed    bool      `json:"audio_transcode_allowed"`
	MaxStreams               int       `json:"max_streams"`
	MaxTranscodes            int       `json:"max_transcodes"`
	AllowedPermissions       []string  `json:"allowed_permissions" nullable:"true"`
	RequestsAllowed          bool      `json:"requests_allowed"`
	IsDefault                bool      `json:"is_default"`
	MemberCount              int       `json:"member_count"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type BloemOrganizationGroupCreate struct {
	Name                     string   `json:"name"`
	Description              string   `json:"description,omitempty"`
	LibraryIDs               []int    `json:"library_ids,omitempty" nullable:"true" doc:"Omit for the default policy; null adds no library restriction. Empty arrays are collapsed to null in group responses."`
	MaxPlaybackQuality       string   `json:"max_playback_quality,omitempty"`
	DownloadAllowed          *bool    `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed *bool    `json:"download_transcode_allowed,omitempty"`
	TranscodeAllowed         *bool    `json:"transcode_allowed,omitempty"`
	AudioTranscodeAllowed    *bool    `json:"audio_transcode_allowed,omitempty"`
	MaxStreams               *int     `json:"max_streams,omitempty"`
	MaxTranscodes            *int     `json:"max_transcodes,omitempty"`
	AllowedPermissions       []string `json:"allowed_permissions,omitempty" nullable:"true" doc:"Null or omission uses the default permission policy. Group responses collapse empty permission arrays to null."`
	RequestsAllowed          *bool    `json:"requests_allowed,omitempty"`
	IsDefault                bool     `json:"is_default,omitempty"`
	ExpectedRevision         int64    `json:"expected_revision" minimum:"1"`
}

type BloemOrganizationGroupUpdate struct {
	Name                     *string  `json:"name,omitempty"`
	Description              *string  `json:"description,omitempty"`
	LibraryIDs               []int    `json:"library_ids,omitempty" nullable:"true"`
	MaxPlaybackQuality       *string  `json:"max_playback_quality,omitempty"`
	DownloadAllowed          *bool    `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed *bool    `json:"download_transcode_allowed,omitempty"`
	TranscodeAllowed         *bool    `json:"transcode_allowed,omitempty"`
	AudioTranscodeAllowed    *bool    `json:"audio_transcode_allowed,omitempty"`
	MaxStreams               *int     `json:"max_streams,omitempty"`
	MaxTranscodes            *int     `json:"max_transcodes,omitempty"`
	AllowedPermissions       []string `json:"allowed_permissions,omitempty" nullable:"true"`
	RequestsAllowed          *bool    `json:"requests_allowed,omitempty"`
	IsDefault                *bool    `json:"is_default,omitempty"`
	ExpectedRevision         int64    `json:"expected_revision" minimum:"1"`
}

type BloemRevisionRequest struct {
	ExpectedRevision int64 `json:"expected_revision" minimum:"1"`
}
type BloemOrganizationEntitlementUpdate struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1"`
	Status           string `json:"status" enum:"active,suspended"`
}
type BloemOrganizationInvitationCreate struct {
	Email            string `json:"email"`
	ExpectedRevision int64  `json:"expected_revision" minimum:"1"`
	AccessGroupID    *int64 `json:"access_group_id,omitempty"`
	LibraryIDs       []int  `json:"library_ids,omitempty" nullable:"true"`
	CreateProfile    *bool  `json:"create_profile,omitempty" doc:"Defaults to true."`
	ShowTour         *bool  `json:"show_tour,omitempty" doc:"Defaults to true."`
	Note             string `json:"note,omitempty"`
}

type BloemEntitlementCohort struct {
	CohortID               uuid.UUID              `json:"cohort_id"`
	OrganizationID         uuid.UUID              `json:"organization_id"`
	Name                   string                 `json:"name"`
	Revision               int64                  `json:"revision"`
	AccessGroupID          int64                  `json:"access_group_id"`
	SourceTemplateKey      string                 `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                  `json:"source_template_revision,omitempty"`
	ParentCohortID         uuid.UUID              `json:"parent_cohort_id,omitempty"`
	DerivationKind         string                 `json:"derivation_kind"`
	Policy                 adminpeople.PolicyView `json:"policy"`
	PolicyDigest           string                 `json:"policy_digest"`
	Archived               bool                   `json:"archived"`
	MemberCount            int64                  `json:"member_count"`
	CreatedByAccountID     int                    `json:"created_by_account_id,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
}

type BloemPeopleSelectionRequest struct {
	Query       string                     `json:"query,omitempty"`
	Status      []tenancy.MembershipStatus `json:"status,omitempty" nullable:"true"`
	GroupIDs    []int                      `json:"group_ids,omitempty" nullable:"true"`
	ActiveSince *time.Time                 `json:"active_since,omitempty"`
	Sort        string                     `json:"sort,omitempty"`
}

// Use the handler type as the field source while documenting its false default.
type BloemPolicyCommand adminpeople.PolicyCommand

func (*BloemPolicyCommand) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Required = []string{"kind"}
	return schema
}

type BloemPolicyBulkAction struct {
	SelectionToken    string             `json:"selection_token"`
	ConfirmationToken string             `json:"confirmation_token"`
	IdempotencyKey    string             `json:"idempotency_key,omitempty" doc:"Required unless supplied by the Idempotency-Key header; when both are present they must match."`
	Command           BloemPolicyCommand `json:"command"`
}

type BloemPeoplePreviewRequest struct {
	SelectionToken string             `json:"selection_token"`
	Command        BloemPolicyCommand `json:"command"`
}
type BloemPeopleMembershipUpdate struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"Person security_revision, not organization policy_revision."`
	Status           string `json:"status" enum:"active,suspended"`
}
type BloemPeopleProfileUpdate struct {
	ExpectedRevision int64 `json:"expected_revision" minimum:"1" doc:"Person security_revision, not organization policy_revision."`
	GroupID          int   `json:"group_id" minimum:"1"`
}

type BloemOrganizationSummaryResponse struct {
	Organization tenancy.OrganizationSummary `json:"organization"`
}

type BloemOrganizationGroupsResponse struct {
	Groups []BloemOrganizationGroup `json:"groups"`
}

type BloemOrganizationGroupResponse struct {
	Group BloemOrganizationGroup `json:"group"`
}

type BloemOrganizationGroupMutationResponse struct {
	Group          BloemOrganizationGroup `json:"group"`
	PolicyRevision int64                  `json:"policy_revision"`
}

type BloemOrganizationLibrariesResponse struct {
	Libraries []resourcetenancy.LibraryProjection `json:"libraries"`
}

type BloemOrganizationEntitlementResponse struct {
	Entitlement resourcetenancy.LibraryEntitlement `json:"entitlement"`
}

type BloemOrganizationInvitationsResponse struct {
	Invitations []BloemOrganizationInvitation `json:"invitations"`
}

type BloemEntitlementCohortsResponse struct {
	Cohorts []BloemEntitlementCohort `json:"cohorts"`
}

type BloemEntitlementCohortResponse struct {
	Cohort BloemEntitlementCohort `json:"cohort"`
}

type BloemPersonResponse struct {
	Person adminpeople.PersonSummary `json:"person"`
}

type BloemPeopleSelectionResponse struct {
	Selection adminpeople.Selection `json:"selection"`
}

type BloemPeoplePreviewResponse struct {
	Preview adminpeople.PolicyPreview `json:"preview"`
}

type BloemPeopleJobResponse struct {
	Job adminpeople.BulkResult `json:"job"`
}

type BloemPolicyDecisionsResponse struct {
	Decisions  []handlers.PolicyDecisionExplanation `json:"decisions"`
	NextCursor string                               `json:"next_cursor,omitempty"`
}

type BloemPolicyDecisionResponse struct {
	Decision handlers.PolicyDecisionExplanation `json:"decision"`
}

func registerBloemOrganizationAdministrationDocument(reg *Registry) {
	// Native policy views preserve nil sets as null; they do not use v2's
	// convention that response arrays are always initialized.
	for _, typ := range []reflect.Type{reflect.TypeFor[adminpeople.PolicyView](), reflect.TypeFor[entitlements.EffectivePolicySnapshot]()} {
		schema := reg.api.OpenAPI().Components.Schemas.Schema(typ, false, "")
		for _, field := range []string{"library_ids", "allowed_permissions"} {
			schema.Properties[field].Nullable = true
		}
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/overview", "getBloemOrganizationOverview", "Read the selected organization overview.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found: the selected organization no longer exists."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationSummaryResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/groups/", "listBloemOrganizationGroups", "List access groups in the selected organization.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationGroupsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/groups/{id}", "getBloemOrganizationGroup", "Read one organization access group.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: bloemOrganizationGroupNotFound})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationGroupResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/groups/", "createBloemOrganizationGroup", "Create an organization access group.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: bloemOrganizationGroupInvalid})
		bloemDocumentDescribe(&op, map[int]string{
			409: bloemOrganizationPolicyConflict + " conflict: the group name already exists.",
			422: bloemOrganizationRevisionInvalid,
			503: bloemOrganizationUnavailable + " Also returned when is_default would replace a default group managed by entitlement policy.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemOrganizationGroupCreate], bloemJSONOutput[BloemOrganizationGroupMutationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PUT", "/admin/organization/groups/{id}", "updateBloemOrganizationGroup", "Update an organization access group.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: bloemOrganizationGroupInvalid})
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemOrganizationGroupNotFound,
			409: bloemOrganizationPolicyConflict + " conflict: the group name already exists, or is_default=false was sent for the current default group.",
			422: bloemOrganizationRevisionInvalid,
			503: bloemOrganizationUnavailable + " Also returned when the group, or a default group it would replace, is managed by entitlement policy.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemOrganizationGroupUpdate], bloemJSONOutput[BloemOrganizationGroupMutationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "DELETE", "/admin/organization/groups/{id}", "deleteBloemOrganizationGroup", "Delete an organization access group and report its impact.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemOrganizationGroupNotFound,
			409: bloemOrganizationPolicyConflict + " conflict: the group is the default group, or no replacement default group exists.",
			422: bloemOrganizationRevisionInvalid,
			503: bloemOrganizationUnavailable + " Also returned when the group is managed by entitlement policy.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[access.GroupDeletionImpact]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/libraries", "listBloemOrganizationLibraries", "List the organization library entitlement projection.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationLibrariesResponse]](reg, op)
	}

	for _, entitlement := range []struct {
		method, id, summary, invalid string
		status                       int
	}{
		{"PUT", "updateBloemOrganizationLibraryEntitlement", "Set an organization library entitlement status.", "validation_failed (fields.request): expected_revision is not positive or status is not active or suspended.", 200},
		{"DELETE", "deleteBloemOrganizationLibraryEntitlement", "Remove an organization library entitlement.", bloemOrganizationRevisionInvalid, 204},
	} {
		op := bloemAdminDocumentOp(reg, entitlement.method, "/admin/organization/entitlements/{folder_id}", entitlement.id, entitlement.summary, "bloemOrganizationContext", entitlement.status)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " expected_revision is the entitlement security_revision returned by the libraries projection. On conflict, current_revision contains the organization policy revision; reload the libraries projection for the entitlement revision."
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: folder_id is not a positive integer, or the library entitlement is absent or hidden from the organization.",
			409: "authorization_state_changed: expected_revision is not the entitlement's security_revision; current_revision carries the organization policy revision.",
			422: entitlement.invalid,
		})
		if entitlement.method == "PUT" {
			registerBloemChiDocument[bloemJSONInput[BloemOrganizationEntitlementUpdate], bloemJSONOutput[BloemOrganizationEntitlementResponse]](reg, op)
		} else {
			registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], struct{}](reg, op)
		}
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/invitations", "listBloemOrganizationInvitations", "List invitation metadata without claim tokens.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription + " Invitations are ordered newest first."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationInvitationsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/invitations", "createBloemOrganizationInvitation", "Create a user invitation and disclose its claim token once.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " Creates role=user only; default expiry is seven days. A pending invitation for the same address in this organization is revoked. Returns claim_token once; this operation does not send email or return a claim URL. Do not automatically retry creation."
		bloemDocumentDescribe(&op, map[int]string{
			409: bloemOrganizationPolicyConflict,
			422: "validation_failed: expected_revision is not positive, or email is not a bare email address.",
			503: bloemOrganizationUnavailable + " Also returned when the email already belongs to an account.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemOrganizationInvitationCreate], bloemJSONOutput[BloemOrganizationInvitationResendResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/entitlement-cohorts", "listBloemOrganizationEntitlementCohorts", "List safe entitlement cohort projections.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentQuery(&op, "include_archived", "true or false; default false. Other query parameters return 422.", "boolean")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: include_archived is not true or false, or another query parameter is present."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/entitlement-cohorts/{cohort_id}", "getBloemOrganizationEntitlementCohort", "Read a safe organization cohort projection.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the cohort ID is malformed or the cohort is absent from the organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/people/", "listBloemOrganizationPeople", "Page organization people and their profiles.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentQuery(&op, "query", "Case-insensitive substring of the account email, username or a profile name.", "string")
		bloemDocumentQuery(&op, "status", "Membership status filter: invited, active or suspended. Repeat the parameter or separate values with commas.", "string")
		bloemDocumentQuery(&op, "group_id", "Access group filter of positive IDs. Repeat the parameter or separate values with commas.", "string")
		bloemDocumentQuery(&op, "active_since", "RFC3339 timestamp; matches people whose membership or a profile changed at or after it.", "string")
		bloemDocumentQuery(&op, "sort", "name (default), email or recent_activity.", "string")
		op.Parameters[len(op.Parameters)-1].Schema.Enum = []any{"name", "email", "recent_activity"}
		bloemDocumentQuery(&op, "cursor", "Opaque next_cursor from the previous page.", "string")
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200; defaults to 50.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: limit, group_id or active_since is malformed, a status or sort value is unknown, or cursor is invalid."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[adminpeople.Page]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/people/{account_id}/", "getBloemOrganizationPerson", "Read an organization person and profile summary.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: bloemOrganizationPersonNotFound})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPersonResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/people/selections", "createBloemOrganizationPeopleSelection", "Create an immutable expiring selection from people filters.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " Selections expire after 15 minutes."
		bloemDocumentOmit(&op, 409)
		bloemDocumentDescribe(&op, map[int]string{422: "validation_failed (fields.filters): a status or sort value is unknown, or a group ID is not positive."})
		registerBloemChiDocument[bloemJSONInput[BloemPeopleSelectionRequest], bloemJSONOutput[BloemPeopleSelectionResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/people/policy-previews", "previewBloemOrganizationPeoplePolicy", "Preview a policy command and obtain a bound confirmation token.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: bloemOrganizationSelectionNotFound + " The command's template or cohort may also not exist."})
		bloemDocumentDescribe(&op, map[int]string{
			409: "selection_expired: create a new selection; authorization_state_changed: the actor's authority changed since the selection.",
			422: "validation_failed (fields.request): the policy command is invalid.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPeoplePreviewRequest], bloemJSONOutput[BloemPeoplePreviewResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/people/bulk-jobs", "createBloemOrganizationPeopleBulkJob", "Enqueue a people bulk mutation.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " kind is assign_group (with a positive group_id), suspend_memberships or reactivate_memberships (without group_id). Repeating the same kind and group for a selection returns the existing job."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: bloemOrganizationSelectionNotFound + " group_id may also not be a group in the organization."})
		bloemDocumentDescribe(&op, map[int]string{
			409: "selection_expired: create a new selection; authorization_state_changed: the actor's authority changed since the selection.",
			422: "validation_failed (fields.request): kind or group_id is invalid for the action, or selection_token is missing.",
		})
		registerBloemChiDocument[bloemJSONInput[adminpeople.BulkAction], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/people/policy-jobs", "createBloemOrganizationPeoplePolicyJob", "Enqueue a confirmed people policy command.", "bloemOrganizationContext", 201)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: bloemOrganizationSelectionNotFound + " The command's template or cohort may also not exist."})
		bloemDocumentDescribe(&op, map[int]string{409: bloemPolicyJobConflict, 422: bloemPolicyJobValidation})
		bloemDocumentCommandKey(&op, bloemPolicyJobKeyRule)
		registerBloemChiDocument[bloemJSONInput[BloemPolicyBulkAction], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/people/bulk-jobs/{job_id}", "getBloemOrganizationPeopleBulkJob", "Read progress and per-record results of a people job.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the job is absent from the organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/people/policy-jobs/{job_id}", "getBloemOrganizationPeoplePolicyJob", "Read progress and per-record results of a people job.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the job is absent from the organization or is not a policy job."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/people/policy-jobs/{job_id}/cancel", "cancelBloemOrganizationPeoplePolicyJob", "Cancel an eligible people policy job.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription + " No request body is read. A completed, failed or cancelled job is returned unchanged."
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the job is absent from the organization."})
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{409: "job_not_cancellable: the job is not a policy job; authorization_state_changed: the actor's authority changed since the job was queued."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/organization/people/{account_id}/memberships/current", "updateBloemOrganizationPersonMembership", "Change an organization person membership status.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemOrganizationPersonNotFound,
			409: bloemPersonRevisionConflict,
			422: "validation_failed (fields.request): expected_revision is not positive, status is not active or suspended, or the organization owner would be suspended.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPeopleMembershipUpdate], bloemJSONOutput[BloemPersonResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/organization/people/{account_id}/profiles/{profile_id}", "updateBloemOrganizationPersonProfile", "Assign an organization person profile to an access group.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemOrganizationPersonNotFound + " The profile or group_id may also be absent from the organization.",
			409: bloemPersonRevisionConflict,
			422: "validation_failed (fields.request): expected_revision or group_id is not positive.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPeopleProfileUpdate], bloemJSONOutput[BloemPersonResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/policy-decisions", "listBloemOrganizationPolicyDecisions", "Page sanitized organization policy decision explanations.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription + " Decisions are ordered newest first; next_cursor is omitted on the last page."
		bloemDocumentQuery(&op, "decision_name", "Exact decision name filter.", "string")
		bloemDocumentQuery(&op, "cursor", "Opaque next_cursor from the previous page. A malformed cursor returns 503 tenant_unavailable, not 422.", "string")
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200; defaults to 100.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: limit is outside 1 to 200."})
		bloemDocumentDescribe(&op, map[int]string{503: bloemOrganizationUnavailable + " Also returned for a malformed cursor."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPolicyDecisionsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/organization/policy-decisions/{id}", "getBloemOrganizationPolicyDecision", "Read a sanitized organization policy decision explanation.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the decision ID is not a positive integer or the decision is absent from the organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPolicyDecisionResponse]](reg, op)
	}
}
