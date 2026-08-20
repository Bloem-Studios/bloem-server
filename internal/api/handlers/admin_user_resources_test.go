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

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

type adminUserResourceSessions struct {
	sessions map[string]*models.AuthSession
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

func (s *adminUserResourceSessions) RevokeByUserAndSession(_ context.Context, userID int, sessionID string) error {
	session, ok := s.sessions[sessionID]
	if !ok || session.UserID != userID {
		return auth.ErrSessionNotFound
	}
	now := time.Now()
	session.RevokedAt = &now
	return nil
}

func (s *adminUserResourceSessions) RevokeAllByUser(_ context.Context, userID int) error {
	now := time.Now()
	for _, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
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
	sessions := &adminUserResourceSessions{sessions: map[string]*models.AuthSession{
		"session-a": {ID: "session-a", UserID: 1, DeviceName: "A browser", ExpiresAt: time.Now().Add(time.Hour)},
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
