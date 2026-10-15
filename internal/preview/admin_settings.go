package preview

import (
	"context"
	"time"
)

// SettingsPatch updates presentation and budgets atomically. Account identity
// and ticket keys use separate operations, so stale forms cannot restore keys.
type SettingsPatch struct {
	Issuer            string  `json:"issuer"`
	Enabled           *bool   `json:"enabled,omitempty"`
	Host              *string `json:"host,omitempty"`
	Brand             *string `json:"brand,omitempty"`
	CTAURL            *string `json:"cta_url,omitempty"`
	CTALabel          *string `json:"cta_label,omitempty"`
	StorefrontURL     *string `json:"storefront_url,omitempty"`
	LaunchURL         *string `json:"launch_url,omitempty"`
	MaturityCeiling   *string `json:"maturity_ceiling,omitempty"`
	SessionTTLSeconds *int    `json:"session_ttl_seconds,omitempty"`
	IdleTTLSeconds    *int    `json:"idle_ttl_seconds,omitempty"`
	MaxConcurrent     *int    `json:"max_concurrent,omitempty"`
}

func (a *Admin) UpdateSettings(ctx context.Context, actor int, p SettingsPatch, servedHost string) error {
	if p.Issuer == "" {
		return ErrInvalidSettings
	}
	tx, s, err := a.begin(ctx, actor, p.Issuer)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if s.AccountID == 0 {
		return ErrUnavailable
	}
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	for _, field := range []struct {
		value  *string
		target *string
	}{{p.Host, &s.Host}, {p.Brand, &s.Brand}, {p.CTAURL, &s.CTAURL}, {p.CTALabel, &s.CTALabel}, {p.StorefrontURL, &s.StorefrontURL}, {p.LaunchURL, &s.LaunchURL}, {p.MaturityCeiling, &s.MaturityCeiling}} {
		if field.value != nil {
			*field.target = *field.value
		}
	}
	if p.SessionTTLSeconds != nil {
		if *p.SessionTTLSeconds < 300 || *p.SessionTTLSeconds > 86400 {
			return ErrInvalidSettings
		}
		s.SessionTTL = time.Duration(*p.SessionTTLSeconds) * time.Second
	}
	if p.IdleTTLSeconds != nil {
		if *p.IdleTTLSeconds < 60 || *p.IdleTTLSeconds > 7200 {
			return ErrInvalidSettings
		}
		s.IdleTTL = time.Duration(*p.IdleTTLSeconds) * time.Second
	}
	if p.MaxConcurrent != nil {
		s.MaxConcurrent = *p.MaxConcurrent
	}
	if err = a.configureInTransaction(ctx, tx, s, servedHost); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
