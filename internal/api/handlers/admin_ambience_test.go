package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

var (
	ambienceStart = time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	ambienceEnd   = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
)

type fakeAmbienceRegistry struct {
	packs      []ambience.Pack
	created    []ambience.Input
	createdBy  int
	updated    map[string]ambience.Input
	deleted    []string
	attached   []string
	err        error
	storage    bool
	servedData []byte
}

func (f *fakeAmbienceRegistry) pack(id string, in ambience.Input) *ambience.Pack {
	n, err := ambience.Normalize(in)
	if err != nil {
		return nil
	}
	return &ambience.Pack{ID: id, EffectID: n.EffectID, Window: n.Window, Intensity: n.Intensity, Surfaces: n.Surfaces, Assets: n.Assets, OrganizationID: n.OrganizationID, CreatedBy: f.createdBy, CreatedAt: ambienceStart, UpdatedAt: ambienceStart}
}

func (f *fakeAmbienceRegistry) List(context.Context) ([]ambience.Pack, error) { return f.packs, f.err }
func (f *fakeAmbienceRegistry) Create(_ context.Context, createdBy int, in ambience.Input) (*ambience.Pack, error) {
	f.createdBy = createdBy
	f.created = append(f.created, in)
	if f.err != nil {
		return nil, f.err
	}
	if _, err := ambience.Normalize(in); err != nil {
		return nil, err
	}
	return f.pack("pack-1", in), nil
}
func (f *fakeAmbienceRegistry) Update(_ context.Context, id string, in ambience.Input) (*ambience.Pack, error) {
	if f.updated == nil {
		f.updated = map[string]ambience.Input{}
	}
	f.updated[id] = in
	if f.err != nil {
		return nil, f.err
	}
	return f.pack(id, in), nil
}
func (f *fakeAmbienceRegistry) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}
func (f *fakeAmbienceRegistry) AttachAsset(_ context.Context, packID, slot string, data []byte) (*ambience.Pack, string, error) {
	f.attached = append(f.attached, packID+":"+slot)
	if f.err != nil {
		return nil, "", f.err
	}
	url := ambience.AssetURL("0123456789abcdef.png")
	p := f.pack(packID, ambience.Input{EffectID: "pumpkins", Window: ambience.Window{StartsAt: ambienceStart, EndsAt: ambienceEnd}, Assets: ambience.Assets{BannerURL: url}})
	return p, url, nil
}
func (f *fakeAmbienceRegistry) ServeAsset(_ context.Context, ref string) ([]byte, string, error) {
	if f.servedData == nil {
		return nil, "", ambience.ErrAssetNotFound
	}
	return f.servedData, "image/png", nil
}
func (f *fakeAmbienceRegistry) HasStorage() bool { return f.storage }

func ambienceRequest(method, target, body string, userID int) *http.Request {
	var b []byte
	if body != "" {
		b = []byte(body)
	}
	return downloadTestRequest(method, target, b, userID, "", "")
}

func TestAdminAmbienceCreateReturnsCreatedPack(t *testing.T) {
	reg := &fakeAmbienceRegistry{}
	h := &AmbienceHandler{registry: reg}
	// This request body is the client contract quoted in
	// docs/specs/client-engagement.md "Admin ambience contract".
	body := `{"effect_id":"halloween","window":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"},"intensity":0.7,"surfaces":["home","login"],"assets":{"banner_url":"https://cdn.example/halloween/banner.png","sprites":["https://cdn.example/halloween/pumpkin.png"]},"organization_id":null}`
	rec := httptest.NewRecorder()
	h.HandleCreate(rec, ambienceRequest(http.MethodPost, "/admin/ambience", body, 42))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (%s), want 201", rec.Code, rec.Body.String())
	}
	if reg.createdBy != 42 || len(reg.created) != 1 || reg.created[0].EffectID != "halloween" {
		t.Fatalf("registry call not recorded: by=%d created=%+v", reg.createdBy, reg.created)
	}
	var got ambience.Pack
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "pack-1" || got.Intensity != 0.7 || strings.Join(got.Surfaces, ",") != "home,login" || got.Assets.BannerURL == "" || got.OrganizationID != nil {
		t.Fatalf("unexpected response: %+v", got)
	}
	want := `{"id":"pack-1","effect_id":"halloween","window":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"},"intensity":0.7,"surfaces":["home","login"],"assets":{"banner_url":"https://cdn.example/halloween/banner.png","sprites":["https://cdn.example/halloween/pumpkin.png"]},"organization_id":null,"created_by":42,"created_at":"2026-10-24T00:00:00Z","updated_at":"2026-10-24T00:00:00Z"}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("response body drifted from the documented contract:\n got %s\nwant %s", rec.Body.String(), want)
	}
}

func TestAdminAmbienceListUpdateDelete(t *testing.T) {
	reg := &fakeAmbienceRegistry{storage: true}
	h := &AmbienceHandler{registry: reg}

	rec := httptest.NewRecorder()
	h.HandleList(rec, ambienceRequest(http.MethodGet, "/admin/ambience", "", 1))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"packs":[]`) || !strings.Contains(rec.Body.String(), `"storage_available":true`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	body := `{"effect_id":"snow","window":{"starts_at":"2026-12-01T00:00:00Z","ends_at":"2027-01-07T00:00:00Z"}}`
	h.HandleUpdate(rec, withChiID(ambienceRequest(http.MethodPut, "/admin/ambience/pack-9", body, 1), "pack-9"))
	if rec.Code != http.StatusOK || reg.updated["pack-9"].EffectID != "snow" || !strings.Contains(rec.Body.String(), `"intensity":1,"surfaces":["all"]`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleDelete(rec, withChiID(ambienceRequest(http.MethodDelete, "/admin/ambience/pack-9", "", 1), "pack-9"))
	if rec.Code != http.StatusNoContent || len(reg.deleted) != 1 || reg.deleted[0] != "pack-9" {
		t.Fatalf("delete: %d deleted=%v", rec.Code, reg.deleted)
	}
}

func TestAdminAmbienceErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
		code string
	}{
		{ambience.ErrNotFound, http.StatusNotFound, "not_found"},
		{errors.Join(ambience.ErrInvalid, errors.New("effect_id is required")), http.StatusBadRequest, "effect_id is required"},
		{errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	}
	body := `{"effect_id":"snow","window":{"starts_at":"2026-12-01T00:00:00Z","ends_at":"2027-01-07T00:00:00Z"}}`
	for _, tc := range cases {
		h := &AmbienceHandler{registry: &fakeAmbienceRegistry{err: tc.err}}
		rec := httptest.NewRecorder()
		h.HandleUpdate(rec, withChiID(ambienceRequest(http.MethodPut, "/admin/ambience/x", body, 1), "x"))
		if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.code) {
			t.Errorf("err %v: status=%d body=%s, want %d/%s", tc.err, rec.Code, rec.Body.String(), tc.want, tc.code)
		}
	}
	// Validation failures surface from Normalize as 400 naming the field.
	rec := httptest.NewRecorder()
	(&AmbienceHandler{registry: &fakeAmbienceRegistry{}}).HandleCreate(rec, ambienceRequest(http.MethodPost, "/admin/ambience", `{"effect_id":"snow","window":{"starts_at":"2027-01-07T00:00:00Z","ends_at":"2026-12-01T00:00:00Z"}}`, 1))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "window.starts_at") {
		t.Fatalf("reversed window: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	(&AmbienceHandler{registry: &fakeAmbienceRegistry{}}).HandleCreate(rec, ambienceRequest(http.MethodPost, "/admin/ambience", `{not json`, 1))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed json: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&AmbienceHandler{}).HandleList(rec, ambienceRequest(http.MethodGet, "/admin/ambience", "", 1))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired handler must 503: %d", rec.Code)
	}
}

func multipartUpload(t *testing.T, slot string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if slot != "" {
		_ = mw.WriteField("slot", slot)
	}
	fw, err := mw.CreateFormFile("file", "art.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestAdminAmbienceAttachAssetReturnsPublicURL(t *testing.T) {
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	reg := &fakeAmbienceRegistry{storage: true}
	h := &AmbienceHandler{registry: reg}
	body, ct := multipartUpload(t, "sprite", img.Bytes())
	r := withChiID(httptest.NewRequest(http.MethodPost, "/admin/ambience/pack-1/assets", body), "pack-1")
	r.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.HandleAttachAsset(rec, r)
	if rec.Code != http.StatusCreated || len(reg.attached) != 1 || reg.attached[0] != "pack-1:sprite" {
		t.Fatalf("attach: %d %s attached=%v", rec.Code, rec.Body.String(), reg.attached)
	}
	var got struct {
		URL  string        `json:"url"`
		Slot string        `json:"slot"`
		Pack ambience.Pack `json:"pack"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.URL, ambience.AssetURLBase) || got.Slot != "sprite" || got.Pack.ID != "pack-1" {
		t.Fatalf("unexpected attach response: %+v", got)
	}

	// Default slot is banner; no storage is 503; unsupported image is 415.
	body, ct = multipartUpload(t, "", img.Bytes())
	r = withChiID(httptest.NewRequest(http.MethodPost, "/admin/ambience/pack-1/assets", body), "pack-1")
	r.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	h.HandleAttachAsset(rec, r)
	if rec.Code != http.StatusCreated || reg.attached[1] != "pack-1:banner" {
		t.Fatalf("default slot: %d %v", rec.Code, reg.attached)
	}
	for _, tc := range []struct {
		reg  *fakeAmbienceRegistry
		want int
	}{
		{&fakeAmbienceRegistry{storage: false}, http.StatusServiceUnavailable},
		{&fakeAmbienceRegistry{storage: true, err: ambience.ErrUnsupportedImage}, http.StatusUnsupportedMediaType},
		{&fakeAmbienceRegistry{storage: true, err: ambience.ErrNotFound}, http.StatusNotFound},
	} {
		body, ct = multipartUpload(t, "banner", img.Bytes())
		r = withChiID(httptest.NewRequest(http.MethodPost, "/admin/ambience/pack-1/assets", body), "pack-1")
		r.Header.Set("Content-Type", ct)
		rec = httptest.NewRecorder()
		(&AmbienceHandler{registry: tc.reg}).HandleAttachAsset(rec, r)
		if rec.Code != tc.want {
			t.Errorf("attach with %+v: %d, want %d", tc.reg.err, rec.Code, tc.want)
		}
	}
}

func TestAmbienceServeAssetIsImmutable(t *testing.T) {
	h := &AmbienceHandler{registry: &fakeAmbienceRegistry{servedData: []byte("png")}}
	rec := httptest.NewRecorder()
	h.HandleServeAsset(rec, withChiID(httptest.NewRequest(http.MethodGet, "/ambience/assets/0123456789abcdef.png", nil), "0123456789abcdef.png"))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("serve: %d %v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	(&AmbienceHandler{registry: &fakeAmbienceRegistry{}}).HandleServeAsset(rec, withChiID(httptest.NewRequest(http.MethodGet, "/ambience/assets/nope", nil), "nope"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown ref: %d", rec.Code)
	}
}

type fakeAmbienceSource struct {
	public  []ambience.Wire
	account map[int][]ambience.Wire
	err     error
}

func (f *fakeAmbienceSource) ActivePublic(context.Context) ([]ambience.Wire, error) {
	return f.public, f.err
}
func (f *fakeAmbienceSource) ActiveForAccount(_ context.Context, id int) ([]ambience.Wire, error) {
	return f.account[id], f.err
}

func snowWire() ambience.Wire {
	return ambience.Wire{ID: "pack-1", EffectID: "snow", Window: ambience.Window{StartsAt: ambienceStart, EndsAt: ambienceEnd}, Intensity: 0.5, Surfaces: []string{"all"}}
}

func TestBrandingPayloadCarriesActiveAmbience(t *testing.T) {
	svc := branding.NewService(&fakeServerSettingsStore{values: map[string]string{}}, nil)
	h := NewBrandingHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleGetBranding(rec, httptest.NewRequest(http.MethodGet, "/theme/branding", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ambience":[]`) {
		t.Fatalf("unwired registry must emit an empty ambience array: %d %s", rec.Code, rec.Body.String())
	}

	h.SetAmbience(&fakeAmbienceSource{public: []ambience.Wire{snowWire()}})
	rec = httptest.NewRecorder()
	h.HandleGetBranding(rec, httptest.NewRequest(http.MethodGet, "/theme/branding", nil))
	want := `"ambience":[{"id":"pack-1","effect_id":"snow","window":{"starts_at":"2026-10-24T00:00:00Z","ends_at":"2026-11-01T00:00:00Z"},"intensity":0.5,"surfaces":["all"],"assets":{}}]`
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("branding echo: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"server_name":"Bloem"`) {
		t.Fatalf("existing branding fields must be untouched: %s", rec.Body.String())
	}

	h.SetAmbience(&fakeAmbienceSource{err: errors.New("db down")})
	rec = httptest.NewRecorder()
	h.HandleGetBranding(rec, httptest.NewRequest(http.MethodGet, "/theme/branding", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ambience":[]`) {
		t.Fatalf("registry errors must degrade to an empty block, never break branding: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNotificationsCapabilityAdvertisesAmbience(t *testing.T) {
	settings := &fakeServerSettingsStore{values: map[string]string{}}
	h := NewNotificationsHandler(&notifications.System{Settings: notifications.NewSettings(settings)}, nil)

	rec := httptest.NewRecorder()
	h.HandleCapability(rec, downloadTestRequest(http.MethodGet, "/notifications/capability", nil, 7, "p1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ambience":false,"ambience_windows":[]`) {
		t.Fatalf("unwired: %d %s", rec.Code, rec.Body.String())
	}

	h.SetAmbience(&fakeAmbienceSource{account: map[int][]ambience.Wire{7: {snowWire()}}})
	rec = httptest.NewRecorder()
	h.HandleCapability(rec, downloadTestRequest(http.MethodGet, "/notifications/capability", nil, 7, "p1", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ambience":true,"ambience_windows":[{"id":"pack-1","effect_id":"snow"`) {
		t.Fatalf("wired: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleCapability(rec, downloadTestRequest(http.MethodGet, "/notifications/capability", nil, 8, "p2", ""))
	if !strings.Contains(rec.Body.String(), `"ambience":true,"ambience_windows":[]`) {
		t.Fatalf("other account sees only its own packs: %s", rec.Body.String())
	}
}
