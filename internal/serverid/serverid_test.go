package serverid

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// memoryStore is a settings store WITHOUT insert-if-absent, so it exercises the
// fallback path. getErr/setErr inject transient database failures.
type memoryStore struct {
	mu       sync.Mutex
	values   map[string]string
	getErr   error
	setErr   error
	getCalls int
	setCalls int
}

func newMemoryStore(values map[string]string) *memoryStore {
	store := &memoryStore{values: map[string]string{}}
	for key, value := range values {
		store.values[key] = value
	}
	return store
}

func (s *memoryStore) Get(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.values[key], nil
}

func (s *memoryStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCalls++
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = value
	return nil
}

func (s *memoryStore) value(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

func (s *memoryStore) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls
}

// racingStore adds insert-if-absent semantics and can simulate the two
// multi-node outcomes this algorithm exists to survive: another node winning the
// insert (concurrentWinner), and a store that reports a lost race while leaving
// the row empty (abandonWrite).
type racingStore struct {
	*memoryStore
	concurrentWinner string
	abandonWrite     bool
	setIfAbsentErr   error
	setIfAbsentCalls int
}

func newRacingStore(values map[string]string) *racingStore {
	return &racingStore{memoryStore: newMemoryStore(values)}
}

func (s *racingStore) SetIfAbsent(_ context.Context, key, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setIfAbsentCalls++
	if s.setIfAbsentErr != nil {
		return false, s.setIfAbsentErr
	}
	if existing := s.values[key]; existing != "" {
		return false, nil
	}
	if s.abandonWrite {
		return false, nil
	}
	if s.concurrentWinner != "" {
		s.values[key] = s.concurrentWinner
		return false, nil
	}
	s.values[key] = value
	return true, nil
}

func (s *racingStore) conditionalWrites() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setIfAbsentCalls
}

func TestResolveMintsOnceAndCaches(t *testing.T) {
	store := newRacingStore(nil)
	resolver := NewResolver(store)

	first, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(first) != 36 {
		t.Fatalf("minted id = %q, want a UUID string", first)
	}
	if got := store.value(SettingKey); got != first {
		t.Fatalf("stored id = %q, want the resolved %q", got, first)
	}
	// Winning the insert is already proof the value is the stored one: a
	// read-back would be a pointless round trip on the common path.
	if reads := store.reads(); reads != 1 {
		t.Fatalf("Get calls = %d, want 1 (the initial miss only)", reads)
	}

	second, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if second != first {
		t.Fatalf("second Resolve = %q, want the cached %q", second, first)
	}
	if reads := store.reads(); reads != 1 {
		t.Fatalf("Get calls after a cached resolve = %d, want 1", reads)
	}
	if writes := store.conditionalWrites(); writes != 1 {
		t.Fatalf("SetIfAbsent calls = %d, want 1", writes)
	}
}

func TestResolveReusesAnExistingRow(t *testing.T) {
	store := newRacingStore(map[string]string{SettingKey: "  pre-existing-instance-id  "})
	resolver := NewResolver(store)

	resolved, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != "pre-existing-instance-id" {
		t.Fatalf("resolved = %q, want the trimmed pre-existing value", resolved)
	}
	if writes := store.conditionalWrites(); writes != 0 {
		t.Fatalf("SetIfAbsent calls = %d, want none against an existing identifier", writes)
	}
	if store.setCalls != 0 {
		t.Fatalf("Set calls = %d, want none against an existing identifier", store.setCalls)
	}
}

func TestResolveAdoptsTheConcurrentWinner(t *testing.T) {
	store := newRacingStore(nil)
	store.concurrentWinner = "winner-instance"
	resolver := NewResolver(store)

	resolved, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved != "winner-instance" {
		t.Fatalf("resolved = %q, want the value the other node wrote", resolved)
	}
	if reads := store.reads(); reads != 2 {
		t.Fatalf("Get calls = %d, want 2 (initial miss, then the read-back that adopts the winner)", reads)
	}

	again, err := resolver.Resolve(context.Background())
	if err != nil || again != "winner-instance" {
		t.Fatalf("second Resolve = %q, %v, want the cached winner", again, err)
	}
}

func TestResolveFailsWhenTheRowIsStillEmptyAfterAWrite(t *testing.T) {
	store := newRacingStore(nil)
	store.abandonWrite = true
	resolver := NewResolver(store)

	resolved, err := resolver.Resolve(context.Background())
	if err == nil {
		t.Fatalf("Resolve = %q, nil; want an error rather than an identifier nothing persisted", resolved)
	}
	if resolved != "" {
		t.Fatalf("Resolve returned %q alongside an error", resolved)
	}

	// The failure must not be papered over on the next call either: a fresh
	// value per attempt is an identity that changes per request.
	retried, err := resolver.Resolve(context.Background())
	if err == nil {
		t.Fatalf("retried Resolve = %q, nil; want the same refusal", retried)
	}
}

func TestResolvePropagatesStoreFailures(t *testing.T) {
	readFailure := errors.New("transient read failure")
	writeFailure := errors.New("transient write failure")

	for _, tc := range []struct {
		name  string
		store Store
		want  error
	}{
		{
			name:  "read fails",
			store: func() Store { s := newRacingStore(nil); s.getErr = readFailure; return s }(),
			want:  readFailure,
		},
		{
			name:  "conditional write fails",
			store: func() Store { s := newRacingStore(nil); s.setIfAbsentErr = writeFailure; return s }(),
			want:  writeFailure,
		},
		{
			name:  "fallback write fails",
			store: func() Store { s := newMemoryStore(nil); s.setErr = writeFailure; return s }(),
			want:  writeFailure,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := NewResolver(tc.store).Resolve(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Resolve = %q, %v; want an error wrapping %v", resolved, err, tc.want)
			}
		})
	}
}

func TestResolveFallsBackToSetWithoutConditionalWrites(t *testing.T) {
	store := newMemoryStore(nil)
	resolver := NewResolver(store)

	resolved, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if store.value(SettingKey) != resolved || store.setCalls != 1 {
		t.Fatalf("stored %q after %d Set calls, want the resolved %q after 1", store.value(SettingKey), store.setCalls, resolved)
	}
}

func TestResolveWithoutAStoreIsUnavailable(t *testing.T) {
	resolved, err := NewResolver(nil).Resolve(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Resolve = %q, %v; want ErrUnavailable", resolved, err)
	}
}

func TestResolveMintsOneIdentifierUnderConcurrency(t *testing.T) {
	store := newRacingStore(nil)
	resolver := NewResolver(store)

	const callers = 8
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range ids {
		go func(slot int) {
			defer wg.Done()
			ids[slot], errs[slot] = resolver.Resolve(context.Background())
		}(i)
	}
	wg.Wait()

	for slot, id := range ids {
		if errs[slot] != nil {
			t.Fatalf("Resolve %d: %v", slot, errs[slot])
		}
		if id == "" || id != ids[0] {
			t.Fatalf("concurrent identifiers disagree: %v", ids)
		}
	}
	if writes := store.conditionalWrites(); writes != 1 {
		t.Fatalf("SetIfAbsent calls = %d, want 1", writes)
	}
}
