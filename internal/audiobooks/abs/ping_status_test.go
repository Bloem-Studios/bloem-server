package abs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatcontract"
	"github.com/Silo-Server/silo-server/internal/models"
)

const (
	ordinaryProfileID = "ordinary-profile-001"
	adultProfileID    = "adult-profile-002"
	ordinaryItemID    = "ordinary-item-001"
	adultItemID       = "adult-item-001"
)

type adultPolicyMediaStore struct{ noopMediaStore }

func (adultPolicyMediaStore) allowed(filter catalog.AccessFilter, libraryID int) bool {
	if filter.AllowedLibraryIDs == nil {
		return false
	}
	for _, id := range filter.AllowedLibraryIDs {
		if id == libraryID {
			return true
		}
	}
	return false
}

func (s adultPolicyMediaStore) ListAudiobookLibraries(_ context.Context, filter catalog.AccessFilter) ([]AudiobookLibrary, error) {
	libs := []AudiobookLibrary{{ID: 1, Name: "Ordinary", Type: "audiobooks"}, {ID: 2, Name: "Adult", Type: "audiobooks"}}
	out := make([]AudiobookLibrary, 0, len(libs))
	for _, lib := range libs {
		if s.allowed(filter, int(lib.ID)) {
			out = append(out, lib)
		}
	}
	return out, nil
}

func (s adultPolicyMediaStore) ListAudiobooks(_ context.Context, libraryID int64, _, _ int, filter catalog.AccessFilter, _ Filter) ([]*models.MediaItem, int, error) {
	if !s.allowed(filter, int(libraryID)) {
		return []*models.MediaItem{}, 0, nil
	}
	item := &models.MediaItem{ContentID: ordinaryItemID, Type: "audiobook", Title: "Ordinary audiobook", PosterPath: "https://images.example/ordinary.webp"}
	if libraryID == 2 {
		item = &models.MediaItem{ContentID: adultItemID, Type: "audiobook", Title: "Adult audiobook", PosterPath: "https://images.example/adult.webp"}
	}
	return []*models.MediaItem{item}, 1, nil
}

func (s adultPolicyMediaStore) GetAudiobookByID(_ context.Context, id string, filter catalog.AccessFilter) (*models.MediaItem, error) {
	libraryID := 1
	item := &models.MediaItem{ContentID: ordinaryItemID, Type: "audiobook", Title: "Ordinary audiobook", PosterPath: "https://images.example/ordinary.webp"}
	if id == adultItemID {
		libraryID = 2
		item = &models.MediaItem{ContentID: adultItemID, Type: "audiobook", Title: "Adult audiobook", PosterPath: "https://images.example/adult.webp"}
	}
	if id != ordinaryItemID && id != adultItemID || !s.allowed(filter, libraryID) {
		return nil, errors.New("not found")
	}
	return item, nil
}

type adultAccessResolver struct{}

func (adultAccessResolver) ResolveABSAccess(_ context.Context, _ string, profileID string) (catalog.AccessFilter, error) {
	switch profileID {
	case ordinaryProfileID:
		return catalog.AccessFilter{AllowedLibraryIDs: []int{1}}, nil
	case adultProfileID:
		return catalog.AccessFilter{AllowedLibraryIDs: []int{1, 2}}, nil
	default:
		return catalog.AccessFilter{AllowedLibraryIDs: []int{}}, nil
	}
}

type fixturePublisher struct{ payloads []string }

func (p *fixturePublisher) Publish(_ string, _ string, payload any) {
	data, _ := json.Marshal(payload)
	p.payloads = append(p.payloads, string(data))
}
func (p *fixturePublisher) Broadcast(_ string, payload any) {
	data, _ := json.Marshal(payload)
	p.payloads = append(p.payloads, string(data))
}

type fixtureProgressStore struct{}

func (fixtureProgressStore) GetProgress(context.Context, string, string, string) (*ProgressRow, error) {
	return nil, nil
}
func (fixtureProgressStore) ListProgressForAudiobooks(context.Context, string, string, int) ([]ProgressRow, error) {
	return []ProgressRow{}, nil
}
func (fixtureProgressStore) UpsertProgress(context.Context, ProgressRow) error { return nil }
func (fixtureProgressStore) UpdateProgressPosition(context.Context, string, string, string, float64) error {
	return nil
}
func (fixtureProgressStore) SetHideFromContinue(context.Context, string, string, string, bool) error {
	return nil
}
func (fixtureProgressStore) DeleteProgress(context.Context, string, string, string) error { return nil }

func adultAccessToken(t *testing.T, store *memTokenStore, cfg *staticConfig, profileID string) string {
	t.Helper()
	jti := "fixture-" + profileID
	token, err := IssueAccessToken(cfg.secret, "fixture-user", profileID, jti, time.Hour)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if err := store.InsertToken(context.Background(), ABSToken{ID: jti, UserID: "fixture-user", ProfileID: profileID, JTI: jti, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("InsertToken: %v", err)
	}
	return token
}

func TestEmbeddedAudiobookshelfAdultPolicyContract(t *testing.T) {
	store := newMemTokenStore()
	cfg := &staticConfig{secret: []byte("test-secret-32-bytes-aaaaaaaaaaaaa")}
	publisher := &fixturePublisher{}
	h := New(Dependencies{Config: cfg, TokenStore: store, MediaStore: adultPolicyMediaStore{}, AccessResolver: adultAccessResolver{}, Publisher: publisher, ProgressStore: fixtureProgressStore{}})
	server := httptest.NewServer(h.Router())
	defer server.Close()
	ordinary := adultAccessToken(t, store, cfg, ordinaryProfileID)
	adult := adultAccessToken(t, store, cfg, adultProfileID)

	run := func(name, token string, suite compatcontract.Suite) {
		t.Helper()
		report, err := compatcontract.Run(context.Background(), compatcontract.Target{BaseURL: server.URL, Client: server.Client(), Credentials: compatcontract.CredentialFunc(func(req *http.Request) error { req.Header.Set("Authorization", "Bearer "+token); return nil })}, suite)
		if err != nil {
			t.Fatalf("%s: %v; report=%s", name, err, report.JSON())
		}
	}
	run("ordinary", ordinary, compatcontract.AudiobookshelfOrdinaryAdultPolicy())
	if len(publisher.payloads) != 0 {
		t.Fatalf("ordinary profile activity leaked: %v", publisher.payloads)
	}
	run("adult", adult, compatcontract.AudiobookshelfAuthorizedAdultPolicy())
}

func TestEmbeddedAudiobookshelfCompatibilityContract(t *testing.T) {
	h := New(Dependencies{
		Config:        &staticConfig{secret: []byte("test-secret-32-bytes-aaaaaaaaaaaaa")},
		TokenStore:    newMemTokenStore(),
		CredValidator: &recordingValidator{},
		MediaStore:    noopMediaStore{},
	})
	server := httptest.NewServer(h.Router())
	defer server.Close()

	report, err := compatcontract.Run(context.Background(), compatcontract.Target{
		BaseURL: server.URL,
		Client:  server.Client(),
	}, compatcontract.AudiobookshelfBaseline())
	if err != nil {
		t.Fatalf("embedded Audiobookshelf compatibility contract: %v; report=%s", err, report.JSON())
	}
}

// TestABSPing_HasSuccess guards the reachability probe: real ABS /ping returns
// {"success": true} and the ABS apps validate a server address by reading that
// field. Without it the app reports "unable to reach".
func TestABSPing_HasSuccess(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleABSPing(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["success"] != true {
		t.Errorf("/ping success = %v, want true", m["success"])
	}
}

// TestABSStatus_HasAuthMethods guards that /status carries authMethods (drives
// the login form) — real ABS Server.js /status shape.
func TestABSStatus_HasAuthMethods(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleABSStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"app", "serverVersion", "isInit", "language", "authMethods", "authFormData"} {
		if _, ok := m[k]; !ok {
			t.Errorf("/status missing key %q", k)
		}
	}
	if methods, ok := m["authMethods"].([]any); !ok || len(methods) == 0 {
		t.Errorf("authMethods = %v, want non-empty array", m["authMethods"])
	}
}
