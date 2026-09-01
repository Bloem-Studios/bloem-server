package notifications

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAlertBodyDefaultsAndTrims(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	got, err := NormalizeAlertBody(AlertBody{Title: "  Maintenance ", Body: " tonight ", Dismissible: true}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Maintenance" || got.Body != "tonight" {
		t.Fatalf("expected trimmed text, got %+v", got)
	}
	if got.Severity != SeverityInfo {
		t.Fatalf("expected default severity info, got %q", got.Severity)
	}
	if !got.Dismissible {
		t.Fatal("info alerts keep the author's dismissible flag")
	}
}

func TestNormalizeAlertBodyCriticalForcesNonDismissible(t *testing.T) {
	got, err := NormalizeAlertBody(AlertBody{Title: "Outage", Severity: "CRITICAL", Dismissible: true}, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Severity != SeverityCritical {
		t.Fatalf("severity should be lower-cased, got %q", got.Severity)
	}
	if got.Dismissible {
		t.Fatal("critical alerts must never be dismissible")
	}
}

func TestNormalizeAlertBodyRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	cases := map[string]AlertBody{
		"missing title":       {Body: "x"},
		"unknown severity":    {Title: "t", Severity: "fatal"},
		"long title":          {Title: strings.Repeat("a", alertTitleMaxLen+1)},
		"javascript link":     {Title: "t", Deeplink: "javascript:alert(1)"},
		"protocol relative":   {Title: "t", Deeplink: "//evil.example/x"},
		"image not http":      {Title: "t", ImageURL: "ftp://x/y.png"},
		"cta missing url":     {Title: "t", CTA: &AlertCTA{Label: "Go"}},
		"cta bad url":         {Title: "t", CTA: &AlertCTA{Label: "Go", URL: "data:text/html,x"}},
		"expires in the past": {Title: "t", ExpiresAt: &past},
	}
	for name, in := range cases {
		if _, err := NormalizeAlertBody(in, now); !errors.Is(err, ErrAlertBodyInvalid) {
			t.Errorf("%s: expected ErrAlertBodyInvalid, got %v", name, err)
		}
	}
}

func TestNormalizeAlertBodyAcceptsLinksAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).In(time.FixedZone("x", 3600))
	got, err := NormalizeAlertBody(AlertBody{
		Title:     "t",
		Deeplink:  "bloem://settings/status",
		ImageURL:  "https://cdn.example/banner.png",
		CTA:       &AlertCTA{Label: " Open ", URL: "https://example.com/x"},
		ExpiresAt: &future,
	}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Deeplink != "bloem://settings/status" {
		t.Fatalf("bloem:// deeplink should be accepted verbatim, got %q", got.Deeplink)
	}
	for _, link := range []string{"https://example.com/x", "bloem://home", "/item/abc"} {
		if !validAlertLink(link) {
			t.Errorf("validAlertLink(%q) should accept", link)
		}
	}
	for _, link := range []string{"javascript:alert(1)", "data:text/html,x", "//evil.example/x", "http://example.com"} {
		if validAlertLink(link) {
			t.Errorf("validAlertLink(%q) should reject", link)
		}
	}
	if got.CTA == nil || got.CTA.Label != "Open" {
		t.Fatalf("cta should be trimmed and kept, got %+v", got.CTA)
	}
	if got.ExpiresAt == nil || got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expires_at should be normalized to UTC, got %v", got.ExpiresAt)
	}
	if _, ok := ParseAlertBody([]byte(`{"title":"t","severity":"warning"}`)); !ok {
		t.Fatal("ParseAlertBody should decode a stored body")
	}
	if _, ok := ParseAlertBody(nil); ok {
		t.Fatal("ParseAlertBody should report absent bodies")
	}
}
