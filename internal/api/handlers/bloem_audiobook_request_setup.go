package handlers

import (
	"context"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/go-chi/chi/v5"
)

type AudiobookRequestSetup struct {
	Available     bool                                       `json:"available"`
	Integrations  []bookrequests.AudiobookIntegrationSummary `json:"integrations"`
	Libraries     []bookrequests.AudiobookLibrary            `json:"libraries"`
	Installations []EbookRequestInstallation                 `json:"installations"`
}
type AudiobookRequestConnectionInput struct {
	Name           string            `json:"name"`
	InstallationID int               `json:"installation_id"`
	CapabilityID   string            `json:"capability_id"`
	PluginConfig   map[string]any    `json:"plugin_config"`
	Credentials    map[string]string `json:"credentials"`
}

// AudiobookRequestConnectionUpdate edits a saved connection. A nil
// plugin_config keeps the saved settings. A credential that is absent or empty
// keeps the saved value only while the URL it is bound to is unchanged; a
// cleared URL drops its credentials.
type AudiobookRequestConnectionUpdate struct {
	Name         string            `json:"name,omitempty"`
	PluginConfig map[string]any    `json:"plugin_config,omitempty"`
	Credentials  map[string]string `json:"credentials,omitempty"`
}
type BloemAudiobookRequestSetup interface {
	// List reads the setup. editor asks for each connection's saved
	// plugin_config and credential names; the setup returns them only when
	// the viewer also holds platform administrator authority.
	List(ctx context.Context, v bookrequests.Viewer, editor bool) (AudiobookRequestSetup, error)
	Create(context.Context, bookrequests.Viewer, AudiobookRequestConnectionInput) (bookrequests.AudiobookIntegrationSummary, error)
	Update(context.Context, bookrequests.Viewer, string, AudiobookRequestConnectionUpdate) (bookrequests.AudiobookIntegrationSummary, error)
	SetLibrary(context.Context, bookrequests.Viewer, string, int, []string) (bookrequests.AudiobookIntegrationSummary, error)
	Test(context.Context, bookrequests.Viewer, string) (RequestIntegrationTestResult, error)
}

func (h *BloemBookRequestsHandler) SetAudiobookRequestSetup(s BloemAudiobookRequestSetup) {
	if h != nil {
		h.audiobookSetup = s
	}
}
func (h *BloemBookRequestsHandler) audiobookSetupViewer(w http.ResponseWriter, r *http.Request, write bool) (bookrequests.Viewer, bool) {
	t, ok := requireBloemOrganizationContext(w, r)
	if !ok {
		return bookrequests.Viewer{}, false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if write {
		c, valid := apimw.GetAdminContextClaims(r.Context())
		if !valid || c.EffectiveAuthority != "platform_admin" {
			h.writeRequestError(w, bookrequests.ErrForbidden)
			return bookrequests.Viewer{}, false
		}
	}
	if h == nil || h.audiobookSetup == nil {
		h.unavailable(w)
		return bookrequests.Viewer{}, false
	}
	return bookrequests.Viewer{UserID: t.AccountID, OrganizationID: t.OrganizationID, Admin: true}, true
}

// HandleAudiobookIntegrationList answers any organization administrator.
// Saved settings and credential names are editor state: they are requested
// only for a platform administrator context, which alone may edit.
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationList(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, false)
	if !ok {
		return
	}
	c, valid := apimw.GetAdminContextClaims(r.Context())
	out, err := h.audiobookSetup.List(r.Context(), v, valid && c.EffectiveAuthority == "platform_admin")
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationCreate(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, true)
	if !ok {
		return
	}
	var in AudiobookRequestConnectionInput
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.audiobookSetup.Create(r.Context(), v, in)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationLibrary(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, true)
	if !ok {
		return
	}
	var in struct {
		LibraryID     int      `json:"library_id"`
		DownloadRoots []string `json:"download_roots"`
	}
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.audiobookSetup.SetLibrary(r.Context(), v, chi.URLParam(r, "id"), in.LibraryID, in.DownloadRoots)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationUpdate(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, true)
	if !ok {
		return
	}
	var in AudiobookRequestConnectionUpdate
	if !readBookRequestBody(w, r, &in) {
		return
	}
	out, err := h.audiobookSetup.Update(r.Context(), v, chi.URLParam(r, "id"), in)
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// HandleAudiobookIntegrationTest runs the plugin's connection checks against
// the saved connection. It sends stored credentials to their bound services,
// so it needs platform administrator authority like any other write.
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationTest(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, true)
	if !ok {
		return
	}
	out, err := h.audiobookSetup.Test(r.Context(), v, chi.URLParam(r, "id"))
	if err != nil {
		h.writeRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
