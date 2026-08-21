package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type adminUserResourceSessions struct {
	sessions                map[string]*models.AuthSession
	cancelAfterRevoke       func()
	perSessionRevokeCalls   int
	scopedAtomicRevokeCalls int
}

type adminOrganizationUserStore struct {
	userstore.UserStore
	organizationID string
}

func (s *adminOrganizationUserStore) GetProfile(ctx context.Context, profileID string) (*userstore.Profile, error) {
	profile, err := s.UserStore.GetProfile(ctx, profileID)
	if profile != nil {
		profile.OrganizationID = s.organizationID
	}
	return profile, err
}

func (s *adminOrganizationUserStore) ListProfiles(ctx context.Context) ([]userstore.Profile, error) {
	profiles, err := s.UserStore.ListProfiles(ctx)
	for i := range profiles {
		profiles[i].OrganizationID = s.organizationID
	}
	return profiles, err
}

func (s *adminOrganizationUserStore) RegisterDevice(ctx context.Context, entry userstore.DeviceEntry) error {
	return s.UserStore.(userstore.DeviceRegistry).RegisterDevice(ctx, entry)
}

func (s *adminOrganizationUserStore) ListDevices(ctx context.Context) ([]userstore.DeviceEntry, error) {
	return s.UserStore.(userstore.DeviceRegistry).ListDevices(ctx)
}

func (s *adminOrganizationUserStore) DeviceExists(ctx context.Context, profileID, deviceID string) (bool, error) {
	return s.UserStore.(userstore.DeviceRegistry).DeviceExists(ctx, profileID, deviceID)
}

func (s *adminOrganizationUserStore) ForgetDevice(ctx context.Context, profileID, deviceID string) error {
	return s.UserStore.(userstore.DeviceRegistry).ForgetDevice(ctx, profileID, deviceID)
}

type adminUserResourceAvatarStore struct {
	keys      []string
	deleted   []string
	listErr   error
	deleteErr error
}

func (s *adminUserResourceAvatarStore) PutObject(context.Context, string, string, []byte) error {
	return nil
}

func (s *adminUserResourceAvatarStore) DeleteObject(_ context.Context, _ string, key string) error {
	s.deleted = append(s.deleted, key)
	return s.deleteErr
}

func (s *adminUserResourceAvatarStore) ListObjects(context.Context, string, string) ([]string, error) {
	return append([]string(nil), s.keys...), s.listErr
}

func (s *adminUserResourceAvatarStore) PresignGetURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func (s *adminUserResourceAvatarStore) Bucket() string { return "profiles" }

type adminUserResourceProfilePurger struct {
	calls []struct {
		userID    int
		profileID string
	}
	err error
}

func (p *adminUserResourceProfilePurger) PurgeProfileDevices(_ context.Context, userID int, profileID string) error {
	p.calls = append(p.calls, struct {
		userID    int
		profileID string
	}{userID: userID, profileID: profileID})
	return p.err
}

func (s *adminUserResourceSessions) GetByID(_ context.Context, id string) (*models.AuthSession, error) {
	session, ok := s.sessions[id]
	if !ok {
		return nil, auth.ErrSessionNotFound
	}
	copy := *session
	return &copy, nil
}

func (s *adminUserResourceSessions) ListByUser(_ context.Context, userID int) ([]*models.AuthSession, error) {
	out := make([]*models.AuthSession, 0)
	for _, session := range s.sessions {
		if session.UserID == userID {
			copy := *session
			out = append(out, &copy)
		}
	}
	return out, nil
}

func (s *adminUserResourceSessions) RevokeByUserAndSession(ctx context.Context, userID int, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, ok := s.sessions[sessionID]
	if !ok || session.UserID != userID {
		return auth.ErrSessionNotFound
	}
	now := time.Now()
	session.RevokedAt = &now
	s.perSessionRevokeCalls++
	if s.cancelAfterRevoke != nil {
		s.cancelAfterRevoke()
	}
	return nil
}

func (s *adminUserResourceSessions) RevokeAllByUser(ctx context.Context, userID int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	for _, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	if s.cancelAfterRevoke != nil {
		s.cancelAfterRevoke()
	}
	return nil
}

func (s *adminUserResourceSessions) RevokeAllByUserAndProfiles(ctx context.Context, userID int, profileIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected := make(map[string]struct{}, len(profileIDs))
	s.scopedAtomicRevokeCalls++
	for _, profileID := range profileIDs {
		selected[profileID] = struct{}{}
	}
	now := time.Now()
	for _, session := range s.sessions {
		if session.UserID != userID || session.ProfileID == nil || session.RevokedAt != nil {
			continue
		}
		if _, ok := selected[*session.ProfileID]; ok {
			session.RevokedAt = &now
		}
	}
	if s.cancelAfterRevoke != nil {
		s.cancelAfterRevoke()
	}
	return ctx.Err()
}

func (s *adminUserResourceSessions) RevokeAllByImpersonator(ctx context.Context, _ int) error {
	return ctx.Err()
}

type cancellationAdminUserRepo struct {
	user   *models.User
	cancel func()
}

func (r *cancellationAdminUserRepo) List(context.Context) ([]*models.User, error) {
	return []*models.User{r.user}, nil
}

func (r *cancellationAdminUserRepo) Create(context.Context, models.CreateUserInput) (*models.User, error) {
	return nil, errors.New("unexpected create")
}

func (r *cancellationAdminUserRepo) Update(_ context.Context, _ int, _ models.UpdateUserInput) error {
	r.cancel()
	return nil
}

func (r *cancellationAdminUserRepo) Delete(_ context.Context, _ int) error {
	r.cancel()
	return nil
}

func (r *cancellationAdminUserRepo) GetByID(_ context.Context, _ int) (*models.User, error) {
	copy := *r.user
	return &copy, nil
}

func newAdminUserResourceHandler(t *testing.T) (*AdminHandler, map[int]userstore.UserStore, *adminUserResourceSessions) {
	t.Helper()
	stores := map[int]userstore.UserStore{
		1: newAdminUserResourceStore(t, "a"),
		2: newAdminUserResourceStore(t, "b"),
	}
	for userID, profiles := range map[int][]userstore.Profile{
		1: {{ID: "profile-a-primary", Name: "Main A"}, {ID: "profile-a-secondary", Name: "Kids A"}},
		2: {{ID: "profile-b-primary", Name: "Main B"}, {ID: "profile-b-secondary", Name: "Kids B"}},
	} {
		for _, profile := range profiles {
			if err := stores[userID].CreateProfile(context.Background(), profile); err != nil {
				t.Fatalf("create profile %q: %v", profile.ID, err)
			}
		}
	}
	seedDevice(t, stores[1], "profile-a-secondary", "device-a", "A television")
	seedDevice(t, stores[2], "profile-b-secondary", "device-b", "B television")

	users := testAdminUserRepo{users: map[int]*models.User{
		1: {ID: 1, Username: "account-a", MaxProfiles: 4},
		2: {ID: 2, Username: "account-b", MaxProfiles: 4},
	}}
	profileA := "profile-a-secondary"
	sessions := &adminUserResourceSessions{sessions: map[string]*models.AuthSession{
		"session-a": {ID: "session-a", UserID: 1, DeviceName: "A browser", ProfileID: &profileA, ExpiresAt: time.Now().Add(time.Hour)},
		"session-b": {ID: "session-b", UserID: 2, DeviceName: "B browser", ExpiresAt: time.Now().Add(time.Hour)},
	}}
	handler := NewAdminHandler(users, nil, mappedTestUserStoreProvider{stores: stores})
	handler.sessionRepo = sessions
	return handler, stores, sessions
}

func newAdminUserResourceStore(t *testing.T, suffix string) userstore.UserStore {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + suffix + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := userdb.InitSchema(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return userdb.NewSQLiteUserStore(db)
}

func routeAdminUserResources(handler *AdminHandler) http.Handler {
	router := chi.NewRouter()
	router.Route("/api/v1/admin/users/{user_id}", func(r chi.Router) {
		r.Get("/profiles", handler.HandleListUserProfiles)
		r.Post("/profiles", handler.HandleCreateUserProfile)
		r.Put("/profiles/{profile_id}", handler.HandleUpdateUserProfile)
		r.Delete("/profiles/{profile_id}", handler.HandleDeleteUserProfile)
		r.Get("/devices", handler.HandleListUserDevices)
		r.Delete("/devices/{device_id}", handler.HandleDeleteUserDevice)
		r.Get("/auth-sessions", handler.HandleListUserAuthSessions)
		r.Delete("/auth-sessions/{session_id}", handler.HandleRevokeUserAuthSession)
		r.Delete("/auth-sessions", handler.HandleRevokeAllUserAuthSessions)
	})
	return router
}

func adminUserResourceRequest(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

type cancellationCompatState struct {
	persistent map[string]string
	memory     map[string]string
}

func newCancellationCompatState() *cancellationCompatState {
	return &cancellationCompatState{
		persistent: map[string]string{"profile-a": "profile-a-secondary", "profile-b": "profile-b", "account": ""},
		memory:     map[string]string{"profile-a": "profile-a-secondary", "profile-b": "profile-b", "account": ""},
	}
}

func (s *cancellationCompatState) invalidateUser(ctx context.Context, _ int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clear(s.persistent)
	clear(s.memory)
	return nil
}

func (s *cancellationCompatState) invalidateProfiles(ctx context.Context, _ int, profileIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	selected := make(map[string]struct{}, len(profileIDs))
	for _, profileID := range profileIDs {
		selected[profileID] = struct{}{}
	}
	for token, profileID := range s.persistent {
		if _, ok := selected[profileID]; ok {
			delete(s.persistent, token)
		}
	}
	for token, profileID := range s.memory {
		if _, ok := selected[profileID]; ok {
			delete(s.memory, token)
		}
	}
	return nil
}

func adminUserResourceRequestWithContext(t *testing.T, router http.Handler, ctx context.Context, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAdminUserResources_ListsOnlyURLUserResources(t *testing.T) {
	handler, _, _ := newAdminUserResourceHandler(t)
	router := routeAdminUserResources(handler)

	for _, test := range []struct {
		path      string
		ownedID   string
		foreignID string
	}{
		{path: "/api/v1/admin/users/1/profiles", ownedID: "profile-a-primary", foreignID: "profile-b-primary"},
		{path: "/api/v1/admin/users/1/devices", ownedID: "device-a", foreignID: "device-b"},
		{path: "/api/v1/admin/users/1/auth-sessions", ownedID: "session-a", foreignID: "session-b"},
	} {
		recorder := adminUserResourceRequest(t, router, http.MethodGet, test.path, "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", test.path, recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if !strings.Contains(body, test.ownedID) || strings.Contains(body, test.foreignID) {
			t.Fatalf("GET %s body = %s, want %q and no %q", test.path, body, test.ownedID, test.foreignID)
		}
	}
}

func TestAdminUserResources_RejectsCrossAccountSubordinates(t *testing.T) {
	handler, stores, sessions := newAdminUserResourceHandler(t)
	router := routeAdminUserResources(handler)

	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPut, path: "/api/v1/admin/users/1/profiles/profile-b-secondary", body: `{"name":"stolen"}`},
		{method: http.MethodDelete, path: "/api/v1/admin/users/1/profiles/profile-b-secondary"},
		{method: http.MethodDelete, path: "/api/v1/admin/users/1/devices/device-b"},
		{method: http.MethodDelete, path: "/api/v1/admin/users/1/auth-sessions/session-b"},
	} {
		recorder := adminUserResourceRequest(t, router, test.method, test.path, test.body)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d: %s, want 404", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}

	profile, err := stores[2].GetProfile(context.Background(), "profile-b-secondary")
	if err != nil || profile == nil || profile.Name != "Kids B" {
		t.Fatalf("foreign profile changed: %+v, %v", profile, err)
	}
	deviceExists, err := stores[2].(userstore.DeviceRegistry).DeviceExists(context.Background(), "profile-b-secondary", "device-b")
	if err != nil || !deviceExists {
		t.Fatalf("foreign device exists = %v, %v", deviceExists, err)
	}
	if sessions.sessions["session-b"].RevokedAt != nil {
		t.Fatal("foreign session was revoked")
	}
}

func TestAdminUserProfiles_PreserveDomainRulesAndResponseSemantics(t *testing.T) {
	t.Run("create and update return resulting resources", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		router := routeAdminUserResources(handler)
		created := adminUserResourceRequest(t, router, http.MethodPost,
			"/api/v1/admin/users/1/profiles", `{"name":" Guest "}`)
		if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"name":"Guest"`) {
			t.Fatalf("create = %d: %s", created.Code, created.Body.String())
		}
		var body profileResponse
		if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode created profile: %v", err)
		}
		updated := adminUserResourceRequest(t, router, http.MethodPut,
			"/api/v1/admin/users/1/profiles/"+body.ID, `{"name":" Visitor "}`)
		if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"name":"Visitor"`) {
			t.Fatalf("update = %d: %s", updated.Code, updated.Body.String())
		}
	})

	t.Run("duplicate name is conflict", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodPost,
			"/api/v1/admin/users/1/profiles", `{"name":" main a "}`)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("profile quota is unprocessable", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		handler.userRepo = testAdminUserRepo{users: map[int]*models.User{
			1: {ID: 1, MaxProfiles: 2},
			2: {ID: 2, MaxProfiles: 4},
		}}
		recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodPost,
			"/api/v1/admin/users/1/profiles", `{"name":"Over quota"}`)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("managed group profile quota is unprocessable", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		organizationID := uuid.New()
		groupID := int64(91)
		handler.userRepo = testAdminUserRepo{users: map[int]*models.User{
			1: {ID: 1, MaxProfiles: 5},
			2: {ID: 2, MaxProfiles: 4},
		}}
		handler.AccessGroups = profileCapAccessGroups{group: &access.Group{ID: groupID, OrganizationID: organizationID, MaxProfiles: 2}}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/profiles", strings.NewReader(`{"name":"Over managed quota"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(withAdminResourceOrganization(request.Context(), organizationID))
		recorder := httptest.NewRecorder()
		routeAdminUserResources(handler).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("managed zero profile quota is unprocessable", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		organizationID := uuid.New()
		groupID := int64(92)
		key := "browse-only"
		handler.userRepo = testAdminUserRepo{users: map[int]*models.User{1: {ID: 1, MaxProfiles: 5}}}
		handler.AccessGroups = profileCapAccessGroups{group: &access.Group{ID: groupID, OrganizationID: organizationID, MaxProfiles: 0, ManagedTemplateKey: &key}}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/profiles", strings.NewReader(`{"name":"Over managed zero quota"}`))
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(withAdminResourceOrganization(request.Context(), organizationID))
		recorder := httptest.NewRecorder()
		routeAdminUserResources(handler).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("primary profile cannot be deleted", func(t *testing.T) {
		handler, _, _ := newAdminUserResourceHandler(t)
		recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodDelete,
			"/api/v1/admin/users/1/profiles/profile-a-primary", "")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestAdminUserProfiles_UpdateRemovesReplacedUploadedAvatar(t *testing.T) {
	handler, stores, _ := newAdminUserResourceHandler(t)
	avatar := "upload:profile-avatars/1/profile-a-secondary/original.webp"
	if err := stores[1].UpdateProfile(context.Background(), "profile-a-secondary", userstore.UpdateProfileInput{Avatar: &avatar}); err != nil {
		t.Fatalf("seed uploaded avatar: %v", err)
	}
	avatarStore := &adminUserResourceAvatarStore{keys: []string{
		"profile-avatars/1/profile-a-secondary/original.webp",
		"profile-avatars/1/profile-a-secondary/w256.webp",
	}}
	profileHandler := NewProfileHandler(handler.storeProv)
	profileHandler.AvatarStore = avatarStore
	handler.SetProfileHandler(profileHandler)

	recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodPut,
		"/api/v1/admin/users/1/profiles/profile-a-secondary", `{"avatar":"avatar-1"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(avatarStore.deleted) != 2 {
		t.Fatalf("deleted avatar objects = %v, want both uploaded variants", avatarStore.deleted)
	}
}

func TestAdminUserProfiles_DeleteRemovesUploadedAvatarAndPurgesProfileDevices(t *testing.T) {
	handler, stores, _ := newAdminUserResourceHandler(t)
	avatar := "upload:profile-avatars/1/profile-a-secondary/original.webp"
	if err := stores[1].UpdateProfile(context.Background(), "profile-a-secondary", userstore.UpdateProfileInput{Avatar: &avatar}); err != nil {
		t.Fatalf("seed uploaded avatar: %v", err)
	}
	avatarStore := &adminUserResourceAvatarStore{keys: []string{
		"profile-avatars/1/profile-a-secondary/original.webp",
		"profile-avatars/1/profile-a-secondary/w256.webp",
	}}
	purger := &adminUserResourceProfilePurger{}
	profileHandler := NewProfileHandler(handler.storeProv)
	profileHandler.AvatarStore = avatarStore
	profileHandler.DeviceLibraryPurger = purger
	handler.SetProfileHandler(profileHandler)

	recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodDelete,
		"/api/v1/admin/users/1/profiles/profile-a-secondary", "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if len(avatarStore.deleted) != 2 {
		t.Fatalf("deleted avatar objects = %v, want both uploaded variants", avatarStore.deleted)
	}
	if len(purger.calls) != 1 || purger.calls[0].userID != 1 || purger.calls[0].profileID != "profile-a-secondary" {
		t.Fatalf("purge calls = %+v, want selected user and profile", purger.calls)
	}
}

func TestAdminUserProfiles_CleanupFailuresFollowNativeMutationSemantics(t *testing.T) {
	t.Run("update commits even when avatar cleanup fails", func(t *testing.T) {
		handler, stores, _ := newAdminUserResourceHandler(t)
		avatar := "upload:profile-avatars/1/profile-a-secondary/original.webp"
		if err := stores[1].UpdateProfile(context.Background(), "profile-a-secondary", userstore.UpdateProfileInput{Avatar: &avatar}); err != nil {
			t.Fatalf("seed uploaded avatar: %v", err)
		}
		profileHandler := NewProfileHandler(handler.storeProv)
		profileHandler.AvatarStore = &adminUserResourceAvatarStore{listErr: errors.New("storage unavailable")}
		handler.SetProfileHandler(profileHandler)

		recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodPut,
			"/api/v1/admin/users/1/profiles/profile-a-secondary", `{"avatar":"avatar-1"}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		profile, err := stores[1].GetProfile(context.Background(), "profile-a-secondary")
		if err != nil || profile == nil || profile.Avatar != "preset:avatar-1" {
			t.Fatalf("updated profile = %+v, %v", profile, err)
		}
	})

	t.Run("delete commits even when cleanup and purge fail", func(t *testing.T) {
		handler, stores, _ := newAdminUserResourceHandler(t)
		avatar := "upload:profile-avatars/1/profile-a-secondary/original.webp"
		if err := stores[1].UpdateProfile(context.Background(), "profile-a-secondary", userstore.UpdateProfileInput{Avatar: &avatar}); err != nil {
			t.Fatalf("seed uploaded avatar: %v", err)
		}
		profileHandler := NewProfileHandler(handler.storeProv)
		profileHandler.AvatarStore = &adminUserResourceAvatarStore{listErr: errors.New("storage unavailable")}
		profileHandler.DeviceLibraryPurger = &adminUserResourceProfilePurger{err: errors.New("database unavailable")}
		handler.SetProfileHandler(profileHandler)

		recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodDelete,
			"/api/v1/admin/users/1/profiles/profile-a-secondary", "")
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
		profile, err := stores[1].GetProfile(context.Background(), "profile-a-secondary")
		if err != nil || profile != nil {
			t.Fatalf("profile after delete = %+v, %v; want deleted", profile, err)
		}
	})
}

func TestAdminUserResources_UnknownDeviceAndSessionDeletesAreIdempotent(t *testing.T) {
	handler, _, _ := newAdminUserResourceHandler(t)
	router := routeAdminUserResources(handler)
	for _, path := range []string{
		"/api/v1/admin/users/1/devices/never-seen",
		"/api/v1/admin/users/1/auth-sessions/never-issued",
	} {
		recorder := adminUserResourceRequest(t, router, http.MethodDelete, path, "")
		if recorder.Code != http.StatusNoContent {
			t.Errorf("DELETE %s = %d: %s, want 204", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAdminUserResources_OwnedDeviceAndSessionDeletesReturnNoContent(t *testing.T) {
	handler, stores, sessions := newAdminUserResourceHandler(t)
	router := routeAdminUserResources(handler)

	deviceDelete := adminUserResourceRequest(t, router, http.MethodDelete,
		"/api/v1/admin/users/1/devices/device-a", "")
	if deviceDelete.Code != http.StatusNoContent {
		t.Fatalf("device delete = %d: %s", deviceDelete.Code, deviceDelete.Body.String())
	}
	exists, err := stores[1].(userstore.DeviceRegistry).DeviceExists(
		context.Background(), "profile-a-secondary", "device-a",
	)
	if err != nil || exists {
		t.Fatalf("owned device exists = %v, %v; want removed", exists, err)
	}

	sessionDelete := adminUserResourceRequest(t, router, http.MethodDelete,
		"/api/v1/admin/users/1/auth-sessions/session-a", "")
	if sessionDelete.Code != http.StatusNoContent {
		t.Fatalf("session delete = %d: %s", sessionDelete.Code, sessionDelete.Body.String())
	}
	if sessions.sessions["session-a"].RevokedAt == nil {
		t.Fatal("owned session remains active")
	}
}

func TestAdminUserAuthSessions_RevokeAllOnlyTouchesSelectedUser(t *testing.T) {
	handler, _, sessions := newAdminUserResourceHandler(t)
	recorder := adminUserResourceRequest(t, routeAdminUserResources(handler), http.MethodDelete,
		"/api/v1/admin/users/1/auth-sessions", "")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if sessions.sessions["session-a"].RevokedAt == nil {
		t.Fatal("selected user's session remains active")
	}
	if sessions.sessions["session-b"].RevokedAt != nil {
		t.Fatal("another user's session was revoked")
	}
}

func TestAdminUserAuthSessions_CompatInvalidationSurvivesRequestCancellation(t *testing.T) {
	tenantID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	for _, test := range []struct {
		name   string
		path   string
		scoped bool
	}{
		{name: "global single", path: "/api/v1/admin/users/1/auth-sessions/session-a"},
		{name: "global all", path: "/api/v1/admin/users/1/auth-sessions"},
		{name: "profile scoped single", path: "/api/v1/admin/users/1/auth-sessions/session-a", scoped: true},
		{name: "profile scoped all", path: "/api/v1/admin/users/1/auth-sessions", scoped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, stores, sessions := newAdminUserResourceHandler(t)
			state := newCancellationCompatState()
			handler.OnUserSessionsRevoked = state.invalidateUser
			handler.OnUserProfileSessionsRevoked = state.invalidateProfiles
			ctx, cancel := context.WithCancel(context.Background())
			sessions.cancelAfterRevoke = cancel
			if test.scoped {
				stores[1] = &adminOrganizationUserStore{UserStore: stores[1], organizationID: tenantID.String()}
				ctx = withAdminResourceOrganization(ctx, tenantID)
			}

			recorder := adminUserResourceRequestWithContext(t, routeAdminUserResources(handler), ctx, http.MethodDelete, test.path)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d: %s, want 204", recorder.Code, recorder.Body.String())
			}
			if test.scoped {
				if _, ok := state.persistent["profile-a"]; ok {
					t.Fatal("profile-scoped durable compat session remains")
				}
				if _, ok := state.memory["profile-a"]; ok {
					t.Fatal("profile-scoped in-memory compat session remains")
				}
				for _, token := range []string{"profile-b", "account"} {
					if _, ok := state.persistent[token]; !ok {
						t.Fatalf("unrelated durable compat session %q was removed", token)
					}
					if _, ok := state.memory[token]; !ok {
						t.Fatalf("unrelated in-memory compat session %q was removed", token)
					}
				}
			} else if len(state.persistent) != 0 || len(state.memory) != 0 {
				t.Fatalf("global compat sessions remain: persistent=%v memory=%v", state.persistent, state.memory)
			}
		})
	}
}

func TestAdminUserAuthSessions_ProfileScopedRevokeAllIsAtomicAcrossRequestCancellation(t *testing.T) {
	tenantID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	handler, stores, sessions := newAdminUserResourceHandler(t)
	stores[1] = &adminOrganizationUserStore{UserStore: stores[1], organizationID: tenantID.String()}
	profileA := "profile-a-secondary"
	profileB := "profile-other-tenant"
	sessions.sessions = map[string]*models.AuthSession{
		"tenant-a-1": {ID: "tenant-a-1", UserID: 1, ProfileID: &profileA, ExpiresAt: time.Now().Add(time.Hour)},
		"tenant-a-2": {ID: "tenant-a-2", UserID: 1, ProfileID: &profileA, ExpiresAt: time.Now().Add(time.Hour)},
		"tenant-b":   {ID: "tenant-b", UserID: 1, ProfileID: &profileB, ExpiresAt: time.Now().Add(time.Hour)},
		"account":    {ID: "account", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)},
	}
	state := newCancellationCompatState()
	handler.OnUserProfileSessionsRevoked = state.invalidateProfiles
	requestCtx, cancelRequest := context.WithCancel(withAdminResourceOrganization(context.Background(), tenantID))
	sessions.cancelAfterRevoke = cancelRequest

	recorder := adminUserResourceRequestWithContext(t, routeAdminUserResources(handler), requestCtx, http.MethodDelete,
		"/api/v1/admin/users/1/auth-sessions")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s, want 204", recorder.Code, recorder.Body.String())
	}
	for _, sessionID := range []string{"tenant-a-1", "tenant-a-2"} {
		if sessions.sessions[sessionID].RevokedAt == nil {
			t.Fatalf("in-scope native session %q remains active", sessionID)
		}
	}
	for _, sessionID := range []string{"tenant-b", "account"} {
		if sessions.sessions[sessionID].RevokedAt != nil {
			t.Fatalf("out-of-scope native session %q was revoked", sessionID)
		}
	}
	if _, ok := state.persistent["profile-a"]; ok {
		t.Fatal("in-scope durable compat session remains")
	}
	if _, ok := state.memory["profile-a"]; ok {
		t.Fatal("in-scope memory compat session remains")
	}
	for _, token := range []string{"profile-b", "account"} {
		if _, ok := state.persistent[token]; !ok {
			t.Fatalf("out-of-scope durable compat session %q was removed", token)
		}
		if _, ok := state.memory[token]; !ok {
			t.Fatalf("out-of-scope memory compat session %q was removed", token)
		}
	}
	if sessions.scopedAtomicRevokeCalls != 1 || sessions.perSessionRevokeCalls != 0 {
		t.Fatalf("scoped revoke calls: atomic=%d per-session=%d, want one atomic boundary", sessions.scopedAtomicRevokeCalls, sessions.perSessionRevokeCalls)
	}
}

func TestAdminUserSecurityMutations_CompatInvalidationSurvivesRequestCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "update", method: http.MethodPut, path: "/api/v1/admin/users/1", body: `{"password":"new-password"}`, want: http.StatusOK},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/admin/users/1", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			repo := &cancellationAdminUserRepo{user: &models.User{ID: 1, Username: "account-a", Enabled: true}, cancel: cancel}
			handler := NewAdminHandler(repo, nil, nil)
			handler.sessionRepo = &adminUserResourceSessions{sessions: map[string]*models.AuthSession{
				"session-a": {ID: "session-a", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)},
			}}
			state := newCancellationCompatState()
			handler.OnUserSessionsRevoked = state.invalidateUser
			router := chi.NewRouter()
			router.Put("/api/v1/admin/users/{id}", handler.HandleUpdateUser)
			router.Delete("/api/v1/admin/users/{id}", handler.HandleDeleteUser)

			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d: %s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
			if len(state.persistent) != 0 || len(state.memory) != 0 {
				t.Fatalf("compat sessions remain: persistent=%v memory=%v", state.persistent, state.memory)
			}
		})
	}
}

func TestAdminUserResources_InvalidUserAndMissingUserAreSafe(t *testing.T) {
	handler, _, _ := newAdminUserResourceHandler(t)
	router := routeAdminUserResources(handler)
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/v1/admin/users/not-a-number/profiles", want: http.StatusBadRequest},
		{path: "/api/v1/admin/users/999/profiles", want: http.StatusNotFound},
	} {
		recorder := adminUserResourceRequest(t, router, http.MethodGet, test.path, "")
		if recorder.Code != test.want {
			t.Errorf("GET %s = %d: %s, want %d", test.path, recorder.Code, recorder.Body.String(), test.want)
		}
	}
}

var _ adminUserSessionRepository = (*adminUserResourceSessions)(nil)
