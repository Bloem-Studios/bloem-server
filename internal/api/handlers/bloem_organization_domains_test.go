package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/orgdomains"
)

type orgDomainsAssets struct{ objects map[string][]byte }

func (m *orgDomainsAssets) Put(_ context.Context, key string, data []byte) error {
	m.objects[key] = data
	return nil
}
func (m *orgDomainsAssets) Get(_ context.Context, key string) (io.ReadCloser, blobstore.ObjectInfo, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, blobstore.ObjectInfo{}, blobstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), blobstore.ObjectInfo{Size: int64(len(data))}, nil
}
func (m *orgDomainsAssets) Stat(_ context.Context, key string) (blobstore.ObjectInfo, error) {
	if _, ok := m.objects[key]; !ok {
		return blobstore.ObjectInfo{}, blobstore.ErrNotFound
	}
	return blobstore.ObjectInfo{}, nil
}

type orgDomainsServerSettings struct{ values map[string]string }

func (s *orgDomainsServerSettings) Get(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *orgDomainsServerSettings) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

type orgDomainsFixture struct {
	pool       *pgxpool.Pool
	handler    *BloemOrganizationDomainsHandler
	defaultOrg uuid.UUID
	org2       uuid.UUID
	org3       uuid.UUID
}

func newOrgDomainsFixture(t *testing.T) orgDomainsFixture {
	t.Helper()
	ctx := context.Background()
	pool := newInvitationTestDatabase(t, ctx)
	f := orgDomainsFixture{pool: pool}
	if err := pool.QueryRow(ctx, `SELECT public.bloem_default_organization_id()`).Scan(&f.defaultOrg); err != nil {
		t.Fatal(err)
	}
	seed := func(slug string) uuid.UUID {
		var owner int
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash, role, enabled) VALUES ($1, $2, 'x', 'user', true) RETURNING id`, slug+"-owner", slug+"-owner@example.test").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ($1, $1, 'active', $2) RETURNING id`, slug, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id, name, is_default) VALUES ($1, $2, true)`, id, slug+" default"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role) VALUES ($1, $2, 'active', 'admin')
			ON CONFLICT (account_id) DO UPDATE SET organization_id = EXCLUDED.organization_id`, id, owner); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.org2, f.org3 = seed("main"), seed("other")
	store := orgdomains.NewStore(pool)
	directory := orgdomains.NewDirectory(store, time.Hour)
	brand := branding.NewService(orgdomains.NewSettingsOverlay(&orgDomainsServerSettings{values: map[string]string{}}, directory, store, orgdomains.OverlayOptions{}),
		&orgDomainsAssets{objects: map[string][]byte{}})
	f.handler = NewBloemOrganizationDomainsHandler(store, directory, brand)
	return f
}

// call serves one request to fn with chi path parameters and, unless claims is
// nil, the credential's claims.
func (f orgDomainsFixture) call(t *testing.T, fn http.HandlerFunc, method string, params map[string]string, body io.Reader, contentType string, claims *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/", body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if claims != nil {
		ctx = apimw.SetClaims(ctx, claims)
	}
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

var platformClaims = &auth.Claims{UserID: 1, SessionID: "s", TokenType: auth.TokenTypeAccess}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error
}

func TestOrganizationDomainsLifecycle(t *testing.T) {
	f := newOrgDomainsFixture(t)
	org := map[string]string{"id": f.org2.String()}
	for _, host := range []string{"main.domain.com", "watch.domain.com"} {
		if rec := f.call(t, f.handler.HandleAddDomain, http.MethodPost, org, jsonBody(map[string]any{"host": host}), "application/json", platformClaims); rec.Code != http.StatusCreated {
			t.Fatalf("add %s = %d %s", host, rec.Code, rec.Body)
		}
	}
	rec := f.call(t, f.handler.HandleListDomains, http.MethodGet, org, nil, "", platformClaims)
	var list BloemOrganizationDomainsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK || len(list.Domains) != 2 || list.Domains[0].Host != "main.domain.com" || !list.Domains[0].Primary {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	withHost := map[string]string{"id": f.org2.String(), "host": "watch.domain.com"}
	if rec := f.call(t, f.handler.HandleSetPrimaryDomain, http.MethodPut, withHost, nil, "", platformClaims); rec.Code != http.StatusNoContent {
		t.Fatalf("set primary = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, f.handler.HandleRemoveDomain, http.MethodDelete, map[string]string{"id": f.org2.String(), "host": "main.domain.com"}, nil, "", platformClaims); rec.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", rec.Code, rec.Body)
	}
	rec = f.call(t, f.handler.HandleListDomains, http.MethodGet, org, nil, "", platformClaims)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Domains) != 1 || list.Domains[0].Host != "watch.domain.com" || !list.Domains[0].Primary {
		t.Fatalf("after set primary and remove: %s", rec.Body)
	}
	if got, ok := f.handler.directory.Lookup(context.Background(), "watch.domain.com"); !ok || got != f.org2 {
		t.Fatal("the directory did not see the writes")
	}
	rec = f.call(t, f.handler.HandleAddDomain, http.MethodPost, map[string]string{"id": f.org3.String()}, jsonBody(map[string]any{"host": "watch.domain.com"}), "application/json", platformClaims)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "domain_taken" {
		t.Fatalf("taken = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, f.handler.HandleRemoveDomain, http.MethodDelete, map[string]string{"id": f.org3.String(), "host": "watch.domain.com"}, nil, "", platformClaims); rec.Code != http.StatusNotFound {
		t.Fatalf("removing another organization's domain = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, f.handler.HandleListDomains, http.MethodGet, map[string]string{"id": uuid.NewString()}, nil, "", platformClaims); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown organization = %d %s", rec.Code, rec.Body)
	}
}

func TestOrganizationDomainsInvalidHost(t *testing.T) {
	f := newOrgDomainsFixture(t)
	rec := f.call(t, f.handler.HandleAddDomain, http.MethodPost, map[string]string{"id": f.org2.String()}, jsonBody(map[string]any{"host": "https://main.domain.com"}), "application/json", platformClaims)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "invalid_host" {
		t.Fatalf("invalid host = %d %s", rec.Code, rec.Body)
	}
}

func TestDomainsSourceFollowsCredential(t *testing.T) {
	f := newOrgDomainsFixture(t)
	org := map[string]string{"id": f.org2.String()}
	f.call(t, f.handler.HandleAddDomain, http.MethodPost, org, jsonBody(map[string]any{"host": "a.domain.com"}), "application/json", platformClaims)
	f.call(t, f.handler.HandleAddDomain, http.MethodPost, org, jsonBody(map[string]any{"host": "b.domain.com"}), "application/json", &auth.Claims{UserID: 1, TokenType: auth.TokenTypeAPIKey})
	domains, err := orgdomains.NewStore(f.pool).ListDomains(context.Background(), f.org2)
	if err != nil || len(domains) != 2 || domains[0].Source != orgdomains.SourcePlatform || domains[1].Source != orgdomains.SourceGarden {
		t.Fatalf("domains = %+v, %v; want a from platform, b from garden", domains, err)
	}
}

func testBrandingPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{R: 30, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestOrganizationBrandingRoundTrip(t *testing.T) {
	f := newOrgDomainsFixture(t)
	org := map[string]string{"id": f.org2.String()}
	rec := f.call(t, f.handler.HandleUpdateBranding, http.MethodPut, org, jsonBody(map[string]any{"server_name": "Main", "accent_color": "#336699"}), "application/json", platformClaims)
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="mark.png"`)
	header.Set("Content-Type", "image/png")
	part, _ := mw.CreatePart(header)
	_, _ = part.Write(testBrandingPNG(t))
	_ = mw.Close()
	rec = f.call(t, f.handler.HandleUploadBrandingAsset, http.MethodPut, map[string]string{"id": f.org2.String(), "kind": "mark"}, &body, mw.FormDataContentType(), platformClaims)
	var uploaded BloemOrganizationBrandingAssetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil || rec.Code != http.StatusOK || uploaded.Ref == "" {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body)
	}
	rec = f.call(t, f.handler.HandleGetBranding, http.MethodGet, org, nil, "", platformClaims)
	var got BloemOrganizationBranding
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ServerName != "Main" || got.AccentColor != "#336699" || got.Assets["mark"] != uploaded.Ref {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	rec = f.call(t, f.handler.HandleUpdateBranding, http.MethodPut, org, jsonBody(map[string]any{"accent_color": "blue"}), "application/json", platformClaims)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "validation" {
		t.Fatalf("invalid accent color = %d %s", rec.Code, rec.Body)
	}
	rec = f.call(t, f.handler.HandleUpdateBranding, http.MethodPut, org, jsonBody(map[string]any{"server_name": ""}), "application/json", platformClaims)
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.ServerName != "" || got.AccentColor != "#336699" {
		t.Fatalf("clearing the name only = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, f.handler.HandleDeleteBrandingAsset, http.MethodDelete, map[string]string{"id": f.org2.String(), "kind": "mark"}, nil, "", platformClaims); rec.Code != http.StatusNoContent {
		t.Fatalf("delete asset = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(t, f.handler.HandleDeleteBrandingAsset, http.MethodDelete, map[string]string{"id": f.org2.String(), "kind": "nope"}, nil, "", platformClaims); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d %s", rec.Code, rec.Body)
	}
}

func TestDefaultOrganizationBrandingIsServerWide(t *testing.T) {
	f := newOrgDomainsFixture(t)
	org := map[string]string{"id": f.defaultOrg.String()}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"get":    f.call(t, f.handler.HandleGetBranding, http.MethodGet, org, nil, "", platformClaims),
		"update": f.call(t, f.handler.HandleUpdateBranding, http.MethodPut, org, strings.NewReader(`{"server_name":"x"}`), "application/json", platformClaims),
	} {
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "default_organization" {
			t.Errorf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := f.call(t, f.handler.HandleAddDomain, http.MethodPost, org, jsonBody(map[string]any{"host": "lib.example.org"}), "application/json", platformClaims); rec.Code != http.StatusCreated {
		t.Fatalf("the default organization can register its domain: %d %s", rec.Code, rec.Body)
	}
}

func TestCapabilityAdvertisesOrganizationDomains(t *testing.T) {
	h := NewBloemSystemHandler(nil)
	h.SetOrganizationDomainsAvailable(true)
	rec := httptest.NewRecorder()
	h.HandleCapabilities(rec, httptest.NewRequest(http.MethodGet, "/capabilities", nil))
	if !strings.Contains(rec.Body.String(), `"organization_domains_v1"`) {
		t.Fatalf("capabilities = %s", rec.Body)
	}
}
