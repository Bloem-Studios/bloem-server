package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

var accessGroupHandlerOrganizationID = uuid.MustParse("10000000-0000-0000-0000-000000000001")

func TestAccessGroupHandlerIsDefaultRoundTrips(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	handler := NewAccessGroupHandler(store)

	rec := httptest.NewRecorder()
	req := accessGroupRequestWithTenant(httptest.NewRequest(http.MethodPost, "/api/v1/admin/access-groups", strings.NewReader(`{
		"name": "Users",
		"is_default": true
	}`)))
	handler.HandleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HandleCreate status = %d, body %s", rec.Code, rec.Body.String())
	}
	created := decodeAccessGroupResponse(t, rec)
	if !created.IsDefault {
		t.Fatalf("created is_default = false, want true")
	}

	rec = httptest.NewRecorder()
	req = accessGroupRequestWithID(http.MethodGet, "/api/v1/admin/access-groups/1", nil, "1")
	handler.HandleGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGet status = %d, body %s", rec.Code, rec.Body.String())
	}
	got := decodeAccessGroupResponse(t, rec)
	if !got.IsDefault {
		t.Fatalf("get is_default = false, want true")
	}
}

func TestAccessGroupHandlerUpdateDefaultUnsetsPrevious(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	store.groups[1] = access.Group{ID: 1, Name: "Group A", DownloadAllowed: true, RequestsAllowed: true, IsDefault: true}
	store.groups[2] = access.Group{ID: 2, Name: "Group B", DownloadAllowed: true, RequestsAllowed: true}
	store.nextID = 3
	handler := NewAccessGroupHandler(store)

	rec := httptest.NewRecorder()
	req := accessGroupRequestWithID(http.MethodPut, "/api/v1/admin/access-groups/2", strings.NewReader(`{
		"is_default": true
	}`), "2")
	handler.HandleUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleUpdate status = %d, body %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = accessGroupRequestWithTenant(httptest.NewRequest(http.MethodGet, "/api/v1/admin/access-groups", nil))
	handler.HandleList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleList status = %d, body %s", rec.Code, rec.Body.String())
	}
	var groups []accessGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&groups); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	defaults := map[int64]bool{}
	for _, group := range groups {
		defaults[group.ID] = group.IsDefault
	}
	if defaults[1] {
		t.Fatalf("group A remained default after setting group B")
	}
	if !defaults[2] {
		t.Fatalf("group B is_default = false, want true")
	}
}

func TestAccessGroupHandlerUsesOnlyValidatedTenantContext(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	handler := NewAccessGroupHandler(store)
	foreignOrganizationID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/access-groups", strings.NewReader(`{
		"name": "Tenant Bound",
		"organization_id": "20000000-0000-0000-0000-000000000002"
	}`))
	req.Header.Set("X-Organization-ID", foreignOrganizationID.String())
	req = accessGroupRequestWithTenant(req)
	rec := httptest.NewRecorder()

	handler.HandleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("HandleCreate status = %d, body %s", rec.Code, rec.Body.String())
	}
	if store.lastOrganizationID != accessGroupHandlerOrganizationID {
		t.Fatalf("store organization = %s, want validated tenant %s", store.lastOrganizationID, accessGroupHandlerOrganizationID)
	}
	if store.lastOrganizationID == foreignOrganizationID {
		t.Fatal("request-selected organization reached store")
	}
}

func TestAccessGroupHandlerDefaultGroupGuards(t *testing.T) {
	store := newAccessGroupHandlerTestStore()
	store.groups[1] = access.Group{ID: 1, Name: "Default", DownloadAllowed: true, RequestsAllowed: true, IsDefault: true}
	store.nextID = 2
	handler := NewAccessGroupHandler(store)

	rec := httptest.NewRecorder()
	req := accessGroupRequestWithID(http.MethodDelete, "/api/v1/admin/access-groups/1", nil, "1")
	handler.HandleDelete(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("HandleDelete(default) status = %d, want %d, body %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if _, ok := store.groups[1]; !ok {
		t.Fatalf("default group removed despite conflict response")
	}

	rec = httptest.NewRecorder()
	req = accessGroupRequestWithID(http.MethodPut, "/api/v1/admin/access-groups/1", strings.NewReader(`{
		"is_default": false
	}`), "1")
	handler.HandleUpdate(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("HandleUpdate(demote default) status = %d, want %d, body %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !store.groups[1].IsDefault {
		t.Fatalf("default group demoted despite conflict response")
	}
}

type accessGroupHandlerTestStore struct {
	nextID             int64
	groups             map[int64]access.Group
	lastOrganizationID uuid.UUID
}

func newAccessGroupHandlerTestStore() *accessGroupHandlerTestStore {
	return &accessGroupHandlerTestStore{
		nextID: 1,
		groups: map[int64]access.Group{},
	}
}

func (s *accessGroupHandlerTestStore) List(_ context.Context, organizationID uuid.UUID) ([]access.Group, error) {
	s.lastOrganizationID = organizationID
	groups := make([]access.Group, 0, len(s.groups))
	for _, group := range s.groups {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].ID < groups[j].ID
	})
	return groups, nil
}

func (s *accessGroupHandlerTestStore) Get(_ context.Context, organizationID uuid.UUID, id int64) (*access.Group, error) {
	s.lastOrganizationID = organizationID
	group, ok := s.groups[id]
	if !ok {
		return nil, access.ErrGroupNotFound
	}
	return &group, nil
}

func (s *accessGroupHandlerTestStore) Create(_ context.Context, organizationID uuid.UUID, input access.CreateGroupInput) (*access.Group, error) {
	s.lastOrganizationID = organizationID
	now := time.Unix(1, 0).UTC()
	group := access.Group{
		ID:                       s.nextID,
		OrganizationID:           organizationID,
		Name:                     input.Name,
		Description:              input.Description,
		LibraryIDs:               append([]int(nil), input.LibraryIDs...),
		MaxPlaybackQuality:       input.MaxPlaybackQuality,
		DownloadAllowed:          input.DownloadAllowed,
		DownloadTranscodeAllowed: input.DownloadTranscodeAllowed,
		MaxStreams:               input.MaxStreams,
		MaxTranscodes:            input.MaxTranscodes,
		AllowedPermissions:       append([]string(nil), input.AllowedPermissions...),
		RequestsAllowed:          input.RequestsAllowed,
		IsDefault:                input.IsDefault,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	if input.IsDefault {
		s.clearDefault()
	}
	s.groups[group.ID] = group
	s.nextID++
	return &group, nil
}

func (s *accessGroupHandlerTestStore) Update(_ context.Context, organizationID uuid.UUID, id int64, input access.UpdateGroupInput) (*access.Group, error) {
	s.lastOrganizationID = organizationID
	group, ok := s.groups[id]
	if !ok {
		return nil, access.ErrGroupNotFound
	}
	if input.Name != nil {
		group.Name = *input.Name
	}
	if input.Description != nil {
		group.Description = *input.Description
	}
	if input.LibraryIDs != nil {
		group.LibraryIDs = append([]int(nil), (*input.LibraryIDs)...)
	}
	if input.MaxPlaybackQuality != nil {
		group.MaxPlaybackQuality = *input.MaxPlaybackQuality
	}
	if input.DownloadAllowed != nil {
		group.DownloadAllowed = *input.DownloadAllowed
	}
	if input.DownloadTranscodeAllowed != nil {
		group.DownloadTranscodeAllowed = *input.DownloadTranscodeAllowed
	}
	if input.MaxStreams != nil {
		group.MaxStreams = *input.MaxStreams
	}
	if input.MaxTranscodes != nil {
		group.MaxTranscodes = *input.MaxTranscodes
	}
	if input.AllowedPermissions != nil {
		group.AllowedPermissions = append([]string(nil), (*input.AllowedPermissions)...)
	}
	if input.RequestsAllowed != nil {
		group.RequestsAllowed = *input.RequestsAllowed
	}
	if input.IsDefault != nil {
		if *input.IsDefault {
			s.clearDefault()
		} else if group.IsDefault {
			return nil, access.ErrDefaultGroupRequired
		}
		group.IsDefault = *input.IsDefault
	}
	group.UpdatedAt = time.Unix(2, 0).UTC()
	s.groups[id] = group
	return &group, nil
}

func (s *accessGroupHandlerTestStore) Delete(_ context.Context, organizationID uuid.UUID, id int64) error {
	s.lastOrganizationID = organizationID
	group, ok := s.groups[id]
	if !ok {
		return access.ErrGroupNotFound
	}
	if group.IsDefault {
		return access.ErrDefaultGroupRequired
	}
	delete(s.groups, id)
	return nil
}

func (s *accessGroupHandlerTestStore) clearDefault() {
	for id, group := range s.groups {
		group.IsDefault = false
		s.groups[id] = group
	}
}

func accessGroupRequestWithID(method, path string, body *strings.Reader, id string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	return accessGroupRequestWithTenant(req)
}

func accessGroupRequestWithTenant(req *http.Request) *http.Request {
	tenant := tenancy.Context{
		OrganizationID: accessGroupHandlerOrganizationID,
		AccountID:      1,
		Legacy:         true,
	}
	return req.WithContext(tenancy.WithContext(req.Context(), tenant))
}

func decodeAccessGroupResponse(t *testing.T, rec *httptest.ResponseRecorder) accessGroupResponse {
	t.Helper()
	var response accessGroupResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}
