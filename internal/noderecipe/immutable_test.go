package noderecipe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestImmutableRecipeRedis(t *testing.T) {
	rawURL := os.Getenv("SILO_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("SILO_TEST_REDIS_URL not set")
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := t.Context()
	store, proxy := NewStore(client, time.Minute), NewProxyGrantStore(client, time.Minute)
	card := playback.RecipeCard{SessionID: uuid.NewString(), MediaFileID: 42, SourceAudioChannels: 8,
		Executor: &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}}
	successor := card
	nextExecutor := *card.Executor
	nextExecutor.Epoch++
	successor.Executor = &nextExecutor
	keys := []string{immutableKey(*card.Executor), immutableKey(nextExecutor), store.key(card.SessionID), proxy.key(card.SessionID)}
	t.Cleanup(func() {
		if err := client.Del(context.Background(), keys...).Err(); err != nil {
			t.Error(err)
		}
	})
	locator, err := store.PutImmutable(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	got, err := proxy.GetImmutable(ctx, locator)
	if err != nil || !reflect.DeepEqual(got, &card) {
		t.Fatalf("cross-prefix roundtrip: got=%+v err=%v", got, err)
	}
	// Shorten expiry directly: identical replay must not renew the original lease.
	if err := client.PExpire(ctx, keys[0], 20*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	replay, err := proxy.PutImmutable(ctx, card)
	if err != nil || replay != locator {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if ttl := client.PTTL(ctx, keys[0]).Val(); ttl <= 0 || ttl > 20*time.Second {
		t.Fatalf("replay extended expiry: %v", ttl)
	}
	changed := card
	changed.SourceAudioChannels = 6
	if _, err := store.PutImmutable(ctx, changed); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("conflict: %v", err)
	}
	newLocator, err := store.PutImmutable(ctx, successor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.PutImmutable(ctx, card); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteImmutable(ctx, locator); err != nil {
		t.Fatal(err)
	}
	if got, err := proxy.GetImmutable(ctx, newLocator); err != nil || !reflect.DeepEqual(got, &successor) {
		t.Fatalf("stale cleanup changed successor: %+v %v", got, err)
	}
	if _, err := store.GetImmutable(ctx, locator); !errors.Is(err, redis.Nil) {
		t.Fatalf("deleted recipe still present: %v", err)
	}
	if err := store.DeleteImmutable(ctx, locator); err != nil {
		t.Fatalf("delete replay: %v", err)
	}
	badLocator := newLocator
	badLocator.Digest = strings.Repeat("0", 64)
	if _, err := store.GetImmutable(ctx, badLocator); err == nil {
		t.Fatal("digest mismatch accepted")
	}
	if err := store.DeleteImmutable(ctx, badLocator); err == nil {
		t.Fatal("digest mismatch deleted recipe")
	}
	if _, err := proxy.GetImmutable(ctx, newLocator); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, card.SessionID, card); err == nil {
		t.Fatal("legacy Put accepted executor")
	}
	flat, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	enveloped, err := client.Get(ctx, immutableKey(nextExecutor)).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{flat, enveloped} {
		if _, ok := unmarshalCard(data); ok {
			t.Fatal("legacy decoder accepted executor recipe")
		}
		for _, legacy := range []*Store{store, proxy} {
			if err := client.Set(ctx, legacy.key(card.SessionID), data, time.Minute).Err(); err != nil {
				t.Fatal(err)
			}
			if _, ok := legacy.Get(ctx, card.SessionID); ok {
				t.Fatal("legacy reader downgraded executor recipe")
			}
		}
	}
	// Tampered bytes fail even when their JSON remains valid.
	if err := client.Set(ctx, immutableKey(nextExecutor), flat, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetImmutable(ctx, newLocator); err == nil {
		t.Fatal("tampered bytes accepted")
	}
	// Concurrent differing writers for one executor must have exactly one winner.
	raceCard := card
	raceExecutor := *card.Executor
	raceExecutor.ExecutorID = uuid.NewString()
	raceCard.Executor = &raceExecutor
	keys = append(keys, immutableKey(raceExecutor))
	var wg sync.WaitGroup
	results := make(chan error, 12)
	start := make(chan struct{})
	for i := range 12 {
		wg.Go(func() {
			<-start
			candidate := raceCard
			candidate.MediaFileID = i + 100
			_, err := store.PutImmutable(ctx, candidate)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrImmutableConflict) {
			t.Fatalf("unexpected racing write error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("immutable write winners=%d, want 1", winners)
	}
}

func TestImmutableRecipeDisabled(t *testing.T) {
	for _, store := range []*Store{nil, NewStore(nil, 0)} {
		if _, err := store.PutImmutable(t.Context(), playback.RecipeCard{}); err == nil {
			t.Fatal("disabled put succeeded")
		}
		if _, err := store.GetImmutable(t.Context(), playback.ExecutorRecipeLocatorV3{}); err == nil {
			t.Fatal("disabled get succeeded")
		}
		if err := store.DeleteImmutable(t.Context(), playback.ExecutorRecipeLocatorV3{}); err == nil {
			t.Fatal("disabled delete succeeded")
		}
	}
}
