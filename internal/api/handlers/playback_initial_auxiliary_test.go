package handlers

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestInitialProxyAuxiliaryURLsPreserveCapturedPins(t *testing.T) {
	plan := &playback.PlanV3{SessionID: "session", Stream: playback.StreamV3{URL: "https://proxy.example.test/prefix/stream/direct/signed"}, Subtitle: playback.SubtitleDecisionV3{Inventory: []playback.SubtitleInventoryItemV3{{URL: "/stream/session/subtitles/1.ass?file_id=42&external_subtitle_key=identity", FontBundleURL: "/stream/session/subtitles/1/fonts?file_id=42&external_subtitle_key=identity"}}, Artifact: &playback.SubtitleArtifactV3{URL: "/api/v1/stream/session/subtitles/1.ass?file_id=42&external_subtitle_key=identity&st=old"}}}
	if err := bindInitialProxyAuxiliaryURLsV3(plan, "profile"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{plan.Subtitle.Inventory[0].URL, plan.Subtitle.Inventory[0].FontBundleURL, plan.Subtitle.Artifact.URL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host != "proxy.example.test" || !strings.HasPrefix(u.Path, "/prefix/stream/v3/session/subtitles/") || u.Query().Get("file_id") != "42" || u.Query().Get("external_subtitle_key") != "identity" || u.Query().Has("st") {
			t.Fatalf("captured URL: %s %v", raw, err)
		}
	}
	if len(plan.Stream.Headers) != 1 || plan.Stream.Headers["X-Profile-Id"] != "profile" {
		t.Fatal("selector absent or credentials added", plan.Stream.Headers)
	}
	before := plan.Subtitle.Artifact.URL
	if err := bindInitialProxyAuxiliaryURLsV3(plan, "profile"); err != nil || before != plan.Subtitle.Artifact.URL {
		t.Fatal("successor rewrote captured auxiliary identity", err)
	}
	for _, raw := range []string{"https://other.example.test/stream/v3/session/subtitles/1.ass?file_id=42", "/stream/foreign/subtitles/1.ass?file_id=42", "/stream/session/subtitles/1.ass"} {
		plan.Subtitle.Artifact.URL = raw
		if err := bindInitialProxyAuxiliaryURLsV3(plan, "profile"); err == nil {
			t.Fatal("changed auxiliary identity accepted", raw)
		}
	}
}

func TestInitialSubtitleSuccessorRefreshesOnlyExecutorReference(t *testing.T) {
	plan := &playback.PlanV3{Subtitle: playback.SubtitleDecisionV3{Artifact: &playback.SubtitleArtifactV3{URL: "/api/v1/stream/session/subtitles/0.ass?file_id=42&external_subtitle_key=identity&st=old"}}}
	bindInitialSubtitleURLsV3(plan, "new")
	u, err := url.Parse(plan.Subtitle.Artifact.URL)
	if err != nil || u.Query().Get("st") != "new" || len(u.Query()["st"]) != 1 || u.Query().Get("external_subtitle_key") != "identity" || u.Query().Get("file_id") != "42" {
		t.Fatalf("successor reference: %s %v", plan.Subtitle.Artifact.URL, err)
	}
}
