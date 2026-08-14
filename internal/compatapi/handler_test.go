package compatapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// --- fakes -----------------------------------------------------------------

var errFakeUnknownBearer = errors.New("unknown bearer")

type fakeApps struct {
	mu       sync.Mutex
	byBearer map[string]AppIdentity
}

func (f *fakeApps) Authenticate(_ context.Context, bearer string, _ *tls.ConnectionState) (AppIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byBearer[bearer]
	if !ok {
		return AppIdentity{}, errFakeUnknownBearer
	}
	return id, nil
}

type fakeEnroller struct {
	mu         sync.Mutex
	calls      int
	credential ServiceCredential
	err        error
	lastSecret string
	lastReq    EnrollmentRequest
}

func (f *fakeEnroller) Enroll(_ context.Context, secret string, req EnrollmentRequest) (ServiceCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastSecret = secret
	f.lastReq = req
	if f.err != nil {
		return ServiceCredential{}, f.err
	}
	return f.credential, nil
}

type fakeRenewer struct {
	credential ServiceCredential
	err        error
}

func (f *fakeRenewer) Renew(_ context.Context, _ string) (ServiceCredential, error) {
	if f.err != nil {
		return ServiceCredential{}, f.err
	}
	return f.credential, nil
}

type fakeHeartbeats struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeHeartbeats) Heartbeat(_ context.Context, applicationID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, applicationID)
	return nil
}

type fakeSubjects struct {
	mu           sync.Mutex
	loginDirect  func(email, password string, device DeviceClaim) (Subject, error)
	loginAccount func(username, password string, device DeviceClaim) (Subject, error)
	current      func(subject Subject) (Subject, error)
	profiles     []ProfileTile
	verifyPIN    func(subject Subject, profileID, pin string) error
	switchTo     func(subject Subject, profileID, pin string) (Subject, error)
	logoutCalls  int
}

func (f *fakeSubjects) LoginDirect(_ context.Context, email, password string, device DeviceClaim) (Subject, error) {
	if f.loginDirect == nil {
		return Subject{}, ErrInvalidCredentials
	}
	return f.loginDirect(email, password, device)
}

func (f *fakeSubjects) LoginAccount(_ context.Context, username, password string, device DeviceClaim) (Subject, error) {
	if f.loginAccount == nil {
		return Subject{}, ErrInvalidCredentials
	}
	return f.loginAccount(username, password, device)
}

func (f *fakeSubjects) CurrentSubject(_ context.Context, subject Subject) (Subject, error) {
	if f.current == nil {
		return subject, nil
	}
	return f.current(subject)
}

func (f *fakeSubjects) DeviceProfiles(_ context.Context, _ Subject) ([]ProfileTile, error) {
	return f.profiles, nil
}

func (f *fakeSubjects) VerifyPIN(_ context.Context, subject Subject, profileID, pin string) error {
	if f.verifyPIN == nil {
		return ErrPINInvalid
	}
	return f.verifyPIN(subject, profileID, pin)
}

func (f *fakeSubjects) SwitchProfile(_ context.Context, subject Subject, profileID, pin string) (Subject, error) {
	if f.switchTo == nil {
		return Subject{}, ErrPINInvalid
	}
	return f.switchTo(subject, profileID, pin)
}

func (f *fakeSubjects) Logout(_ context.Context, _ Subject) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logoutCalls++
	return nil
}

type fakeCatalog struct {
	mu         sync.Mutex
	libraries  []Library
	page       ItemPage
	itemsCalls []ItemQuery
	item       Item
	itemErr    error
	person     Person
	grant      func(subject Subject, audience, itemID, kind string) (DeliveryGrant, error)
}

func (f *fakeCatalog) Libraries(_ context.Context, _ Subject) ([]Library, error) {
	return f.libraries, nil
}

func (f *fakeCatalog) Items(_ context.Context, _ Subject, q ItemQuery) (ItemPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemsCalls = append(f.itemsCalls, q)
	return f.page, nil
}

func (f *fakeCatalog) Item(_ context.Context, _ Subject, _ string) (Item, error) {
	if f.itemErr != nil {
		return Item{}, f.itemErr
	}
	return f.item, nil
}

func (f *fakeCatalog) Children(_ context.Context, _ Subject, _ string, q ItemQuery) (ItemPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemsCalls = append(f.itemsCalls, q)
	return f.page, nil
}

func (f *fakeCatalog) Search(_ context.Context, _ Subject, q SearchQuery) (ItemPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemsCalls = append(f.itemsCalls, ItemQuery{Cursor: q.Cursor, Limit: q.Limit})
	return f.page, nil
}

func (f *fakeCatalog) Person(_ context.Context, _ Subject, _ string) (Person, error) {
	return f.person, nil
}

func (f *fakeCatalog) ArtworkGrant(_ context.Context, subject Subject, audience, itemID, kind string) (DeliveryGrant, error) {
	if f.grant == nil {
		return DeliveryGrant{}, ErrNotFound
	}
	return f.grant(subject, audience, itemID, kind)
}

type fakeState struct {
	mu            sync.Mutex
	progress      Progress
	setCalls      int
	lastUpdate    ProgressUpdate
	favoritesPage ItemPage
	bookmarks     []Bookmark
	collections   CollectionPage
	collection    Collection
	playlists     PlaylistPage
	downloads     []Download
	download      Download
}

func (f *fakeState) Progress(_ context.Context, _ Subject, _ string) (Progress, error) {
	return f.progress, nil
}

func (f *fakeState) SetProgress(_ context.Context, _ Subject, itemID string, p ProgressUpdate) (Progress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.lastUpdate = p
	out := f.progress
	out.ItemID = itemID
	out.PositionSeconds = p.PositionSeconds
	return out, nil
}

func (f *fakeState) SetWatched(_ context.Context, _ Subject, _ string, _ bool) error { return nil }

func (f *fakeState) Favorites(_ context.Context, _ Subject, cursor string, limit int) (ItemPage, error) {
	return f.favoritesPage, nil
}

func (f *fakeState) SetFavorite(_ context.Context, _ Subject, _ string, _ bool) error { return nil }

func (f *fakeState) Bookmarks(_ context.Context, _ Subject, _ string) ([]Bookmark, error) {
	return f.bookmarks, nil
}

func (f *fakeState) CreateBookmark(_ context.Context, _ Subject, b BookmarkCreate) (Bookmark, error) {
	return Bookmark{ID: "bm1", ItemID: b.ItemID, PositionSeconds: b.PositionSeconds, Title: b.Title}, nil
}

func (f *fakeState) DeleteBookmark(_ context.Context, _ Subject, _ string) error { return nil }

func (f *fakeState) Collections(_ context.Context, _ Subject, cursor string, limit int) (CollectionPage, error) {
	return f.collections, nil
}

func (f *fakeState) Collection(_ context.Context, _ Subject, _ string) (Collection, error) {
	return f.collection, nil
}

func (f *fakeState) Playlists(_ context.Context, _ Subject, cursor string, limit int) (PlaylistPage, error) {
	return f.playlists, nil
}

func (f *fakeState) CreatePlaylist(_ context.Context, _ Subject, p PlaylistWrite) (Playlist, error) {
	return Playlist{ID: "pl1", Name: p.Name, ItemIDs: p.ItemIDs}, nil
}

func (f *fakeState) UpdatePlaylist(_ context.Context, _ Subject, id string, p PlaylistWrite) (Playlist, error) {
	return Playlist{ID: id, Name: p.Name, ItemIDs: p.ItemIDs}, nil
}

func (f *fakeState) DeletePlaylist(_ context.Context, _ Subject, _ string) error { return nil }

func (f *fakeState) Downloads(_ context.Context, _ Subject) ([]Download, error) {
	return f.downloads, nil
}

func (f *fakeState) CreateDownload(_ context.Context, _ Subject, itemID string) (Download, error) {
	out := f.download
	out.ItemID = itemID
	return out, nil
}

func (f *fakeState) DeleteDownload(_ context.Context, _ Subject, _ string) error { return nil }

type fakePlayback struct {
	plan     Plan
	start    func(subject Subject, audience string, req PlaybackStart) (PlaybackSession, error)
	session  PlaybackSession
	sessions []PlaybackSession
	stopErr  error
}

func (f *fakePlayback) Plan(_ context.Context, _ Subject, _ PlaybackPlanRequest) (Plan, error) {
	return f.plan, nil
}

func (f *fakePlayback) Start(_ context.Context, subject Subject, audience string, req PlaybackStart) (PlaybackSession, error) {
	if f.start == nil {
		return f.session, nil
	}
	return f.start(subject, audience, req)
}

func (f *fakePlayback) Session(_ context.Context, _ Subject, _ string) (PlaybackSession, error) {
	return f.session, nil
}

func (f *fakePlayback) Sessions(_ context.Context, _ Subject) ([]PlaybackSession, error) {
	return f.sessions, nil
}

func (f *fakePlayback) Progress(_ context.Context, _ Subject, _ string, _ PlaybackProgress) error {
	return nil
}

func (f *fakePlayback) Stop(_ context.Context, _ Subject, _ string) error { return f.stopErr }

type fakeLiveTV struct {
	channels   ChannelPage
	guide      GuidePage
	tuners     []Tuner
	grant      func(subject Subject, audience, channelID string) (DeliveryGrant, error)
	rules      []DVRRule
	rule       DVRRule
	recordings RecordingPage
	recording  Recording
}

func (f *fakeLiveTV) Channels(_ context.Context, _ Subject, cursor string, limit int) (ChannelPage, error) {
	return f.channels, nil
}

func (f *fakeLiveTV) Guide(_ context.Context, _ Subject, _ GuideQuery) (GuidePage, error) {
	return f.guide, nil
}

func (f *fakeLiveTV) Tuners(_ context.Context, _ Subject) ([]Tuner, error) { return f.tuners, nil }

func (f *fakeLiveTV) AuthorizeStream(_ context.Context, subject Subject, audience, channelID string) (DeliveryGrant, error) {
	if f.grant == nil {
		return DeliveryGrant{}, ErrUnavailable
	}
	return f.grant(subject, audience, channelID)
}

func (f *fakeLiveTV) DVRRules(_ context.Context, _ Subject) ([]DVRRule, error) { return f.rules, nil }

func (f *fakeLiveTV) CreateDVRRule(_ context.Context, _ Subject, _ DVRRuleCreate) (DVRRule, error) {
	return f.rule, nil
}

func (f *fakeLiveTV) DeleteDVRRule(_ context.Context, _ Subject, _ string) error { return nil }

func (f *fakeLiveTV) Recordings(_ context.Context, _ Subject, cursor string, limit int) (RecordingPage, error) {
	return f.recordings, nil
}

func (f *fakeLiveTV) Recording(_ context.Context, _ Subject, _ string) (Recording, error) {
	return f.recording, nil
}

func (f *fakeLiveTV) DeleteRecording(_ context.Context, _ Subject, _ string) error { return nil }

type fakeEvents struct {
	mu         sync.Mutex
	batch      EventBatch
	lastCursor string
	err        error
}

func (f *fakeEvents) Events(_ context.Context, _ Subject, cursor string, limit int) (EventBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCursor = cursor
	if f.err != nil {
		return EventBatch{}, f.err
	}
	return f.batch, nil
}

type fakeLimiter struct {
	mu     sync.Mutex
	allow  bool
	denied []string
}

func (f *fakeLimiter) Allow(_ context.Context, _ AppIdentity, operationID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.allow {
		f.denied = append(f.denied, operationID)
	}
	return f.allow
}

// --- fixture ---------------------------------------------------------------

const (
	bearerFull    = "svc-full"
	bearerCatalog = "svc-catalog-only"
	bearerOther   = "svc-other-app"
)

func testSubject() Subject {
	return Subject{
		AccountID:          7,
		ProfileID:          "prof-1",
		OrganizationID:     "org-1",
		MembershipID:       "mem-1",
		PolicyRevision:     3,
		SecurityRevision:   4,
		CredentialRevision: 5,
		Device:             DeviceClaim{ID: "dev-1", Name: "Living Room", Platform: "jellyfin-web", IPAddress: "203.0.113.9"},
		AuthMethod:         "direct_profile",
	}
}

type fixture struct {
	handler    *Handler
	apps       *fakeApps
	enroller   *fakeEnroller
	renewer    *fakeRenewer
	heartbeats *fakeHeartbeats
	subjects   *fakeSubjects
	catalog    *fakeCatalog
	state      *fakeState
	playback   *fakePlayback
	livetv     *fakeLiveTV
	events     *fakeEvents
	now        time.Time
	nowMu      sync.Mutex
}

func (f *fixture) advance(d time.Duration) {
	f.nowMu.Lock()
	defer f.nowMu.Unlock()
	f.now = f.now.Add(d)
}

func newFixture(t *testing.T, mutate func(*Services)) *fixture {
	t.Helper()
	f := &fixture{
		apps: &fakeApps{byBearer: map[string]AppIdentity{
			bearerFull: {
				ApplicationID: "app-full",
				Kind:          "jellyfin",
				InstanceID:    "inst-full",
				Capabilities:  KnownCapabilities(),
			},
			bearerCatalog: {
				ApplicationID: "app-catalog",
				Kind:          "audiobookshelf",
				InstanceID:    "inst-catalog",
				Capabilities:  []string{CapabilityService, CapabilityCatalog},
			},
			bearerOther: {
				ApplicationID: "app-other",
				Kind:          "audiobookshelf",
				InstanceID:    "inst-other",
				Capabilities:  KnownCapabilities(),
			},
		}},
		enroller: &fakeEnroller{credential: ServiceCredential{
			ApplicationID:       "app-new",
			Token:               "svc-new-token",
			ExpiresAt:           time.Date(2026, 8, 14, 12, 15, 0, 0, time.UTC),
			GrantedCapabilities: []string{CapabilityService, CapabilityCatalog},
		}},
		renewer: &fakeRenewer{credential: ServiceCredential{
			ApplicationID: "app-full",
			Token:         "svc-renewed",
			ExpiresAt:     time.Date(2026, 8, 14, 12, 15, 0, 0, time.UTC),
		}},
		heartbeats: &fakeHeartbeats{},
		subjects:   &fakeSubjects{},
		catalog:    &fakeCatalog{},
		state:      &fakeState{},
		playback:   &fakePlayback{},
		livetv:     &fakeLiveTV{},
		events:     &fakeEvents{},
		now:        time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	}
	f.subjects.loginDirect = func(email, password string, device DeviceClaim) (Subject, error) {
		if email == "reader@example.test" && password == "correct horse" {
			s := testSubject()
			s.Device = device
			return s, nil
		}
		return Subject{}, ErrInvalidCredentials
	}
	services := Services{
		Apps:       f.apps,
		Enroller:   f.enroller,
		Renewer:    f.renewer,
		Heartbeats: f.heartbeats,
		Subjects:   f.subjects,
		Catalog:    f.catalog,
		State:      f.state,
		Playback:   f.playback,
		LiveTV:     f.livetv,
		Events:     f.events,
	}
	if mutate != nil {
		mutate(&services)
	}
	handler, err := New(Config{
		SubjectTokenKey: bytes.Repeat([]byte{0xA1}, 32),
		CursorKey:       bytes.Repeat([]byte{0xB2}, 32),
		Version:         "test-build",
		APIRange:        APIRange{Min: 1, Max: 1},
		Now: func() time.Time {
			f.nowMu.Lock()
			defer f.nowMu.Unlock()
			return f.now
		},
	}, services)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.handler = handler
	return f
}

type reqOpt func(*http.Request)

func withBearer(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func withSubjectToken(token string) reqOpt {
	return func(r *http.Request) { r.Header.Set("X-Vondel-Subject-Token", token) }
}

func withIdempotencyKey(key string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Idempotency-Key", key) }
}

func withHeader(name, value string) reqOpt {
	return func(r *http.Request) { r.Header.Set(name, value) }
}

func (f *fixture) do(t *testing.T, method, target string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(b))
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, opt := range opts {
		opt(req)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		TraceID string `json:"trace_id"`
	} `json:"error"`
}

func requireErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) errorBody {
	t.Helper()
	requireStatus(t, rec, status)
	var body errorBody
	decodeJSON(t, rec, &body)
	if body.Error.Code != code {
		t.Fatalf("error code = %q, want %q; body: %s", body.Error.Code, code, rec.Body.String())
	}
	if body.Error.TraceID == "" {
		t.Fatal("error envelope must carry a trace id")
	}
	return body
}

func (f *fixture) login(t *testing.T, bearer string) string {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/identity/login", map[string]any{
		"grant_type": "direct_profile",
		"email":      "reader@example.test",
		"password":   "correct horse",
		"device":     map[string]any{"id": "dev-1", "name": "Living Room", "platform": "jellyfin-web", "ip_address": "203.0.113.9"},
	}, withBearer(bearer), withIdempotencyKey("login-"+bearer))
	requireStatus(t, rec, http.StatusOK)
	var body struct {
		SubjectToken string `json:"subject_token"`
	}
	decodeJSON(t, rec, &body)
	if body.SubjectToken == "" {
		t.Fatal("login must return a subject token")
	}
	return body.SubjectToken
}

// --- contract parity -------------------------------------------------------

type contractOperation struct {
	Method     string
	Path       string
	Capability string
	Mutates    bool
	Subject    bool
}

func loadContractOperations(t *testing.T) map[string]contractOperation {
	t.Helper()
	data, err := os.ReadFile("../../contracts/compat/v1/openapi.yaml")
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	paths, _ := root["paths"].(map[string]any)
	out := map[string]contractOperation{}
	for path, rawItem := range paths {
		item, _ := rawItem.(map[string]any)
		for _, method := range []string{"get", "put", "post", "delete", "patch"} {
			op, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			capability, _ := op["x-vondel-capability"].(string)
			subject, _ := op["x-vondel-subject"].(bool)
			key := strings.ToUpper(method) + " " + path
			out[key] = contractOperation{
				Method:     strings.ToUpper(method),
				Path:       path,
				Capability: capability,
				Mutates:    method != "get",
				Subject:    subject,
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("contract declares no operations")
	}
	return out
}

func TestRoutesMatchContract(t *testing.T) {
	f := newFixture(t, nil)
	contract := loadContractOperations(t)
	registered := map[string]Operation{}
	for _, op := range f.handler.Operations() {
		registered[op.Method+" "+op.Path] = op
	}
	var contractKeys, registeredKeys []string
	for k := range contract {
		contractKeys = append(contractKeys, k)
	}
	for k := range registered {
		registeredKeys = append(registeredKeys, k)
	}
	sort.Strings(contractKeys)
	sort.Strings(registeredKeys)
	if !reflect.DeepEqual(contractKeys, registeredKeys) {
		t.Fatalf("contract and handler operations differ:\ncontract:   %v\nregistered: %v", contractKeys, registeredKeys)
	}
	for key, want := range contract {
		got := registered[key]
		if got.Capability != want.Capability {
			t.Errorf("%s: handler capability %q, contract %q", key, got.Capability, want.Capability)
		}
		if got.SubjectScoped != want.Subject {
			t.Errorf("%s: handler subject-scoped=%v, contract=%v", key, got.SubjectScoped, want.Subject)
		}
		if got.Mutates != want.Mutates {
			t.Errorf("%s: handler mutates=%v, contract=%v", key, got.Mutates, want.Mutates)
		}
	}
}

// --- authentication and capabilities ---------------------------------------

func TestRequestsWithoutServiceBearerAreRefused(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodGet, "/health", nil)
	requireErrorCode(t, rec, http.StatusUnauthorized, "unauthorized")

	rec = f.do(t, http.MethodGet, "/health", nil, withBearer("no-such-bearer"))
	requireErrorCode(t, rec, http.StatusUnauthorized, "unauthorized")
}

func TestCapabilityIsEnforcedPerOperation(t *testing.T) {
	f := newFixture(t, nil)
	// The catalog-only application may not touch the state surface even with
	// a valid subject token minted through a fully capable application: the
	// subject token is audience-bound, so it first needs its own login. Use
	// the identity surface directly to prove the 403 fires before anything
	// else subject-related.
	rec := f.do(t, http.MethodPost, "/identity/login", map[string]any{
		"grant_type": "direct_profile", "email": "reader@example.test", "password": "correct horse",
		"device": map[string]any{"id": "dev-1"},
	}, withBearer(bearerCatalog), withIdempotencyKey("k1"))
	requireErrorCode(t, rec, http.StatusForbidden, "capability_missing")

	// Its granted catalog surface still works once a subject exists; the
	// subject-token check fires after capability enforcement.
	rec = f.do(t, http.MethodGet, "/catalog/libraries", nil, withBearer(bearerCatalog))
	requireErrorCode(t, rec, http.StatusUnauthorized, "subject_token_required")
}

func TestSubjectTokenIsAudienceBound(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)

	// The application that minted it can use it.
	rec := f.do(t, http.MethodGet, "/identity/subject", nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)

	// A different application presenting the same token is refused.
	rec = f.do(t, http.MethodGet, "/identity/subject", nil, withBearer(bearerOther), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusUnauthorized, "subject_token_invalid")
}

func TestLoginReturnsAllSubjectFacts(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodPost, "/identity/login", map[string]any{
		"grant_type": "direct_profile",
		"email":      "reader@example.test",
		"password":   "correct horse",
		"device":     map[string]any{"id": "dev-1", "name": "Living Room", "platform": "jellyfin-web", "ip_address": "203.0.113.9"},
	}, withBearer(bearerFull), withIdempotencyKey("login-facts"))
	requireStatus(t, rec, http.StatusOK)
	var body struct {
		SubjectToken string      `json:"subject_token"`
		ExpiresAt    time.Time   `json:"expires_at"`
		Subject      subjectWire `json:"subject"`
	}
	decodeJSON(t, rec, &body)
	want := subjectToWire(testSubject())
	if !reflect.DeepEqual(body.Subject, want) {
		t.Fatalf("subject facts = %+v, want %+v", body.Subject, want)
	}
	if !body.ExpiresAt.After(f.now) {
		t.Fatalf("subject token must expire in the future, got %v", body.ExpiresAt)
	}
}

func TestSubjectIsReauthorizedOnEveryRequest(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)

	// Revocation (password reset, suspension, revision bump) refuses the
	// token even though its signature and expiry are still valid.
	f.subjects.current = func(Subject) (Subject, error) { return Subject{}, ErrSubjectRevoked }
	rec := f.do(t, http.MethodGet, "/catalog/libraries", nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusUnauthorized, "subject_revoked")

	// A transient resolution failure is not a revocation: it must fail the
	// request without claiming the subject is gone.
	f.subjects.current = func(Subject) (Subject, error) { return Subject{}, errors.New("db down") }
	rec = f.do(t, http.MethodGet, "/catalog/libraries", nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusInternalServerError, "internal")
}

func TestExpiredSubjectTokensAreRefused(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)
	f.advance(16 * time.Minute)
	rec := f.do(t, http.MethodGet, "/identity/subject", nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusUnauthorized, "subject_token_invalid")
}

func TestTamperedSubjectTokensAreRefused(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)
	tampered := token[:len(token)-2] + "zz"
	rec := f.do(t, http.MethodGet, "/identity/subject", nil, withBearer(bearerFull), withSubjectToken(tampered))
	requireErrorCode(t, rec, http.StatusUnauthorized, "subject_token_invalid")
}

// --- enrollment, renewal, health -------------------------------------------

func TestEnrollExchangesTheSecretExactlyOnce(t *testing.T) {
	f := newFixture(t, nil)
	body := map[string]any{
		"secret":      "enroll-secret",
		"kind":        "audiobookshelf",
		"instance_id": "inst-new",
		"version":     "0.1.0",
		"api":         map[string]int{"min": 1, "max": 1},
		"requested_capabilities": []string{
			CapabilityCatalog,
		},
	}
	rec := f.do(t, http.MethodPost, "/enroll", body, withIdempotencyKey("enroll-1"))
	requireStatus(t, rec, http.StatusCreated)
	var cred struct {
		ApplicationID string    `json:"application_id"`
		Token         string    `json:"token"`
		ExpiresAt     time.Time `json:"expires_at"`
		Granted       []string  `json:"granted_capabilities"`
	}
	decodeJSON(t, rec, &cred)
	if cred.Token != "svc-new-token" || cred.ApplicationID != "app-new" {
		t.Fatalf("credential = %+v", cred)
	}
	if f.enroller.lastSecret != "enroll-secret" || f.enroller.lastReq.Kind != "audiobookshelf" {
		t.Fatalf("enroller saw secret=%q req=%+v", f.enroller.lastSecret, f.enroller.lastReq)
	}

	// Idempotent replay: same key and body returns the stored response
	// without consuming the enrollment path a second time.
	rec2 := f.do(t, http.MethodPost, "/enroll", body, withIdempotencyKey("enroll-1"))
	requireStatus(t, rec2, http.StatusCreated)
	if rec2.Body.String() != rec.Body.String() {
		t.Fatalf("replay body differs: %s vs %s", rec2.Body.String(), rec.Body.String())
	}
	if f.enroller.calls != 1 {
		t.Fatalf("enroller called %d times, want 1", f.enroller.calls)
	}

	// Same key with a different payload is a conflict, not a silent replay.
	body["instance_id"] = "inst-imposter"
	rec3 := f.do(t, http.MethodPost, "/enroll", body, withIdempotencyKey("enroll-1"))
	requireErrorCode(t, rec3, http.StatusConflict, "idempotency_conflict")
}

func TestEnrollWithInvalidSecretIsUnauthorized(t *testing.T) {
	f := newFixture(t, func(s *Services) {})
	f.enroller.err = ErrInvalidCredentials
	rec := f.do(t, http.MethodPost, "/enroll", map[string]any{
		"secret": "wrong", "kind": "jellyfin", "instance_id": "x",
		"api": map[string]int{"min": 1, "max": 1},
	}, withIdempotencyKey("enroll-bad"))
	requireErrorCode(t, rec, http.StatusUnauthorized, "invalid_credentials")
}

func TestHealthReportsAPIRangeAndRecordsHeartbeat(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodGet, "/health", nil, withBearer(bearerFull))
	requireStatus(t, rec, http.StatusOK)
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
		API     struct {
			Min int `json:"min"`
			Max int `json:"max"`
		} `json:"api"`
		Application struct {
			ApplicationID string   `json:"application_id"`
			Kind          string   `json:"kind"`
			InstanceID    string   `json:"instance_id"`
			Capabilities  []string `json:"capabilities"`
		} `json:"application"`
	}
	decodeJSON(t, rec, &body)
	if body.Status != "ok" || body.API.Min != 1 || body.API.Max != 1 || body.Version != "test-build" {
		t.Fatalf("health = %+v", body)
	}
	if body.Application.ApplicationID != "app-full" || body.Application.InstanceID != "inst-full" {
		t.Fatalf("health application = %+v", body.Application)
	}
	if len(f.heartbeats.calls) != 1 || f.heartbeats.calls[0] != "app-full" {
		t.Fatalf("heartbeats = %v", f.heartbeats.calls)
	}
}

func TestRenewIssuesAFreshCredential(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodPost, "/credentials/renew", nil, withBearer(bearerFull), withIdempotencyKey("renew-1"))
	requireStatus(t, rec, http.StatusOK)
	var cred struct {
		Token string `json:"token"`
	}
	decodeJSON(t, rec, &cred)
	if cred.Token != "svc-renewed" {
		t.Fatalf("token = %q", cred.Token)
	}
}

// --- idempotency ------------------------------------------------------------

func TestMutationsRequireAnIdempotencyKey(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodPut, "/state/progress/item-1", map[string]any{"position_seconds": 30},
		withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusBadRequest, "idempotency_key_required")
	if f.state.setCalls != 0 {
		t.Fatalf("service invoked despite missing key")
	}
}

func TestIdempotentReplayDoesNotReinvokeTheService(t *testing.T) {
	f := newFixture(t, nil)
	token := f.login(t, bearerFull)
	body := map[string]any{"position_seconds": 42, "duration_seconds": 100}
	rec := f.do(t, http.MethodPut, "/state/progress/item-1", body,
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("prog-1"))
	requireStatus(t, rec, http.StatusOK)
	rec2 := f.do(t, http.MethodPut, "/state/progress/item-1", body,
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("prog-1"))
	requireStatus(t, rec2, http.StatusOK)
	if rec.Body.String() != rec2.Body.String() {
		t.Fatalf("replayed body differs")
	}
	if f.state.setCalls != 1 {
		t.Fatalf("service called %d times, want 1", f.state.setCalls)
	}
	// A different application may reuse the same key without collision.
	otherToken := f.login(t, bearerOther)
	rec3 := f.do(t, http.MethodPut, "/state/progress/item-1", body,
		withBearer(bearerOther), withSubjectToken(otherToken), withIdempotencyKey("prog-1"))
	requireStatus(t, rec3, http.StatusOK)
	if f.state.setCalls != 2 {
		t.Fatalf("second application's write must execute, calls=%d", f.state.setCalls)
	}
}

// --- cursors ----------------------------------------------------------------

func TestListCursorsAreSignedAndTamperProof(t *testing.T) {
	f := newFixture(t, nil)
	f.catalog.page = ItemPage{
		Items:      []Item{{ID: "item-1", Kind: "movie", Title: "Tester"}},
		NextCursor: "domain-cursor-token",
	}
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/catalog/items?limit=1", nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	decodeJSON(t, rec, &page)
	if page.NextCursor == "" || page.NextCursor == "domain-cursor-token" {
		t.Fatalf("wire cursor must be a signed opaque value, got %q", page.NextCursor)
	}

	// Round trip: the domain service receives its own token back.
	rec = f.do(t, http.MethodGet, "/catalog/items?cursor="+page.NextCursor, nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)
	calls := f.catalog.itemsCalls
	if got := calls[len(calls)-1].Cursor; got != "domain-cursor-token" {
		t.Fatalf("domain cursor = %q, want domain-cursor-token", got)
	}

	// Tampering is refused.
	tampered := page.NextCursor[:len(page.NextCursor)-2] + "zz"
	rec = f.do(t, http.MethodGet, "/catalog/items?cursor="+tampered, nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusBadRequest, "invalid_cursor")

	// A cursor signed for one operation cannot be replayed on another.
	rec = f.do(t, http.MethodGet, "/events?cursor="+page.NextCursor, nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusBadRequest, "invalid_cursor")
}

// --- rate limiting and availability ----------------------------------------

func TestRateLimitedRequestsReceive429(t *testing.T) {
	limiter := &fakeLimiter{allow: false}
	f := newFixture(t, func(s *Services) { s.RateLimiter = limiter })
	rec := f.do(t, http.MethodGet, "/health", nil, withBearer(bearerFull))
	requireErrorCode(t, rec, http.StatusTooManyRequests, "rate_limited")
}

func TestMissingServicesFailClosedWithUnavailable(t *testing.T) {
	f := newFixture(t, func(s *Services) {
		s.LiveTV = nil
		s.Enroller = nil
	})
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/livetv/channels", nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusServiceUnavailable, "unavailable")

	rec = f.do(t, http.MethodPost, "/enroll", map[string]any{"secret": "s", "kind": "jellyfin", "instance_id": "i", "api": map[string]int{"min": 1, "max": 1}}, withIdempotencyKey("e1"))
	requireErrorCode(t, rec, http.StatusServiceUnavailable, "unavailable")
}

func TestDomainUnavailabilityMapsTo503(t *testing.T) {
	f := newFixture(t, nil)
	f.events.err = ErrUnavailable
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/events", nil, withBearer(bearerFull), withSubjectToken(token))
	requireErrorCode(t, rec, http.StatusServiceUnavailable, "unavailable")
}

func TestNotFoundIsNonDisclosing(t *testing.T) {
	f := newFixture(t, nil)
	f.catalog.itemErr = ErrNotFound
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/catalog/items/adult-item", nil, withBearer(bearerFull), withSubjectToken(token))
	body := requireErrorCode(t, rec, http.StatusNotFound, "not_found")
	if strings.Contains(body.Error.Message, "adult") {
		t.Fatalf("not-found message must not echo the identifier: %q", body.Error.Message)
	}
}

func TestUnknownServiceErrorsAreNotEchoed(t *testing.T) {
	f := newFixture(t, nil)
	f.catalog.itemErr = errors.New("pq: relation media_files_secret is broken")
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/catalog/items/item-1", nil, withBearer(bearerFull), withSubjectToken(token))
	body := requireErrorCode(t, rec, http.StatusInternalServerError, "internal")
	if strings.Contains(body.Error.Message, "pq:") || strings.Contains(body.Error.Message, "media_files") {
		t.Fatalf("internal errors must not leak, got %q", body.Error.Message)
	}
}

// --- delivery grants --------------------------------------------------------

func TestDeliveryGrantsAreAudienceBound(t *testing.T) {
	f := newFixture(t, nil)
	expires := f.now.Add(2 * time.Minute)
	f.playback.start = func(_ Subject, audience string, _ PlaybackStart) (PlaybackSession, error) {
		return PlaybackSession{
			SessionID: "sess-1",
			ItemID:    "item-1",
			State:     "playing",
			Grant:     &DeliveryGrant{URL: "/media/sess-1/stream.m3u8", Method: "GET", ExpiresAt: expires, Audience: audience},
		}, nil
	}
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodPost, "/playback/sessions", map[string]any{"item_id": "item-1", "plan_id": "plan-1"},
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("start-1"))
	requireStatus(t, rec, http.StatusCreated)
	var session struct {
		Grant struct {
			URL      string    `json:"url"`
			Audience string    `json:"audience"`
			Expires  time.Time `json:"expires_at"`
		} `json:"grant"`
	}
	decodeJSON(t, rec, &session)
	if session.Grant.Audience != "app-full" {
		t.Fatalf("grant audience = %q, want app-full", session.Grant.Audience)
	}

	// A grant minted for the wrong audience is a server bug and must never
	// leave the boundary.
	f.playback.start = func(_ Subject, _ string, _ PlaybackStart) (PlaybackSession, error) {
		return PlaybackSession{
			SessionID: "sess-2",
			Grant:     &DeliveryGrant{URL: "/media/x", Method: "GET", ExpiresAt: expires, Audience: "someone-else"},
		}, nil
	}
	rec = f.do(t, http.MethodPost, "/playback/sessions", map[string]any{"item_id": "item-1", "plan_id": "plan-1"},
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("start-2"))
	requireErrorCode(t, rec, http.StatusInternalServerError, "internal")
}

func TestArtworkResolutionReturnsAGrant(t *testing.T) {
	f := newFixture(t, nil)
	f.catalog.grant = func(_ Subject, audience, itemID, kind string) (DeliveryGrant, error) {
		return DeliveryGrant{URL: "/artwork/" + itemID + "/" + kind, Method: "GET", ExpiresAt: f.now.Add(time.Minute), Audience: audience}, nil
	}
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/catalog/items/item-1/artwork/primary", nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)
	var grant struct {
		URL      string `json:"url"`
		Audience string `json:"audience"`
	}
	decodeJSON(t, rec, &grant)
	if grant.URL != "/artwork/item-1/primary" || grant.Audience != "app-full" {
		t.Fatalf("grant = %+v", grant)
	}
}

// --- events -----------------------------------------------------------------

func TestEventCursorsRoundTripAndSignalResync(t *testing.T) {
	f := newFixture(t, nil)
	f.events.batch = EventBatch{
		Events:     []Event{{ID: "ev-1", Type: "item.updated", ItemID: "item-1", OccurredAt: f.now}},
		NextCursor: "events-domain-cursor",
	}
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodGet, "/events?limit=10", nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)
	var batch struct {
		Events     []map[string]any `json:"events"`
		NextCursor string           `json:"next_cursor"`
		Resync     bool             `json:"resync"`
	}
	decodeJSON(t, rec, &batch)
	if len(batch.Events) != 1 || batch.NextCursor == "" || batch.NextCursor == "events-domain-cursor" {
		t.Fatalf("batch = %+v", batch)
	}

	f.events.batch = EventBatch{NextCursor: "events-domain-cursor-2", Resync: true}
	rec = f.do(t, http.MethodGet, "/events?cursor="+batch.NextCursor, nil, withBearer(bearerFull), withSubjectToken(token))
	requireStatus(t, rec, http.StatusOK)
	decodeJSON(t, rec, &batch)
	if !batch.Resync {
		t.Fatal("resync flag must survive to the wire")
	}
	if f.events.lastCursor != "events-domain-cursor" {
		t.Fatalf("domain cursor = %q", f.events.lastCursor)
	}
}

// --- tracing ----------------------------------------------------------------

func TestTraceIDsPropagate(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodGet, "/health", nil, withBearer(bearerFull), withHeader("X-Trace-Id", "trace-abc"))
	requireStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-Trace-Id"); got != "trace-abc" {
		t.Fatalf("trace id = %q, want trace-abc", got)
	}
	// A generated trace id appears when the caller supplies none.
	rec = f.do(t, http.MethodGet, "/health", nil, withBearer(bearerFull))
	if rec.Header().Get("X-Trace-Id") == "" {
		t.Fatal("a trace id must be generated")
	}
}

// --- wire hygiene -----------------------------------------------------------

// TestWireTypesExposeNoForbiddenFields walks every wire struct and refuses
// JSON keys that would leak filesystem paths, credentials, or raw database
// internals to a companion.
func TestWireTypesExposeNoForbiddenFields(t *testing.T) {
	forbidden := map[string]bool{
		"path": true, "file_path": true, "filename": true, "file": true,
		"secret": true, "password": true, "password_hash": true,
		"db_id": true, "database_id": true, "signing_key": true,
	}
	types := []any{
		Library{}, Item{}, Person{}, PersonRef{}, ArtworkRef{},
		Progress{}, Bookmark{}, Collection{}, Playlist{}, Download{},
		Plan{}, PlaybackSession{}, DeliveryGrant{},
		Channel{}, GuideEntry{}, Tuner{}, DVRRule{}, Recording{},
		Event{}, ProfileTile{},
	}
	var walk func(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool)
	walk = func(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if forbidden[tag] || strings.HasSuffix(tag, "_path") {
				t.Errorf("%s.%s exposes forbidden wire field %q", typ.Name(), field.Name, tag)
			}
			walk(t, field.Type, seen)
		}
	}
	seen := map[reflect.Type]bool{}
	for _, v := range types {
		walk(t, reflect.TypeOf(v), seen)
	}
}

// --- construction -----------------------------------------------------------

func TestNewRefusesWeakOrMissingConfiguration(t *testing.T) {
	valid := Services{Apps: &fakeApps{byBearer: map[string]AppIdentity{}}}
	cases := []struct {
		name string
		cfg  Config
		svc  Services
	}{
		{"missing app authenticator", Config{SubjectTokenKey: bytes.Repeat([]byte{1}, 32), CursorKey: bytes.Repeat([]byte{2}, 32)}, Services{}},
		{"short subject key", Config{SubjectTokenKey: []byte("short"), CursorKey: bytes.Repeat([]byte{2}, 32)}, valid},
		{"short cursor key", Config{SubjectTokenKey: bytes.Repeat([]byte{1}, 32), CursorKey: []byte("short")}, valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg, tc.svc); err == nil {
				t.Fatal("New must refuse the configuration")
			}
		})
	}
}

// TestSubjectRoutesRequireASubjectToken sweeps every subject-scoped operation
// and proves none of them answers without a subject token.
func TestSubjectRoutesRequireASubjectToken(t *testing.T) {
	f := newFixture(t, nil)
	for _, op := range f.handler.Operations() {
		if !op.SubjectScoped {
			continue
		}
		path := op.Path
		for _, arg := range [][2]string{{"{itemID}", "item-1"}, {"{personID}", "p-1"}, {"{bookmarkID}", "b-1"}, {"{collectionID}", "c-1"}, {"{playlistID}", "pl-1"}, {"{downloadID}", "d-1"}, {"{sessionID}", "s-1"}, {"{ruleID}", "r-1"}, {"{recordingID}", "rec-1"}, {"{artworkKind}", "primary"}} {
			path = strings.ReplaceAll(path, arg[0], arg[1])
		}
		opts := []reqOpt{withBearer(bearerFull)}
		if op.Mutates {
			opts = append(opts, withIdempotencyKey("sweep-"+op.ID))
		}
		rec := f.do(t, op.Method, path, map[string]any{}, opts...)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d without a subject token", op.Method, op.Path, rec.Code)
		}
	}
}

// TestOperationsAreDistinct guards the Operations index itself: no duplicate
// method+path registrations, every operation carries a capability and an id.
func TestOperationsAreDistinct(t *testing.T) {
	f := newFixture(t, nil)
	seen := map[string]bool{}
	for _, op := range f.handler.Operations() {
		key := op.Method + " " + op.Path
		if seen[key] {
			t.Errorf("duplicate operation %s", key)
		}
		seen[key] = true
		if op.Capability == "" || op.ID == "" {
			t.Errorf("operation %s is missing capability or id", key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("handler registers no operations")
	}
}

func TestStateSurfaceRoundTrips(t *testing.T) {
	f := newFixture(t, nil)
	f.state.progress = Progress{ItemID: "item-1", PositionSeconds: 10, DurationSeconds: 100}
	f.state.bookmarks = []Bookmark{{ID: "bm-0", ItemID: "item-1", PositionSeconds: 5}}
	f.state.downloads = []Download{{ID: "dl-1", ItemID: "item-1", State: "ready"}}
	token := f.login(t, bearerFull)
	auth := []reqOpt{withBearer(bearerFull), withSubjectToken(token)}

	rec := f.do(t, http.MethodGet, "/state/progress/item-1", nil, auth...)
	requireStatus(t, rec, http.StatusOK)

	rec = f.do(t, http.MethodPost, "/state/bookmarks", map[string]any{"item_id": "item-1", "position_seconds": 12, "title": "mark"},
		append(auth, withIdempotencyKey("bm-1"))...)
	requireStatus(t, rec, http.StatusCreated)

	rec = f.do(t, http.MethodDelete, "/state/bookmarks/bm-1", nil, append(auth, withIdempotencyKey("bm-del"))...)
	requireStatus(t, rec, http.StatusNoContent)

	rec = f.do(t, http.MethodPut, "/state/watched/item-1", map[string]any{"watched": true}, append(auth, withIdempotencyKey("w-1"))...)
	requireStatus(t, rec, http.StatusNoContent)

	rec = f.do(t, http.MethodPut, "/state/favorites/item-1", map[string]any{"favorite": true}, append(auth, withIdempotencyKey("f-1"))...)
	requireStatus(t, rec, http.StatusNoContent)

	rec = f.do(t, http.MethodGet, "/state/downloads", nil, auth...)
	requireStatus(t, rec, http.StatusOK)
}

func TestProfileSwitchMintsANewAudienceBoundToken(t *testing.T) {
	f := newFixture(t, nil)
	switched := testSubject()
	switched.ProfileID = "prof-2"
	f.subjects.switchTo = func(subject Subject, profileID, pin string) (Subject, error) {
		if subject.ProfileID != "prof-1" || profileID != "prof-2" || pin != "1234" {
			return Subject{}, ErrPINInvalid
		}
		return switched, nil
	}
	token := f.login(t, bearerFull)
	rec := f.do(t, http.MethodPost, "/identity/switch", map[string]any{"profile_id": "prof-2", "pin": "1234"},
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("switch-1"))
	requireStatus(t, rec, http.StatusOK)
	var body struct {
		SubjectToken string      `json:"subject_token"`
		Subject      subjectWire `json:"subject"`
	}
	decodeJSON(t, rec, &body)
	if body.Subject.ProfileID != "prof-2" {
		t.Fatalf("switched subject = %+v", body.Subject)
	}
	rec = f.do(t, http.MethodGet, "/identity/subject", nil, withBearer(bearerFull), withSubjectToken(body.SubjectToken))
	requireStatus(t, rec, http.StatusOK)

	// Wrong PIN refuses.
	rec = f.do(t, http.MethodPost, "/identity/switch", map[string]any{"profile_id": "prof-2", "pin": "9999"},
		withBearer(bearerFull), withSubjectToken(token), withIdempotencyKey("switch-2"))
	requireErrorCode(t, rec, http.StatusForbidden, "pin_invalid")
}

func TestMalformedJSONBodiesAreRefused(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodPost, "/identity/login", `{"grant_type":`, withBearer(bearerFull), withIdempotencyKey("bad-json"))
	requireErrorCode(t, rec, http.StatusBadRequest, "invalid_request")
}

func TestUnknownPathsReturnStableEnvelope(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.do(t, http.MethodGet, "/no/such/route", nil, withBearer(bearerFull))
	requireErrorCode(t, rec, http.StatusNotFound, "not_found")
}
