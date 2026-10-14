package preview

import (
	"testing"
	"time"
)

func TestPreviewEventsBoundEachSessionAndAggregate(t *testing.T) {
	l := NewEventLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	for range 3 {
		if !l.Allow("one") {
			t.Fatal("initial event refused")
		}
	}
	if l.Allow("one") {
		t.Fatal("session cap bypassed")
	}
	admitted := 3
	for i := range 30 {
		if l.Allow(string(rune('a' + i))) {
			admitted++
		}
	}
	if admitted > 20 {
		t.Fatalf("global cap bypassed: %d", admitted)
	}
	now = now.Add(time.Hour)
	if !l.Allow("one") {
		t.Fatal("session did not refill")
	}
}
