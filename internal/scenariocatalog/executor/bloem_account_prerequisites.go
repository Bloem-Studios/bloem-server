package executor

import (
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Bind the existing seeded session to its real account incarnation, as the
// production auth service does. Preserve all generated expiry/type/session
// claims; do not insert a new session while constructing a scenario request.
func (e *Env) bindFixtureIncarnation(token string, user *models.User) string {
	claims, err := e.jwt.ValidateToken(token)
	if err != nil {
		e.t.Fatalf("scenario token validation: %v", err)
	}
	if user == nil || user.AccountIncarnationID == uuid.Nil || claims.UserID != user.ID {
		e.t.Fatal("scenario account incarnation is unavailable")
	}
	claims.AccountIncarnationID = user.AccountIncarnationID.String()
	claims.AuthMethod = "account"
	bound, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtSecret))
	if err != nil {
		e.t.Fatalf("scenario account token signing: %v", err)
	}
	return bound
}
