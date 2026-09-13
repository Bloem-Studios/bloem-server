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

// TestHandleABSAuthorize_ResolvesUsername verifies that authorize responses
// expose the resolved username instead of the internal user ID.
func TestHandleABSAuthorize_ResolvesUsername(t *testing.T) {
	// Given
	h, _, _ := newRefreshTestHandler(t)
	h.deps.UsernameResolver = func(context.Context, string, string) string { return "sara" }

	req := httptest.NewRequest(http.MethodPost, "/api/authorize", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, ctxAuth{
		UserID:    "15",
		ProfileID: "profile-1",
		Token:     "access.jwt",
	}))
	rec := httptest.NewRecorder()

	// When
	h.handleABSAuthorize(rec, req)

	// Then
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	user, ok := env["user"].(map[string]any)
	if !ok {
		t.Fatalf("user is not a map: %T", env["user"])
	}
	if user["username"] != "sara" {
		t.Errorf("username = %v, want sara", user["username"])
	}
	if user["id"] != "15" {
		t.Errorf("id = %v, want 15", user["id"])
	}
}
