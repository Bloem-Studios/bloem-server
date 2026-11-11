package handlers

// Bloem-owned. An organization's domains and branding, managed from the
// platform routes under /api/bloem/v2/admin/platform/tenants/{id}: by the
// platform administrator's web pages and by the storefront (Bloem Garden)
// with its API key. See docs/architecture/bloem-organization-domains.md.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/branding"
	"github.com/Silo-Server/silo-server/internal/orgdomains"
)

// BloemOrganizationDomainsHandler serves an organization's domains and
// branding. A nil brand service leaves asset uploads unavailable.
type BloemOrganizationDomainsHandler struct {
	store     *orgdomains.Store
	directory *orgdomains.Directory
	brand     *branding.Service
}

func NewBloemOrganizationDomainsHandler(store *orgdomains.Store, directory *orgdomains.Directory, brand *branding.Service) *BloemOrganizationDomainsHandler {
	return &BloemOrganizationDomainsHandler{store: store, directory: directory, brand: brand}
}

// BloemOrganizationDomain is one host registered to an organization.
type BloemOrganizationDomain struct {
	Host           string    `json:"host" doc:"The registered host, lower case." example:"main.example.com"`
	OrganizationID uuid.UUID `json:"organization_id" doc:"The organization the host belongs to."`
	Primary        bool      `json:"primary" doc:"Whether this is the organization's primary domain, the address its links use."`
	Source         string    `json:"source" enum:"platform,garden" doc:"Who registered it: the platform administrator, or the storefront with its API key."`
	CreatedAt      time.Time `json:"created_at" doc:"When it was registered."`
}

func bloemOrganizationDomain(d orgdomains.Domain) BloemOrganizationDomain {
	return BloemOrganizationDomain{Host: d.Host, OrganizationID: d.OrganizationID, Primary: d.Primary, Source: d.Source, CreatedAt: d.CreatedAt}
}

// BloemOrganizationDomainsResponse lists an organization's domains, the
// primary first.
type BloemOrganizationDomainsResponse struct {
	Domains []BloemOrganizationDomain `json:"domains"`
}

// BloemOrganizationDomainResponse is one registered domain.
type BloemOrganizationDomainResponse struct {
	Domain BloemOrganizationDomain `json:"domain"`
}

// BloemOrganizationDomainRequest registers a domain.
type BloemOrganizationDomainRequest struct {
	Host    string `json:"host"`
	Primary bool   `json:"primary"`
}

// BloemOrganizationBranding is an organization's own branding. An empty value
// is Bloem's built-in default on the organization's domains; Assets maps each
// uploaded asset kind to its reference.
type BloemOrganizationBranding struct {
	ServerName    string            `json:"server_name"`
	LoginSubtitle string            `json:"login_subtitle"`
	AccentColor   string            `json:"accent_color"`
	DefaultTheme  string            `json:"default_theme"`
	Assets        map[string]string `json:"assets"`
}

// BloemOrganizationBrandingUpdate changes the fields it names; an empty
// string clears one.
type BloemOrganizationBrandingUpdate struct {
	ServerName    *string `json:"server_name,omitempty"`
	LoginSubtitle *string `json:"login_subtitle,omitempty"`
	AccentColor   *string `json:"accent_color,omitempty"`
	DefaultTheme  *string `json:"default_theme,omitempty"`
}

// BloemOrganizationBrandingAssetResponse is an uploaded asset's reference.
type BloemOrganizationBrandingAssetResponse struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

var (
	accentColorPattern  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	defaultThemePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
)

// organization is the path's organization; false once a 404 is written.
func (h *BloemOrganizationDomainsHandler) organization(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	org, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such organization")
		return uuid.Nil, false
	}
	exists, err := h.store.OrganizationExists(r.Context(), org)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load the organization")
		return uuid.Nil, false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "No such organization")
		return uuid.Nil, false
	}
	return org, true
}

// brandable is organization for the branding routes, which refuse the
// default organization: it uses the server's branding settings.
func (h *BloemOrganizationDomainsHandler) brandable(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	org, ok := h.organization(w, r)
	if !ok {
		return uuid.Nil, false
	}
	if h.directory.IsDefault(r.Context(), org) {
		writeError(w, http.StatusConflict, "default_organization", "The default organization uses the server's branding settings")
		return uuid.Nil, false
	}
	return org, true
}

func (h *BloemOrganizationDomainsHandler) writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orgdomains.ErrInvalidHost):
		writeError(w, http.StatusUnprocessableEntity, "invalid_host", "A host is a lower-case name with at least one dot, without scheme, port or path")
	case errors.Is(err, orgdomains.ErrDomainTaken):
		writeError(w, http.StatusConflict, "domain_taken", "The host is already registered")
	case errors.Is(err, orgdomains.ErrDomainNotFound):
		writeError(w, http.StatusNotFound, "not_found", "The organization has no such domain")
	case errors.Is(err, orgdomains.ErrOrganizationNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such organization")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to change the organization's domains")
	}
}

// HandleListDomains handles GET /admin/platform/tenants/{id}/domains.
func (h *BloemOrganizationDomainsHandler) HandleListDomains(w http.ResponseWriter, r *http.Request) {
	org, ok := h.organization(w, r)
	if !ok {
		return
	}
	domains, err := h.store.ListDomains(r.Context(), org)
	if err != nil {
		h.writeDomainError(w, err)
		return
	}
	out := make([]BloemOrganizationDomain, 0, len(domains))
	for _, d := range domains {
		out = append(out, bloemOrganizationDomain(d))
	}
	writeJSON(w, http.StatusOK, BloemOrganizationDomainsResponse{Domains: out})
}

// HandleAddDomain handles POST /admin/platform/tenants/{id}/domains. The
// storefront's API key records the domain as Garden's; any other credential
// as the platform administrator's.
func (h *BloemOrganizationDomainsHandler) HandleAddDomain(w http.ResponseWriter, r *http.Request) {
	org, ok := h.organization(w, r)
	if !ok {
		return
	}
	var req BloemOrganizationDomainRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "The body is not valid JSON")
		return
	}
	source := orgdomains.SourcePlatform
	if claims := apimw.GetClaims(r.Context()); claims != nil && claims.TokenType == auth.TokenTypeAPIKey {
		source = orgdomains.SourceGarden
	}
	domain, err := h.store.AddDomain(r.Context(), org, req.Host, source, req.Primary)
	if err != nil {
		h.writeDomainError(w, err)
		return
	}
	h.directory.Invalidate()
	writeJSON(w, http.StatusCreated, BloemOrganizationDomainResponse{Domain: bloemOrganizationDomain(domain)})
}

// HandleSetPrimaryDomain handles PUT /admin/platform/tenants/{id}/domains/{host}/primary.
func (h *BloemOrganizationDomainsHandler) HandleSetPrimaryDomain(w http.ResponseWriter, r *http.Request) {
	org, ok := h.organization(w, r)
	if !ok {
		return
	}
	if err := h.store.SetPrimary(r.Context(), org, chi.URLParam(r, "host")); err != nil {
		h.writeDomainError(w, err)
		return
	}
	h.directory.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// HandleRemoveDomain handles DELETE /admin/platform/tenants/{id}/domains/{host}.
func (h *BloemOrganizationDomainsHandler) HandleRemoveDomain(w http.ResponseWriter, r *http.Request) {
	org, ok := h.organization(w, r)
	if !ok {
		return
	}
	if err := h.store.RemoveDomain(r.Context(), org, chi.URLParam(r, "host")); err != nil {
		h.writeDomainError(w, err)
		return
	}
	h.directory.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}

func (h *BloemOrganizationDomainsHandler) brandingOf(r *http.Request, org uuid.UUID) (BloemOrganizationBranding, error) {
	values, err := h.store.BrandingValues(r.Context(), org)
	if err != nil {
		return BloemOrganizationBranding{}, err
	}
	out := BloemOrganizationBranding{
		ServerName:    values[branding.KeyServerName],
		LoginSubtitle: values[branding.KeyLoginSubtitle],
		AccentColor:   values[branding.KeyAccentColor],
		DefaultTheme:  values[branding.KeyDefaultTheme],
		Assets:        map[string]string{},
	}
	for _, kind := range branding.AssetKinds() {
		if key, ok := branding.AssetSettingKey(kind); ok && values[key] != "" {
			out.Assets[string(kind)] = values[key]
		}
	}
	return out, nil
}

// HandleGetBranding handles GET /admin/platform/tenants/{id}/branding.
func (h *BloemOrganizationDomainsHandler) HandleGetBranding(w http.ResponseWriter, r *http.Request) {
	org, ok := h.brandable(w, r)
	if !ok {
		return
	}
	out, err := h.brandingOf(r, org)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load the organization's branding")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// validBrandingText is text without control characters, at most limit runes.
func validBrandingText(value string, limit int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// HandleUpdateBranding handles PUT /admin/platform/tenants/{id}/branding.
func (h *BloemOrganizationDomainsHandler) HandleUpdateBranding(w http.ResponseWriter, r *http.Request) {
	org, ok := h.brandable(w, r)
	if !ok {
		return
	}
	var req BloemOrganizationBrandingUpdate
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "The body is not valid JSON")
		return
	}
	type change struct {
		key   string
		value *string
		valid func(string) bool
		rule  string
	}
	changes := []change{
		{branding.KeyServerName, req.ServerName, func(v string) bool { return validBrandingText(v, 80) }, "server_name is at most 80 characters, without control characters"},
		{branding.KeyLoginSubtitle, req.LoginSubtitle, func(v string) bool { return validBrandingText(v, 200) }, "login_subtitle is at most 200 characters, without control characters"},
		{branding.KeyAccentColor, req.AccentColor, accentColorPattern.MatchString, "accent_color is a color such as #336699"},
		{branding.KeyDefaultTheme, req.DefaultTheme, defaultThemePattern.MatchString, "default_theme is a theme name such as dark"},
	}
	for _, c := range changes {
		if c.value != nil && *c.value != "" && !c.valid(*c.value) {
			writeError(w, http.StatusUnprocessableEntity, "validation", c.rule)
			return
		}
	}
	for _, c := range changes {
		if c.value == nil {
			continue
		}
		if err := h.store.SetBrandingValue(r.Context(), org, c.key, *c.value); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to save the organization's branding")
			return
		}
	}
	h.directory.Invalidate()
	out, err := h.brandingOf(r, org)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load the organization's branding")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleUploadBrandingAsset handles PUT /admin/platform/tenants/{id}/branding/assets/{kind},
// with the limits and answers of Silo's branding upload.
func (h *BloemOrganizationDomainsHandler) HandleUploadBrandingAsset(w http.ResponseWriter, r *http.Request) {
	org, ok := h.brandable(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	if !branding.IsValidKind(kind) {
		writeError(w, http.StatusBadRequest, "bad_request", "Unknown branding asset")
		return
	}
	if h.brand == nil || !h.brand.HasStorage() {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Asset upload storage is not configured")
		return
	}
	maxBytes := branding.MaxUploadBytes(branding.AssetKind(kind))
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<20))
	if err := r.ParseMultipartForm(maxBytes + (1 << 20)); err != nil { //nolint:gosec // the body is capped by http.MaxBytesReader just above, as in Silo's branding upload
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read upload")
		return
	}
	if int64(len(data)) > maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Image exceeds the maximum size for this asset")
		return
	}
	ref, err := h.brand.UploadAsset(orgdomains.WithBrandingWrite(r.Context(), org), branding.AssetKind(kind), data, header.Header.Get("Content-Type"))
	switch {
	case errors.Is(err, branding.ErrUnsupportedImage):
		writeError(w, http.StatusBadRequest, "bad_request", "Unsupported image type; use PNG, JPEG, WebP (or PNG/ICO/SVG for favicon)")
		return
	case errors.Is(err, branding.ErrStorageUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Asset storage is not configured")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to store asset")
		return
	}
	writeJSON(w, http.StatusOK, BloemOrganizationBrandingAssetResponse{Kind: kind, Ref: ref})
}

// HandleDeleteBrandingAsset handles DELETE /admin/platform/tenants/{id}/branding/assets/{kind}.
func (h *BloemOrganizationDomainsHandler) HandleDeleteBrandingAsset(w http.ResponseWriter, r *http.Request) {
	org, ok := h.brandable(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	key, valid := branding.AssetSettingKey(branding.AssetKind(kind))
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request", "Unknown branding asset")
		return
	}
	if err := h.store.SetBrandingValue(r.Context(), org, key, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove asset")
		return
	}
	h.directory.Invalidate()
	w.WriteHeader(http.StatusNoContent)
}
