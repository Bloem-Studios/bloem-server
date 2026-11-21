package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// While Silo switching is on the database refuses to make an account a login;
// the caller hears a definite conflict, not a server error it would retry.
func TestLinkingLoginWhileSiloSwitchingIsAConflict(t *testing.T) {
	refused := fmt.Errorf("link: %w", &pgconn.PgError{Code: "P0001", Message: "bloem_silo_switching_household_login"})
	var apiErr *APIError
	if err := bloemHouseholdLinkError(refused); !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "silo_switching_on" {
		t.Fatalf("switching refusal = %v, want 409 silo_switching_on", err)
	}
	other := errors.New("connection reset")
	if err := bloemHouseholdLinkError(other); !errors.Is(err, other) {
		t.Fatalf("other error = %v, want it unchanged", err)
	}
}
