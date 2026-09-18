package apiv2

import (
	"time"

	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type BloemOrganizationActivityInput struct {
	Cursor string `query:"cursor" maxLength:"1024" doc:"Opaque next_cursor from the previous page. Omit for the newest events; invalid cursors return 400 invalid_cursor."`
}

type BloemOrganizationActivityOutput struct {
	Body tenancy.OrganizationAuditPage
}

type BloemOrganizationInvitationRevision struct {
	ExpectedRevision int64 `json:"expected_revision" minimum:"1" doc:"Current organization policy revision from the reviewed administrative context; not an invitation revision. A mismatch returns 409 authorization_state_changed with current_revision."`
}

type BloemOrganizationInvitationLifecycleInput struct {
	ID   int64 `path:"id" minimum:"1" doc:"Invitation in the selected organization. Invalid, missing or hidden IDs return 404."`
	Body BloemOrganizationInvitationRevision
}

// BloemOrganizationInvitation restates handlers.invitationResponse, which is
// unexported. The token is deliberately separate from this reusable record.
type BloemOrganizationInvitation struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	AccessGroupID *int64    `json:"access_group_id,omitempty"`
	LibraryIDs    []int     `json:"library_ids,omitempty"`
	CreateProfile bool      `json:"create_profile"`
	ShowTour      bool      `json:"show_tour"`
	Note          string    `json:"note,omitempty"`
	InvitedBy     int64     `json:"invited_by"`
	InvitedByName string    `json:"invited_by_name,omitempty"`
	Status        string    `json:"status" enum:"pending,accepted,expired,revoked"`
	ExpiresAt     time.Time `json:"expires_at"`
	AcceptedAt    *string   `json:"accepted_at,omitempty" format:"date-time"`
	AcceptedUser  *int64    `json:"accepted_user_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type BloemOrganizationInvitationResendResponse struct {
	Invitation BloemOrganizationInvitation `json:"invitation"`
	ClaimToken string                      `json:"claim_token" readOnly:"true" doc:"Single-use claim token returned only by this rotation response. The server retains only its hash; later reads cannot recover it. Treat it as a secret and deliver it directly to the invitee."`
}

type BloemOrganizationInvitationResendOutput struct {
	Body BloemOrganizationInvitationResendResponse
}

func registerBloemOrganizationWorkflowDocument(reg *Registry) {
	const authority = "Requires an organization-scoped administrative-context token matching the resolved account, organization and membership. The originating session, current authority and policy/security revisions are revalidated on each request. No organization path or header can broaden this scope."
	activity := bloemChiDocumentOp(reg, "GET", "/admin/organization/activity", "listBloemOrganizationActivity", "tenancy", "Page the selected organization's sanitized activity and entitlement audit.", "bloemOrganizationContext", 200)
	activity.Description = authority + " Returns at most 50 events ordered newest first by created_at, source and id. next_cursor is omitted on the last page. Events exclude before/after documents and credential data."
	bloemDocumentErrors[BloemNativeError](reg, &activity, map[int]string{400: "invalid_cursor: reload the audit list to reset pagination."})
	registerBloemChiDocument[BloemOrganizationActivityInput, BloemOrganizationActivityOutput](reg, activity)
	for _, method := range []string{"POST", "DELETE"} {
		path, id, summary, status := "/admin/organization/invitations/{id}/resend", "resendBloemOrganizationInvitation", "Rotate an unaccepted, unrevoked user invitation and return its new claim token once.", 201
		detail := " Only role=user invitations that have not been accepted or revoked can be renewed; expired invitations remain eligible. Rotation invalidates the previous token and renews expiry for seven days. This endpoint returns claim_token, not claim_url or email_sent, and does not send email. The token is disclosed once and only its hash is stored. Do not automatically retry: a second successful call rotates the link again."
		if method == "DELETE" {
			path, id, summary, status = "/admin/organization/invitations/{id}", "revokeBloemOrganizationInvitation", "Revoke an unaccepted, unrevoked user invitation in the selected organization.", 204
			detail = " Only role=user invitations that have not been accepted or revoked can be revoked here. Ineligible or already revoked invitations return 404. Success has no response body."
		}
		op := bloemChiDocumentOp(reg, method, path, id, "tenancy", summary, "bloemOrganizationContext", status)
		op.Description = authority + " A JSON body containing expected_revision is required, including on DELETE. It must match the resolved organization's policy revision." + detail
		bloemDocumentValidation(reg, &op)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found: invitation ID is invalid, absent, hidden by organization scope or ineligible for revocation."})
		conflict := "authorization_state_changed: reload the current organization policy revision."
		if method == "POST" {
			conflict += " invitation_not_claimable: only unaccepted, unrevoked user invitations can be renewed; reload before trying again."
		}
		bloemDocumentErrors[BloemNativeRevisionConflict](reg, &op, map[int]string{409: conflict})
		if method == "POST" {
			registerBloemChiDocument[BloemOrganizationInvitationLifecycleInput, BloemOrganizationInvitationResendOutput](reg, op)
		} else {
			registerBloemChiDocument[BloemOrganizationInvitationLifecycleInput, struct{}](reg, op)
		}
	}
}
