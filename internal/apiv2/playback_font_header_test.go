package apiv2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
)

func fontHeaderRequest(t *testing.T, ctx context.Context, target string, user int, profile string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if user != 0 {
		token, err := auth.NewJWTService("synthetic-test-secret", time.Hour, time.Hour).GenerateAccessToken(user, "user", "font-login")
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if profile != "" {
		r.Header.Set("X-Profile-Id", profile)
	}
	return r
}

func TestSubtitleFontHeaderHumaLifetime(t *testing.T) {
	media, ffmpeg, font := fontLifetimeMedia(t)
	f := newFontLifetimeFixture(t, media, ffmpeg, "", nil, true)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		f.handler.ServeHTTP(&pausedFontWriter{ResponseWriter: w, request: r, entered: entered, expired: make(chan struct{}, 1), release: release}, r)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(fontHeaderRequest(t, t.Context(), server.URL+f.path, 1, "p-owner"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		close(release)
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("typed font status=%d body=%s", response.StatusCode, body)
	}
	awaitFontSignal(t, entered)
	grant := f.first.Load()
	if grant == nil || grant.Check() != nil {
		close(release)
		t.Fatal("grant closed before actual Huma JSON write")
	}
	close(release)
	body, err := io.ReadAll(response.Body)
	awaitFontSignal(t, done)
	var items []PlaybackSubtitleFont
	if err != nil || json.Unmarshal(body, &items) != nil || len(items) != 1 || items[0].Data != base64.StdEncoding.EncodeToString(font) {
		t.Fatalf("font body length=%d err=%v", len(body), err)
	}
	if grant.Check() == nil {
		t.Fatal("completed Huma response leaked grant")
	}
}

func TestSubtitleFontHeaderHumaIdentity(t *testing.T) {
	media, ffmpeg, _ := fontLifetimeMedia(t)
	for _, tc := range []struct {
		name, profile, mode string
		user                int
		want                int
	}{{"valid", "p-owner", "", 1, 200}, {"signed", "p-owner", "", 1, 200}, {"missing-bearer", "p-owner", "", 0, 401}, {"wrong-account", "p-owner", "", 2, 503}, {"missing-profile", "", "", 1, 503}, {"wrong-profile", "different-profile", "", 1, 404}, {"no-captured-session", "p-owner", "foreign-account", 1, 503}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFontLifetimeFixture(t, media, ffmpeg, tc.mode, nil, tc.name != "signed")
			server := httptest.NewServer(f.handler)
			defer server.Close()
			response, err := server.Client().Do(fontHeaderRequest(t, t.Context(), server.URL+f.path, tc.user, tc.profile))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if tc.want != 200 && f.grants.Load() != 0 {
				t.Fatal("refused identity acquired grant")
			}
		})
	}
}
