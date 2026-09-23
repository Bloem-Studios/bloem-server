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

func TestJWT_LegacyTokenLeavesTenantClaimsEmpty(t *testing.T) {
	svc := newTestJWTService()

	token, err := svc.GenerateAccessToken(1, "user", "sess-legacy")
	if err != nil {
		t.Fatalf("GenerateAccessToken() error: %v", err)
	}
	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	if claims.OrganizationID != "" || claims.MembershipID != "" || claims.PolicyRevision != 0 || claims.SecurityRevision != 0 {
		t.Fatalf("legacy token unexpectedly gained tenant claims: %#v", claims)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	for _, field := range []string{"organization_id", "membership_id", "policy_revision", "security_revision"} {
		if _, ok := fields[field]; ok {
			t.Errorf("legacy token payload contains optional field %q", field)
		}
	}
}

func TestJWT_OrganizationClaimsRoundTrip(t *testing.T) {
	svc := newTestJWTService()
	want := auth.Claims{
		UserID:           42,
		Role:             "tenant-curator",
		SessionID:        "sess-tenant",
		ProfileID:        "profile-7",
		OrganizationID:   "9ee0f0d9-2527-4f5e-bda6-98d3ec049fc8",
		MembershipID:     "38c9fae8-b802-4b2b-91b0-5668124f6f39",
		PolicyRevision:   12,
		SecurityRevision: 19,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		TokenType: auth.TokenTypeAccess,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &want).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign tenant token: %v", err)
	}

	got, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error: %v", err)
	}
	if got.OrganizationID != want.OrganizationID || got.MembershipID != want.MembershipID ||
		got.PolicyRevision != want.PolicyRevision || got.SecurityRevision != want.SecurityRevision {
		t.Fatalf("tenant claims = %#v, want %#v", got, want)
	}
}
