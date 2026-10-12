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
type BloemAudiobookRequestSetup interface {
	List(context.Context, bookrequests.Viewer) (AudiobookRequestSetup, error)
	Create(context.Context, bookrequests.Viewer, AudiobookRequestConnectionInput) (bookrequests.AudiobookIntegrationSummary, error)
	SetLibrary(context.Context, bookrequests.Viewer, string, int, []string) (bookrequests.AudiobookIntegrationSummary, error)
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
func (h *BloemBookRequestsHandler) HandleAudiobookIntegrationList(w http.ResponseWriter, r *http.Request) {
	v, ok := h.audiobookSetupViewer(w, r, false)
	if !ok {
		return
	}
	out, err := h.audiobookSetup.List(r.Context(), v)
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
