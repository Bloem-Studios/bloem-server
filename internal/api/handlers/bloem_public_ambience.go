package handlers

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/ambience"
)

// BloemPublicAmbienceHandler serves GET /api/bloem/v2/ambience/public: the
// deployment-wide seasonal packs active now, without a credential. The web
// app's signed-out seasonal layer reads it. The packs used to ride Silo's
// branding route on the retired alpha v1 API; Silo's /api/v2/theme/branding
// does not carry them. Bloem-owned.
type BloemPublicAmbienceHandler struct {
	branding *BrandingHandler
}

// BloemPublicAmbienceResponse is the active deployment-wide packs.
type BloemPublicAmbienceResponse struct {
	Ambience []ambience.Wire `json:"ambience"`
}

// NewBloemPublicAmbienceHandler serves source's active packs; a nil source
// serves an empty list.
func NewBloemPublicAmbienceHandler(source ambiencePublicSource) *BloemPublicAmbienceHandler {
	h := &BrandingHandler{}
	if source != nil {
		h.SetAmbience(source)
	}
	return &BloemPublicAmbienceHandler{branding: h}
}

func (h *BloemPublicAmbienceHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, BloemPublicAmbienceResponse{Ambience: h.branding.bloemPublicAmbience(r)})
}
