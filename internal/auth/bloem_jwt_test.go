package auth_test

// Bloem tenant/incarnation claim coverage moved out of Silo's jwt_test.go so
// that file stays byte-identical to upstream.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestJWTAccountIncarnationRoundTrips(t *testing.T) {
	const secret = "incarnation-round-trip-secret"
	service := auth.NewJWTService(secret, time.Hour, 24*time.Hour)
	incarnation := uuid.New()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
		UserID: 17, Role: "user", SessionID: "session-incarnation",
		AccountIncarnationID: incarnation.String(),
		TokenType:            auth.TokenTypeAccess,
		RegisteredClaims:     jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("generateAccessToken() error: %v", err)
	}
	claims, err := service.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	if claims.AccountIncarnationID != incarnation.String() {
		t.Fatalf("account incarnation = %q, want %q", claims.AccountIncarnationID, incarnation)
	}
}

// TestJWT_AccountTokenCarriesNoProfileSignInClaims: the tenant, device and
// credential claims a direct-profile token once carried are gone from every
// token the server issues.
func TestJWT_AccountTokenCarriesNoProfileSignInClaims(t *testing.T) {
	svc := newTestJWTService()

	token, err := svc.GenerateAccessToken(1, "user", "sess-legacy")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}
	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	for _, field := range []string{"organization_id", "membership_id", "policy_revision", "security_revision", "credential_revision", "device_id"} {
		if _, ok := fields[field]; ok {
			t.Errorf("account token payload contains removed field %q", field)
		}
	}
}
