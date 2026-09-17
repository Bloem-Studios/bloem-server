package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
)

func streamTokenRequest(scope *access.Scope) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/livetv/live-hls/p1/index.m3u8", nil)
	ctx := context.WithValue(req.Context(), streamTokenAuthorizedKey, true)
	if scope != nil {
		ctx = access.SetScope(ctx, *scope)
	}
	return req.WithContext(ctx)
}

// A stream token used to skip the Live TV permission outright, so revoking Live
// TV left an unexpired token streaming. The token's scope is resolved fresh on
// every request and must carry the permission like any other viewer.
func TestRequireLiveTVStreamAccessChecksTokenScope(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tt := range []struct {
		name  string
		scope *access.Scope
		want  int
	}{
		{name: "revoked", scope: &access.Scope{UserID: 7, LiveTVAllowed: false}, want: http.StatusForbidden},
		{name: "granted", scope: &access.Scope{UserID: 7, LiveTVAllowed: true}, want: http.StatusNoContent},
		{name: "unresolved", want: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			RequireLiveTVStreamAccess(ok).ServeHTTP(rec, streamTokenRequest(tt.scope))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestStreamTokenViewer(t *testing.T) {
	userID, profileID, ok := StreamTokenViewer(streamTokenRequest(&access.Scope{UserID: 7, ProfileID: "p1"}))
	if !ok || userID != 7 || profileID != "p1" {
		t.Fatalf("StreamTokenViewer = %d, %q, %v; want 7, p1, true", userID, profileID, ok)
	}
	if _, _, ok := StreamTokenViewer(streamTokenRequest(nil)); ok {
		t.Fatal("a token request without a resolved scope produced a viewer")
	}
	bearer := httptest.NewRequest(http.MethodGet, "/", nil)
	bearer = bearer.WithContext(access.SetScope(bearer.Context(), access.Scope{UserID: 7}))
	if _, _, ok := StreamTokenViewer(bearer); ok {
		t.Fatal("a request no stream token authorized produced a token viewer")
	}
}
