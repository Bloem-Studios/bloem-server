package planstore

import (
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestExecutorRecipePublicationAndStaleCleanup(t *testing.T) {
	raw := os.Getenv("SILO_TEST_REDIS_URL")
	if raw == "" {
		t.Skip("SILO_TEST_REDIS_URL is required for real Redis recipe publication")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	recipes := noderecipe.NewStore(client, time.Minute)
	f := newGrantFixture(t)
	f.stage(t)
	put := func(ns playback.ExecutorNamespaceV3, session string) playback.ExecutorRecipeLocatorV3 {
		t.Helper()
		locator, err := recipes.PutImmutable(t.Context(), playback.RecipeCard{SessionID: session, Executor: &ns})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = recipes.DeleteImmutable(t.Context(), locator) })
		return locator
	}
	old := put(f.route.Executor, f.record.SessionID)
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, old); err != nil {
		t.Fatal(err)
	}
	// A lost CAS reply is recovered by reading the authoritative locator.
	got, err := f.store.GetAttemptRecipeLocator(t.Context(), f.authority)
	if err != nil || got == nil || *got != old {
		t.Fatalf("published locator = %+v, %v", got, err)
	}
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, got, old); err != nil {
		t.Fatal(err)
	}
	requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, old))
	for _, mutate := range []func(*playback.AttemptAuthorityV3){
		func(a *playback.AttemptAuthorityV3) { a.OwnerID = uuid.NewString() },
		func(a *playback.AttemptAuthorityV3) { a.Incarnation = uuid.NewString() },
		func(a *playback.AttemptAuthorityV3) { a.Epoch++ },
	} {
		stale := f.authority
		mutate(&stale)
		requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), stale, &old, old))
		_, err := f.store.GetAttemptRecipeLocator(t.Context(), stale)
		requireStaleGrant(t, err)
	}
	other := old
	other.Executor.ExecutorID = uuid.NewString()
	requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, nil, other))
	changed := old
	changed.Digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, &old, changed))
	// Only an expired preparing attempt with no issued grants may be reclaimed.
	expireAuthorityLease(t, f.planstoreFixture, f.record.PlaybackAttemptID)
	request := f.reservation
	request.OwnerID = uuid.NewString()
	next, err := f.store.ReserveAttempt(t.Context(), request)
	if err != nil || !next.Owned {
		t.Fatalf("reclaim: %+v %v", next, err)
	}
	_, err = f.store.GetAttemptRecipeLocator(t.Context(), next.Authority)
	requireStaleGrant(t, err)
	record := f.attemptRecord(uuid.NewString(), request.PlaybackAttemptID, request.RequestDigest)
	route := f.route
	route.Executor = playback.ExecutorNamespaceV3{Incarnation: next.Authority.Incarnation, Epoch: next.Authority.Epoch, ExecutorID: uuid.NewString()}
	if err := f.store.StageAttemptRoute(t.Context(), next.Authority, record, route); err != nil {
		t.Fatal(err)
	}
	current := put(route.Executor, record.SessionID)
	if err := f.store.PublishAttemptRecipeLocator(t.Context(), next.Authority, nil, current); err != nil {
		t.Fatal(err)
	}
	requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), f.authority, &old, old))
	// Delayed old generation writes and cleanup cannot touch the successor key.
	if replay := put(f.route.Executor, f.record.SessionID); replay != old {
		t.Fatal("immutable replay changed locator")
	}
	if err := recipes.DeleteImmutable(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	got, err = f.store.GetAttemptRecipeLocator(t.Context(), next.Authority)
	if err != nil || got == nil || *got != current {
		t.Fatalf("successor locator = %+v, %v", got, err)
	}
	card, err := recipes.GetImmutable(t.Context(), *got)
	if err != nil || card == nil || card.SessionID != record.SessionID {
		t.Fatalf("successor recipe = %+v, %v", card, err)
	}
	if _, err := f.store.BeginAttemptDrain(t.Context(), next.Authority); err != nil {
		t.Fatal(err)
	}
	requireStaleGrant(t, f.store.PublishAttemptRecipeLocator(t.Context(), next.Authority, &current, current))
	_, err = f.store.GetAttemptRecipeLocator(t.Context(), next.Authority)
	requireStaleGrant(t, err)
}
