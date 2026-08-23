package playback

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRecordMediaAuthAttemptAcceptsOnlyBoundedModes(t *testing.T) {
	for _, mode := range []MediaAuthModeV3{MediaAuthLegacy, MediaAuthHeaderAPI, MediaAuthHeaderProxy} {
		before := mediaAuthCounterValue(t, mediaAuthAttempts.WithLabelValues(string(mode)))
		if !RecordMediaAuthAttempt(mode) {
			t.Fatalf("valid mode %q was rejected", mode)
		}
		after := mediaAuthCounterValue(t, mediaAuthAttempts.WithLabelValues(string(mode)))
		if after != before+1 {
			t.Fatalf("mode %q counter = %v, want %v", mode, after, before+1)
		}
	}

	if RecordMediaAuthAttempt(MediaAuthModeV3("https://private.example/media?token=secret")) {
		t.Fatal("invalid metric label was accepted")
	}
}

func mediaAuthCounterValue(t testing.TB, counter prometheus.Counter) float64 {
	t.Helper()
	metric := &dto.Metric{}
	if err := counter.Write(metric); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return metric.GetCounter().GetValue()
}
