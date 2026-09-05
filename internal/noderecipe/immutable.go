package noderecipe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/playback"
)

const immutableKeyPrefix = "silo:executor-recipe:v1:"
const immutableEnvelopeVersion = 3

var ErrImmutableConflict = errors.New("executor recipe already contains different bytes")

type immutableEnvelope struct {
	Version int                 `json:"version"`
	Recipe  playback.RecipeCard `json:"executor_recipe"`
}

var putImmutableScript = redis.NewScript(`
local existing = redis.call("GET", KEYS[1])
if existing then
  if existing == ARGV[1] then return 0 end
  return -1
end
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2], "NX")
return 1
`)

var deleteImmutableScript = redis.NewScript(`
local existing = redis.call("GET", KEYS[1])
if not existing then return 0 end
if existing ~= ARGV[1] then return -1 end
return redis.call("DEL", KEYS[1])
`)

func immutableKey(executor playback.ExecutorNamespaceV3) string {
	// JSON preserves component boundaries even when identifiers contain separators.
	data, _ := json.Marshal(executor)
	return immutableKeyPrefix + recipeDigest(data)
}

func recipeDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PutImmutable stores a descriptor, not active playback authority. Replays
// preserve the original expiry and cannot change an executor's recipe.
func (s *Store) PutImmutable(ctx context.Context, card playback.RecipeCard) (playback.ExecutorRecipeLocatorV3, error) {
	var locator playback.ExecutorRecipeLocatorV3
	if s == nil || s.rdb == nil {
		return locator, errors.New("immutable recipe store unavailable")
	}
	if card.Executor == nil {
		return locator, errors.New("executor namespace required")
	}
	if err := card.Executor.Validate(); err != nil {
		return locator, err
	}
	// Do not use legacy marshalCard: it intentionally drops some audio fields.
	data, err := json.Marshal(immutableEnvelope{Version: immutableEnvelopeVersion, Recipe: card})
	if err != nil {
		return locator, fmt.Errorf("marshal immutable recipe: %w", err)
	}
	locator = playback.ExecutorRecipeLocatorV3{Executor: *card.Executor, Digest: recipeDigest(data)}
	result, err := putImmutableScript.Run(ctx, s.rdb, []string{immutableKey(locator.Executor)}, data, max(s.ttl.Milliseconds(), 1)).Int()
	if err != nil {
		return playback.ExecutorRecipeLocatorV3{}, fmt.Errorf("put immutable recipe: %w", err)
	}
	if result < 0 {
		return playback.ExecutorRecipeLocatorV3{}, ErrImmutableConflict
	}
	return locator, nil
}

func (s *Store) loadImmutable(ctx context.Context, locator playback.ExecutorRecipeLocatorV3) (*playback.RecipeCard, []byte, error) {
	if s == nil || s.rdb == nil {
		return nil, nil, errors.New("immutable recipe store unavailable")
	}
	if err := locator.Validate(); err != nil {
		return nil, nil, err
	}
	data, err := s.rdb.Get(ctx, immutableKey(locator.Executor)).Bytes()
	if err != nil {
		return nil, nil, fmt.Errorf("load immutable recipe: %w", err)
	}
	if recipeDigest(data) != locator.Digest {
		return nil, nil, errors.New("immutable recipe digest mismatch")
	}
	var envelope immutableEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode immutable recipe: %w", err)
	}
	if envelope.Version != immutableEnvelopeVersion || envelope.Recipe.Executor == nil || *envelope.Recipe.Executor != locator.Executor {
		return nil, nil, errors.New("immutable recipe namespace or version mismatch")
	}
	return &envelope.Recipe, data, nil
}

// GetImmutable verifies the descriptor bytes and namespace. The caller must
// independently resolve current authority before using the descriptor.
func (s *Store) GetImmutable(ctx context.Context, locator playback.ExecutorRecipeLocatorV3) (*playback.RecipeCard, error) {
	card, _, err := s.loadImmutable(ctx, locator)
	return card, err
}

// DeleteImmutable removes only the exact descriptor, never a stable session key.
func (s *Store) DeleteImmutable(ctx context.Context, locator playback.ExecutorRecipeLocatorV3) error {
	_, data, err := s.loadImmutable(ctx, locator)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	result, err := deleteImmutableScript.Run(ctx, s.rdb, []string{immutableKey(locator.Executor)}, data).Int()
	if err != nil {
		return fmt.Errorf("delete immutable recipe: %w", err)
	}
	if result < 0 {
		return ErrImmutableConflict
	}
	return nil
}
