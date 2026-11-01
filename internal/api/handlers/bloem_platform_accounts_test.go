package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Absent leaves a policy field alone, null clears it, a value sets it.
func TestBloemPlatformAccountUpdateDecodesTriState(t *testing.T) {
	var req BloemPlatformAccountUpdateRequest
	if err := json.Unmarshal([]byte(`{"max_streams":3,"library_ids":null}`), &req); err != nil {
		t.Fatal(err)
	}
	if !req.MaxStreams.Set || req.MaxStreams.Value == nil || *req.MaxStreams.Value != 3 {
		t.Fatalf("max_streams = %+v, want set to 3", req.MaxStreams)
	}
	if !req.LibraryIDs.Set || req.LibraryIDs.Value != nil {
		t.Fatalf("library_ids = %+v, want set to null", req.LibraryIDs)
	}
	if req.MaxTranscodes.Set || req.DownloadAllowed.Set {
		t.Fatalf("absent fields were set: %+v %+v", req.MaxTranscodes, req.DownloadAllowed)
	}
}

func TestBloemPlatformAccountPlanFieldsComeTogether(t *testing.T) {
	h := &AdminHandler{}
	for _, tc := range []struct {
		key      string
		revision int64
		status   int
	}{
		{key: "standard", revision: 0, status: http.StatusBadRequest},
		{key: "", revision: 2, status: http.StatusBadRequest},
		{key: "standard", revision: -1, status: http.StatusBadRequest},
		// Both given, but this handler has no plan store.
		{key: " standard ", revision: 2, status: http.StatusServiceUnavailable},
	} {
		rec := httptest.NewRecorder()
		key := tc.key
		if _, ok := h.bloemPlatformPlanRequested(rec, &key, tc.revision); ok || rec.Code != tc.status {
			t.Fatalf("%q/%d: ok=%v status=%d, want %d", tc.key, tc.revision, ok, rec.Code, tc.status)
		}
	}
	rec := httptest.NewRecorder()
	key := ""
	if planned, ok := h.bloemPlatformPlanRequested(rec, &key, 0); planned || !ok {
		t.Fatal("no plan fields must mean no plan")
	}
}

func TestBloemPlatformAccountCreateRefusesABadBody(t *testing.T) {
	rec := httptest.NewRecorder()
	(&AdminHandler{}).HandleBloemPlatformAccountCreate(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}
