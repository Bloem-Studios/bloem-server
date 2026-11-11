package middleware

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

func TestPreviewPrincipalCarriesItsLoginSessionForSearch(t *testing.T) {
	ctx := context.WithValue(t.Context(), claimsKey, &auth.Claims{UserID: 169, SessionID: "guest-session-1"})
	ctx = withPreviewPrincipal(ctx)
	if scope, ok := previewcontext.SearchScope(ctx); !ok || scope != "guest-session-1" {
		t.Fatalf("search scope = %q, %v", scope, ok)
	}
	if !previewcontext.Active(ctx) || !IsPreviewPrincipal(ctx) {
		t.Fatal("request no longer marked as a preview")
	}
	if _, ok := previewcontext.SearchScope(withPreviewPrincipal(t.Context())); ok {
		t.Fatal("a request without claims got a search scope")
	}
}
