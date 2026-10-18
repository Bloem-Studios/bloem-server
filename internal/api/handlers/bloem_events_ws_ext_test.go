package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// bloemEventsWSTestConnAfterTicket connects as RequireAuth leaves a Bloem
// events handshake once it has consumed the audience ticket: the ticket's
// profile in the claims and its PIN proof as X-Profile-Token. It is
// eventsWSTestConnActing with that header.
func bloemEventsWSTestConnAfterTicket(
	t *testing.T,
	handler *EventsHandler,
	claims *auth.Claims,
	profileToken, query string,
	decide apimw.ActingAdminDecider,
) (*websocket.Conn, func(wantType string) map[string]json.RawMessage) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := apimw.SetClaims(r.Context(), claims)
		ctx = apimw.SetActingAdminDecider(ctx, decide)
		handler.HandleWebSocket(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+query, http.Header{"X-Profile-Token": {profileToken}})
	if err != nil {
		t.Fatalf("dialing events websocket: %v", err)
	}
	if resp != nil && resp.Body != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	t.Cleanup(func() { _ = conn.Close() })

	readFrame := func(wantType string) map[string]json.RawMessage {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("setting read deadline: %v", err)
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("reading %s frame: %v", wantType, err)
		}
		var frame map[string]json.RawMessage
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("frame is not JSON: %v (%s)", err, data)
		}
		if string(frame["type"]) != `"`+wantType+`"` {
			t.Fatalf("frame type = %s, want %q (frame: %s)", frame["type"], wantType, data)
		}
		return frame
	}

	return conn, readFrame
}
