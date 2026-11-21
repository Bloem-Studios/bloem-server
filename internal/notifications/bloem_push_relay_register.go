package notifications

import (
	"context"
	"errors"
	"net/http"
)

// ErrBloemRelayUnavailable reports a server with no Bloem relay sender (no
// push backing), so there is nothing to register.
var ErrBloemRelayUnavailable = errors.New("bloem push relay is unavailable")

// RegisterBloemRelay signs this server up with Bloem's relay again, or rotates
// the credential it holds there, and stores the result under Bloem's settings
// (bloem.notifications.push_relay_*). It is what Silo's administrator register
// (handlers.AdminApplePushHandler.RegisterNotificationRelay) does for Silo's
// relay, which Silo's push settings now manage on their own.
//
// It is the only way back after Bloem's relay disabled this deployment: the
// sender then parks Bloem's credential and never registers again on its own
// (pushSender.handleRelayRejectionLocked). A usable credential at Bloem's relay
// is rotated, and a relay that no longer accepts it (401) gets a fresh
// registration. A parked, missing or legacy credential, or one another Bloem
// relay origin issued, is replaced by a fresh registration. The origin is the
// sender's own: Bloem's relay, or BLOEM_PUSH_RELAY_DEVELOPMENT_URL; the
// sender's origin guard refuses every other.
//
// It holds the sender's renewal lock, so this process's sends never renew or
// replace the credential while it is being registered.
func (s *AdminPushService) RegisterBloemRelay(ctx context.Context) (RelayCredentialResult, error) {
	if s == nil || s.sender == nil || s.sender.settings == nil {
		return RelayCredentialResult{}, ErrBloemRelayUnavailable
	}
	sender := s.sender
	sender.renewMu.Lock()
	defer sender.renewMu.Unlock()

	relayURL := sender.developmentRelayURL
	current := LoadPushRelayCredential(ctx, sender.settings)
	currentURL, err := NormalizePushRelayURL(current.RelayURL, relayURL)
	usable := current.APIKey != "" && !IsLegacyPushRelayKey(current.APIKey) && !current.ReregistrationRequired
	if !usable || err != nil || currentURL != relayURL {
		return RegisterRelayCredential(ctx, sender.settings, sender.client, relayURL)
	}
	current.RelayURL = currentURL
	result, err := RotateRelayCredential(ctx, sender.settings, sender.client, current)
	if relayErr, ok := errors.AsType[RelayCredentialError](err); ok && relayErr.Status == http.StatusUnauthorized {
		return RegisterRelayCredential(ctx, sender.settings, sender.client, relayURL)
	}
	return result, err
}
