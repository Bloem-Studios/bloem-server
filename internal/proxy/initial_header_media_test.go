package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func TestInitialHeaderProxyBoundAuthority(t *testing.T) {
	card := playback.NewDirectRecipeCard("logical", 7, "profile", 42)
	card.InputPath = writeSocketProxyMedia(t)
	card.Executor = &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}
	card.TranscodeTransportID = "transport"
	card.RoutingWorkload, card.RoutingExecution, card.RoutingEgress = string(noderouting.WorkloadDirectPlay), string(noderouting.ExecutionNone), string(noderouting.EgressProxy)
	card.RoutingEgressNodeID = 2
	s := proxyExecutorFixture(t, &card)
	s.SetMediaGrantAuthority(stubGrantStore{cards: map[string]playback.RecipeCard{"logical": card}}, stubLoginSessions{valid: map[string]bool{"login": true}})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	for _, tc := range []struct {
		name, profile, login string
		user                 int
		want                 int
	}{{"valid", "profile", "login", 7, 200}, {"wrong profile", "other", "login", 7, 403}, {"missing profile", "", "login", 7, 403}, {"wrong account", "profile", "login", 8, 403}, {"revoked login", "profile", "revoked", 7, 401}} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := auth.NewJWTService("executor-test", time.Hour, time.Hour).GenerateAccessToken(tc.user, "user", tc.login)
			if err != nil {
				t.Fatal(err)
			}
			response := socketProxyRequest(t, server.Client(), http.MethodGet, server.URL+"/stream/v3/logical", map[string]string{"Authorization": "Bearer " + token, "X-Profile-Id": tc.profile})
			if response.status != tc.want {
				t.Fatalf("status %d body %q", response.status, response.body)
			}
			if tc.want == 200 && response.body != socketProxyMedia {
				t.Fatal("wrong media")
			}
		})
	}
}
