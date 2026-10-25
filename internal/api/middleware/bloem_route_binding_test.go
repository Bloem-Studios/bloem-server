package middleware

import (
	"testing"
)

func TestBloemLiveHLSTokensRejectNestedRoutes(t *testing.T) {
	for _, path := range []string{"/evil/livetv/live-hls/s/index.m3u8", "/api/v1/nested/livetv/live-hls/s/index.m3u8", "/api/v2/livetv/live-hls/s/index.m3u8", "/api/v1/livetv/live-hls/s/.."} {
		if _, ok := liveHLSDeliverySession(path); ok {
			t.Errorf("token accepted nested path %s", path)
		}
	}
	for _, path := range []string{"/api/v1/livetv/live-hls/s/index.m3u8", "/api/bloem/v1/livetv/live-hls/s/index.m3u8", "/livetv/live-hls/s/index.m3u8"} {
		if id, ok := liveHLSDeliverySession(path); !ok || id != "s" {
			t.Errorf("token rejected supported path %s", path)
		}
	}
}
