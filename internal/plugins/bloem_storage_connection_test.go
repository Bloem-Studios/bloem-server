package plugins

import "testing"

func TestPickConnectionConfigPrefersConnectionKey(t *testing.T) {
	configs := []*RuntimeConfig{{Key: "other", Value: map[string]any{"a": 1}}, {Key: "connection", Value: map[string]any{"bucket": "b"}}}
	if got := pickConnectionConfig(configs); got["bucket"] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := pickConnectionConfig(configs[:1]); got["a"] != 1 {
		t.Fatalf("fallback to first config failed: %v", got)
	}
	if pickConnectionConfig(nil) != nil {
		t.Fatal("expected nil")
	}
}
