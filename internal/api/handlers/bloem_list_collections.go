package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/listcollections"
)

type BloemListConnection struct {
	InstallationID  int    `json:"installation_id"`
	Origin          string `json:"origin"`
	CanCreateServer bool   `json:"can_create_server"`
}
type BloemListCapability struct {
	Available   bool                  `json:"available"`
	Connections []BloemListConnection `json:"connections"`
}
type BloemListPreviewCommand struct {
	InstallationID int    `json:"installation_id"`
	URL            string `json:"url"`
}
type BloemListCreateCommand struct {
	PreviewID  string `json:"preview_id"`
	Scope      string `json:"scope"`
	Name       string `json:"name"`
	IsShared   bool   `json:"is_shared"`
	LibraryIDs []int  `json:"library_ids,omitempty"`
}
type BloemListBindingStatus struct {
	ID          string                        `json:"id"`
	SourceID    string                        `json:"source_id"`
	TargetID    string                        `json:"target_id"`
	Scope       string                        `json:"scope"`
	Status      string                        `json:"status"`
	Generation  uint64                        `json:"generation"`
	LastSuccess *time.Time                    `json:"last_success"`
	Matched     int                           `json:"matched"`
	Unmatched   []listcollections.Unmatched   `json:"unmatched"`
	Failure     string                        `json:"failure"`
	Requests    listcollections.RequestCounts `json:"requests"`
}
type BloemListCollectionBackend interface {
	Connections(context.Context, int, string) ([]BloemListConnection, error)
	Subject(context.Context, int, string, int, string, string) (listcollections.Subject, error)
	ValidateURL(string) error
}
type BloemListCollectionsHandler struct {
	Service *listcollections.Service
	Backend BloemListCollectionBackend
}

func (h *BloemListCollectionsHandler) viewer(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	claims := apimw.GetClaims(r.Context())
	profile := apimw.GetProfileID(r.Context())
	scope, ok := access.GetScope(r.Context())
	if claims == nil || claims.UserID < 1 {
		writeError(w, 401, "unauthorized", "Authentication required")
		return 0, "", false
	}
	if !ok || profile == "" || scope.UserID != claims.UserID || scope.ProfileID != profile || !scope.ProfileVerified || scope.PINVerificationSkipped {
		writeError(w, 403, "viewer_required", "Select and verify a profile")
		return 0, "", false
	}
	return claims.UserID, profile, true
}
func listCollectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, listcollections.ErrSnapshot):
		writeError(w, 400, "invalid_list_source", "Check the list URL and collection settings")
	case errors.Is(err, listcollections.ErrConflict):
		writeError(w, 409, "list_conflict", "The list changed; refresh the preview and try again")
	case errors.Is(err, listcollections.ErrExpired):
		writeError(w, 409, "preview_expired", "Refresh the expired preview")
	case errors.Is(err, listcollections.ErrAuthority):
		writeError(w, 403, "list_denied", "This list connection or collection is unavailable to this profile")
	default:
		writeError(w, 503, "list_unavailable", "List synchronization is temporarily unavailable")
	}
}
func listBody(w http.ResponseWriter, r *http.Request, out any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		listCollectionError(w, listcollections.ErrSnapshot)
		return false
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		listCollectionError(w, listcollections.ErrSnapshot)
		return false
	}
	return true
}
func (h *BloemListCollectionsHandler) HandleCapabilities(w http.ResponseWriter, r *http.Request) {
	user, profile, ok := h.viewer(w, r)
	if !ok {
		return
	}
	out := BloemListCapability{Connections: []BloemListConnection{}}
	if h.Backend != nil && h.Service != nil {
		connections, err := h.Backend.Connections(r.Context(), user, profile)
		if err != nil {
			listCollectionError(w, err)
			return
		}
		out.Connections = connections
		out.Available = len(connections) > 0
	}
	writeJSON(w, 200, out)
}
func (h *BloemListCollectionsHandler) subject(w http.ResponseWriter, r *http.Request, installation int, id, resource string) (listcollections.Subject, bool) {
	user, profile, ok := h.viewer(w, r)
	if !ok {
		return listcollections.Subject{}, false
	}
	if h.Service == nil || h.Backend == nil {
		listCollectionError(w, errors.New("unconfigured"))
		return listcollections.Subject{}, false
	}
	subject, err := h.Backend.Subject(r.Context(), user, profile, installation, id, resource)
	if err != nil {
		listCollectionError(w, err)
		return subject, false
	}
	return subject, true
}
func (h *BloemListCollectionsHandler) HandlePreview(w http.ResponseWriter, r *http.Request) {
	var in BloemListPreviewCommand
	if !listBody(w, r, &in) {
		return
	}
	s, ok := h.subject(w, r, in.InstallationID, "", "preview")
	if !ok {
		return
	}
	if err := h.Backend.ValidateURL(in.URL); err != nil {
		listCollectionError(w, err)
		return
	}
	job, err := h.Service.QueuePreview(r.Context(), s, listcollections.PreviewInput{URL: in.URL})
	if err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}
func (h *BloemListCollectionsHandler) HandleGetPreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.subject(w, r, 0, id, "preview")
	if !ok {
		return
	}
	out, err := h.Service.GetPreview(r.Context(), s, id)
	if err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func listBindingStatus(b listcollections.Binding, c listcollections.RequestCounts) BloemListBindingStatus {
	if b.Unmatched == nil {
		b.Unmatched = []listcollections.Unmatched{}
	}
	return BloemListBindingStatus{ID: b.ID, SourceID: b.SourceID, TargetID: b.TargetID, Scope: b.Scope, Status: b.Status, Generation: b.Generation, LastSuccess: b.LastSuccess, Matched: b.Matched, Unmatched: b.Unmatched, Failure: b.Failure, Requests: c}
}
func (h *BloemListCollectionsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var in BloemListCreateCommand
	if !listBody(w, r, &in) {
		return
	}
	s, ok := h.subject(w, r, 0, in.PreviewID, "preview")
	if !ok {
		return
	}
	b, err := h.Service.CreateBinding(r.Context(), s, listcollections.CreateInput{PreviewID: in.PreviewID, Scope: in.Scope, Name: in.Name, IsShared: in.IsShared, LibraryIDs: in.LibraryIDs})
	if err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 202, listBindingStatus(b, listcollections.RequestCounts{}))
}
func (h *BloemListCollectionsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.subject(w, r, 0, id, "binding")
	if !ok {
		return
	}
	b, c, err := h.Service.GetBinding(r.Context(), s, id)
	if err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 200, listBindingStatus(b, c))
}
func (h *BloemListCollectionsHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.subject(w, r, 0, id, "binding")
	if !ok {
		return
	}
	job, err := h.Service.Refresh(r.Context(), s, id)
	if err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"job_id": job.ID})
}
func (h *BloemListCollectionsHandler) HandleDisconnect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.subject(w, r, 0, id, "binding")
	if !ok {
		return
	}
	if err := h.Service.Disconnect(r.Context(), s, id); err != nil {
		listCollectionError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "detached"})
}
