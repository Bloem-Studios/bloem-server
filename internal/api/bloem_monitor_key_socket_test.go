package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// A monitoring key opens the events socket through the whole router, as Silo
// lets it: the ticket route admits the key, and the socket greets it. A key
// without the scope gets no ticket.
func TestMonitorKeyEventsSocketThroughTheRouterPostgres(t *testing.T) {
	pool := newV1TenancyDatabase(t)
	users := auth.NewUserRepository(pool)
	admin, err := users.Create(t.Context(), models.CreateUserInput{Username: "monitor-admin", Email: "monitor-admin@example.test", Password: "synthetic-password", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	keys := auth.NewAPIKeyRepository(pool)
	monitor, err := keys.Create(t.Context(), admin.ID, "monitoring tool", []string{auth.ScopeAdminMonitorRead})
	if err != nil {
		t.Fatal(err)
	}
	other, err := keys.Create(t.Context(), admin.ID, "libraries only", []string{auth.ScopeLibrariesRead})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(Dependencies{DB: pool, EventsHub: evt.NewHub("monitor-key-test", nil), UserStoreProvider: pgstore.NewPostgresProvider(pool),
		Config: &config.Config{Auth: config.AuthConfig{JWTSecret: "monitor-key-test-secret", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}}})
	mint := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/events/ws-ticket", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := mint(other.Key); w.Code == http.StatusOK {
		t.Fatalf("a key without admin:monitor:read got a ticket: %s", w.Body)
	}
	w := mint(monitor.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("monitor key ticket: %d %s", w.Code, w.Body)
	}
	var ticket struct {
		Ticket   string `json:"ticket"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ticket); err != nil || ticket.Ticket == "" || ticket.Protocol != handlers.EventsSocketProtocol {
		t.Fatalf("ticket body %s: %v", w.Body, err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{handlers.EventsSocketProtocol, "silo.ticket." + ticket.Ticket}}
	conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v2/events/ws", nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("monitor key handshake: %v (HTTP %d)", err, status)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(body), `"type":"hello"`) {
		t.Fatalf("hello: %s %v", body, err)
	}
}
