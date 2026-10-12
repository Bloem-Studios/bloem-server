package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The ordinary Silo create body remains the authority for library options.
// These two v2 options have no JSON names in its frozen v1 representation.
type BloemLibraryCreateOptions struct {
	LibraryCreateRequest
	RealtimeMonitoring *bool `json:"realtime_monitoring,omitempty"`
	TrickplayEnabled   bool  `json:"trickplay_enabled,omitempty"`
}
type BloemLibraryLocationsCreateRequest struct {
	Library        BloemLibraryCreateOptions     `json:"library"`
	Selections     []nativestorage.RootSelection `json:"selections" nullable:"false" maxItems:"128"`
	OrganizationID *uuid.UUID                    `json:"organization_id,omitempty"`
}
type BloemLibraryLocationsCreated struct {
	Library   LibraryView                    `json:"library"`
	Locations nativestorage.LibrarySelection `json:"locations"`
}
type BloemLibraryLocationsHandler struct {
	Libraries    *LibraryHandler
	Locations    *nativestorage.LibraryLocations
	Capabilities interface{ LibraryLocationEditingReady(context.Context) bool }
	scope        auth.AdminScope
}

func (h *BloemLibraryLocationsHandler) ForAdminScope(scope auth.AdminScope) *BloemLibraryLocationsHandler {
	if h == nil {
		return &BloemLibraryLocationsHandler{scope: scope}
	}
	copy := *h
	copy.scope = scope
	return &copy
}
func (h *BloemLibraryLocationsHandler) authority() *BloemNativeStorageManagementHandler {
	return &BloemNativeStorageManagementHandler{scope: h.scope}
}
func (h *BloemLibraryLocationsHandler) available(w http.ResponseWriter, r *http.Request) bool {
	if h == nil || h.Libraries == nil || h.Locations == nil || h.Capabilities == nil || !h.Capabilities.LibraryLocationEditingReady(r.Context()) {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: nativeStorageUnavailableCode}, false)
		return false
	}
	return true
}
func writeLibraryLocationsError(w http.ResponseWriter, err error) {
	if errors.Is(err, catalog.ErrDuplicatePath) {
		err = apiError(http.StatusConflict, "conflict", "A library with this path already exists")
	}
	var api *APIError
	if errors.As(err, &api) {
		nativeStorageWrite(w, api.Status, map[string]any{"error": api.Code, "message": api.Message})
		return
	}
	writeNativeStorageError(w, err, false)
}
func (h *BloemLibraryLocationsHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := h.authority().actor(w, r)
	if !ok {
		return
	}
	var req BloemLibraryLocationsCreateRequest
	fields, ok := h.authority().command(w, r, &req)
	if !ok {
		return
	}
	if req.Library.Paths == nil || req.Selections == nil || len(req.Library.Paths)+len(req.Selections) == 0 || req.Library.Name == "" || req.Library.Type == "" {
		writeNativeStorageError(w, nativeStorageInvalid(), false)
		return
	}
	if _, present := fields["organization_id"]; present && h.scope == auth.AdminScopeOrganization {
		writeNativeStorageError(w, nativeStorageInvalid(), false)
		return
	}
	if !h.available(w, r) {
		return
	}
	p, err := h.Locations.PrepareCreation(r.Context(), a, req.OrganizationID, len(req.Library.Paths) > 0, req.Selections)
	if err != nil {
		writeLibraryLocationsError(w, err)
		return
	}
	options := req.Library.LibraryCreateRequest
	options.RealtimeMonitoring = req.Library.RealtimeMonitoring
	options.TrickplayEnabled = req.Library.TrickplayEnabled
	library, err := h.Libraries.CreateLibraryWithAttachment(r.Context(), options, func(ctx context.Context, tx pgx.Tx, id int) error {
		return h.Locations.AttachCreationTx(ctx, tx, a, id, p)
	})
	if err != nil {
		writeLibraryLocationsError(w, err)
		return
	}
	locations, err := h.Locations.ReadSelections(r.Context(), a, library.ID)
	if err != nil {
		writeLibraryLocationsError(w, &catalog.MutationOutcomeUnknown{OperationID: uuid.New(), LibraryID: library.ID, Operation: "create_library", Cause: err})
		return
	}
	nativeStorageWrite(w, http.StatusCreated, BloemLibraryLocationsCreated{Library: library, Locations: locations})
}
func libraryLocationsPathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := nativeStoragePositiveID(chi.URLParam(r, "library_id"))
	if err != nil {
		writeNativeStorageError(w, &catalog.NativeOnboardingError{Code: "not_found"}, false)
		return 0, false
	}
	return id, true
}
func (h *BloemLibraryLocationsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	a, ok := h.authority().actor(w, r)
	if !ok {
		return
	}
	if !h.authority().readRequest(w, r) {
		return
	}
	id, ok := libraryLocationsPathID(w, r)
	if !ok {
		return
	}
	if !h.available(w, r) {
		return
	}
	view, err := h.Locations.ReadSelections(r.Context(), a, id)
	if err != nil {
		writeLibraryLocationsError(w, err)
		return
	}
	nativeStorageWrite(w, http.StatusOK, view)
}
func (h *BloemLibraryLocationsHandler) HandleReplace(w http.ResponseWriter, r *http.Request) {
	a, ok := h.authority().actor(w, r)
	if !ok {
		return
	}
	var req nativestorage.ReplaceLocationsRequest
	if _, ok := h.authority().command(w, r, &req); !ok {
		return
	}
	id, ok := libraryLocationsPathID(w, r)
	if !ok {
		return
	}
	if len(req.Paths) > 0 {
		paths, err := normalizeLibraryPaths(req.Paths)
		if err != nil {
			writeLibraryLocationsError(w, err)
			return
		}
		req.Paths = paths
	}
	if !h.available(w, r) {
		return
	}
	result, err := h.Locations.ReplaceSelections(r.Context(), a, id, req)
	if err != nil {
		writeLibraryLocationsError(w, err)
		return
	}
	h.Libraries.pokeRealtimeMonitor()
	// The same follow-up as Silo's UpdateLibrary, after a confirmed commit only.
	if !slices.Equal(result.PreviousPaths, result.Paths) {
		if folder, err := h.Libraries.folderRepo.GetByID(r.Context(), id); err != nil {
			slog.WarnContext(r.Context(), "load library for path-change scan failed", "component", "api", "library_id", id, "error", err)
		} else {
			h.Libraries.queuePathChangeScan(r.Context(), folder)
		}
	}
	nativeStorageWrite(w, http.StatusOK, result.LibrarySelection)
}

// explicitAttachmentError reports whether an explicit attachment's caller
// receives err unchanged: a domain refusal is its answer, and an unknown
// commit outcome is logged here before the caller reconciles it.
func explicitAttachmentError(ctx context.Context, err error) bool {
	var native *catalog.NativeOnboardingError
	if errors.As(err, &native) {
		return true
	}
	var unknown *catalog.MutationOutcomeUnknown
	if errors.As(err, &unknown) {
		slog.ErrorContext(ctx, "creating library", "component", "api", "library_id", unknown.LibraryID, "operation_id", unknown.OperationID, "error", unknown.Cause)
		return true
	}
	return false
}

// libraryHasStorageLocations reports whether a library retains any plugin
// storage location. The scalar StorageSourceKey covers only one location
// without filesystem paths, so mixed and multi-location libraries are read
// through the location collection.
func (h *LibraryHandler) libraryHasStorageLocations(ctx context.Context, folder *models.MediaFolder) (bool, error) {
	if folder.StorageSourceKey != nil {
		return true, nil
	}
	if h.pool == nil {
		return false, nil
	}
	locations, err := storagesource.NewRepository(h.pool).LocationSelections(ctx, folder.ID)
	return len(locations) > 0, err
}
