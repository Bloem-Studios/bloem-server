package handlers

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func TestStrictestProfileLimit_ManagedZeroAllowsOnlyPrimary(t *testing.T) {
	if got := strictestProfileLimit(5, 0, true); got != 1 {
		t.Fatalf("managed zero limit = %d, want 1 total profile", got)
	}
	if got := strictestProfileLimit(5, 0, false); got != 5 {
		t.Fatalf("legacy unmanaged zero limit = %d, want account limit 5", got)
	}
}

func TestProfilesForOrganizationDoesNotCountAnotherTenant(t *testing.T) {
	organizationID := uuid.New()
	ctx := withAdminResourceOrganization(context.Background(), organizationID)
	profiles := []userstore.Profile{
		{ID: "current", OrganizationID: organizationID.String()},
		{ID: "other", OrganizationID: uuid.NewString()},
	}
	got := profilesForOrganization(ctx, profiles)
	if len(got) != 1 || got[0].ID != "current" {
		t.Fatalf("profilesForOrganization() = %+v, want only current tenant", got)
	}
}

type profileCapAccessGroups struct {
	group *access.Group
}

type recordingProfileStore struct {
	userstore.UserStore
	created userstore.Profile
}

type recordingProfileWriter struct {
	userstore.PreferenceSettingsWriter
	created *userstore.Profile
}

func (w *recordingProfileWriter) CreateProfile(ctx context.Context, profile userstore.Profile) error {
	*w.created = profile
	return w.PreferenceSettingsWriter.CreateProfile(ctx, profile)
}

func (s *recordingProfileStore) WithPreferenceSettingsTransaction(ctx context.Context, fn func(userstore.PreferenceSettingsWriter) error) error {
	transactioner := s.UserStore.(userstore.PreferenceSettingsTransactioner)
	return transactioner.WithPreferenceSettingsTransaction(ctx, func(writer userstore.PreferenceSettingsWriter) error {
		return fn(&recordingProfileWriter{PreferenceSettingsWriter: writer, created: &s.created})
	})
}

func (g profileCapAccessGroups) Get(context.Context, uuid.UUID, int64) (*access.Group, error) {
	return g.group, nil
}

func (g profileCapAccessGroups) GetForAccount(context.Context, int, int64) (*access.Group, error) {
	return g.group, nil
}

func (g profileCapAccessGroups) GetDefault(context.Context, uuid.UUID) (*access.Group, error) {
	return g.group, nil
}

func (g profileCapAccessGroups) List(context.Context, uuid.UUID) ([]access.Group, error) {
	if g.group == nil {
		return nil, nil
	}
	return []access.Group{*g.group}, nil
}

func (g profileCapAccessGroups) ResolvePolicy(context.Context, access.GroupSubject) (*access.GroupPolicy, error) {
	if g.group == nil {
		return nil, nil
	}
	policy := g.group.Policy()
	return &policy, nil
}

func TestHandleCreateProfile_EnforcesManagedGroupProfileLimit(t *testing.T) {
	store := newProfileTestStore(t)
	groupID := int64(88)
	organizationID := uuid.New()
	handler := NewProfileHandler(testUserStoreProvider{store: store})
	handler.UserRepo = testProfileUserRepo{user: &models.User{ID: 1, MaxProfiles: 5, AccessGroupID: &groupID}}
	handler.AccessGroups = profileCapAccessGroups{group: &access.Group{ID: groupID, OrganizationID: organizationID, MaxProfiles: 1}}

	req := newAuthorizedProfileRequestWithRole(http.MethodPost, "/profiles", `{"name":"Kids"}`, "admin", "")
	req = req.WithContext(tenancy.WithContext(req.Context(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      1,
		Legacy:         true,
	}))
	rr := httptest.NewRecorder()
	handler.HandleCreateProfile(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestHandleCreateProfile_ManagedZeroRejectsSecondaryProfile(t *testing.T) {
	store := newProfileTestStore(t)
	groupID := int64(89)
	organizationID := uuid.New()
	key := "browse-only"
	handler := NewProfileHandler(testUserStoreProvider{store: store})
	handler.UserRepo = testProfileUserRepo{user: &models.User{ID: 1, MaxProfiles: 5, AccessGroupID: &groupID}}
	handler.AccessGroups = profileCapAccessGroups{group: &access.Group{ID: groupID, OrganizationID: organizationID, MaxProfiles: 0, ManagedTemplateKey: &key}}
	req := newAuthorizedProfileRequestWithRole(http.MethodPost, "/profiles", `{"name":"Kids"}`, "admin", "")
	req = req.WithContext(tenancy.WithContext(req.Context(), tenancy.Context{OrganizationID: organizationID, AccountID: 1, Legacy: true}))
	rr := httptest.NewRecorder()
	handler.HandleCreateProfile(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestHandleCreateProfile_InheritsAccountManagedGroup(t *testing.T) {
	store := &recordingProfileStore{UserStore: newProfileTestStore(t)}
	groupID := int64(88)
	organizationID := uuid.New()
	handler := NewProfileHandler(testUserStoreProvider{store: store})
	handler.UserRepo = testProfileUserRepo{user: &models.User{ID: 1, MaxProfiles: 5, AccessGroupID: &groupID}}
	handler.AccessGroups = profileCapAccessGroups{group: &access.Group{ID: groupID, OrganizationID: organizationID, MaxProfiles: 5}}

	req := newAuthorizedProfileRequestWithRole(http.MethodPost, "/profiles", `{"name":"Kids"}`, "admin", "")
	req = req.WithContext(tenancy.WithContext(req.Context(), tenancy.Context{
		OrganizationID: organizationID,
		AccountID:      1,
		Legacy:         true,
	}))
	rr := httptest.NewRecorder()
	handler.HandleCreateProfile(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusCreated, rr.Body.String())
	}
	if store.created.AccessGroupID == nil || *store.created.AccessGroupID != groupID {
		t.Fatalf("created profile access group = %v, want %d", store.created.AccessGroupID, groupID)
	}
}
