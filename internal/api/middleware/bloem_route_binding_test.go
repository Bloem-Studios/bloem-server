package middleware

import (
	"net/http/httptest"
	"testing"
)

func TestBloemAudienceTicketsRejectNestedAndV2Routes(t *testing.T) {
	for _, path := range []string{"/api/v1/other/events/ws", "/evil/api/v1/events/ws", "/api/v2/events/ws", "/api/v1/nested/playback/sessions/s/control/ws", "/api/v1/nested/watch-together/rooms/r/ws"} {
		if _, _, ok := audienceTicketRoute(httptest.NewRequest("GET", path, nil)); ok {
			t.Errorf("ticket accepted nested path %s", path)
		}
	}
	for _, path := range []string{"/api/v1/events/ws", "/events/ws", "/api/v1/playback/sessions/s/control/ws", "/api/v1/watch-together/rooms/r/ws"} {
		if _, _, ok := audienceTicketRoute(httptest.NewRequest("GET", path, nil)); !ok {
			t.Errorf("ticket rejected valid path %s", path)
		}
		if _, _, ok := audienceTicketRoute(httptest.NewRequest("POST", path, nil)); ok {
			t.Errorf("ticket accepted non-handshake %s", path)
		}
	}
}
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
