package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type v2OrganizationStoreStub struct {
	memberships   []tenancy.Membership
	organizations map[uuid.UUID]tenancy.Organization
	err           error
}

func (s v2OrganizationStoreStub) ListMemberships(context.Context, int) ([]tenancy.Membership, error) {
	return s.memberships, s.err
}

func (s v2OrganizationStoreStub) GetOrganization(_ context.Context, id uuid.UUID) (tenancy.Organization, error) {
	organization, ok := s.organizations[id]
	if !ok {
		return tenancy.Organization{}, tenancy.ErrOrganizationNotFound
	}
	return organization, nil
}

func TestV2CapabilitiesExactContract(t *testing.T) {
	// Capability discovery must track what is actually wired: a server with
	// direct profile login advertises it, one without does not.
	handler := NewV2SystemHandler(nil)
	handler.SetDirectProfileLoginAvailable(true)
	rec := httptest.NewRecorder()
	handler.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := `{"api":"v2","identity_schema":1,` +
		`"features":{"legacy_silo_v1":true,"organization_memberships":true,"tenant_bounded_media_scope":true,` +
		`"direct_profile_login":true,"shared_device_pairing":false,"delegated_admin_roles":false},` +
		`"media_types":["movie","series","episode","audiobook","ebook","manga","music_album"],` +
		`"feature_tokens":["playback_plan_v3","neutral_playback_v3_contract_v1","layout_aware_passthrough",` +
		`"playback_route_diagnostics","device_quirks_v1","seek_reanchor_v1","output_change_v1",` +
		`"direct_stream_resume_v1","header_authenticated_media_v1","authorized_media_origins_v1",` +
		`"software_video_decode_v1","plan_invalidated_v1","plan_source_duration_v1","declared_event_channels",` +
		`"watch_document_v1","device_pairing_v1","progress_sync_v1","music_catalog_v1"]}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("body = %s, want %s", rec.Body.String(), want)
	}
}

func TestV2CapabilitiesDistinguishLifecycleSupportFromEnforcement(t *testing.T) {
	for _, test := range []struct {
		name         string
		phase        lifecycleidempotency.Phase
		wantRequired bool
	}{
		{name: "optional", phase: lifecycleidempotency.PhaseOptional},
		{name: "required", phase: lifecycleidempotency.PhaseRequired, wantRequired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewV2SystemHandler(nil)
			handler.SetLifecycleIdempotencyPhase(func(context.Context) (lifecycleidempotency.Phase, error) {
				return test.phase, nil
			})
			rec := httptest.NewRecorder()
			handler.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))
			var body struct {
				FeatureTokens []string `json:"feature_tokens"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode capabilities: %v", err)
			}
			if !slices.Contains(body.FeatureTokens, "lifecycle_idempotency_v1") {
				t.Fatalf("support token missing: %v", body.FeatureTokens)
			}
			if got := slices.Contains(body.FeatureTokens, "lifecycle_idempotency_required_v1"); got != test.wantRequired {
				t.Fatalf("required token = %t, want %t: %v", got, test.wantRequired, body.FeatureTokens)
			}
		})
	}
}

// The three fields v2 published before the client surface moved here keep their
// names, their types and their values. Everything the TV clients need arrived
// beside them, never through them.
func TestV2CapabilitiesGrewAdditively(t *testing.T) {
	handler := NewV2SystemHandler(nil)
	rec := httptest.NewRecorder()
	handler.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities", nil))

	var body struct {
		API            string          `json:"api"`
		IdentitySchema int             `json:"identity_schema"`
		Features       map[string]bool `json:"features"`
		MediaTypes     []string        `json:"media_types"`
		FeatureTokens  []string        `json:"feature_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if body.API != "v2" || body.IdentitySchema != 1 {
		t.Fatalf("api/identity_schema = %q/%d, want v2/1", body.API, body.IdentitySchema)
	}
	for name, want := range map[string]bool{
		"legacy_silo_v1": true, "organization_memberships": true, "tenant_bounded_media_scope": true,
		"direct_profile_login": false, "shared_device_pairing": false, "delegated_admin_roles": false,
	} {
		got, present := body.Features[name]
		if !present || got != want {
			t.Errorf("features[%q] = %v (present %v), want %v", name, got, present, want)
		}
	}
	if len(body.Features) != 6 {
		t.Errorf("features has %d keys, want the original 6: %v", len(body.Features), body.Features)
	}

	// The tokens the TV clients feature-detect on. A client that cannot find
	// one of these disables the feature entirely, so their absence is silent.
	for _, token := range []string{"watch_document_v1", "device_pairing_v1", "progress_sync_v1", "music_catalog_v1"} {
		if !slices.Contains(body.FeatureTokens, token) {
			t.Errorf("feature_tokens is missing %q: %v", token, body.FeatureTokens)
		}
	}
	// Playback advertises the same tokens here as on /playback/capability.
	for _, token := range playback.ServerFeaturesV3() {
		if !slices.Contains(body.FeatureTokens, token) {
			t.Errorf("feature_tokens is missing playback token %q: %v", token, body.FeatureTokens)
		}
	}
	if !slices.Contains(body.MediaTypes, "movie") || !slices.Contains(body.MediaTypes, "manga") {
		t.Errorf("media_types = %v, want the types this build serves", body.MediaTypes)
	}
}

func TestV2OrganizationsReturnsOnlyActiveMembershipsAndOrganizations(t *testing.T) {
	activeID, hiddenID, invitedID, ownerlessID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	activeMembershipID := uuid.New()
	ownerAccountID := 7
	handler := NewV2SystemHandler(v2OrganizationStoreStub{
		memberships: []tenancy.Membership{
			{ID: activeMembershipID, OrganizationID: activeID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "admin", SecurityRevision: 4},
			{ID: uuid.New(), OrganizationID: hiddenID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "user", SecurityRevision: 2},
			{ID: uuid.New(), OrganizationID: invitedID, AccountID: 7, Status: tenancy.MembershipInvited, LegacyRole: "user", SecurityRevision: 1},
			{ID: uuid.New(), OrganizationID: ownerlessID, AccountID: 7, Status: tenancy.MembershipActive, LegacyRole: "user", SecurityRevision: 1},
		},
		organizations: map[uuid.UUID]tenancy.Organization{
			activeID:    {ID: activeID, Slug: "bloem", Name: "Bloem", Status: tenancy.OrganizationActive, OwnerAccountID: &ownerAccountID, PolicyRevision: 9, Default: true},
			hiddenID:    {ID: hiddenID, Slug: "hidden", Name: "Hidden", Status: tenancy.OrganizationSuspended, PolicyRevision: 3},
			ownerlessID: {ID: ownerlessID, Slug: "ownerless", Name: "Ownerless", Status: tenancy.OrganizationActive, PolicyRevision: 1},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Organizations []map[string]any `json:"organizations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Organizations) != 1 {
		t.Fatalf("organizations = %#v, want exactly one", body.Organizations)
	}
	organization := body.Organizations[0]
	if organization["id"] != activeID.String() || organization["membership_id"] != activeMembershipID.String() || organization["name"] != "Bloem" {
		t.Fatalf("organization = %#v", organization)
	}
	for _, forbidden := range []string{"owner_email", "owner_account_id", "member_count", "members"} {
		if _, ok := organization[forbidden]; ok {
			t.Errorf("organization leaks %q: %#v", forbidden, organization)
		}
	}
}

func TestV2OrganizationsRequiresAuthentication(t *testing.T) {
	handler := NewV2SystemHandler(v2OrganizationStoreStub{})
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil))
	if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized","message":"Authentication required"}` {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestV2OrganizationsUnavailableWithoutStore(t *testing.T) {
	handler := NewV2SystemHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestV2OrganizationsFailsClosedOnStoreError(t *testing.T) {
	handler := NewV2SystemHandler(v2OrganizationStoreStub{err: errors.New("database failed")})
	req := httptest.NewRequest(http.MethodGet, "/api/v2/organizations", nil)
	req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: 7}))
	rec := httptest.NewRecorder()
	handler.HandleOrganizations(rec, req)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "database failed") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
