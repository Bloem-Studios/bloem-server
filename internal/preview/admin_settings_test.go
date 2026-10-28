package preview_test

import (
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func TestPreviewAdminSettingsKeepIssuerOwnership(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	s := validSettings(t, insertAccount(t, ctx, pool, "user"), pub)
	if err := store.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	for _, issuer := range []string{"second-operator", ""} {
		next := s
		next.Enabled, next.Issuer = false, issuer
		if err := store.Put(ctx, next); !errors.Is(err, preview.ErrInvalidSettings) {
			t.Errorf("issuer %q takeover: %v", issuer, err)
		}
	}
	got, err := store.Get(ctx)
	if err != nil || got.Issuer != s.Issuer {
		t.Fatalf("ownership changed: %q (%v)", got.Issuer, err)
	}
}

func TestPreviewAdminSettingsRejectUnsafePresentation(t *testing.T) {
	ctx, pool := previewTestDB(t)
	store := preview.NewStore(pool)
	pub, _ := newKey(t)
	s := validSettings(t, insertAccount(t, ctx, pool, "user"), pub)
	for name, change := range map[string]func(*preview.Settings){
		"javascript CTA":  func(s *preview.Settings) { s.CTAURL = "javascript:alert(1)" },
		"credential URL":  func(s *preview.Settings) { s.LaunchURL = "https://user:pass@example.test/launch" },
		"relative store":  func(s *preview.Settings) { s.StorefrontURL = "//example.test/store" },
		"unknown ceiling": func(s *preview.Settings) { s.MaturityCeiling = "everything" },
		"duplicate keys":  func(s *preview.Settings) { s.TicketPublicKeys = append(s.TicketPublicKeys, pub) },
	} {
		t.Run(name, func(t *testing.T) {
			next := s
			change(&next)
			if err := store.Put(ctx, next); !errors.Is(err, preview.ErrInvalidSettings) {
				t.Fatalf("unsafe settings accepted: %v", err)
			}
		})
	}
}

// The operator's second banner button (the subscription, beside the trial)
// travels from the settings patch to what the preview shows.
func TestPreviewAdminSettingsCarryTheSubscribeButton(t *testing.T) {
	ctx, pool := previewTestDB(t)
	actor := insertAccount(t, ctx, pool, "admin")
	a := preview.NewAdmin(pool, staticServerID("server-test"))
	id, err := a.EnsureAccount(ctx, actor, "operator-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false,account_id=NULL; DELETE FROM users WHERE username='bloem-preview'`)
	})
	pub, _ := newKey(t)
	s := validSettings(t, id, pub)
	s.Issuer, s.SubscribeLabel, s.SubscribeURL = "operator-test", "", ""
	if err = a.Configure(ctx, actor, s, "lib.example.test"); err != nil {
		t.Fatal(err)
	}
	label, link := "Subscribe!", "https://shop.example.test/plans"
	if err = a.UpdateSettings(ctx, actor, preview.SettingsPatch{Issuer: "operator-test", SubscribeLabel: &label, SubscribeURL: &link}, "lib.example.test"); err != nil {
		t.Fatal(err)
	}
	shown, err := preview.NewStore(pool).Presentation(ctx, id, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if shown.SubscribeLabel != label || shown.SubscribeURL != link {
		t.Fatalf("presentation subscribe = %q %q", shown.SubscribeLabel, shown.SubscribeURL)
	}
}
