package compatgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitAllowsOneHalfOpenProbeAndIgnoresStaleCompletions(t *testing.T) {
	now := time.Now()
	g := New(Config{FailureThreshold: 1, CircuitCooldown: time.Second, Now: func() time.Time { return now }})
	first, _ := g.beginAttempt(KindJellyfin)
	stale, _ := g.beginAttempt(KindJellyfin)
	g.finishAttempt(KindJellyfin, first, circuitFailure)
	g.finishAttempt(KindJellyfin, stale, circuitSuccess)
	if _, blocked := g.beginAttempt(KindJellyfin); !blocked {
		t.Fatal("stale response closed circuit")
	}
	now = now.Add(time.Second)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	var probe circuitAttempt
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt, blocked := g.beginAttempt(KindJellyfin)
			if !blocked {
				admitted.Add(1)
				probe = attempt
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d probes", admitted.Load())
	}
	g.finishAttempt(KindJellyfin, probe, circuitIgnored)
	retry, blocked := g.beginAttempt(KindJellyfin)
	if blocked || !retry.probe {
		t.Fatal("abandoned probe did not release permit")
	}
	g.finishAttempt(KindJellyfin, probe, circuitSuccess)
	if _, blocked := g.beginAttempt(KindJellyfin); !blocked {
		t.Fatal("old probe completed a newer attempt")
	}
	g.finishAttempt(KindJellyfin, retry, circuitSuccess)
	if _, blocked := g.beginAttempt(KindJellyfin); blocked {
		t.Fatal("successful probe did not close circuit")
	}
}

func TestCircuitDoesNotCountClientFailures(t *testing.T) {
	for _, failure := range []error{context.Canceled, &http.MaxBytesError{Limit: 4}} {
		states := &fakeStates{}
		states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
		transport := &recordingTransport{respond: func(*http.Request) (*http.Response, error) { return nil, failure }}
		g := New(Config{States: states, Transport: transport, FailureThreshold: 1})
		for i := 0; i < 3; i++ {
			g.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/System/Info", nil))
		}
		if len(transport.recorded()) != 3 {
			t.Fatalf("client failure %v opened shared circuit", failure)
		}
	}
}
