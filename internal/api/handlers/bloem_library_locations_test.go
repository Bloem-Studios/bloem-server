package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"context"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLibraryLocationsRequiresAdminBeforeBody(t *testing.T) {
	h := (&BloemLibraryLocationsHandler{}).ForAdminScope(auth.AdminScopePlatform)
	for _, call := range []http.HandlerFunc{h.HandleCreate, h.HandleGet, h.HandleReplace} {
		r := httptest.NewRequest("POST", "/", nil)
		r.Body = nativeUnreadBody{t}
		w := httptest.NewRecorder()
		call(w, r)
		assertNativeError(t, w, 401, "tenant_session_required")
	}
}
func TestLibraryLocationsRequestBoundaries(t *testing.T) {
	for _, raw := range []string{
		`{"library":{"name":"x","type":"ebooks","paths":[],"storage_source":"00000000-0000-0000-0000-000000000001"},"selections":[]}`,
		`{"library":{"name":"x","type":"ebooks","unknown":true},"selections":[]}`,
		`{"library":{"name":"x","name":"other","type":"ebooks"},"selections":[]}`,
		`{"library":{"name":"x","type":"ebooks"},"selections":[{"source_key":"bad"}]}`,
	} {
		var req BloemLibraryLocationsCreateRequest
		if _, err := nativeStorageDecodeJSON([]byte(raw), &req); err == nil {
			t.Fatalf("invalid mixed body accepted: %s", raw)
		}
	}
	var req BloemLibraryLocationsCreateRequest
	if _, err := nativeStorageDecodeJSON([]byte(`{"library":{"name":"x","type":"ebooks","paths":["/media"],"realtime_monitoring":false,"trickplay_enabled":false},"selections":[]}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Library.RealtimeMonitoring == nil || *req.Library.RealtimeMonitoring || req.Selections == nil {
		t.Fatal("create options lost")
	}
}

func TestLibraryLocationsCreateRequiresDocumentedArrays(t *testing.T) {
	h := (&BloemLibraryLocationsHandler{}).ForAdminScope(auth.AdminScopePlatform)
	a := auth.AdminContextClaims{AccountID: 1, AccountIncarnationID: uuid.New(), SessionID: "fixture-session", Scope: auth.AdminScopePlatform}
	for _, paths := range []string{"", `,"paths":null`} {
		raw := `{"library":{"name":"x","type":"ebooks"` + paths + `},"selections":[{"source_key":"00000000-0000-0000-0000-000000000201","configuration_revision":1,"selection_ref":"host-reference"}]}`
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(middleware.SetAdminContextClaims(r.Context(), a))
		w := httptest.NewRecorder()
		h.HandleCreate(w, r)
		assertNativeError(t, w, http.StatusBadRequest, "invalid_request")
	}
}
func TestLibraryLocationsErrorRevisionAndUnknownOutcome(t *testing.T) {
	revision := int64(7)
	w := httptest.NewRecorder()
	writeLibraryLocationsError(w, &catalog.NativeOnboardingError{Code: "revision_conflict", CurrentLibraryRevision: &revision})
	body := assertNativeError(t, w, 409, "revision_conflict")
	if body["current_library_revision"] != float64(7) {
		t.Fatalf("lost revision: %+v", body)
	}
	w = httptest.NewRecorder()
	writeLibraryLocationsError(w, &catalog.MutationOutcomeUnknown{OperationID: uuid.New(), LibraryID: 23, Operation: "replace_library_locations", Cause: errors.New("private")})
	body = assertNativeError(t, w, 503, "mutation_outcome_unknown")
	if body["library_id"] != float64(23) || body["operation"] != "replace_library_locations" {
		t.Fatalf("unknown outcome lost reconciliation: %+v", body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	nativeStorageContractRoundTrip(t, raw, &nativeStorageErrorResponse{})
}

func TestLibraryLocationsDuplicatePathKeepsLegacyConflict(t *testing.T) {
	w := httptest.NewRecorder()
	writeLibraryLocationsError(w, catalog.MapNativeOnboardingError(catalog.ErrDuplicatePath))
	assertNativeError(t, w, http.StatusConflict, "conflict")
}
func TestLibraryCreationAttachmentRejectsLegacySourceAndNilHook(t *testing.T) {
	h := &LibraryHandler{}
	source := uuid.New()
	for _, request := range []LibraryCreateRequest{{Name: "x", Type: "ebooks", StorageSource: &source}, {Name: "x", Type: "ebooks", Paths: []string{"/media"}}} {
		var attach LibraryCreateAttachment
		if request.StorageSource != nil {
			attach = func(context.Context, pgx.Tx, int) error { return nil }
		}
		_, err := h.CreateLibraryWithAttachment(t.Context(), request, attach)
		var decision *APIError
		if !errors.As(err, &decision) || decision.Status != 400 {
			t.Fatalf("invalid attachment create: %v", err)
		}
	}
	_, err := h.CreateLibrary(t.Context(), LibraryCreateRequest{Name: "legacy", Type: "ebooks", StorageSource: &source, Paths: []string{"/media"}})
	var decision *APIError
	if !errors.As(err, &decision) || decision.Status != 400 {
		t.Fatal("legacy paths/source exclusivity changed", err)
	}
}

func TestLibraryLocationsMixingUnavailableIsUnprocessable(t *testing.T) {
	w := httptest.NewRecorder()
	writeLibraryLocationsError(w, &catalog.NativeOnboardingError{Code: "location_mixing_unavailable"})
	body := assertNativeError(t, w, http.StatusUnprocessableEntity, "location_mixing_unavailable")
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	nativeStorageContractRoundTrip(t, raw, &nativeStorageErrorResponse{})
}

type libraryLocationCapabilityFixture struct{ editing, mixing bool }

func (c libraryLocationCapabilityFixture) LibraryLocationBrowsingReady(context.Context) bool {
	return c.editing
}
func (c libraryLocationCapabilityFixture) LibraryLocationEditingReady(context.Context) bool {
	return c.editing
}
func (c libraryLocationCapabilityFixture) NativeAudioReady(context.Context) bool   { return false }
func (c libraryLocationCapabilityFixture) NativeStorageReady(context.Context) bool { return c.editing }
func (c libraryLocationCapabilityFixture) LibraryLocationMixingReady(context.Context) bool {
	return c.editing && c.mixing
}

// Clients detect mixed filesystem/plugin libraries before offering them.
func TestLibraryLocationsMixingCapability(t *testing.T) {
	for _, tc := range []struct {
		name            string
		cap             libraryLocationCapabilityFixture
		editing, mixing bool
	}{
		{"guarded", libraryLocationCapabilityFixture{editing: true}, true, false},
		{"mixed scanning", libraryLocationCapabilityFixture{editing: true, mixing: true}, true, true},
		{"editing absent", libraryLocationCapabilityFixture{mixing: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := (&BloemNativeStorageManagementHandler{Capabilities: tc.cap}).nativeStorageCapabilities(t.Context())
			if body["library_location_atomic_editing"] != tc.editing || body["library_location_mixing"] != tc.mixing {
				t.Fatalf("library location capabilities: %v", body)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			nativeStorageContractRoundTrip(t, raw, &nativeStorageCapabilitiesResponse{})
		})
	}
}
