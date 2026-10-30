package notifications

import (
	"context"
	"errors"
	"testing"
	"time"
)

type upgradePromptBackendStub struct {
	prompt UpgradePrompt
	sent   map[string]AlertBody
}

func (s *upgradePromptBackendStub) upgradePrompt(context.Context) (UpgradePrompt, error) {
	return s.prompt, nil
}

func (s *upgradePromptBackendStub) dispatchUpgradePrompt(_ context.Context, _ int, _ string, id string, body AlertBody) error {
	if s.sent == nil {
		s.sent = map[string]AlertBody{}
	}
	s.sent[id] = body
	return nil
}

func TestUpgradePromptStaysQuietUntilTurnedOn(t *testing.T) {
	backend := &upgradePromptBackendStub{}
	n := &UpgradePromptNotifier{backend: backend, now: time.Now}
	if err := n.notify(t.Context(), 7, "p1"); err != nil {
		t.Fatal(err)
	}
	if len(backend.sent) != 0 {
		t.Fatalf("sent %d prompts while turned off", len(backend.sent))
	}
}

func TestUpgradePromptIsOncePerProfilePerDay(t *testing.T) {
	backend := &upgradePromptBackendStub{prompt: UpgradePrompt{
		Enabled: true, Title: "Watch on more screens", Body: "Your plan is in use on every screen it allows.",
		CTALabel: "See plans", CTAURL: "https://shop.example.test/services",
	}}
	day := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	n := &UpgradePromptNotifier{backend: backend, now: func() time.Time { return day }}
	for range 3 {
		if err := n.notify(t.Context(), 7, "p1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.notify(t.Context(), 7, "p2"); err != nil {
		t.Fatal(err)
	}
	if len(backend.sent) != 2 {
		t.Fatalf("delivery IDs = %d, want one per profile", len(backend.sent))
	}
	day = day.Add(24 * time.Hour)
	if err := n.notify(t.Context(), 7, "p1"); err != nil {
		t.Fatal(err)
	}
	if len(backend.sent) != 3 {
		t.Fatalf("delivery IDs = %d after a day, want a new one", len(backend.sent))
	}
	for _, body := range backend.sent {
		if body.CTA == nil || body.CTA.URL != "https://shop.example.test/services" || !body.Dismissible || body.ExpiresAt == nil {
			t.Fatalf("alert body = %+v", body)
		}
	}
}

func TestUpgradePromptRefusesUnsafeOrIncompleteSettings(t *testing.T) {
	for name, prompt := range map[string]UpgradePrompt{
		"plain http":     {Enabled: true, Title: "t", CTALabel: "l", CTAURL: "http://shop.example.test"},
		"script":         {CTAURL: "javascript:alert(1)"},
		"no button":      {Enabled: true, Title: "t", CTAURL: "https://shop.example.test"},
		"title too long": {Title: string(make([]rune, 81))},
	} {
		if _, err := prompt.normalize(); !errors.Is(err, ErrUpgradePromptInvalid) {
			t.Errorf("%s: err = %v, want ErrUpgradePromptInvalid", name, err)
		}
	}
	if _, err := (UpgradePrompt{}).normalize(); err != nil {
		t.Fatalf("an empty, switched-off prompt was refused: %v", err)
	}
}

func TestUpgradePromptSettingsRoundTripDatabase(t *testing.T) {
	pool := schemaDB(t, "upgrade_prompt_")
	if _, err := pool.Exec(t.Context(), `CREATE TABLE bloem_upgrade_prompt (LIKE public.bloem_upgrade_prompt INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	off, err := LoadUpgradePrompt(t.Context(), pool)
	if err != nil || off.Enabled {
		t.Fatalf("before saving = %+v, %v; want off", off, err)
	}
	saved, err := SaveUpgradePrompt(t.Context(), pool, UpgradePrompt{
		Enabled: true, Title: " Watch on more screens ", CTALabel: "See plans", CTAURL: "https://shop.example.test/services",
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadUpgradePrompt(t.Context(), pool)
	if err != nil || !loaded.Enabled || loaded.Title != "Watch on more screens" || !loaded.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
}
