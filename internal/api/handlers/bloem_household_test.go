package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// TestCanManageHouseholdDirectProfileSession extends Silo's
// TestCanManageHousehold for Bloem's direct-profile sessions. Such a session
// authenticates one profile, so the household boundary must read that binding
// rather than the self-asserted header. Otherwise a non-primary profile with
// its own credential could claim the primary profile's ID and manage the whole
// household.
func TestCanManageHouseholdDirectProfileSession(t *testing.T) {
	ctx := context.Background()
	store := newHouseholdTestStore(t)
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "primary", Name: "Sam", IsPrimary: true}); err != nil {
		t.Fatalf("create primary: %v", err)
	}
	if err := store.CreateProfile(ctx, userstore.Profile{ID: "child", Name: "Robin"}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	tokens := access.NewProfileTokenService("test-secret-value-at-least-32-chars", 0)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Profile-Id", "primary")
	req = req.WithContext(apimw.SetClaims(req.Context(), &auth.Claims{
		UserID:     1,
		SessionID:  "session-1",
		ProfileID:  "child",
		AuthMethod: auth.AuthMethodDirectProfile,
	}))

	ok, err := canManageHousehold(req, store, nil, tokens)
	if err != nil || ok {
		t.Fatalf("direct profile = (%v, %v), want (false, nil)", ok, err)
	}
}
