package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeHealthSettings struct {
	mu     sync.Mutex
	values map[string]string
	err    error
	reads  atomic.Int32
	block  chan struct{}
}

func (f *fakeHealthSettings) Get(ctx context.Context, key string) (string, error) {
	f.reads.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.values[key], nil
}

type fakeHealthNamer struct {
	mu   sync.Mutex
	name string
	err  error
}

func (f *fakeHealthNamer) ServerName(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.name, f.err
}

type fakeHealthIdentity struct {
	id    string
	err   error
	calls atomic.Int32
}

func (f *fakeHealthIdentity) ServerID(context.Context) (string, error) {
	f.calls.Add(1)
	return f.id, f.err
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func newTestBloemHealth(settings *fakeHealthSettings, names *fakeHealthNamer, identity *fakeHealthIdentity) (*BloemHealthHandler, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)}
	h := NewBloemHealthHandler(settings, names, identity)
	h.now = clock.Now
	return h, clock
}

func getBloemHealth(t *testing.T, h http.Handler) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

func wantHealthBody(t *testing.T, body map[string]any, want map[string]any) {
	t.Helper()
	if len(body) != len(want) {
		t.Fatalf("body = %v, want %v", body, want)
	}
	for key, value := range want {
		if body[key] != value {
			t.Fatalf("body = %v, want %v", body, want)
		}
	}
}

// An upgraded Silo database answers the server ID it answered before the
// compatibility removal, so clients that stored it keep the same server.
func TestBloemHealthAnswersThePreservedServerID(t *testing.T) {
	settings := &fakeHealthSettings{values: map[string]string{bloemCompatServerIDKey: " b2d9e6c9-1237-5add-a687-5dae547ece33 "}}
	identity := &fakeHealthIdentity{id: "deployment-id"}
	h, _ := newTestBloemHealth(settings, &fakeHealthNamer{name: "Wave"}, identity)

	wantHealthBody(t, getBloemHealth(t, h), map[string]any{
		"status": "ok", "server_name": "Wave", "server_id": "b2d9e6c9-1237-5add-a687-5dae547ece33",
	})
	if identity.calls.Load() != 0 {
		t.Fatal("consulted the deployment identity although the preserved ID exists")
	}
}

// A database that never had the compatibility row (a fresh Bloem install)
// answers the deployment identity.
func TestBloemHealthFallsBackToTheDeploymentIdentity(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"absent": {},
		"blank":  {bloemCompatServerIDKey: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newTestBloemHealth(&fakeHealthSettings{values: values}, &fakeHealthNamer{name: "Bloem"}, &fakeHealthIdentity{id: "deployment-id"})
			wantHealthBody(t, getBloemHealth(t, h), map[string]any{
				"status": "ok", "server_name": "Bloem", "server_id": "deployment-id",
			})
		})
	}
}

// A failed read is not a fresh install: health answers without identity and
// tries again, rather than caching the deployment identity in place of the
// preserved one.
func TestBloemHealthOutageOmitsIdentityAndRetries(t *testing.T) {
	settings := &fakeHealthSettings{values: map[string]string{bloemCompatServerIDKey: "preserved-id"}, err: errors.New("database down")}
	names := &fakeHealthNamer{err: errors.New("database down")}
	identity := &fakeHealthIdentity{id: "deployment-id"}
	h, clock := newTestBloemHealth(settings, names, identity)

	wantHealthBody(t, getBloemHealth(t, h), map[string]any{"status": "ok"})
	if identity.calls.Load() != 0 {
		t.Fatal("fell back to the deployment identity during an outage")
	}
	// Within the retry wait health answers from the cache without reading.
	reads := settings.reads.Load()
	getBloemHealth(t, h)
	if settings.reads.Load() != reads {
		t.Fatal("read the database again inside the retry wait")
	}

	settings.mu.Lock()
	settings.err = nil
	settings.mu.Unlock()
	names.mu.Lock()
	names.err, names.name = nil, "Wave"
	names.mu.Unlock()
	clock.now = clock.now.Add(bloemHealthRetryWait)
	wantHealthBody(t, getBloemHealth(t, h), map[string]any{
		"status": "ok", "server_name": "Wave", "server_id": "preserved-id",
	})
}

// The server ID is resolved once; the name follows branding renames after
// its cache expires, and a failed rename read keeps the last good name.
func TestBloemHealthCachesTheIDAndRefreshesTheName(t *testing.T) {
	settings := &fakeHealthSettings{values: map[string]string{bloemCompatServerIDKey: "preserved-id"}}
	names := &fakeHealthNamer{name: "Old"}
	h, clock := newTestBloemHealth(settings, names, &fakeHealthIdentity{})

	getBloemHealth(t, h)
	settings.mu.Lock()
	settings.values[bloemCompatServerIDKey] = "changed-id"
	settings.mu.Unlock()
	names.mu.Lock()
	names.name = "New"
	names.mu.Unlock()

	clock.now = clock.now.Add(bloemHealthNameTTL - time.Second)
	wantHealthBody(t, getBloemHealth(t, h), map[string]any{"status": "ok", "server_name": "Old", "server_id": "preserved-id"})

	clock.now = clock.now.Add(time.Second)
	wantHealthBody(t, getBloemHealth(t, h), map[string]any{"status": "ok", "server_name": "New", "server_id": "preserved-id"})

	names.mu.Lock()
	names.err = errors.New("database down")
	names.mu.Unlock()
	clock.now = clock.now.Add(bloemHealthNameTTL)
	wantHealthBody(t, getBloemHealth(t, h), map[string]any{"status": "ok", "server_name": "New", "server_id": "preserved-id"})
}

// A hung database delays health by the read bound at most, and requests that
// arrive during a refresh answer at once from the cache.
func TestBloemHealthDoesNotWaitOnAHungDatabase(t *testing.T) {
	settings := &fakeHealthSettings{block: make(chan struct{})}
	defer close(settings.block)
	h, _ := newTestBloemHealth(settings, &fakeHealthNamer{name: "Wave"}, &fakeHealthIdentity{id: "deployment-id"})

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
		first <- rec
	}()
	deadline := time.Now().Add(5 * time.Second)
	for settings.reads.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first request never reached the database")
		}
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	wantHealthBody(t, getBloemHealth(t, h), map[string]any{"status": "ok"})
	if elapsed := time.Since(started); elapsed > bloemHealthIdentityTimeout/2 {
		t.Fatalf("a concurrent request waited %s for the refresh", elapsed)
	}
	select {
	case rec := <-first:
		wantHealthBody(t, getBloemHealth(t, recordedHandler{rec}), map[string]any{"status": "ok", "server_name": "Wave"})
	case <-time.After(5 * bloemHealthIdentityTimeout):
		t.Fatal("the refreshing request was not bounded")
	}
}

// recordedHandler replays a recorded response, so a response produced on
// another goroutine is checked on the test goroutine.
type recordedHandler struct{ rec *httptest.ResponseRecorder }

func (h recordedHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	for key, values := range h.rec.Header() {
		w.Header()[key] = values
	}
	w.WriteHeader(h.rec.Code)
	_, _ = w.Write(h.rec.Body.Bytes())
}
