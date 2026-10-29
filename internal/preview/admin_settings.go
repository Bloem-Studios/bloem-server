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
	ItemCTALabel      *string `json:"item_cta_label,omitempty" doc:"Replaces Subscribe to watch, read or listen on title pages; empty keeps that wording."`
	CTAMessage        *string `json:"cta_message,omitempty" doc:"The subscribe dialog's sentence; empty keeps the default."`
	SubscribeLabel    *string `json:"subscribe_label,omitempty" doc:"The banner's subscription button beside the trial; empty keeps the client's wording."`
	SubscribeURL      *string `json:"subscribe_url,omitempty" doc:"Where the banner's subscription button goes; empty keeps the plain exit to the storefront."`
	StorefrontURL     *string `json:"storefront_url,omitempty"`
	LaunchURL         *string `json:"launch_url,omitempty"`
	MaturityCeiling   *string `json:"maturity_ceiling,omitempty"`
	SessionTTLSeconds *int    `json:"session_ttl_seconds,omitempty"`
	IdleTTLSeconds    *int    `json:"idle_ttl_seconds,omitempty"`
	MaxConcurrent     *int    `json:"max_concurrent,omitempty"`
	FreePlayEnabled   *bool   `json:"free_play_enabled,omitempty" doc:"Lets a guest whose launch ticket carries a signed allowance stream video until it is used."`
	FreePlayMaxActive *int    `json:"free_play_max_active,omitempty" minimum:"1" maximum:"1000" doc:"Allowances that may stream at once; a further start is refused as busy."`
	FreePlayURL       *string `json:"free_play_url,omitempty" doc:"The storefront page where a guest asks for free play; empty shows no free-play button."`
	FreePlayLabel     *string `json:"free_play_label,omitempty" maxLength:"80" doc:"The free-play button's wording; empty keeps the client's (Play free for N minutes)."`
	FreePlaySeconds   *int    `json:"free_play_seconds,omitempty" minimum:"60" maximum:"14400" doc:"The allowance the storefront offers, for the button's wording; the ticket carries the real one."`
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
	if p.FreePlayEnabled != nil {
		s.FreePlayEnabled = *p.FreePlayEnabled
	}
	for _, field := range []struct {
		value  *string
		target *string
	}{{p.Host, &s.Host}, {p.Brand, &s.Brand}, {p.CTAURL, &s.CTAURL}, {p.CTALabel, &s.CTALabel}, {p.ItemCTALabel, &s.ItemCTALabel}, {p.CTAMessage, &s.CTAMessage}, {p.SubscribeLabel, &s.SubscribeLabel}, {p.SubscribeURL, &s.SubscribeURL}, {p.StorefrontURL, &s.StorefrontURL}, {p.LaunchURL, &s.LaunchURL}, {p.MaturityCeiling, &s.MaturityCeiling}, {p.FreePlayURL, &s.FreePlayURL}, {p.FreePlayLabel, &s.FreePlayLabel}} {
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
	for _, field := range []struct {
		value  *int
		target *int
	}{{p.FreePlayMaxActive, &s.FreePlayMaxActive}, {p.FreePlaySeconds, &s.FreePlaySeconds}} {
		if field.value != nil {
			if *field.value == 0 {
				return ErrInvalidSettings
			}
			*field.target = *field.value
		}
	}
	if err = a.configureInTransaction(ctx, tx, s, servedHost); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
