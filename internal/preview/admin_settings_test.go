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
