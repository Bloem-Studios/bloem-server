package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/compatapi"
)

type compatMountApps struct{}

func (compatMountApps) Authenticate(_ context.Context, _ string, _ *tls.ConnectionState) (compatapi.AppIdentity, error) {
	return compatapi.AppIdentity{ApplicationID: "mount-probe", Kind: "jellyfin", InstanceID: "mount-probe-1"}, nil
}

// TestCompatAPIMountDispatchesThroughTheRouter proves /api/internal/compat/v1
// requests built through NewRouter reach the compat handler with relative
// paths intact.
func TestCompatAPIMountDispatchesThroughTheRouter(t *testing.T) {
	handler, err := compatapi.New(compatapi.Config{
		SubjectTokenKey: bytes.Repeat([]byte{1}, 32),
		CursorKey:       bytes.Repeat([]byte{2}, 32),
		Version:         "mount-probe",
		APIRange:        compatapi.APIRange{Min: 1, Max: 1},
	}, compatapi.Services{Apps: compatMountApps{}})
	if err != nil {
		t.Fatalf("compatapi.New: %v", err)
	}
	router := NewRouter(Dependencies{CompatAPIV1: handler})

	req := httptest.NewRequest(http.MethodGet, "/api/internal/compat/v1/health", nil)
	req.Header.Set("Authorization", "Bearer probe")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mount-probe-1"`) {
		t.Fatalf("mounted compat health = %d %s", rec.Code, rec.Body.String())
	}

	// A compat sub-path the handler does not register answers with the
	// compat handler's own envelope, not the SPA fallback.
	req = httptest.NewRequest(http.MethodGet, "/api/internal/compat/v1/no/such/route", nil)
	req.Header.Set("Authorization", "Bearer probe")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) {
		t.Fatalf("unknown compat path = %d %s, want the compat error envelope", rec.Code, rec.Body.String())
	}
}

// TestCompatAPIMountFailsClosedWhenAbsent proves that a router built without
// the compat handler registers no /api/internal/compat routes at all.
func TestCompatAPIMountFailsClosedWhenAbsent(t *testing.T) {
	router := NewRouter(Dependencies{})
	req := httptest.NewRequest(http.MethodGet, "/api/internal/compat/v1/health", nil)
	req.Header.Set("Authorization", "Bearer probe")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("compat surface must fail closed when unwired, got %d %s", rec.Code, rec.Body.String())
	}
}
