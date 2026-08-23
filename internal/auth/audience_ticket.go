package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Audience string

const (
	AudienceEventsWS          Audience = "events_ws"
	AudienceWatchTogetherWS   Audience = "watch_together_ws"
	AudiencePlaybackControlWS Audience = "playback_control_ws"
	AudienceMediaDelivery     Audience = "media_delivery"
	AudienceTicketTTL                  = 30 * time.Second
	AuthMethodAudienceTicket           = "audience_ticket"
)

var (
	ErrAudienceTicketInvalid  = errors.New("audience ticket is invalid, expired, or already used")
	ErrAudienceTicketMismatch = errors.New("audience ticket does not match this route")
)

// AudienceTicket is the principal and exact route authority captured when a
// short-lived URL-safe ticket is minted from an authenticated request.
type AudienceTicket struct {
	Audience   Audience `json:"audience"`
	AccountID  int      `json:"account_id"`
	ProfileID  string   `json:"profile_id,omitempty"`
	ResourceID string   `json:"resource_id,omitempty"`
	Role       string   `json:"role,omitempty"`
	SessionID  string   `json:"session_id,omitempty"`
	TokenType  string   `json:"token_type,omitempty"`
	AuthMethod string   `json:"auth_method,omitempty"`
	ExpiresAt  int64    `json:"expires_at"`
}

type AudienceTicketStore interface {
	Mint(ctx context.Context, value AudienceTicket) (ticket string, ttl time.Duration, err error)
	Consume(ctx context.Context, ticket string, audience Audience, resourceID string) (AudienceTicket, error)
}

func NewAudienceTicketStore(client *redis.Client) AudienceTicketStore {
	if client != nil {
		return &redisAudienceTicketStore{client: client}
	}
	return &memoryAudienceTicketStore{tickets: make(map[string]AudienceTicket)}
}

func newAudienceTicketValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate audience ticket: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func audienceTicketKey(ticket string) string {
	digest := sha256.Sum256([]byte(ticket))
	return hex.EncodeToString(digest[:])
}

func validateAudienceTicket(value AudienceTicket, audience Audience, resourceID string) error {
	if value.AccountID <= 0 || time.Now().UnixNano() >= value.ExpiresAt {
		return ErrAudienceTicketInvalid
	}
	if value.Audience != audience || value.ResourceID != resourceID {
		return ErrAudienceTicketMismatch
	}
	return nil
}

type memoryAudienceTicketStore struct {
	mu      sync.Mutex
	tickets map[string]AudienceTicket
}

func (s *memoryAudienceTicketStore) Mint(_ context.Context, value AudienceTicket) (string, time.Duration, error) {
	if value.AccountID <= 0 || value.Audience == "" {
		return "", 0, fmt.Errorf("audience ticket requires account and audience")
	}
	ticket, err := newAudienceTicketValue()
	if err != nil {
		return "", 0, err
	}
	value.ExpiresAt = time.Now().Add(AudienceTicketTTL).UnixNano()
	now := time.Now().UnixNano()
	s.mu.Lock()
	for key, candidate := range s.tickets {
		if candidate.ExpiresAt <= now {
			delete(s.tickets, key)
		}
	}
	s.tickets[audienceTicketKey(ticket)] = value
	s.mu.Unlock()
	return ticket, AudienceTicketTTL, nil
}

func (s *memoryAudienceTicketStore) Consume(_ context.Context, ticket string, audience Audience, resourceID string) (AudienceTicket, error) {
	key := audienceTicketKey(ticket)
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.tickets[key]
	if !ok {
		return AudienceTicket{}, ErrAudienceTicketInvalid
	}
	if err := validateAudienceTicket(value, audience, resourceID); err != nil {
		if errors.Is(err, ErrAudienceTicketInvalid) {
			delete(s.tickets, key)
		}
		return AudienceTicket{}, err
	}
	delete(s.tickets, key)
	return value, nil
}

type redisAudienceTicketStore struct {
	client *redis.Client
}

const audienceTicketRedisPrefix = "silo:auth:audience-ticket:"

func (s *redisAudienceTicketStore) Mint(ctx context.Context, value AudienceTicket) (string, time.Duration, error) {
	if value.AccountID <= 0 || value.Audience == "" {
		return "", 0, fmt.Errorf("audience ticket requires account and audience")
	}
	ticket, err := newAudienceTicketValue()
	if err != nil {
		return "", 0, err
	}
	value.ExpiresAt = time.Now().Add(AudienceTicketTTL).UnixNano()
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", 0, fmt.Errorf("encode audience ticket: %w", err)
	}
	if err := s.client.Set(ctx, audienceTicketRedisPrefix+audienceTicketKey(ticket), encoded, AudienceTicketTTL).Err(); err != nil {
		return "", 0, fmt.Errorf("store audience ticket: %w", err)
	}
	return ticket, AudienceTicketTTL, nil
}

var consumeAudienceTicketScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current then return nil end
if current ~= ARGV[1] then return nil end
redis.call('DEL', KEYS[1])
return current
`)

func (s *redisAudienceTicketStore) Consume(ctx context.Context, ticket string, audience Audience, resourceID string) (AudienceTicket, error) {
	key := audienceTicketRedisPrefix + audienceTicketKey(ticket)
	encoded, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return AudienceTicket{}, ErrAudienceTicketInvalid
	}
	var value AudienceTicket
	if err := json.Unmarshal(encoded, &value); err != nil {
		return AudienceTicket{}, ErrAudienceTicketInvalid
	}
	if err := validateAudienceTicket(value, audience, resourceID); err != nil {
		if errors.Is(err, ErrAudienceTicketInvalid) {
			_ = s.client.Del(ctx, key).Err()
		}
		return AudienceTicket{}, err
	}
	result, err := consumeAudienceTicketScript.Run(ctx, s.client, []string{key}, string(encoded)).Result()
	if err != nil || result == nil {
		return AudienceTicket{}, ErrAudienceTicketInvalid
	}
	return value, nil
}
