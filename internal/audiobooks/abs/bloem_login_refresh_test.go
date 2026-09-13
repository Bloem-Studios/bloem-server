package abs

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleRefresh_ResolvesUsername verifies that refresh responses expose
// the resolved username instead of the internal user ID.
func TestHandleRefresh_ResolvesUsername(t *testing.T) {
	// Given
	h, store, cfg := newRefreshTestHandler(t)
	h.deps.UsernameResolver = func(context.Context, string, string) string { return "sara" }
	refresh, _ := mintAndPersistRefresh(t, store, cfg, "15")

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.Header.Set("x-refresh-token", refresh)
	rec := httptest.NewRecorder()

	// When
	h.handleRefresh(rec, req)

	// Then
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	user, ok := resp["user"].(map[string]any)
	if !ok {
		t.Fatalf("user object missing")
	}
	if user["username"] != "sara" {
		t.Errorf("username = %v, want sara", user["username"])
	}
	if user["id"] != "15" {
		t.Errorf("id = %v, want 15", user["id"])
	}
}
