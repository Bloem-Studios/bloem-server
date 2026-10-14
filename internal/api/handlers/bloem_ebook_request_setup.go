package handlers

import (
	"context"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
)

type EbookRequestInstallation struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	CapabilityID string `json:"capability_id"`
}
type EbookRequestSetup struct {
	Available     bool                            `json:"available"`
	Enabled       bool                            `json:"enabled"`
	Integrations  []bookrequests.EbookIntegration `json:"integrations"`
	Libraries     []bookrequests.EbookLibrary     `json:"libraries"`
	Installations []EbookRequestInstallation      `json:"installations"`
}
type EbookRequestConnectionInput struct {
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	InstallationID int    `json:"installation_id"`
	CapabilityID   string `json:"capability_id"`
	AllowHTTP      bool   `json:"allow_http,omitempty"`
}

// RequestIntegrationTestResult is a request plugin's connection test verdict.
// A failed test is still a 200 response: the test ran and reported failure.
// The message is plugin or host text with credentials withheld.
type RequestIntegrationTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type BloemEbookRequestSetup interface {
	List(context.Context, bookrequests.Viewer) (EbookRequestSetup, error)
	Create(context.Context, bookrequests.Viewer, EbookRequestConnectionInput) (bookrequests.EbookIntegration, error)
	SetLibrary(context.Context, bookrequests.Viewer, string, int) (bookrequests.EbookIntegration, error)
	SetEnabled(context.Context, bookrequests.Viewer, bool) error
	Test(context.Context, bookrequests.Viewer, string) (RequestIntegrationTestResult, error)
}

type EbookRequestPolicy struct {
	Enabled bool `json:"enabled"`
}

func (h *BloemBookRequestsHandler) HandleEbookRequestPolicy(w http.ResponseWriter, r *http.Request) {
	v, ok := h.ebookSetupViewer(w, r)
	if !ok {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !readBookRequestBody(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		h.writeRequestError(w, bookrequests.ErrInvalid)
		return
	}
	if err := h.ebookSetup.SetEnabled(r.Context(), v, *in.Enabled); err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, EbookRequestPolicy{Enabled: *in.Enabled})
}

func (h *BloemBookRequestsHandler) SetEbookRequestSetup(setup BloemEbookRequestSetup) {
	if h != nil && h.ebooks != nil {
		h.ebooks.ebookSetup = setup
	}
}

func (h *BloemBookRequestsHandler) HandleEbookIntegrationList(w http.ResponseWriter, r *http.Request) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if !t.OrganizationDefault {
		writeJSON(w, http.StatusOK, EbookRequestSetup{Integrations: []bookrequests.EbookIntegration{}, Libraries: []bookrequests.EbookLibrary{}, Installations: []EbookRequestInstallation{}})
		return
	}
	if h == nil || h.ebookSetup == nil {
		h.unavailable(w)
		return
	}
	out, err := h.ebookSetup.List(r.Context(), bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true})
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *BloemBookRequestsHandler) ebookSetupViewer(w http.ResponseWriter, r *http.Request) (bookrequests.Viewer, bool) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return bookrequests.Viewer{}, false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	claims, valid := apimw.GetAdminContextClaims(r.Context())
	if !valid || claims.EffectiveAuthority != "platform_admin" || !t.OrganizationDefault {
		writeError(w, http.StatusForbidden, "request_forbidden", "Platform administrator authority in the default organization is required")
		return bookrequests.Viewer{}, false
	}
	if h == nil || h.ebookSetup == nil {
		h.unavailable(w)
		return bookrequests.Viewer{}, false
	}
	return bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true}, true
}

func (h *BloemBookRequestsHandler) HandleEbookIntegrationCreate(w http.ResponseWriter, r *http.Request) {
	v, ok := h.ebookSetupViewer(w, r)
	if !ok {
		return
	}
	var in EbookRequestConnectionInput
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.ebookSetup.Create(r.Context(), v, in)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *BloemBookRequestsHandler) HandleEbookIntegrationLibrary(w http.ResponseWriter, r *http.Request) {
	v, ok := h.ebookSetupViewer(w, r)
	if !ok {
		return
	}
	var in struct {
		LibraryID int `json:"library_id"`
	}
	if !readBookRequestBody(w, r, &in) {
		return
	}
	if in.LibraryID < 0 {
		h.writeRequestError(w, bookrequests.ErrInvalid)
		return
	}
	out, err := h.ebookSetup.SetLibrary(r.Context(), v, chi.URLParam(r, "id"), in.LibraryID)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleEbookIntegrationTest runs the plugin's connection test against the
// saved connection. The request has no body; credentials never leave the host
// except to the plugin, and never appear in the response.
func (h *BloemBookRequestsHandler) HandleEbookIntegrationTest(w http.ResponseWriter, r *http.Request) {
	v, ok := h.ebookSetupViewer(w, r)
	if !ok {
		return
	}
	out, err := h.ebookSetup.Test(r.Context(), v, chi.URLParam(r, "id"))
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
