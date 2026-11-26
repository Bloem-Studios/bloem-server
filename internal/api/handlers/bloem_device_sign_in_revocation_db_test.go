package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/bloemtestdevice"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Every Bloem path that signs an account out everywhere, or deletes it, also
// ends the device sign-ins it approved that no device has collected: a device
// approved before cannot collect a session after. The household paths reach
// Silo's own revocation (MutateAdminAccount) or the household trigger; the
// sign-out routes reach auth.BloemWithdrawDeviceSignIns.
func TestBloemRevocationsWithdrawApprovedDeviceSignInsDB(t *testing.T) {
	f := newMovesFixture(t)
	ctx := t.Context()
	off, password := false, "Replaced-Password-1"
	login := func(main int, name string) int {
		return f.create(main, name, `"max_streams":1,"max_profiles":2`).ID
	}

	t.Run("login deleted", func(t *testing.T) {
		id := login(f.main("delete-main"), "deleted")
		signIn := bloemtestdevice.Approve(t, f.pool, id)
		if err := f.h.DeleteAdminAccount(ctx, id, -1, 0); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("login disabled", func(t *testing.T) {
		id := login(f.main("disable-main"), "disabled")
		signIn := bloemtestdevice.Approve(t, f.pool, id)
		if _, err := f.h.UpdateAdminAccount(ctx, id, -1, 0, models.UpdateUserInput{Enabled: &off}); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("main account disabled and enabled again", func(t *testing.T) {
		main := f.main("household-main")
		id := login(main, "follows")
		signIn := bloemtestdevice.Approve(t, f.pool, id)
		if _, err := f.h.UpdateAdminAccount(ctx, main, -1, 0, models.UpdateUserInput{Enabled: &off}); err != nil {
			t.Fatal(err)
		}
		on := true
		if _, err := f.h.UpdateAdminAccount(ctx, main, -1, 0, models.UpdateUserInput{Enabled: &on}); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("login password reset", func(t *testing.T) {
		id := login(f.main("reset-main"), "reset")
		signIn := bloemtestdevice.Approve(t, f.pool, id)
		if _, err := f.h.UpdateAdminAccount(ctx, id, -1, 0, models.UpdateUserInput{Password: &password}); err != nil {
			t.Fatal(err)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("profile moved to a new login", func(t *testing.T) {
		main := f.main("move-main")
		f.addProfile(main, "Main")
		moving := f.addProfile(main, "Moving")
		signIn := bloemtestdevice.ApproveForProfile(t, f.pool, main, moving)
		if rec := f.moveProfile(main, moving, "moved"); rec.Code != http.StatusCreated {
			t.Fatalf("move profile: %d %s", rec.Code, rec.Body)
		}
		signIn.RequireWithdrawn(t)
	})
	t.Run("login moved to its main account", func(t *testing.T) {
		main := f.main("back-main")
		id := login(main, "back")
		signIn := bloemtestdevice.Approve(t, f.pool, id)
		if rec := f.post(fmt.Sprintf("/accounts/%d/logins/%d/move-to-main", main, id), ""); rec.Code != http.StatusOK {
			t.Fatalf("move to main: %d %s", rec.Code, rec.Body)
		}
		signIn.RequireWithdrawn(t)
	})

	revokeAll := func(t *testing.T, account int, organization uuid.UUID) {
		t.Helper()
		params := chi.NewRouteContext()
		params.URLParams.Add("user_id", strconv.Itoa(account))
		reqCtx := context.WithValue(ctx, chi.RouteCtxKey, params)
		if organization != uuid.Nil {
			reqCtx = withAdminResourceOrganization(reqCtx, organization)
		}
		req := httptest.NewRequestWithContext(reqCtx, http.MethodDelete, fmt.Sprintf("/admin/users/%d/auth-sessions", account), nil)
		rec := httptest.NewRecorder()
		f.h.HandleRevokeAllUserAuthSessions(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("revoke every session: %d %s", rec.Code, rec.Body)
		}
	}
	t.Run("platform sign out everywhere", func(t *testing.T) {
		account := f.main("platform-sign-out")
		signIn := bloemtestdevice.Approve(t, f.pool, account)
		revokeAll(t, account, uuid.Nil)
		signIn.RequireWithdrawn(t)
	})
	t.Run("organization sign out everywhere", func(t *testing.T) {
		account := f.main("organization-sign-out")
		var organization uuid.UUID
		if err := f.pool.QueryRow(ctx, `SELECT organization_id FROM organization_memberships WHERE account_id = $1`, account).Scan(&organization); err != nil {
			t.Fatalf("the account's organization: %v", err)
		}
		signIn := bloemtestdevice.Approve(t, f.pool, account)
		revokeAll(t, account, organization)
		signIn.RequireWithdrawn(t)
	})
}
