package handlers

import (
	"context"
	"testing"

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
	ctx = apimw.SetClaims(ctx, &auth.Claims{
		UserID:     1,
		SessionID:  "session-1",
		ProfileID:  "child",
		AuthMethod: auth.AuthMethodDirectProfile,
	})

	// The session claims the primary profile and even passes verification.
	ok, err := canManageHouseholdAs(ctx, store, "primary", func(*userstore.Profile) error { return nil })
	if err != nil || ok {
		t.Fatalf("direct profile = (%v, %v), want (false, nil)", ok, err)
	}
}
