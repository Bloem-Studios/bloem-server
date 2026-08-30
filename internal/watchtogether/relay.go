package watchtogether

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const watchTogetherRelayChannel = "bloem:watch-together:commands:v1"

var ErrRoomRelayUnavailable = errors.New("watch together room relay unavailable")

type Command struct {
	RoomID        string          `json:"room_id"`
	Generation    int64           `json:"generation"`
	CommandID     string          `json:"command_id"`
	Kind          string          `json:"kind"`
	OriginNodeID  string          `json:"origin_node_id,omitempty"`
	TargetNodeID  string          `json:"target_node_id,omitempty"`
	MemberKey     string          `json:"member_key,omitempty"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type RelaySubscription interface {
	Close() error
}

type RoomRelay interface {
	Publish(context.Context, Command) error
	Subscribe(context.Context, func(Command)) (RelaySubscription, error)
	Claim(context.Context, string, int64, string, time.Duration) (bool, error)
}

type RedisRoomRelay struct {
	client *redis.Client
}

func NewRedisRoomRelay(client *redis.Client) *RedisRoomRelay {
	return &RedisRoomRelay{client: client}
}

func (relay *RedisRoomRelay) Claim(ctx context.Context, roomID string, generation int64, commandID string, ttl time.Duration) (bool, error) {
	if relay == nil || relay.client == nil || roomID == "" || generation <= 0 || commandID == "" || ttl <= 0 {
		return false, ErrRoomRelayUnavailable
	}
	key := fmt.Sprintf("bloem:watch-together:claim:v1:%s:%d:%s", roomID, generation, commandID)
	// go-redis implements SetNX as SET key value EX ttl NX, which is exactly the
	// "Set with NX option" the deprecation notice asks for; the notice is echoed
	// from the Redis SETNX command docs, not from go-redis dropping the method.
	// Rewriting this claim primitive to SetArgs would change the not-acquired
	// signal from (false, nil) to redis.Nil for no behavioral gain.
	//nolint:staticcheck // SA1019: see above.
	return relay.client.SetNX(ctx, key, "1", ttl).Result()
}

func (relay *RedisRoomRelay) Publish(ctx context.Context, command Command) error {
	if relay == nil || relay.client == nil || command.RoomID == "" || command.Generation <= 0 || command.CommandID == "" || command.Kind == "" {
		return ErrRoomRelayUnavailable
	}
	encoded, err := json.Marshal(command)
	if err != nil {
		return err
	}
	return relay.client.Publish(ctx, watchTogetherRelayChannel, encoded).Err()
}

func (relay *RedisRoomRelay) Subscribe(ctx context.Context, handler func(Command)) (RelaySubscription, error) {
	if relay == nil || relay.client == nil || handler == nil {
		return nil, ErrRoomRelayUnavailable
	}
	pubsub := relay.client.Subscribe(ctx, watchTogetherRelayChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, err
	}
	subscription := &redisRelaySubscription{pubsub: pubsub, done: make(chan struct{}), seen: make(map[string]struct{})}
	go subscription.consume(ctx, handler)
	return subscription, nil
}

type redisRelaySubscription struct {
	pubsub *redis.PubSub
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	seen   map[string]struct{}
}

func (subscription *redisRelaySubscription) consume(ctx context.Context, handler func(Command)) {
	defer close(subscription.done)
	channel := subscription.pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-channel:
			if !ok {
				return
			}
			var command Command
			if json.Unmarshal([]byte(message.Payload), &command) != nil || command.RoomID == "" || command.Generation <= 0 || command.CommandID == "" || command.Kind == "" {
				continue
			}
			subscription.mu.Lock()
			if _, exists := subscription.seen[command.CommandID]; exists {
				subscription.mu.Unlock()
				continue
			}
			subscription.seen[command.CommandID] = struct{}{}
			if len(subscription.seen) > 4096 {
				subscription.seen = map[string]struct{}{command.CommandID: {}}
			}
			subscription.mu.Unlock()
			handler(command)
		}
	}
}

func (subscription *redisRelaySubscription) Close() error {
	var err error
	subscription.once.Do(func() { err = subscription.pubsub.Close() })
	return err
}
