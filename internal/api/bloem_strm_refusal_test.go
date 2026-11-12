package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/remotesource"
)

// An address the guard refuses is a refusal, not an outage: the open error
// still says ErrBlockedAddress, and reads as a 403 API error (which the
// Jellyfin native media host answers with) that does not name the URL.
func TestStrmRefusedAddressReadsAs403(t *testing.T) {
	server := strmMediaServer(t, map[string][]byte{"/one.mkv": strmBlob(4096)})
	f := newStrmFixture(t, false)
	f.sources.put(f.file.FilePath, server.URL+"/one.mkv?token=very-secret", "")
	_, err := f.service.Open(t.Context(), f.file)
	if !errors.Is(err, remotesource.ErrBlockedAddress) {
		t.Fatalf("Open = %v, want ErrBlockedAddress", err)
	}
	var apiErr *handlers.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("refused open as an API error = %+v", apiErr)
	}
	for _, text := range []string{err.Error(), apiErr.Message} {
		if strings.Contains(text, "very-secret") || strings.Contains(text, "127.0.0.1") {
			t.Fatalf("the refusal names the URL: %q", text)
		}
	}
}
