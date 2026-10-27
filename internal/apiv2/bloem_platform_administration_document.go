package apiv2

import (
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

const (
	bloemPlatformAuthorityDescription     = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
	bloemEntitlementApplyDescription      = " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
	bloemEntitlementApplyReplay           = " The confirmation token is checked before the idempotency receipt, so a repeat after the token expires returns 422."
	bloemPlatformStrictJSONError          = "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."
	bloemPlatformBulkDescription          = " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
	bloemPlatformBulkOrganizationNotFound = "not_found: the organization is malformed, absent or not active; direct paths resolve the deployment default organization."
	bloemEntitlementOrganizationNotFound  = "not_found: the organization ID is malformed, or the organization does not exist or is not externally provisioned."
	bloemEntitlementApplyConflict         = "confirmation_stale: the preview no longer matches current state, dry-run again; revision_conflict: this idempotency_key was already used for a different template selection."
	bloemEntitlementApplyValidation       = "validation_failed with fields: the template selection, confirmation_token or idempotency_key is missing or invalid, or the confirmation token is expired or bound to another target or actor. template_unavailable without fields: the template revision is disabled or archived."
)

// Mirror private entitlement/session wire types, not persistence models.

type BloemAdminSessionRequest struct {
	Scope          string `json:"scope" enum:"platform,organization"`
	OrganizationID string `json:"organization_id,omitempty"`
}

type BloemAdminSessionResponse struct {
	AccessToken string                   `json:"access_token"`
	ExpiresAt   time.Time                `json:"expires_at"`
	Context     BloemAdminContextSummary `json:"context"`
}

type BloemAdminContextSummary struct {
	Key              string `json:"key"`
	Scope            string `json:"scope"`
	OrganizationID   string `json:"organization_id,omitempty"`
	MembershipID     string `json:"membership_id,omitempty"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Authority        string `json:"authority"`
	PolicyRevision   int64  `json:"policy_revision,omitempty"`
	SecurityRevision int64  `json:"security_revision,omitempty"`
}

type BloemEntitlementPolicy struct {
	AllLibraries             bool     `json:"all_libraries"`
	LibraryIDs               []int    `json:"library_ids" nullable:"true"`
	PlaybackAllowed          bool     `json:"playback_allowed"`
	MaxStreams               int      `json:"max_streams"`
	MaxProfiles              int      `json:"max_profiles"`
	TranscodeAllowed         bool     `json:"transcode_allowed"`
	MaxTranscodes            int      `json:"max_transcodes"`
	DownloadAllowed          bool     `json:"download_allowed"`
	DownloadTranscodeAllowed bool     `json:"download_transcode_allowed"`
	MaxPlaybackQuality       string   `json:"max_playback_quality"`
	AllowedPermissions       []string `json:"allowed_permissions" nullable:"true"`
	RequestsAllowed          bool     `json:"requests_allowed"`
}

// Missing input policy fields use the Go decoder's zero values. Response
// policies still carry every field, so retain a separate required-field list.
type BloemEntitlementPolicyInput BloemEntitlementPolicy

func (*BloemEntitlementPolicyInput) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.Required = nil
	return schema
}

type BloemEntitlementTemplate struct {
	Key       string                 `json:"key"`
	Name      string                 `json:"name"`
	Revision  int64                  `json:"revision"`
	Enabled   bool                   `json:"enabled"`
	Archived  bool                   `json:"archived"`
	Status    string                 `json:"status"`
	Policy    BloemEntitlementPolicy `json:"policy"`
	CreatedAt time.Time              `json:"created_at"`
}

type BloemEntitlementApplyRequest struct {
	TemplateKey       string `json:"template_key"`
	TemplateRevision  int64  `json:"template_revision"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
	DryRunToken       string `json:"dry_run_token,omitempty"`
	IdempotencyKey    string `json:"idempotency_key,omitempty"`
}

type BloemEntitlementApplyResponse struct {
	OrganizationID           uuid.UUID              `json:"organization_id"`
	AccountID                int                    `json:"account_id,omitempty"`
	TemplateKey              string                 `json:"template_key"`
	TemplateRevision         int64                  `json:"template_revision"`
	GroupID                  int64                  `json:"group_id,omitempty"`
	DryRun                   bool                   `json:"dry_run"`
	Changed                  bool                   `json:"changed"`
	ProfilesMoved            int                    `json:"profiles_moved,omitempty"`
	PreviousTemplateKey      string                 `json:"previous_template_key,omitempty"`
	PreviousTemplateRevision int64                  `json:"previous_template_revision,omitempty"`
	Policy                   BloemEntitlementPolicy `json:"policy"`
}

type BloemPlatformOrganizationCreate struct {
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	OwnerAccountID int    `json:"owner_account_id" minimum:"1"`
}
type BloemPlatformOrganizationUpdate struct {
	ExpectedRevision int64   `json:"expected_revision" minimum:"1"`
	Name             *string `json:"name,omitempty"`
	Slug             *string `json:"slug,omitempty"`
}
type BloemPlatformOwnershipTransfer struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1"`
	OwnerAccountID   int    `json:"owner_account_id" minimum:"1"`
	Confirmed        bool   `json:"confirmed" enum:"true"`
	Password         string `json:"password" writeOnly:"true" doc:"Current actor account password, required for reauthentication."`
}
type BloemPlatformMembershipCreate struct {
	ExpectedRevision int64  `json:"expected_revision" minimum:"1" doc:"Organization policy revision."`
	AccountID        int    `json:"account_id" minimum:"1"`
	LegacyRole       string `json:"legacy_role" enum:"admin,user"`
	Status           string `json:"status,omitempty" enum:"invited,active,suspended"`
}
type BloemPlatformMembershipUpdate struct {
	ExpectedRevision int64   `json:"expected_revision" minimum:"1" doc:"Membership security revision."`
	LegacyRole       *string `json:"legacy_role,omitempty" enum:"admin,user"`
	Status           *string `json:"status,omitempty" enum:"invited,active,suspended"`
}
type BloemEntitlementTemplateCreate struct {
	Key     string                      `json:"key"`
	Name    string                      `json:"name"`
	Enabled bool                        `json:"enabled,omitempty"`
	Policy  BloemEntitlementPolicyInput `json:"policy,omitempty"`
}
type BloemEntitlementTemplateRevise struct {
	ExpectedRevision int64                        `json:"expected_revision" minimum:"1"`
	SourceRevision   int64                        `json:"source_revision,omitempty" doc:"If positive, copy this historical revision's policy into a new revision."`
	Name             string                       `json:"name"`
	Enabled          bool                         `json:"enabled,omitempty"`
	Policy           *BloemEntitlementPolicyInput `json:"policy,omitempty" doc:"Required unless source_revision selects a historical policy."`
}
type BloemEntitlementTemplateClone struct {
	SourceRevision int64  `json:"source_revision"`
	Key            string `json:"key"`
	Name           string `json:"name"`
}
type BloemEntitlementManagedGroup struct {
	ID     int64                  `json:"id"`
	Name   string                 `json:"name"`
	Policy BloemEntitlementPolicy `json:"policy"`
}
type BloemOrganizationEntitlementRead struct {
	TemplateKey         string                        `json:"template_key"`
	TemplateRevision    int64                         `json:"template_revision"`
	ManagedDefaultGroup *BloemEntitlementManagedGroup `json:"managed_default_group"`
	TenantLimits        struct {
		Slots      int `json:"slots"`
		Transcodes int `json:"transcodes"`
	} `json:"tenant_limits"`
	LibraryIDs       []int      `json:"library_ids" nullable:"true"`
	LastReconciledAt *time.Time `json:"last_reconciled_at"`
	AuditHistoryHref string     `json:"audit_history_href"`
}
type BloemLegacyAccountEntitlementRead struct {
	OrganizationID      uuid.UUID                     `json:"organization_id"`
	AccountID           int                           `json:"account_id"`
	TemplateKey         string                        `json:"template_key"`
	TemplateRevision    int64                         `json:"template_revision"`
	ManagedDefaultGroup *BloemEntitlementManagedGroup `json:"managed_default_group"`
	ManagedGroup        *BloemEntitlementManagedGroup `json:"managed_group,omitempty"`
	LibraryIDs          []int                         `json:"library_ids" nullable:"true"`
	LastReconciledAt    *time.Time                    `json:"last_reconciled_at"`
}
type BloemEntitlementDryRunChange struct {
	Field  string `json:"field"`
	Before struct {
		TemplateKey      string `json:"template_key"`
		TemplateRevision int64  `json:"template_revision"`
	} `json:"before"`
	After struct {
		TemplateKey      string `json:"template_key"`
		TemplateRevision int64  `json:"template_revision"`
	} `json:"after"`
}
type BloemEntitlementDryRunResponse struct {
	Result            BloemEntitlementApplyResponse  `json:"result"`
	TemplateKey       string                         `json:"template_key"`
	TemplateRevision  int64                          `json:"template_revision"`
	Changed           bool                           `json:"changed"`
	DryRunToken       string                         `json:"dry_run_token"`
	ConfirmationToken string                         `json:"confirmation_token"`
	ExpiresAt         time.Time                      `json:"expires_at"`
	Changes           []BloemEntitlementDryRunChange `json:"changes"`
	Warnings          []string                       `json:"warnings"`
}
type BloemAccountPolicySnapshotsRequest struct {
	AccountIDs []int `json:"account_ids" minItems:"1" maxItems:"10000"`
}
type BloemPlatformPolicyPreviewRequest struct {
	AccountIDs []int              `json:"account_ids" minItems:"1" maxItems:"10000"`
	Command    BloemPolicyCommand `json:"command"`
}

type BloemPlatformOrganizationsResponse struct {
	Organizations []tenancy.OrganizationSummary `json:"organizations"`
	NextCursor    string                        `json:"next_cursor,omitempty"`
}

type BloemPlatformOrganizationResponse struct {
	Organization tenancy.Organization `json:"organization"`
}

type BloemPlatformMembershipsResponse struct {
	Memberships []tenancy.MembershipSummary `json:"memberships"`
	NextCursor  string                      `json:"next_cursor,omitempty"`
}

type BloemPlatformMembershipResponse struct {
	Membership   tenancy.Membership   `json:"membership"`
	Organization tenancy.Organization `json:"organization"`
}

type BloemEntitlementTemplatesResponse struct {
	Templates []BloemEntitlementTemplate `json:"templates"`
}

type BloemEntitlementTemplateResponse struct {
	Template BloemEntitlementTemplate `json:"template"`
}

type BloemEntitlementRevisionsResponse struct {
	Revisions []BloemEntitlementTemplate `json:"revisions"`
}

type BloemEntitlementAuditResponse struct {
	Events []entitlements.AuditEvent `json:"events"`
}

type BloemAccountPolicySnapshotsResponse struct {
	ObservedAt time.Time                                  `json:"observed_at"`
	Items      []entitlements.AccountPolicySnapshotResult `json:"items"`
}

type BloemPlatformPolicyPreviewResponse struct {
	Selection adminpeople.Selection     `json:"selection"`
	Preview   adminpeople.PolicyPreview `json:"preview"`
}

func registerBloemPlatformAdministrationDocument(reg *Registry) {
	// Huma cannot mark object references nullable with a field tag. Preserve
	// the referenced required fields while admitting the nulls written/read by
	// these chi handlers, as the native engagement schemas do.
	for typ, field := range map[reflect.Type]string{
		reflect.TypeFor[BloemEntitlementTemplateRevise]():    "policy",
		reflect.TypeFor[BloemOrganizationEntitlementRead]():  "managed_default_group",
		reflect.TypeFor[BloemLegacyAccountEntitlementRead](): "managed_default_group",
	} {
		schema := reg.api.OpenAPI().Components.Schemas.Schema(typ, false, "")
		schema.Properties[field] = &huma.Schema{AnyOf: []*huma.Schema{schema.Properties[field], {Type: "null"}}}
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/session", "createBloemAdminSession", "Exchange a login session for a scoped administrative context.", "bloemAdminLoginSession", 200)
		op.Description = "Requires an authenticated account with a current login-session ID and account incarnation. API keys without a login session are refused. scope=platform forbids organization_id and requires current platform authority. scope=organization requires organization_id and current organization administrator or platform authority; membership and organization must be active. The new token is bound to the originating login session. Strict JSON; body limit 16 KiB."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid scope, JSON, organization selection or body exceeding 16 KiB.", 404: "not_found: the selected organization is absent, hidden or still initializing, or this account has no membership in it or only an invitation."})
		bloemDocumentDescribe(&op, map[int]string{
			401: "unauthorized: authentication is missing or invalid, the account identity is incomplete, or the credential has no login session (API keys cannot exchange); authorization_state_stale: the membership or its revisions changed during the exchange.",
			403: "insufficient_platform_authority: scope=platform without current platform authority; insufficient_organization_authority: the membership is not an organization administrator; organization_suspended: the organization or membership is suspended; forbidden or password_change_required: this session cannot use the route.",
			503: "tenant_unavailable: administrative authorization is unavailable; service_unavailable: the login session could not be checked, retry after Retry-After.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemAdminSessionRequest], bloemJSONOutput[BloemAdminSessionResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/", "listBloemPlatformOrganizations", "Page organizations with current summary counts.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription + " Organizations are ordered by case-insensitive name; next_cursor is omitted on the last page."
		bloemDocumentQuery(&op, "query", "Case-insensitive substring of the organization name or slug.", "string")
		bloemDocumentQuery(&op, "status", "Organization status filter.", "string")
		op.Parameters[len(op.Parameters)-1].Schema.Enum = []any{"initializing", "active", "suspended"}
		bloemDocumentQuery(&op, "cursor", "Opaque next_cursor from the previous page.", "string")
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200; defaults to 50.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: limit is outside 1 to 200, status is unknown or cursor is invalid."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPlatformOrganizationsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/", "createBloemPlatformOrganization", "Create an organization for an existing owner account.", "bloemPlatformContext", 201)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			409: "organization_slug_conflict: the slug is already in use; membership_conflict: the owner membership conflicts with existing state.",
			422: "validation_failed: name or slug is invalid, or owner_account_id does not identify an enabled account.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOrganizationCreate], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/", "getBloemPlatformOrganization", "Read an organization summary.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the organization ID is malformed or the organization does not exist."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationSummaryResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/platform/organizations/{id}/", "updateBloemPlatformOrganization", "Change organization name or slug.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " At least one of name or slug is required."
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the organization ID is malformed or the organization does not exist.",
			409: bloemOrganizationPolicyConflict + " organization_slug_conflict: the slug is already in use.",
			422: "validation_failed: expected_revision is not positive, neither name nor slug is present, or a value is invalid.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOrganizationUpdate], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	for _, transition := range []struct{ path, id, summary string }{
		{"/admin/platform/organizations/{id}/suspend", "suspendBloemPlatformOrganization", "Suspend an organization."},
		{"/admin/platform/organizations/{id}/reactivate", "reactivateBloemPlatformOrganization", "Reactivate an organization."},
	} {
		op := bloemAdminDocumentOp(reg, "POST", transition.path, transition.id, transition.summary, "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the organization ID is malformed or the organization does not exist.",
			409: bloemOrganizationPolicyConflict,
			422: "validation_failed: expected_revision is not positive.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/transfer-ownership", "transferBloemPlatformOrganizationOwnership", "Transfer ownership after explicit confirmation and reauthentication.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription + " Field validation runs before the password check. The new owner must already be an active member and an enabled account."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			401: bloemAdminContextUnauthorized + " reauthentication_required: password is missing or incorrect.",
			404: "not_found: the organization ID is malformed, the organization does not exist, or the new owner has no membership in it.",
			409: bloemOrganizationPolicyConflict,
			422: "validation_failed: expected_revision or owner_account_id is not positive, confirmed is not true, or the new owner is not an enabled active member.",
			503: "tenant_unavailable: administrative authorization, password verification or the administration store is unavailable.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOwnershipTransfer], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/memberships", "listBloemPlatformOrganizationMemberships", "Page an organization membership directory.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription + " Memberships are ordered by account ID; next_cursor is omitted on the last page. An unknown organization returns an empty page."
		bloemDocumentQuery(&op, "cursor", "Opaque next_cursor from the previous page.", "string")
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200; defaults to 50.", "integer")
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the organization ID is malformed."})
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: limit is outside 1 to 200 or cursor is invalid."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPlatformMembershipsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/memberships", "createBloemPlatformOrganizationMembership", "Create an organization membership for an existing account.", "bloemPlatformContext", 201)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the organization ID is malformed or the organization does not exist.",
			409: bloemOrganizationPolicyConflict + " membership_conflict: the account is already a member.",
			422: "validation_failed: a field is invalid, or account_id does not identify an existing account.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformMembershipCreate], bloemJSONOutput[BloemPlatformMembershipResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/platform/organizations/{id}/memberships/{membership_id}", "updateBloemPlatformOrganizationMembership", "Change a membership role or status.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " At least one of legacy_role or status is required."
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: an ID is malformed, or the organization or membership does not exist.",
			409: "authorization_state_changed: expected_revision is not the membership's security_revision; current_revision carries the current value. membership_conflict: the owner's membership must stay an active admin.",
			422: "validation_failed: expected_revision is not positive, neither legacy_role nor status is present, or a value is invalid.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformMembershipUpdate], bloemJSONOutput[BloemPlatformMembershipResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates", "listBloemEntitlementTemplates", "List the entitlement template catalog.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementAccountsKey")
		bloemDocumentQuery(&op, "include_archived", "Defaults to true. Parsed with Go strconv.ParseBool; an unparsable value is treated as false.", "string")
		bloemDocumentQuery(&op, "status", "enabled (case-insensitive) returns only enabled, unarchived templates; other values are ignored.", "string")
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementTemplatesResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates", "createBloemEntitlementTemplate", "Create a versioned entitlement template.", "bloemPlatformContext", 201)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		op.Description += " Catalog creation requires a platform context even though catalog reads accept an API key; an API key reaches the handler and is refused with 403 insufficient_platform_authority."
		bloemDocumentDescribe(&op, map[int]string{
			409: "template_conflict: the key or name already exists; protected_template: the key is reserved for a protected template.",
			422: "validation_failed: key or name is missing, or the policy is invalid (fields.policy).",
		})
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateCreate], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates/{key}", "getBloemEntitlementTemplate", "Read the latest or a selected entitlement template revision.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentQuery(&op, "revision", "Positive revision; omit for latest.", "integer")
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the template or selected revision does not exist."})
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: revision is not a positive integer."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	for _, revisions := range []struct{ path, id, summary string }{
		{"/admin/platform/entitlement-templates/{key}/revisions", "listBloemEntitlementTemplateRevisions", "List immutable template revisions."},
		{"/admin/platform/entitlement-templates/{key}/history", "listBloemEntitlementTemplateHistory", "List immutable template revisions (history alias)."},
	} {
		op := bloemAdminDocumentOp(reg, "GET", revisions.path, revisions.id, revisions.summary, "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription + " Revisions are ordered newest first."
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the template does not exist."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementRevisionsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/revisions", "reviseBloemEntitlementTemplate", "Create a new revision or copy a historical policy into a new revision.", "bloemPlatformContext", 201)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the template or source_revision does not exist.",
			409: "revision_conflict: expected_revision is not the template's current revision; protected_template: a protected template cannot be weakened; template_conflict: the name is already used.",
			422: "validation_failed with fields: name, expected_revision or policy is missing or invalid. template_unavailable without fields: the template is archived.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateRevise], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/clone", "cloneBloemEntitlementTemplate", "Clone a template revision into a disabled new template.", "bloemPlatformContext", 201)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the source template revision does not exist.",
			409: "template_conflict: the new key or name already exists; protected_template: the new key is reserved for a protected template.",
			422: "validation_failed: key or name is missing.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateClone], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/archive", "archiveBloemEntitlementTemplate", "Archive a template using its current revision.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentOmit(&op, 422)
		bloemDocumentDescribe(&op, map[int]string{
			404: "not_found: the template does not exist.",
			409: "revision_conflict: expected_revision is not the current revision, or the template is already archived; protected_template: protected templates cannot be archived.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/entitlement", "getBloemPlatformOrganizationEntitlement", "Read the managed organization entitlement.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentDescribe(&op, map[int]string{404: bloemEntitlementOrganizationNotFound})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationEntitlementRead]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/entitlement/audit", "listBloemPlatformOrganizationEntitlementAudit", "Read organization entitlement audit history.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription + " Returns at most 200 events, newest first. An unknown organization returns an empty list."
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: the organization ID is malformed."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementAuditResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/users/{user_id}/entitlement", "getBloemLegacyAccountEntitlement", "Read the legacy managed account entitlement projection.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		op.Description += " Legacy alias projection; /accounts/{account_id}/entitlement returns the authoritative account and profile policy snapshot instead."
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: user_id is malformed, or the account is not an active member of the deployment default organization with an assigned access group."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLegacyAccountEntitlementRead]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/entitlement/dry-run", "previewBloemOrganizationEntitlement", "Preview a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += bloemEntitlementApplyDescription
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemEntitlementOrganizationNotFound + " The template revision may also not exist.",
			409: "managed_cohort_immutable: the organization's managed cohort group cannot be changed to this template.",
			422: "validation_failed with fields: template_key or template_revision is missing. template_unavailable without fields: the template revision is disabled or archived.",
		})
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementDryRunResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/entitlement/apply", "applyBloemOrganizationEntitlement", "Apply a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += bloemEntitlementApplyDescription + bloemEntitlementApplyReplay
		bloemDocumentDescribe(&op, map[int]string{
			404: bloemEntitlementOrganizationNotFound + " The template revision may also not exist.",
			409: bloemEntitlementApplyConflict + " managed_cohort_immutable: the organization's managed cohort group cannot be changed to this template.",
			422: bloemEntitlementApplyValidation,
		})
		bloemDocumentCommandKey(&op, bloemEntitlementApplyKeyRule)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementApplyResponse]](reg, op)
	}

	for _, account := range []struct {
		prefix, previewID, applyID string
		apiKey                     bool
	}{
		{"/admin/platform/accounts/{account_id}", "previewBloemAccountEntitlement", "applyBloemAccountEntitlement", true},
		{"/admin/platform/users/{user_id}", "previewBloemLegacyAccountEntitlement", "applyBloemLegacyAccountEntitlement", false},
	} {
		const notFound = "not_found: the account ID is malformed, the account is not an active member of the deployment default organization, or the template revision does not exist."
		preview := bloemAdminDocumentOp(reg, "POST", account.prefix+"/entitlement/dry-run", account.previewID, "Preview a pinned entitlement template.", "bloemPlatformContext", 200)
		preview.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &preview)
		bloemDocumentTemplateValidation(reg, &preview)
		bloemDocumentOmit(&preview, 409)
		bloemDocumentDescribe(&preview, map[int]string{
			404: notFound,
			422: "validation_failed with fields: template_key or template_revision is missing. template_unavailable without fields: the template revision is disabled or archived.",
		})
		if account.apiKey {
			bloemDocumentPlatformAPIKey(&preview, "bloemEntitlementAccountsKey")
		}
		preview.Description += bloemEntitlementApplyDescription
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementDryRunResponse]](reg, preview)

		apply := bloemAdminDocumentOp(reg, "POST", account.prefix+"/entitlement/apply", account.applyID, "Apply a pinned entitlement template.", "bloemPlatformContext", 200)
		apply.Description = bloemPlatformAuthorityDescription
		bloemAdminDocumentMutation(reg, &apply)
		bloemDocumentTemplateValidation(reg, &apply)
		bloemDocumentDescribe(&apply, map[int]string{
			404: notFound,
			409: bloemEntitlementApplyConflict,
			422: bloemEntitlementApplyValidation,
		})
		if account.apiKey {
			bloemDocumentPlatformAPIKey(&apply, "bloemEntitlementAccountsKey")
		}
		apply.Description += bloemEntitlementApplyDescription + bloemEntitlementApplyReplay
		bloemDocumentCommandKey(&apply, bloemEntitlementApplyKeyRule)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementApplyResponse]](reg, apply)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/accounts/{account_id}/entitlement", "getBloemDirectAccountPolicy", "Read authoritative account and profile entitlement policies.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: account_id is malformed or the account is not a member of the deployment default organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[entitlements.AccountPolicySnapshot]](reg, op)
	}

	for _, snapshots := range []struct{ path, id string }{
		{"/admin/platform/accounts/entitlement-snapshots", "readBloemDirectAccountPolicySnapshots"},
		{"/admin/platform/organizations/{organization_id}/entitlement-snapshots", "readBloemOrganizationAccountPolicySnapshots"},
	} {
		op := bloemAdminDocumentOp(reg, "POST", snapshots.path, snapshots.id, "Read bounded account policy snapshots in one observation.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentValidation(reg, &op)
		bloemDocumentDescribe(&op, map[int]string{
			400: bloemPlatformStrictJSONError,
			422: "validation_failed: account_ids is empty, has more than 10000 entries or contains a non-positive ID.",
		})
		if _, ok := op.Responses["404"]; ok {
			bloemDocumentDescribe(&op, map[int]string{404: "not_found: the organization ID is malformed or nil. Accounts outside the organization are reported per item."})
		}
		op.Description += " Strict JSON body limit is 1 MiB; account_ids contains 1 to 10000 positive account IDs. Per-account failures use items[].error, not an HTTP failure."
		registerBloemChiDocument[bloemJSONInput[BloemAccountPolicySnapshotsRequest], bloemJSONOutput[BloemAccountPolicySnapshotsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "getBloemOrganizationAccountPolicy", "Read authoritative account and profile entitlement policies.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentDescribe(&op, map[int]string{404: "not_found: an ID is malformed or the account is not a member of the organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[entitlements.AccountPolicySnapshot]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/entitlement-cohorts", "listBloemPlatformOrganizationEntitlementCohorts", "List organization entitlement cohorts.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentQuery(&op, "include_archived", "true or false; defaults to false. Other query parameters return 422.", "boolean")
		bloemDocumentDescribe(&op, map[int]string{404: bloemPlatformBulkOrganizationNotFound})
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "validation_failed: include_archived is not true or false, or another query parameter is present."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}", "getBloemPlatformOrganizationEntitlementCohort", "Read one organization entitlement cohort.", "bloemPlatformContext", 200)
		op.Description = bloemPlatformAuthorityDescription
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentDescribe(&op, map[int]string{404: bloemPlatformBulkOrganizationNotFound + " The cohort may also be malformed or absent from the organization."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortResponse]](reg, op)
	}

	// Each bulk family exists for the deployment default organization (direct
	// account paths) and for an explicit organization.
	for _, family := range []struct{ prefix, idPart string }{
		{"/admin/platform/accounts/entitlement-bulk", "Direct"},
		{"/admin/platform/organizations/{organization_id}/entitlement-bulk", "Organization"},
	} {
		{
			op := bloemAdminDocumentOp(reg, "POST", family.prefix+"/policy-previews", "previewBloemPlatform"+family.idPart+"Policy", "Create a selection and preview a bulk policy command.", "bloemPlatformContext", 201)
			op.Description = bloemPlatformAuthorityDescription
			bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
			op.Description += bloemPlatformBulkDescription
			bloemDocumentValidation(reg, &op)
			bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
				400: bloemPlatformStrictJSONError,
				404: bloemPlatformBulkOrganizationNotFound + " Every account must be an active member, and the command's template or cohort must exist.",
				409: "authorization_state_changed: the actor's authority changed during the preview; selection_expired: the selection expired before the preview completed.",
			})
			bloemDocumentDescribe(&op, map[int]string{422: "validation_failed: account_ids is empty, has more than 10000 entries, or contains a non-positive or duplicate ID; or the command is invalid (fields.request)."})
			registerBloemChiDocument[bloemJSONInput[BloemPlatformPolicyPreviewRequest], bloemJSONOutput[BloemPlatformPolicyPreviewResponse]](reg, op)
		}

		{
			op := bloemAdminDocumentOp(reg, "POST", family.prefix+"/policy-jobs", "createBloemPlatform"+family.idPart+"PolicyJob", "Enqueue a confirmed bulk entitlement policy job.", "bloemPlatformContext", 201)
			op.Description = bloemPlatformAuthorityDescription
			bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
			op.Description += bloemPlatformBulkDescription
			bloemDocumentValidation(reg, &op)
			bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
				400: bloemPlatformStrictJSONError,
				404: bloemPlatformBulkOrganizationNotFound + " The selection token may also be invalid, or the command's template or cohort absent.",
				409: bloemPolicyJobConflict,
			})
			bloemDocumentDescribe(&op, map[int]string{422: bloemPolicyJobValidation})
			bloemDocumentCommandKey(&op, bloemPolicyJobKeyRule)
			registerBloemChiDocument[bloemJSONInput[BloemPolicyBulkAction], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
		}

		{
			op := bloemAdminDocumentOp(reg, "GET", family.prefix+"/policy-jobs/{job_id}", "getBloemPlatform"+family.idPart+"PolicyJob", "Read a bulk entitlement policy job.", "bloemPlatformContext", 200)
			op.Description = bloemPlatformAuthorityDescription
			bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
			op.Description += bloemPlatformBulkDescription
			bloemDocumentDescribe(&op, map[int]string{404: bloemPlatformBulkOrganizationNotFound + " The job may also be absent from the organization, longer than 128 characters or not a policy job."})
			registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
		}

		{
			op := bloemAdminDocumentOp(reg, "POST", family.prefix+"/policy-jobs/{job_id}/cancel", "cancelBloemPlatform"+family.idPart+"PolicyJob", "Cancel an eligible bulk entitlement policy job.", "bloemPlatformContext", 200)
			op.Description = bloemPlatformAuthorityDescription
			bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
			op.Description += bloemPlatformBulkDescription
			bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
				400: bloemPlatformStrictJSONError,
				404: bloemPlatformBulkOrganizationNotFound + " The job may also be absent from the organization or longer than 128 characters.",
				409: "job_not_cancellable: the job is not a policy job; authorization_state_changed: the actor's authority changed.",
			})
			op.Description += " Send an empty JSON object; missing JSON is invalid. A completed, failed or cancelled job is returned unchanged."
			registerBloemChiDocument[bloemJSONInput[struct{}], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
		}
	}
}
