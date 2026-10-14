package preview

import (
	"fmt"
	"testing"
	"time"
)

func TestHourlyLimiterBoundsAdmissionState(t *testing.T) {
	now := time.Now()
	l := NewHourlyLimiter(20)
	l.now = func() time.Time { return now }
	for i := range 100_000 {
		if ok, _ := l.Allow(fmt.Sprintf("client-%d", i)); !ok {
			t.Fatal("capacity reached early")
		}
	}
	if ok, _ := l.Allow("overflow"); ok {
		t.Fatal("new clients can grow admission state beyond its bound")
	}
	if ok, _ := l.Allow("client-0"); !ok {
		t.Fatal("an existing client lost its remaining budget")
	}
	now = now.Add(time.Hour + time.Second)
	if ok, _ := l.Allow("overflow"); !ok {
		t.Fatal("expired clients did not release capacity")
	}
}
