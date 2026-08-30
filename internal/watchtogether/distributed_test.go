package watchtogether

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type concurrentRecordingConn struct {
	mu       sync.Mutex
	payloads []any
}

func (connection *concurrentRecordingConn) WriteJSON(value any) error {
	connection.mu.Lock()
	connection.payloads = append(connection.payloads, value)
	connection.mu.Unlock()
	return nil
}
func (*concurrentRecordingConn) Close() error { return nil }

func (connection *concurrentRecordingConn) hasMemberCount(want int) bool {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	for _, value := range connection.payloads {
		encoded, _ := json.Marshal(value)
		var frame struct {
			Type string `json:"type"`
			Room struct {
				MemberCount int `json:"member_count"`
			} `json:"room"`
		}
		if json.Unmarshal(encoded, &frame) == nil && frame.Type == "snapshot" && frame.Room.MemberCount == want {
			return true
		}
	}
	return false
}

func (connection *concurrentRecordingConn) hasTransport(action TransportAction) bool {
	return connection.transportCount(action) > 0
}

func (connection *concurrentRecordingConn) transportCount(action TransportAction) int {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	count := 0
	for _, value := range connection.payloads {
		encoded, _ := json.Marshal(value)
		var frame struct {
			Type    string           `json:"type"`
			Command TransportCommand `json:"command"`
		}
		if json.Unmarshal(encoded, &frame) == nil && frame.Type == "transport_command" && frame.Command.Action == action && frame.Command.OwnerGeneration > 0 {
			count++
		}
	}
	return count
}

func (connection *concurrentRecordingConn) clear() {
	connection.mu.Lock()
	connection.payloads = nil
	connection.mu.Unlock()
}

type distributedSessions map[string]*playback.Session

func (sessions distributedSessions) GetSession(sessionID string) (*playback.Session, error) {
	session := sessions[sessionID]
	if session == nil {
		return nil, playback.ErrSessionNotFound
	}
	copy := *session
	return &copy, nil
}

type setupOrderSubscription struct{}

func (setupOrderSubscription) Close() error { return nil }

type setupOrderRelay struct {
	onSubscribe func()
}

func (*setupOrderRelay) Publish(context.Context, Command) error { return nil }

func (*setupOrderRelay) Claim(context.Context, string, int64, string, time.Duration) (bool, error) {
	return true, nil
}

func (relay *setupOrderRelay) Subscribe(context.Context, func(Command)) (RelaySubscription, error) {
	if relay.onSubscribe != nil {
		relay.onSubscribe()
	}
	return setupOrderSubscription{}, nil
}

func TestDistributedRuntimeIsConfiguredBeforeSubscriptionStarts(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil, nil)
	t.Cleanup(service.Close)
	relay := &setupOrderRelay{}
	configuredBeforeSubscribe := false
	relay.onSubscribe = func() {
		configuredBeforeSubscribe = service.nodeID == "node-a" && service.owner != nil && service.relay == relay
	}

	err := service.SetDistributedRuntime(context.Background(), "node-a", NewPostgresRoomOwner(nil), relay)
	if err != nil {
		t.Fatalf("SetDistributedRuntime: %v", err)
	}
	if !configuredBeforeSubscribe {
		t.Fatal("distributed runtime was not fully configured before relay subscription started")
	}
}

func TestTwoReplicaRoomOwnershipIsFencedAndFailsOver(t *testing.T) {
	pool := openDistributedTestPool(t)
	store := NewPostgresRoomOwner(pool)
	ctx := context.Background()
	roomID := "distributed-owner-" + time.Now().UTC().Format("150405.000000000")
	now := time.Now().UTC()
	var userID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("test database has no user fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_together_rooms (id, code, join_token, host_user_id, host_profile_id)
		VALUES ($1, $2, $3, $4, 'distributed-test-profile')`, roomID, roomID, roomID, userID); err != nil {
		t.Fatalf("insert room fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM watch_together_rooms WHERE id = $1`, roomID)
	})

	first, err := store.Acquire(ctx, roomID, "node-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Acquire(node-a): %v", err)
	}
	if _, err := store.Acquire(ctx, roomID, "node-b", now.Add(time.Minute)); !errors.Is(err, ErrRoomOwned) {
		t.Fatalf("Acquire(node-b) while leased error = %v, want ErrRoomOwned", err)
	}
	if err := store.Release(ctx, first); err != nil {
		t.Fatalf("Release(node-a): %v", err)
	}
	second, err := store.Acquire(ctx, roomID, "node-b", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Acquire(node-b) after release: %v", err)
	}
	if second.Generation <= first.Generation {
		t.Fatalf("failover generation = %d, want > %d", second.Generation, first.Generation)
	}
	if err := store.Release(ctx, first); !errors.Is(err, ErrRoomOwnerGenerationMismatch) {
		t.Fatalf("stale Release(node-a) error = %v, want generation mismatch", err)
	}
}

func TestTwoReplicaRelayDeliversGenerationTaggedCommandOnce(t *testing.T) {
	clientA := openDistributedTestRedis(t)
	clientB := openDistributedTestRedis(t)
	relayA := NewRedisRoomRelay(clientA)
	relayB := NewRedisRoomRelay(clientB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := Command{
		RoomID: "room-7", Generation: 42, CommandID: "command-1", Kind: "pause",
		Payload: json.RawMessage(`{"position_seconds":12}`),
	}
	received := make(chan Command, 2)
	subscription, err := relayB.Subscribe(ctx, func(receivedCommand Command) {
		if receivedCommand.RoomID == command.RoomID && receivedCommand.CommandID == command.CommandID {
			received <- receivedCommand
		}
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer func() { _ = subscription.Close() }()

	if err := relayA.Publish(ctx, command); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-received:
		if got.RoomID != command.RoomID || got.Generation != command.Generation || got.CommandID != command.CommandID || got.Kind != command.Kind {
			t.Fatalf("relayed command = %#v, want %#v", got, command)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed command")
	}
	select {
	case duplicate := <-received:
		t.Fatalf("duplicate relay delivery: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTwoReplicaRelayClaimIsDurableAcrossSubscribers(t *testing.T) {
	clientA := openDistributedTestRedis(t)
	clientB := openDistributedTestRedis(t)
	relayA := NewRedisRoomRelay(clientA)
	relayB := NewRedisRoomRelay(clientB)
	ctx := context.Background()
	commandID := "durable-command-" + time.Now().UTC().Format("150405.000000000")

	claimed, err := relayA.Claim(ctx, "room-claim", 17, commandID, time.Minute)
	if err != nil {
		t.Fatalf("Claim(first subscriber): %v", err)
	}
	if !claimed {
		t.Fatal("Claim(first subscriber) = false, want true")
	}

	claimed, err = relayB.Claim(ctx, "room-claim", 17, commandID, time.Minute)
	if err != nil {
		t.Fatalf("Claim(restarted subscriber): %v", err)
	}
	if claimed {
		t.Fatal("Claim(restarted subscriber) = true, want durable duplicate rejection")
	}
}

func TestTwoReplicaConnectionsShareAuthoritativeMemberCount(t *testing.T) {
	pool := openDistributedTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	roomID, userID := insertDistributedRoomFixture(t, pool)

	clientA := openDistributedTestRedis(t)
	clientB := openDistributedTestRedis(t)
	serviceA := NewService(NewRepository(pool), &stubSessions{}, &stubFiles{}, nil, nil, nil)
	serviceB := NewService(NewRepository(pool), &stubSessions{}, &stubFiles{}, nil, nil, nil)
	t.Cleanup(serviceA.Close)
	t.Cleanup(serviceB.Close)
	if err := serviceA.SetDistributedRuntime(ctx, "node-a", NewPostgresRoomOwner(pool), NewRedisRoomRelay(clientA)); err != nil {
		t.Fatalf("SetDistributedRuntime(A): %v", err)
	}
	if err := serviceB.SetDistributedRuntime(ctx, "node-b", NewPostgresRoomOwner(pool), NewRedisRoomRelay(clientB)); err != nil {
		t.Fatalf("SetDistributedRuntime(B): %v", err)
	}

	host := &concurrentRecordingConn{}
	guest := &concurrentRecordingConn{}
	if _, _, err := serviceA.Connect(ctx, roomID, userID, "host", host); err != nil {
		t.Fatalf("Connect(host): %v", err)
	}
	if _, _, err := serviceB.Connect(ctx, roomID, userID, "guest", guest); err != nil {
		t.Fatalf("Connect(guest): %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if host.hasMemberCount(2) && guest.hasMemberCount(2) {
			snapshot, err := serviceB.UpdatePolicy(ctx, roomID, userID, "host", GuestControlPolicyGuestPlayPause)
			if err != nil {
				t.Fatalf("UpdatePolicy through non-owner: %v", err)
			}
			if snapshot.GuestControlPolicy != GuestControlPolicyGuestPlayPause || snapshot.OwnerGeneration <= 0 {
				t.Fatalf("relayed policy snapshot = %#v", snapshot)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("authoritative member count did not reach both replicas; host=%v guest=%v", host.payloads, guest.payloads)
}

func TestTwoReplicaGuestTransportRunsOnOwnerAndReachesBothSocketsOnce(t *testing.T) {
	pool := openDistributedTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	roomID, userID := insertDistributedRoomFixture(t, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE watch_together_rooms SET phase = 'playing', playback_state = 'playing',
		guest_control_policy = 'guest_play_pause', selected_content_id = 'movie-1', is_paused = false
		WHERE id = $1`, roomID); err != nil {
		t.Fatalf("prepare playing room: %v", err)
	}
	sessions := distributedSessions{
		"host-session":  {ID: "host-session", UserID: userID, ProfileID: "host", MediaFileID: 42},
		"guest-session": {ID: "guest-session", UserID: userID, ProfileID: "guest", MediaFileID: 42},
	}
	files := &stubFiles{file: &models.MediaFile{ID: 42, ContentID: "movie-1"}}
	clientA := openDistributedTestRedis(t)
	clientB := openDistributedTestRedis(t)
	serviceA := NewService(NewRepository(pool), sessions, files, nil, nil, nil)
	serviceB := NewService(NewRepository(pool), sessions, files, nil, nil, nil)
	t.Cleanup(serviceA.Close)
	t.Cleanup(serviceB.Close)
	if err := serviceA.SetDistributedRuntime(ctx, "transport-node-a", NewPostgresRoomOwner(pool), NewRedisRoomRelay(clientA)); err != nil {
		t.Fatalf("SetDistributedRuntime(A): %v", err)
	}
	if err := serviceB.SetDistributedRuntime(ctx, "transport-node-b", NewPostgresRoomOwner(pool), NewRedisRoomRelay(clientB)); err != nil {
		t.Fatalf("SetDistributedRuntime(B): %v", err)
	}
	hostConn := &concurrentRecordingConn{}
	guestConn := &concurrentRecordingConn{}
	hostReg, _, err := serviceA.Connect(ctx, roomID, userID, "host", hostConn)
	if err != nil {
		t.Fatalf("Connect(host): %v", err)
	}
	guestReg, _, err := serviceB.Connect(ctx, roomID, userID, "guest", guestConn)
	if err != nil {
		t.Fatalf("Connect(guest): %v", err)
	}
	waitForDistributed(t, func() bool { return hostConn.hasMemberCount(2) && guestConn.hasMemberCount(2) })
	if _, err := serviceA.AttachSessionForConnection(ctx, hostReg, userID, "host", "host-session"); err != nil {
		t.Fatalf("Attach(host): %v", err)
	}
	if _, err := serviceB.AttachSessionForConnection(ctx, guestReg, userID, "guest", "guest-session"); err != nil {
		t.Fatalf("Attach(guest): %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	hostConn.clear()
	guestConn.clear()
	position := 15.0
	if _, err := serviceB.HandleTransportRequestForConnection(ctx, guestReg, userID, "guest", TransportRequest{
		Action: TransportActionSeek, PositionSeconds: &position,
	}); !errors.Is(err, ErrTransportNotAllowed) {
		t.Fatalf("Seek(guest through node-b) error = %v, want transport not allowed", err)
	}
	if _, err := serviceB.HandleTransportRequestForConnection(ctx, guestReg, userID, "guest", TransportRequest{
		Action: TransportActionPause, PositionSeconds: &position, IsPaused: true,
	}); err != nil {
		t.Fatalf("Pause(guest through node-b): %v", err)
	}
	waitForDistributed(t, func() bool {
		return hostConn.hasTransport(TransportActionPause) && guestConn.hasTransport(TransportActionPause)
	})
	time.Sleep(100 * time.Millisecond)
	if got := hostConn.transportCount(TransportActionPause); got != 1 {
		t.Fatalf("host pause deliveries = %d, want exactly 1", got)
	}
	if got := guestConn.transportCount(TransportActionPause); got != 1 {
		t.Fatalf("guest pause deliveries = %d, want exactly 1", got)
	}
}

func TestOwnerFailoverPreservesIngressAttachmentAndUsesHigherGeneration(t *testing.T) {
	pool := openDistributedTestPool(t)
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	roomID, userID := insertDistributedRoomFixture(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE watch_together_rooms SET phase = 'playing', playback_state = 'paused',
		guest_control_policy = 'guest_play_pause', selected_content_id = 'movie-1', is_paused = true
		WHERE id = $1`, roomID); err != nil {
		t.Fatalf("prepare playing room: %v", err)
	}
	sessions := distributedSessions{
		"host-session":  {ID: "host-session", UserID: userID, ProfileID: "host", MediaFileID: 42},
		"guest-session": {ID: "guest-session", UserID: userID, ProfileID: "guest", MediaFileID: 42},
	}
	files := &stubFiles{file: &models.MediaFile{ID: 42, ContentID: "movie-1"}}
	serviceA := NewService(NewRepository(pool), sessions, files, nil, nil, nil)
	serviceB := NewService(NewRepository(pool), sessions, files, nil, nil, nil)
	t.Cleanup(serviceA.Close)
	t.Cleanup(serviceB.Close)
	if err := serviceA.SetDistributedRuntime(ctxA, "failover-node-a", NewPostgresRoomOwner(pool), NewRedisRoomRelay(openDistributedTestRedis(t))); err != nil {
		t.Fatalf("SetDistributedRuntime(A): %v", err)
	}
	if err := serviceB.SetDistributedRuntime(ctxB, "failover-node-b", NewPostgresRoomOwner(pool), NewRedisRoomRelay(openDistributedTestRedis(t))); err != nil {
		t.Fatalf("SetDistributedRuntime(B): %v", err)
	}
	serviceA.ownerLease = 200 * time.Millisecond
	serviceB.ownerLease = 200 * time.Millisecond
	hostConn := &concurrentRecordingConn{}
	guestConn := &concurrentRecordingConn{}
	if _, _, err := serviceA.Connect(ctxA, roomID, userID, "host", hostConn); err != nil {
		t.Fatalf("Connect(host): %v", err)
	}
	guestReg, _, err := serviceB.Connect(ctxB, roomID, userID, "guest", guestConn)
	if err != nil {
		t.Fatalf("Connect(guest): %v", err)
	}
	waitForDistributed(t, func() bool { return guestConn.hasMemberCount(2) })
	if _, err := serviceB.AttachSessionForConnection(ctxB, guestReg, userID, "guest", "guest-session"); err != nil {
		t.Fatalf("Attach(guest): %v", err)
	}
	serviceA.mu.Lock()
	firstGeneration := serviceA.rooms[roomID].ownership.Generation
	serviceA.mu.Unlock()
	cancelA()
	time.Sleep(250 * time.Millisecond)
	guestConn.clear()
	position := 22.0
	if _, err := serviceB.HandleTransportRequestForConnection(ctxB, guestReg, userID, "guest", TransportRequest{
		Action: TransportActionPlay, PositionSeconds: &position,
	}); err != nil {
		t.Fatalf("Play after owner loss: %v", err)
	}
	waitForDistributed(t, func() bool { return guestConn.hasTransport(TransportActionPlay) })
	serviceB.mu.Lock()
	secondGeneration := serviceB.rooms[roomID].ownership.Generation
	serviceB.mu.Unlock()
	if secondGeneration <= firstGeneration {
		t.Fatalf("failover generation = %d, want > %d", secondGeneration, firstGeneration)
	}
	if err := serviceB.ValidateOwnerGeneration(guestReg, firstGeneration); !errors.Is(err, ErrStaleRoomOwnerGeneration) {
		t.Fatalf("ValidateOwnerGeneration(stale) error = %v, want stale generation", err)
	}
	if err := serviceB.ValidateOwnerGeneration(guestReg, secondGeneration); err != nil {
		t.Fatalf("ValidateOwnerGeneration(current): %v", err)
	}
	guestConn.clear()
	staleBody, _ := json.Marshal(map[string]any{
		"type":    "transport_command",
		"command": TransportCommand{CommandID: "stale-owner-command", OwnerGeneration: firstGeneration, Action: TransportActionPause},
	})
	stalePayload, _ := json.Marshal(relayedFramePayload{Body: staleBody})
	if err := serviceA.relay.Publish(context.Background(), Command{
		RoomID: roomID, Generation: firstGeneration, CommandID: "stale-owner-frame", Kind: "frame",
		OriginNodeID: "failover-node-a", TargetNodeID: "failover-node-b", MemberKey: buildMemberKey(userID, "guest"), Payload: stalePayload,
	}); err != nil {
		t.Fatalf("publish stale owner frame: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := guestConn.transportCount(TransportActionPause); got != 0 {
		t.Fatalf("stale owner deliveries = %d, want 0", got)
	}
}

func waitForDistributed(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for distributed Watch Together state")
}

func insertDistributedRoomFixture(t *testing.T, pool *pgxpool.Pool) (string, int) {
	t.Helper()
	ctx := context.Background()
	roomID := "distributed-room-" + time.Now().UTC().Format("150405.000000000")
	var userID int
	if err := pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Skipf("test database has no user fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO watch_together_rooms (id, code, join_token, host_user_id, host_profile_id)
		VALUES ($1, $2, $3, $4, 'host')`, roomID, roomID, roomID, userID); err != nil {
		t.Fatalf("insert room fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM watch_together_rooms WHERE id = $1`, roomID)
	})
	return roomID, userID
}

func openDistributedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openDistributedTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	address := os.Getenv("SILO_TEST_REDIS_ADDR")
	if address == "" {
		address = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		t.Skipf("Redis unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
