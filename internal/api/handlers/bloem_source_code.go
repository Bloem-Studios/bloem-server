package handlers

import (
	"log/slog"
	"mime"
	"net/http"
	"os"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemsource"
)

// SourceCodeCapabilityToken advertises the source code routes in
// /api/bloem/v2/capabilities.
const SourceCodeCapabilityToken = "source_code_v1"

// SourceCodeArchivePath is where a signed-in user downloads the archive.
const SourceCodeArchivePath = NativeAPIPrefix + "/source-code/archive"

// BloemSourceCodeHandler offers the running build's complete source code to
// every signed-in user, as the AGPL asks of a modified version people use over
// a network (internal/bloemsource).
type BloemSourceCodeHandler struct {
	offer *bloemsource.Offer
}

// NewBloemSourceCodeHandler returns nil when the server has no offer, which
// leaves the routes unmounted.
func NewBloemSourceCodeHandler(offer *bloemsource.Offer) *BloemSourceCodeHandler {
	if offer == nil {
		return nil
	}
	return &BloemSourceCodeHandler{offer: offer}
}

// Available reports whether the routes should be mounted.
func (h *BloemSourceCodeHandler) Available() bool { return h != nil && h.offer != nil }

type bloemSourceCodeResponse struct {
	License     string `json:"license"`
	Revision    string `json:"revision"`
	ArchiveName string `json:"archive_name"`
	ArchivePath string `json:"archive_path"`
	SizeBytes   int64  `json:"size_bytes"`
}

// HandleGet describes the archive of the running version.
func (h *BloemSourceCodeHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	archive, ok := h.archive(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, bloemSourceCodeResponse{
		License:     bloemsource.License,
		Revision:    h.offer.Revision(),
		ArchiveName: archive.Name,
		ArchivePath: SourceCodeArchivePath,
		SizeBytes:   archive.Size,
	})
}

// HandleArchive sends the archive of the running version. It supports range
// requests, so a broken download can resume.
func (h *BloemSourceCodeHandler) HandleArchive(w http.ResponseWriter, r *http.Request) {
	archive, ok := h.archive(w, r)
	if !ok {
		return
	}
	file, err := os.Open(archive.Path)
	if err != nil {
		slog.ErrorContext(r.Context(), "source code archive could not be opened", "error", err)
		writeSourceCodeUnavailable(w)
		return
	}
	defer file.Close()
	// The archive is a few hundred megabytes, longer to send on a slow link
	// than the server's write timeout allows a response.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.WarnContext(r.Context(), "source code download keeps the server write timeout", "error", err)
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": archive.Name}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, archive.Name, archive.UpdatedAt, file)
}

func (h *BloemSourceCodeHandler) archive(w http.ResponseWriter, r *http.Request) (bloemsource.Archive, bool) {
	archive, err := h.offer.Archive()
	if err != nil {
		slog.ErrorContext(r.Context(), "source code archive unavailable", "error", err)
		writeSourceCodeUnavailable(w)
		return bloemsource.Archive{}, false
	}
	return archive, true
}

func writeSourceCodeUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "source_unavailable", "The source code of this version is not available right now")
}
