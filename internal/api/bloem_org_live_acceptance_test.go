package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// orgLiveClient is a socket read in the background (a gorilla read that times
// out is permanent), so "nothing arrived" is a question for a channel.
type orgLiveClient struct {
	conn   *websocket.Conn
	frames chan map[string]any
	done   chan struct{}
	err    error
}

func dialOrgLive(t *testing.T, server *httptest.Server, ticket string) (*orgLiveClient, error) {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{handlers.OrgLiveProtocol, "ticket." + ticket}, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+NativeAPIPrefix+"/organization/live/ws", nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	c := &orgLiveClient{conn: conn, frames: make(chan map[string]any, 256), done: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		defer close(c.done)
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				c.err = err
				return
			}
			var frame map[string]any
			if messageType == websocket.TextMessage && json.Unmarshal(data, &frame) == nil {
				c.frames <- frame
			}
		}
	}()
	return c, nil
}

func (c *orgLiveClient) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case frame := <-c.frames:
		return frame
	case <-c.done:
		t.Fatalf("the socket closed while waiting for a frame: %v", c.err)
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5s")
	}
	return nil
}

func (c *orgLiveClient) quiet(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case frame := <-c.frames:
		t.Fatalf("an unexpected frame: %v", frame)
	case <-c.done:
		t.Fatalf("the socket closed: %v", c.err)
	case <-time.After(within):
	}
}

func (c *orgLiveClient) closedWith(t *testing.T, code int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-c.frames:
		case <-c.done:
			var closeErr *websocket.CloseError
			if !errors.As(c.err, &closeErr) || closeErr.Code != code {
				t.Fatalf("closed with %v, want %d", c.err, code)
			}
			return
		case <-deadline:
			t.Fatalf("not closed with %d within 5s", code)
		}
	}
}

func (c *orgLiveClient) send(t *testing.T, frame map[string]any) {
	t.Helper()
	if err := c.conn.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
}

func mintOrgLiveTicket(t *testing.T, f *supportFixture, pass string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	rec := performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/organization/live/ticket", "", pass, nil)
	if rec.Code != http.StatusOK {
		return "", rec
	}
	var body struct {
		Ticket   string   `json:"ticket"`
		Protocol string   `json:"protocol"`
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Ticket == "" || body.Protocol != handlers.OrgLiveProtocol {
		t.Fatalf("ticket answer = %s (%v)", rec.Body.String(), err)
	}
	return body.Ticket, rec
}

// The organization live socket through the real router, middleware and
// database: passes minted through /admin/session, support actions that write
// real rows, and two organizations that must never see each other.
//
//nolint:funlen,gocyclo // one walk through the feature in the order an administrator meets it
func TestOrganizationLiveAcceptance(t *testing.T) {
	f := newSupportFixtureWith(t, BloemDependencies{})
	server := httptest.NewServer(f.router)
	t.Cleanup(server.Close)

	// The server advertises the feature publicly.
	public := assertStatus(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/capabilities", "", "", nil), http.StatusOK, "capabilities")
	if !strings.Contains(fmt.Sprint(public["feature_tokens"]), "organization_live_v1") {
		t.Fatalf("feature_tokens = %v", public["feature_tokens"])
	}

	// No pass, no ticket; and the socket needs a ticket, not a pass.
	if rec := performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/organization/live/ticket", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ticket without a pass = %d", rec.Code)
	}
	if rec := performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/organization/live/ticket", "", "not-a-pass", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ticket with a bad pass = %d", rec.Code)
	}
	passAsTicket, err := dialOrgLive(t, server, f.ownerPass)
	if err != nil {
		t.Fatalf("the pass offered as a ticket: %v", err)
	}
	passAsTicket.closedWith(t, handlers.OrgLiveCloseInvalidTicket)

	// The North owner and the Canal administrator each open a socket.
	northTicket, rec := mintOrgLiveTicket(t, f, f.ownerPass)
	if northTicket == "" {
		t.Fatalf("north ticket = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"org.people"`) || !strings.Contains(rec.Body.String(), `"org.activity"`) || strings.Contains(rec.Body.String(), `"org.streams"`) {
		t.Fatalf("channels without a hub should be people and activity only: %s", rec.Body.String())
	}
	canalTicket, rec := mintOrgLiveTicket(t, f, f.organizationTokens[1])
	if canalTicket == "" {
		t.Fatalf("canal ticket = %d %s", rec.Code, rec.Body.String())
	}
	north, err := dialOrgLive(t, server, northTicket)
	if err != nil {
		t.Fatalf("north dial: %v", err)
	}
	canal, err := dialOrgLive(t, server, canalTicket)
	if err != nil {
		t.Fatalf("canal dial: %v", err)
	}
	for _, socket := range []*orgLiveClient{north, canal} {
		socket.send(t, map[string]any{"op": "subscribe", "channels": []string{"org.people", "org.activity", "org.streams"}, "resume": nil})
	}
	for name, socket := range map[string]*orgLiveClient{"north": north, "canal": canal} {
		resyncs, errs := map[string]bool{}, 0
		for i := 0; i < 3; i++ {
			frame := socket.next(t)
			switch frame["op"] {
			case "resync":
				resyncs[fmt.Sprint(frame["channel"])] = true
			case "error":
				errs++
				if frame["code"] != "unavailable" || frame["channel"] != "org.streams" {
					t.Fatalf("%s error frame = %v", name, frame)
				}
			}
		}
		if !resyncs["org.people"] || !resyncs["org.activity"] || errs != 1 {
			t.Fatalf("%s subscribe answered resyncs %v and %d errors", name, resyncs, errs)
		}
	}

	// A used ticket opens nothing. Only now is north's ticket known to be used:
	// the handshake completes before the server consumes the ticket, so a replay
	// sent straight after north's dial can reach the server first and take it.
	replay, err := dialOrgLive(t, server, northTicket)
	if err != nil {
		t.Fatalf("replay dial: %v", err)
	}
	replay.closedWith(t, handlers.OrgLiveCloseInvalidTicket)

	// North's owner signs a member out everywhere: it raises the member's
	// security revision and writes an audit entry. North hears about the
	// person and the new activity; Canal hears nothing.
	member := strconv.Itoa(f.memberID)
	revision := f.revision(t, f.memberID)
	assertStatus(t, f.call(t, http.MethodDelete, member+"/sessions", fmt.Sprintf(`{"expected_revision":%d}`, revision), f.ownerPass), http.StatusOK, "sign out everywhere")
	heard := map[string]string{}
	for i := 0; i < 2; i++ {
		frame := north.next(t)
		if frame["op"] != "change" {
			t.Fatalf("north frame = %v", frame)
		}
		heard[fmt.Sprint(frame["channel"])] = fmt.Sprint(frame["id"])
		if frame["rev"] == "" || frame["rev"] == nil {
			t.Fatalf("a change without a revision: %v", frame)
		}
	}
	if heard["org.people"] != member || !strings.HasPrefix(heard["org.activity"], "lifecycle:") {
		t.Fatalf("north heard %v", heard)
	}
	north.quiet(t, 300*time.Millisecond)
	canal.quiet(t, 300*time.Millisecond)

	// The signal names the item, not its content: the ids resolve in the REST
	// lists the client re-reads.
	people := assertStatus(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/people/"+member+"/", "", f.ownerPass, nil), http.StatusOK, "re-read person")
	if fmt.Sprint(people["person"].(map[string]any)["account_id"]) != member {
		t.Fatalf("the person the signal named: %v", people)
	}
	activity := assertStatus(t, performJSONRequest(t, f.router, http.MethodGet, NativeAPIPrefix+"/admin/organization/activity", "", f.ownerPass, nil), http.StatusOK, "re-read activity")
	if !strings.Contains(fmt.Sprint(activity["events"]), strings.TrimPrefix(heard["org.activity"], "lifecycle:")) {
		t.Fatalf("the activity entry the signal named is not in the list: %v", activity)
	}

	// Canal's own change reaches Canal and not North, whether it comes from an
	// API write or straight from the database (a worker, another node).
	if _, err := f.pool.Exec(context.Background(), `UPDATE organization_memberships SET status='suspended', security_revision=security_revision+1, updated_at=now() WHERE id=$1`, f.membershipIDs[1]); err != nil {
		t.Fatal(err)
	}
	frame := canal.next(t)
	if frame["op"] != "change" || frame["channel"] != "org.people" || frame["kind"] != "updated" {
		t.Fatalf("canal frame = %v", frame)
	}
	north.quiet(t, 300*time.Millisecond)

	// A pass revoked between the mint and the connect opens nothing: the
	// second administrator is signed out everywhere, then tries its ticket.
	adminTicket, _ := mintOrgLiveTicket(t, f, f.adminPass)
	if adminTicket == "" {
		t.Fatal("the second administrator got no ticket")
	}
	adminRevision := f.revision(t, f.adminID)
	assertStatus(t, f.call(t, http.MethodDelete, strconv.Itoa(f.adminID)+"/sessions", fmt.Sprintf(`{"expected_revision":%d}`, adminRevision), f.ownerPass), http.StatusOK, "sign the second administrator out")
	late, err := dialOrgLive(t, server, adminTicket)
	if err != nil {
		t.Fatalf("late dial: %v", err)
	}
	late.closedWith(t, handlers.OrgLiveCloseAccessLost)
	if rec := performJSONRequest(t, f.router, http.MethodPost, NativeAPIPrefix+"/admin/organization/live/ticket", "", f.adminPass, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a signed-out administrator minted a ticket: %d", rec.Code)
	}
}
