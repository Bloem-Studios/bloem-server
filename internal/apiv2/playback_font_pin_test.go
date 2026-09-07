package apiv2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubtitleFontHTTPEmbeddedPin(t *testing.T) {
	media, ffmpeg, font := fontLifetimeMedia(t)
	for _, tc := range []struct {
		name, mode, query string
		shiftOrdinal      bool
		status            int
	}{
		{name: "actual stream index", query: "&embedded_stream_index=1", status: http.StatusOK},
		{name: "pin survives ordinal shift", query: "&embedded_stream_index=1", shiftOrdinal: true, status: http.StatusOK},
		{name: "ordinal is not stream index", query: "&embedded_stream_index=0", status: http.StatusNotFound},
		{name: "missing stream", query: "&embedded_stream_index=99", status: http.StatusNotFound},
		{name: "duplicate pin", query: "&embedded_stream_index=1&embedded_stream_index=1", status: http.StatusBadRequest},
		{name: "conflicting duplicate", query: "&embedded_stream_index=1&embedded_stream_index=0", status: http.StatusBadRequest},
		{name: "empty pin", query: "&embedded_stream_index=", status: http.StatusUnprocessableEntity},
		{name: "negative pin", query: "&embedded_stream_index=-1", status: http.StatusUnprocessableEntity},
		{name: "malformed pin", query: "&embedded_stream_index=no", status: http.StatusUnprocessableEntity},
		{name: "external pin conflict", query: "&embedded_stream_index=1&external_subtitle_key=other", status: http.StatusUnprocessableEntity},
		{name: "foreign profile", mode: "foreign-profile", query: "&embedded_stream_index=1", status: http.StatusForbidden},
		{name: "foreign account", mode: "foreign-account", query: "&embedded_stream_index=1", status: http.StatusForbidden},
		{name: "invalid authority", mode: "bad-signature", query: "&embedded_stream_index=1", status: http.StatusServiceUnavailable},
		{name: "wrong egress", mode: "proxy", query: "&embedded_stream_index=1", status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The fixture's combined ordinal is 0 but its actual stream index is 1.
			f := newFontLifetimeFixture(t, media, ffmpeg, tc.mode, nil)
			path := f.path
			if tc.shiftOrdinal {
				path = strings.Replace(path, "/subtitles/0/", "/subtitles/9/", 1)
			}
			server := httptest.NewServer(f.handler)
			defer server.Close()
			response, err := server.Client().Do(fontRequest(t, t.Context(), server.URL+path+tc.query))
			if err != nil {
				t.Fatal("font request failed") // Do not print the signed request URL.
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, tc.status, body)
			}
			if tc.status != http.StatusOK {
				if bytes.Contains(body, []byte("synthetic.ttf")) {
					t.Fatal("refusal exposed fonts")
				}
				return
			}
			var items []PlaybackSubtitleFont
			if err := json.Unmarshal(body, &items); err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Name != "synthetic.ttf" || items[0].Data != base64.StdEncoding.EncodeToString(font) {
				t.Fatal("pinned font bundle differs from generated attachment")
			}
		})
	}
}
