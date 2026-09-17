package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

type ticketKeyLoader struct{ key *models.APIKey }

func (s *ticketKeyLoader) GetByKey(context.Context, string) (*models.APIKey, error) {
	return s.key, nil
}
func (s *ticketKeyLoader) GetByID(context.Context, int64) (*models.APIKey, error) { return s.key, nil }
func (*ticketKeyLoader) UpdateLastUsed(context.Context, int64) error              { return nil }

type ticketUserLoader struct{ user *models.User }

func (s *ticketUserLoader) GetByID(context.Context, int) (*models.User, error) { return s.user, nil }

func TestAudienceTicketRechecksAPIKeyAndOwner(t *testing.T) {
	incarnation := uuid.New()
	for _, change := range []string{"none", "revoked", "foreign-owner", "disabled", "replaced-account", "demoted", "restricted-scopes", "unwired"} {
		t.Run(change, func(t *testing.T) {
			key := &ticketKeyLoader{key: &models.APIKey{ID: 12, UserID: 7}}
			user := &ticketUserLoader{user: &models.User{ID: 7, Role: "admin", Enabled: true, AccountIncarnationID: incarnation}}
			am := NewAuthMiddleware(nil, nil, key, user)
			original := &auth.Claims{UserID: 7, Role: "admin", AccountIncarnationID: incarnation.String(), TokenType: auth.TokenTypeAPIKey, APIKeyID: 12}
			ticket := auth.NewAudienceTicket(auth.AudienceEventsWS, original, "", "")
			switch change {
			case "revoked":
				key.key = nil
			case "foreign-owner":
				key.key.UserID = 8
			case "disabled":
				user.user.Enabled = false
			case "replaced-account":
				user.user.AccountIncarnationID = uuid.New()
			case "demoted":
				user.user.Role = "user"
			case "restricted-scopes":
				key.key.Scopes = []string{auth.ScopeLibrariesRead}
			case "unwired":
				am.apiKeyValidator = nil
			}
			r := httptest.NewRequest("GET", "/api/v1/events/ws", nil)
			w := httptest.NewRecorder()
			_, ok := audienceTicketPrincipal(w, r, am, ticket)
			if ok != (change == "none") {
				t.Fatalf("key ticket accepted=%v status=%d", ok, w.Code)
			}
		})
	}
}
