package promotions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestclock"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Dismiss writes the dismissal delivery already reads: the card leaves that
// surface for that profile only.
func TestDismissHidesTheCardOnThatSurfaceForThatProfile(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	user := seedAccount(t, pool, "dismisser", "user")
	svc := NewService(pool, bloemtestclock.Fixed(promoStart.Add(time.Hour)), pgstore.NewPostgresProvider(pool))
	in := validInput()
	in.Surfaces = []string{"home", "detail"}
	card, err := svc.Create(ctx, user, in)
	if err != nil {
		t.Fatal(err)
	}
	p1 := Viewer{UserID: user, ProfileID: "p1"}
	p2 := Viewer{UserID: user, ProfileID: "p2"}

	if err := svc.Dismiss(ctx, p1, SurfaceHome, card.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dismiss(ctx, p1, SurfaceHome, card.ID); err != nil {
		t.Fatalf("repeating a dismissal: %v", err)
	}
	if cards, _, err := svc.ActiveHome(ctx, p1); err != nil || len(cards) != 0 {
		t.Fatalf("p1 home after dismissal: %v %v", ids(cards), err)
	}
	if cards, _, err := svc.ActiveHome(ctx, p2); err != nil || len(cards) != 1 {
		t.Fatalf("p2 home is unaffected: %v %v", ids(cards), err)
	}
	if cards, err := svc.Active(ctx, Query{Surface: SurfaceDetail, ContentID: "movie-1", Viewer: p1}); err != nil || len(cards) != 1 {
		t.Fatalf("p1 detail is unaffected: %v %v", ids(cards), err)
	}

	for _, tc := range []struct{ surface, id string }{
		{"login", card.ID}, {SurfaceHome, ""}, {SurfaceHome, strings.Repeat("x", 65)},
	} {
		if err := svc.Dismiss(ctx, p1, tc.surface, tc.id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Dismiss(%q, %d bytes) = %v, want ErrInvalid", tc.surface, len(tc.id), err)
		}
	}
	if err := NewService(pool, bloemtestclock.Fixed(promoStart), nil).Dismiss(ctx, p1, SurfaceHome, card.ID); !errors.Is(err, ErrDismissalUnavailable) {
		t.Fatalf("no store: %v", err)
	}
}
