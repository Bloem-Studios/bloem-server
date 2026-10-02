package auth

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// The current account role must not upgrade the authority of a direct-profile
// session or reject its deliberately restricted access token after refresh.
func TestBloemDirectProfileSessionRoleRemainsRestricted(t *testing.T) {
	credentials := newProfileCredentialService(t)
	accountID, profileID := newProfileCredentialFixture(t, credentials.pool, "role-refresh")
	if _, err := credentials.pool.Exec(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Set(t.Context(), accountID, profileID, "role-refresh@example.test", "profile-password"); err != nil {
		t.Fatal(err)
	}
	service, jwt, sessions := newDirectProfileService(t, credentials.pool, credentials.ProfileCredentialService)
	pair, _, err := service.LoginProfile(t.Context(), "role-refresh@example.test", "profile-password", DeviceClaim{ID: "role-device", Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := jwt.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	role, active, err := sessions.ActiveSessionRole(t.Context(), claims.SessionID)
	if err != nil || !active || role != models.RoleUser || claims.Role != role {
		t.Fatalf("role=%q active=%t claim=%q error=%v", role, active, claims.Role, err)
	}
	if err := sessions.Revoke(t.Context(), claims.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, active, err := sessions.ActiveSessionRole(t.Context(), claims.SessionID); err != nil || active {
		t.Fatalf("revoked session active=%t error=%v", active, err)
	}
}
