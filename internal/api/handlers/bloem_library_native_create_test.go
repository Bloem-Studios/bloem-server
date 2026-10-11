package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Supported kinds must reach storage availability and transactional admission;
// unsupported kinds must fail before catalog access. Movie readiness and source
// authority are still checked by LibraryLocations.AttachTx.
func TestCreateNativeLibraryKindAdmission(t *testing.T) {
	source := uuid.New()
	for _, tc := range []struct {
		kind string
		want int
	}{
		{"ebooks", http.StatusServiceUnavailable},
		{"movies", http.StatusServiceUnavailable},
		{"series", http.StatusBadRequest},
		{"audiobooks", http.StatusBadRequest},
		{"mixed", http.StatusBadRequest},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h := &LibraryHandler{}
			_, err := h.CreateLibrary(t.Context(), LibraryCreateRequest{
				Name: "Native test", Type: tc.kind, StorageSource: &source,
			})
			var decision *APIError
			if !errors.As(err, &decision) || decision.Status != tc.want {
				t.Fatalf("create %s without storage runtime: %v, want status %d", tc.kind, err, tc.want)
			}
			if tc.want == http.StatusServiceUnavailable && decision.Code != "storage_unavailable" {
				t.Fatalf("create %s rejected kind before storage availability: %v", tc.kind, err)
			}
		})
	}
}
