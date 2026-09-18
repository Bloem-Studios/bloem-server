package handlers

import (
	"context"
	"encoding/json"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type invitationLifecycleStub struct {
	organizationInvitationStub
	calls int
	hash  string
	id    int64
}

func (s *invitationLifecycleStub) ResendForOrganization(_ context.Context, org uuid.UUID, id, actor int64, hash string, _ time.Time) (*models.Invitation, error) {
	s.calls++
	s.got, s.gotInviter, s.id, s.hash = org, actor, id, hash
	return &models.Invitation{ID: 99, Email: "reader@example.test", Role: "user", ExpiresAt: time.Now().Add(time.Hour)}, s.err
}
func (s *invitationLifecycleStub) RevokeForOrganization(_ context.Context, org uuid.UUID, id int64) error {
	s.calls++
	s.got, s.id = org, id
	return s.err
}

type auditReaderStub struct {
	organizationOverviewStub
	cursor string
	calls  int
}

func (s *auditReaderStub) ListOrganizationAudit(_ context.Context, org uuid.UUID, cursor string) (tenancy.OrganizationAuditPage, error) {
	s.calls++
	s.got, s.cursor = org, cursor
	return tenancy.OrganizationAuditPage{Events: []tenancy.OrganizationAuditEvent{}}, s.err
}
func TestBloemOrganizationInvitationLifecycleBindsContextAndRevision(t *testing.T) {
	org := uuid.New()
	for _, operation := range []string{"resend", "revoke"} {
		for _, variant := range []string{"allowed", "stale revision", "platform context", "anonymous"} {
			t.Run(operation+"/"+variant, func(t *testing.T) {
				store := &invitationLifecycleStub{}
				h := NewBloemAdminOrganizationHandler(nil, nil, nil, store)
				body := `{"expected_revision":7}`
				if variant == "stale revision" {
					body = `{"expected_revision":6}`
				}
				req := organizationRequest(http.MethodPost, "/organization/invitations/41?organization_id="+uuid.NewString(), body, org, 7, map[string]string{"id": "41"})
				if variant == "platform context" {
					req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}))
				}
				if variant == "anonymous" {
					req = req.WithContext(context.Background())
				}
				rec := httptest.NewRecorder()
				if operation == "resend" {
					h.HandleResendInvitation(rec, req)
				} else {
					h.HandleRevokeInvitation(rec, req)
				}
				if variant != "allowed" {
					if rec.Code < 400 || store.calls != 0 {
						t.Fatalf("status=%d calls=%d", rec.Code, store.calls)
					}
					return
				}
				if store.calls != 1 || store.got != org || store.id != 41 {
					t.Fatalf("wrong scoped call: %+v", store)
				}
				if operation == "revoke" {
					if rec.Code != 204 {
						t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
					}
					return
				}
				var response struct {
					ClaimToken string `json:"claim_token"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if rec.Code != 201 || store.gotInviter != 7 || response.ClaimToken == "" || invitations.HashToken(response.ClaimToken) != store.hash {
					t.Fatalf("status=%d actor=%d token rotation did not bind digest", rec.Code, store.gotInviter)
				}
			})
		}
	}
}
func TestBloemOrganizationAuditScopesCursorAndRejectsPlatformContext(t *testing.T) {
	org := uuid.New()
	store := &auditReaderStub{}
	h := NewBloemAdminOrganizationHandler(store, nil, nil, nil)
	req := organizationRequest("GET", "/organization/activity?cursor=opaque&organization_id="+uuid.NewString(), "", org, 7, nil)
	rec := httptest.NewRecorder()
	h.HandleAudit(rec, req)
	if rec.Code != 200 || store.got != org || store.cursor != "opaque" {
		t.Fatalf("status=%d reader=%+v", rec.Code, store)
	}
	store.err = tenancy.ErrInvalidCursor
	rec = httptest.NewRecorder()
	h.HandleAudit(rec, req)
	if rec.Code != 400 {
		t.Fatalf("cursor error status=%d", rec.Code)
	}
	req = req.WithContext(apimw.SetAdminContextClaims(req.Context(), auth.AdminContextClaims{AccountID: 7, Scope: auth.AdminScopePlatform}))
	before := store.calls
	rec = httptest.NewRecorder()
	h.HandleAudit(rec, req)
	if rec.Code != 403 || store.calls != before {
		t.Fatalf("platform audit status=%d calls=%d", rec.Code, store.calls)
	}
}
