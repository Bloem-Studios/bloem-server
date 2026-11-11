package handlers

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// NotificationsHandler serves the profile-scoped notification inbox,
// preferences, capability, and websocket-ticket endpoints. All routes are
// mounted behind RequireProfile.
type NotificationsHandler struct {
	system *notifications.System
	hub    *evt.Hub
	// dismissStore overrides System.Deliveries for the dismiss route (tests).
	dismissStore deliveryDismisser
	bloemNotificationsHandlerExt
	// displayTokens mints the long-lived display token returned from Apple
	// push registration. Nil when JWT auth is not configured; registration
	// then omits the token and the extension falls back to the access token.
	displayTokens ApplePushDisplayTokenIssuer
}

// ApplePushDisplayTokenIssuer mints the profile-scoped token the iOS
// Notification Service extension presents to the display endpoint.
type ApplePushDisplayTokenIssuer interface {
	GenerateApplePushDisplayToken(userID int, role, sessionID, profileID string, impersonatorUserID *int) (string, time.Time, error)
}

// SetApplePushDisplayTokenIssuer wires the token issuer used by
// HandleRegisterApplePushDevice. A nil *auth.JWTService is treated as unset.
func (h *NotificationsHandler) SetApplePushDisplayTokenIssuer(issuer ApplePushDisplayTokenIssuer) {
	if jwt, ok := issuer.(*auth.JWTService); ok && jwt == nil {
		h.displayTokens = nil
		return
	}
	h.displayTokens = issuer
}

// NewNotificationsHandler creates a NotificationsHandler.
func NewNotificationsHandler(system *notifications.System, hub *evt.Hub) *NotificationsHandler {
	return &NotificationsHandler{system: system, hub: hub}
}

type capabilityResponse struct {
	InApp       capabilityInApp          `json:"in_app"`
	ApplePush   capabilityPush           `json:"apple_push"`
	AndroidPush capabilityPush           `json:"android_push"`
	WebPush     capabilityWebPush        `json:"web_push"`
	Webhooks    capabilityWebhooks       `json:"webhooks"`
	Email       capabilityAccountChannel `json:"email"`
	Discord     capabilityAccountChannel `json:"discord"`
	// S-1 (docs/architecture/admin-announcements.md): clients gate the alert
	// banner, the dismiss action, and type-specific rendering on these.
	Announcements  bool     `json:"announcements"`
	SupportedTypes []string `json:"supported_types"`
	Dismiss        bool     `json:"dismiss"`
	// S-3 (docs/architecture/seasonal-scheduling.md): the same block as
	// the branding payload. Key present = capability exists, [] = nothing
	// active, key absent = dormant (registry not wired). Carries the active
	// packs for this account (deployment-wide + the account's organizations).
	Ambience *[]ambience.Wire `json:"ambience,omitempty"`
	// S-2 (docs/architecture/playback-overlays.md): key present =
	// promotions are delivered on the listed surfaces; absent = dormant.
	Promotions *capabilityPromotions `json:"promotions,omitempty"`
	// S-5a (docs/architecture/admin-remote-control.md): the sender side exists;
	// clients still only receive per their own advertised list.
	RemoteControl capabilityRemoteControl `json:"remote_control"`
}

// capabilityAccountChannel describes an account-level digest channel (email,
// Discord DMs).
type capabilityAccountChannel struct {
	Available bool `json:"available"`
	// Modes lists the cadences users may pick (per-episode is an admin
	// allowance); DigestHour tells the UI when daily digests go out.
	Modes      []string `json:"modes"`
	DigestHour int      `json:"digest_hour"`
}

type capabilityWebPush struct {
	Available bool   `json:"available"`
	PublicKey string `json:"public_key,omitempty"`
}

type capabilityInApp struct {
	Enabled bool `json:"enabled"`
}

type capabilityPush struct {
	Available      bool     `json:"available"`
	Provider       string   `json:"provider"`
	SupportedModes []string `json:"supported_modes"`
	// DisplayToken is true when Apple push registration returns a
	// long-lived display token for the Notification Service extension.
	// Additive; Android registration never carries one.
	DisplayToken bool `json:"display_token,omitempty"`
}

type capabilityWebhooks struct {
	Available      bool     `json:"available"`
	MaxPerProfile  int      `json:"max_per_profile"`
	SupportedTypes []string `json:"supported_types"`
}

// NotificationCapabilitiesView is shared by both API projections.
type NotificationCapabilitiesView = capabilityResponse

func (h *NotificationsHandler) NotificationCapabilities(ctx context.Context) NotificationCapabilitiesView {
	webhooks := capabilityWebhooks{Available: false, MaxPerProfile: 0, SupportedTypes: []string{}}
	if h.system.Webhooks != nil && h.system.Settings.WebhooksEnabled(ctx) {
		webhooks = capabilityWebhooks{
			Available:      true,
			MaxPerProfile:  h.system.Settings.WebhooksMaxPerProfile(ctx),
			SupportedTypes: []string{"discord", "generic"},
		}
	}
	webPush := capabilityWebPush{}
	if h.system.WebPush != nil && h.system.Settings.WebPushEnabled(ctx) {
		if publicKey, err := h.system.WebPush.PublicKey(ctx); err == nil && publicKey != "" {
			webPush = capabilityWebPush{Available: true, PublicKey: publicKey}
		}
	}
	email := capabilityAccountChannel{Modes: []string{}}
	if h.system.EmailAvailable(ctx) {
		modes := []string{notifications.ChannelModeDailyDigest}
		if h.system.Settings.EmailAllowPerEpisode(ctx) {
			modes = append(modes,
				notifications.ChannelModePerEpisode,
				notifications.ChannelModePerEpisodeAndDigest)
		}
		email = capabilityAccountChannel{
			Available:  true,
			Modes:      modes,
			DigestHour: h.system.Settings.EmailDigestHour(ctx),
		}
	}
	discordCap := capabilityAccountChannel{Modes: []string{}}
	if h.system.DiscordAvailable(ctx) {
		modes := []string{notifications.ChannelModeDailyDigest}
		if h.system.Settings.DiscordAllowPerEpisode(ctx) {
			modes = append(modes,
				notifications.ChannelModePerEpisode,
				notifications.ChannelModePerEpisodeAndDigest)
		}
		discordCap = capabilityAccountChannel{
			Available:  true,
			Modes:      modes,
			DigestHour: h.system.Settings.DiscordDigestHour(ctx),
		}
	}
	applePush := capabilityPush{Available: false, Provider: "off", SupportedModes: []string{"in_app_only"}}
	androidPush := capabilityPush{Available: false, Provider: "off", SupportedModes: []string{"in_app_only"}}
	// Like web push above, availability requires both the wiring (cipher/store)
	// and the admin delivery toggle: Available must mean "setup will actually
	// deliver", not "the server could store a token".
	if h.system.PushDevices != nil && h.system.PushDevices.Available() {
		if h.system.Settings.ApplePushDeliveryEnabled(ctx) {
			applePush = capabilityPush{
				Available:      true,
				Provider:       notifications.PushProviderSiloRelay,
				SupportedModes: []string{notifications.PushModePrivatePush, notifications.PushModeInAppOnly},
				DisplayToken:   h.displayTokens != nil,
			}
		}
		if h.system.Settings.AndroidPushDeliveryEnabled(ctx) {
			androidPush = capabilityPush{
				Available:      true,
				Provider:       notifications.PushProviderSiloRelay,
				SupportedModes: []string{notifications.PushModePrivatePush, notifications.PushModeInAppOnly},
			}
		}
	}
	resp := capabilityResponse{
		InApp:       capabilityInApp{Enabled: h.system.Settings.UIEnabled(ctx)},
		ApplePush:   applePush,
		AndroidPush: androidPush,
		WebPush:     webPush,
		Webhooks:    webhooks,
		Email:       email,
		Discord:     discordCap,
	}
	h.bloemDecorateCapabilities(ctx, &resp)
	return resp
}
