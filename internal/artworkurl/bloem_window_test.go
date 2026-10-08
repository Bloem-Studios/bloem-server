package artworkurl

import (
	"testing"
	"time"
)

func TestSignWindowKeepsOneURLPerWindow(t *testing.T) {
	signer := NewSigner("test-secret", time.Hour)
	week := 7 * 24 * time.Hour
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Truncate(week)
	first, expires := signer.SignWindow("storage-covers/1/original.r.img", start.Add(time.Minute), week)
	later, _ := signer.SignWindow("storage-covers/1/original.r.img", start.Add(week-time.Minute), week)
	if first != later {
		t.Fatalf("URL changed within its window: %q %q", first, later)
	}
	if remaining := expires.Sub(start.Add(week - time.Minute)); remaining < week {
		t.Fatalf("URL valid for only %s", remaining)
	}
	if next, _ := signer.SignWindow("storage-covers/1/original.r.img", start.Add(week), week); next == first {
		t.Fatal("URL never rotates")
	}
	if key, ok := signer.SignedKey(first, start.Add(time.Minute)); !ok || key != "storage-covers/1/original.r.img" {
		t.Fatalf("windowed URL does not verify: %q %v", key, ok)
	}
}
