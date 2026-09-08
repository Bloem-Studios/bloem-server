package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/playback/planstore"
	"github.com/google/uuid"
)

type initialRuntimeGrantReader struct{ runtime *planstore.ExecutorRuntime }

func (r initialRuntimeGrantReader) Get(ctx context.Context, sessionID string) (*playback.RecipeCard, bool) {
	card, bound, err := r.runtime.ResolveCurrentSession(ctx, sessionID)
	return card, bound && err == nil && card != nil
}

func assertInitialPublishedAuxiliary(t *testing.T, f *initialHTTPFixture, decision playback.DecisionResponseV3) {
	t.Helper()
	login := uuid.NewString()
	if err := auth.NewSessionRepository(f.pool).Create(t.Context(), models.AuthSession{ID: login, UserID: f.userID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	bearer, err := auth.NewJWTService(f.handler.JWTSecret, time.Hour, time.Hour).GenerateAccessToken(f.userID, "user", login)
	if err != nil {
		t.Fatal(err)
	}
	rawPlan, err := json.Marshal(decision.PlaybackPlan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawPlan), bearer) || decision.PlaybackPlan.Stream.Headers["Authorization"] != "" {
		t.Fatal("viewer bearer persisted in plan")
	}
	inventory := decision.PlaybackPlan.Subtitle.Inventory
	if len(inventory) == 0 || inventory[0].URL == "" {
		t.Fatalf("missing published auxiliary inventory: %+v", inventory)
	}
	raw := inventory[0].URL
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Query().Has("st") || parsed.Query().Has("token") || !strings.Contains(parsed.Path, "/stream/v3/"+decision.SessionID+"/subtitles/") {
		t.Fatalf("invalid published URL: %s %v", raw, err)
	}
	fontCount := 0
	for _, item := range inventory {
		if item.FontBundleURL == "" {
			continue
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, item.FontBundleURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Profile-Id", f.request.ProfileID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		fontCount++
		var fonts []playback.SubtitleFontBundleItem
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &fonts) != nil || len(fonts) != 1 {
			t.Fatalf("published font status=%d body=%s", resp.StatusCode, body)
		}
	}
	if len(f.file.SubtitleTracks) > 0 && fontCount != 1 {
		t.Fatal("embedded ASS did not publish font URL")
	}
	t.Logf("published auxiliary plan verified with %d font bundle", fontCount)
	for _, mismatch := range []string{"", "profile", "file"} {
		target := raw
		profile := f.request.ProfileID
		if mismatch == "profile" {
			profile = uuid.NewString()
		}
		if mismatch == "file" {
			u := *parsed
			q := u.Query()
			q.Set("file_id", "2147483647")
			u.RawQuery = q.Encode()
			target = u.String()
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-Profile-Id", profile)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if mismatch == "" {
			if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Auxiliary fixture") {
				t.Fatalf("published auxiliary status=%d body=%s", response.StatusCode, body)
			}
		} else if response.StatusCode == http.StatusOK {
			t.Fatalf("%s mismatch returned auxiliary bytes", mismatch)
		}
	}
}

const initialAuxiliaryASS = `[Script Info]
Title: Auxiliary fixture
ScriptType: v4.00+
PlayResX: 320
PlayResY: 180
[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Liberation Sans,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:40.00,Default,,0,0,0,,Auxiliary fixture
`
