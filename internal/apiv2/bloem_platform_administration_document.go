package apiv2

import (
	"reflect"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
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
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid scope, JSON, organization selection or body exceeding 16 KiB.", 404: "not_found: selected organization or membership is unavailable."})
		registerBloemChiDocument[bloemJSONInput[BloemAdminSessionRequest], bloemJSONOutput[BloemAdminSessionResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/", "listBloemPlatformOrganizations", "Page organizations with current summary counts.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		for _, query := range []string{"query", "cursor", "status"} {
			bloemDocumentQuery(&op, query, "Filter; status is initializing, active or suspended.", "string")
		}
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "Invalid organization filter or cursor."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPlatformOrganizationsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/", "createBloemPlatformOrganization", "Create an organization for an existing owner account.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOrganizationCreate], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/", "getBloemPlatformOrganization", "Read an organization summary.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationSummaryResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/platform/organizations/{id}/", "updateBloemPlatformOrganization", "Change organization name or slug.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " At least one of name or slug is required."
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOrganizationUpdate], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/suspend", "suspendBloemPlatformOrganization", "Suspend an organization.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/reactivate", "reactivateBloemPlatformOrganization", "Reactivate an organization.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/transfer-ownership", "transferBloemPlatformOrganizationOwnership", "Transfer ownership after explicit confirmation and reauthentication.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPlatformOwnershipTransfer], bloemJSONOutput[BloemPlatformOrganizationResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/memberships", "listBloemPlatformOrganizationMemberships", "Page an organization membership directory.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentQuery(&op, "cursor", "Opaque page cursor.", "string")
		bloemDocumentQuery(&op, "limit", "Page size from 1 to 200.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "Invalid membership pagination."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPlatformMembershipsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/memberships", "createBloemPlatformOrganizationMembership", "Create an organization membership for an existing account.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPlatformMembershipCreate], bloemJSONOutput[BloemPlatformMembershipResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "PATCH", "/admin/platform/organizations/{id}/memberships/{membership_id}", "updateBloemPlatformOrganizationMembership", "Change a membership role or status.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " At least one of legacy_role or status is required."
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPlatformMembershipUpdate], bloemJSONOutput[BloemPlatformMembershipResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates", "listBloemEntitlementTemplates", "List the entitlement template catalog.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementAccountsKey")
		bloemDocumentQuery(&op, "include_archived", "Defaults to true; parsed with Go ParseBool.", "string")
		bloemDocumentQuery(&op, "status", "enabled selects only enabled, unarchived revisions.", "string")
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementTemplatesResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates", "createBloemEntitlementTemplate", "Create a versioned entitlement template.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " Catalog creation requires a platform context even though catalog reads accept an API key."
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateCreate], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates/{key}", "getBloemEntitlementTemplate", "Read the latest or a selected entitlement template revision.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentQuery(&op, "revision", "Positive revision; omit for latest.", "integer")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "Invalid revision."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates/{key}/revisions", "listBloemEntitlementTemplateRevisions", "List immutable template revisions.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementRevisionsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/entitlement-templates/{key}/history", "listBloemEntitlementTemplateHistory", "List immutable template revisions (history alias).", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementRevisionsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/revisions", "reviseBloemEntitlementTemplate", "Create a new revision or copy a historical policy into a new revision.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateRevise], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/clone", "cloneBloemEntitlementTemplate", "Clone a template revision into a disabled new template.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementTemplateClone], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/entitlement-templates/{key}/archive", "archiveBloemEntitlementTemplate", "Archive a template using its current revision.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemRevisionRequest], bloemJSONOutput[BloemEntitlementTemplateResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/entitlement", "getBloemPlatformOrganizationEntitlement", "Read the managed organization entitlement.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemOrganizationEntitlementRead]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{id}/entitlement/audit", "listBloemPlatformOrganizationEntitlementAudit", "Read organization entitlement audit history.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementAuditResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/users/{user_id}/entitlement", "getBloemLegacyAccountEntitlement", "Read the legacy managed account entitlement projection.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		op.Description += " Legacy alias projection; /accounts/{account_id}/entitlement returns the authoritative account and profile policy snapshot instead."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemLegacyAccountEntitlementRead]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/entitlement/dry-run", "previewBloemOrganizationEntitlement", "Preview a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementDryRunResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{id}/entitlement/apply", "applyBloemOrganizationEntitlement", "Apply a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementApplyResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/{account_id}/entitlement/dry-run", "previewBloemAccountEntitlement", "Preview a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementAccountsKey")
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementDryRunResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/{account_id}/entitlement/apply", "applyBloemAccountEntitlement", "Apply a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementAccountsKey")
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementApplyResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/users/{user_id}/entitlement/dry-run", "previewBloemLegacyAccountEntitlement", "Preview a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementDryRunResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/users/{user_id}/entitlement/apply", "applyBloemLegacyAccountEntitlement", "Apply a pinned entitlement template.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemAdminDocumentMutation(reg, &op)
		bloemDocumentTemplateValidation(reg, &op)
		op.Description += " Select an explicit template key and positive revision. Dry-run returns both dry_run_token and confirmation_token (the same bound expiring token). Apply requires a confirmation token (dry_run_token is its alias) and a body idempotency_key of at most 200 characters; changes since preview return 409 confirmation_stale. Apply returns the result directly, without a result wrapper."
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemEntitlementApplyRequest], bloemJSONOutput[BloemEntitlementApplyResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/accounts/{account_id}/entitlement", "getBloemDirectAccountPolicy", "Read authoritative account and profile entitlement policies.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		registerBloemChiDocument[struct{}, bloemJSONOutput[entitlements.AccountPolicySnapshot]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/entitlement-snapshots", "readBloemDirectAccountPolicySnapshots", "Read bounded account policy snapshots in one observation.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		op.Description += " Strict JSON body limit is 1 MiB; account_ids contains 1 to 10000 positive account IDs. Per-account failures use items[].error, not an HTTP failure."
		registerBloemChiDocument[bloemJSONInput[BloemAccountPolicySnapshotsRequest], bloemJSONOutput[BloemAccountPolicySnapshotsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/entitlement-bulk/policy-previews", "previewBloemPlatformDirectPolicy", "Create a selection and preview a bulk policy command.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformPolicyPreviewRequest], bloemJSONOutput[BloemPlatformPolicyPreviewResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/entitlement-bulk/policy-jobs", "createBloemPlatformDirectPolicyJob", "Enqueue a confirmed bulk entitlement policy job.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPolicyBulkAction], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}", "getBloemPlatformDirectPolicyJob", "Read a bulk entitlement policy job.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel", "cancelBloemPlatformDirectPolicyJob", "Cancel an eligible bulk entitlement policy job.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		op.Description += " Send an empty JSON object; missing JSON is invalid."
		registerBloemChiDocument[bloemJSONInput[struct{}], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", "getBloemOrganizationAccountPolicy", "Read authoritative account and profile entitlement policies.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		registerBloemChiDocument[struct{}, bloemJSONOutput[entitlements.AccountPolicySnapshot]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{organization_id}/entitlement-snapshots", "readBloemOrganizationAccountPolicySnapshots", "Read bounded account policy snapshots in one observation.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		op.Description += " Strict JSON body limit is 1 MiB; account_ids contains 1 to 10000 positive account IDs. Per-account failures use items[].error, not an HTTP failure."
		registerBloemChiDocument[bloemJSONInput[BloemAccountPolicySnapshotsRequest], bloemJSONOutput[BloemAccountPolicySnapshotsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/entitlement-cohorts", "listBloemPlatformOrganizationEntitlementCohorts", "List organization entitlement cohorts.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentQuery(&op, "include_archived", "true or false; defaults to false. Other query parameters return 422.", "boolean")
		bloemDocumentErrors[BloemNativeValidationError](reg, &op, map[int]string{422: "Invalid query parameters."})
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortsResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}", "getBloemPlatformOrganizationEntitlementCohort", "Read one organization entitlement cohort.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemEntitlementCohortResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews", "previewBloemPlatformOrganizationPolicy", "Create a selection and preview a bulk policy command.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		registerBloemChiDocument[bloemJSONInput[BloemPlatformPolicyPreviewRequest], bloemJSONOutput[BloemPlatformPolicyPreviewResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs", "createBloemPlatformOrganizationPolicyJob", "Enqueue a confirmed bulk entitlement policy job.", "bloemPlatformContext", 201)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		bloemDocumentLifecycle(reg, &op)
		registerBloemChiDocument[bloemJSONInput[BloemPolicyBulkAction], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "GET", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}", "getBloemPlatformOrganizationPolicyJob", "Read a bulk entitlement policy job.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		registerBloemChiDocument[struct{}, bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}

	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel", "cancelBloemPlatformOrganizationPolicyJob", "Cancel an eligible bulk entitlement policy job.", "bloemPlatformContext", 200)
		op.Description = "Requires a current platform administrative context, except where the security alternatives explicitly admit a scoped API key. Originating login session and live platform authority are revalidated."
		bloemDocumentPlatformAPIKey(&op, "bloemEntitlementBulkKey")
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{429: "rate_limited: too many bulk entitlement requests; Retry-After: 1.", 409: "Selection or confirmation expired, authorization state changed, idempotency conflicted, or job cannot be canceled."})
		op.Description += " Direct paths resolve the active deployment default organization; organization paths require an active organization. Bulk request bodies have a 1 MiB limit. Selections and confirmations are immutable, expiring and actor-bound."
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "invalid_request: invalid JSON, unknown fields, trailing JSON or body exceeding 1 MiB."})
		op.Description += " Send an empty JSON object; missing JSON is invalid."
		registerBloemChiDocument[bloemJSONInput[struct{}], bloemJSONOutput[BloemPeopleJobResponse]](reg, op)
	}
}
