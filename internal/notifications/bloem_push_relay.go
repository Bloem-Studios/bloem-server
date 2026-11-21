package notifications

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/secret"
)

// A Bloem server delivers push through two relays, one per app family, and
// the boundary between them is the app that registered the device:
//
//   - Silo's apps (silo-android, silo-apple) register through Silo's routes
//     (push_devices: the Apple topic org.siloserver.silo, an FCM token from
//     Silo's Firebase project). Only Silo's relay (push.siloserver.org,
//     Silo's APNs/FCM credentials) can deliver to them, so they go there and
//     only there, through Silo's own sender and Silo's own settings, exactly
//     as on a Silo server. DefaultPushRelayURL stays Silo's.
//   - Bloem's apps register through Bloem's routes (bloem_admin_push_devices:
//     the topic com.bloemstudios.admin, an FCM token from the bloem-push
//     project). They go to Bloem's relay (BloemPushRelayURL) and only there,
//     through a second sender that keeps its credential under Bloem's own
//     setting names (bloemRelayKey) and whose transport refuses every other
//     origin.
//
// Neither relay ever sees the other's tokens, capability or deliveries.
// Silo's registration route accepts no Bloem app identity (Silo's Apple topic
// only; FCM carries none), so a Bloem consumer app needs a Bloem registration
// path of its own before it ships a Bloem push identity.
// docs/architecture/bloem-push-relays.md describes the whole boundary.

// BloemPushRelayURL is Bloem's relay (Bloem-Studios/bloem-push-relay), the
// origin of every push to a Bloem app. Its capabilities name it as issuer.
const BloemPushRelayURL = "https://push.bloem-studios.com"

// bloemPushRelayDevelopmentEnv names a staging Bloem relay origin, as
// SILO_PUSH_RELAY_DEVELOPMENT_URL does for Silo's.
const bloemPushRelayDevelopmentEnv = "BLOEM_PUSH_RELAY_DEVELOPMENT_URL"

// Bloem's relay credential, one setting per field of Silo's. Do not rename
// these: the API key is encrypted at rest, bound to its key name.
const (
	bloemRelaySettingPrefix           = "bloem."
	BloemSettingPushRelayURL          = bloemRelaySettingPrefix + SettingPushRelayURL
	BloemSettingPushRelayDeploymentID = bloemRelaySettingPrefix + SettingPushRelayDeploymentID
	BloemSettingPushRelayAPIKey       = bloemRelaySettingPrefix + SettingPushRelayAPIKey
	BloemSettingPushRelayExpiresAt    = bloemRelaySettingPrefix + SettingPushRelayExpiresAt
	BloemSettingPushRelayKeyPrefix    = bloemRelaySettingPrefix + SettingPushRelayKeyPrefix
	BloemSettingPushRelayReregister   = bloemRelaySettingPrefix + SettingPushRelayReregister
)

// relayCredentialKeys are Silo's names for one relay credential.
var relayCredentialKeys = []string{
	SettingPushRelayURL, SettingPushRelayDeploymentID, SettingPushRelayAPIKey,
	SettingPushRelayExpiresAt, SettingPushRelayKeyPrefix, SettingPushRelayReregister,
}

// BloemRelaySettingKeys are Bloem's relay credential settings.
func BloemRelaySettingKeys() []string {
	keys := make([]string, 0, len(relayCredentialKeys))
	for _, key := range relayCredentialKeys {
		keys = append(keys, bloemRelayKey(key))
	}
	return keys
}

func isRelayCredentialKey(key string) bool {
	for _, k := range relayCredentialKeys {
		if k == key {
			return true
		}
	}
	return false
}

// bloemRelayKey is Bloem's name for one of Silo's relay credential settings.
func bloemRelayKey(siloKey string) string { return bloemRelaySettingPrefix + siloKey }

func bloemRelayOrigins(developmentURL string) map[string]bool {
	origins := map[string]bool{BloemPushRelayURL: true}
	if dev := strings.TrimRight(strings.TrimSpace(developmentURL), "/"); dev != "" {
		origins[dev] = true
	}
	return origins
}

// bloemRelayStore shows Silo's relay credential code Bloem's credential under
// Silo's setting names, so that code runs unchanged against Bloem's relay.
// Every read and write of a relay credential setting goes to Bloem's name;
// Silo's values are never visible through it and never written by it. Other
// settings pass through.
type bloemRelayStore struct {
	inner SettingReader
	// defaultURL stands in for an empty URL, as DefaultPushRelayURL does on
	// Silo's side, so a credential read through this store is never Silo's.
	defaultURL string
}

func (s bloemRelayStore) Get(ctx context.Context, key string) (string, error) {
	if !isRelayCredentialKey(key) {
		return s.inner.Get(ctx, key)
	}
	value, err := s.inner.Get(ctx, bloemRelayKey(key))
	if err != nil {
		return "", err
	}
	if key == SettingPushRelayURL && strings.TrimSpace(value) == "" {
		return s.defaultURL, nil
	}
	return value, nil
}

func (s bloemRelayStore) SetMany(ctx context.Context, values map[string]string) error {
	writer, ok := s.inner.(settingBatchWriter)
	if !ok {
		return errors.New("push relay settings do not support batch writes")
	}
	return writer.SetMany(ctx, s.rename(values))
}

func (s bloemRelayStore) UpdateAtomic(ctx context.Context, update func(current map[string]string) (map[string]string, error)) error {
	updater, ok := s.inner.(settingsAtomicUpdater)
	if !ok {
		return errRelaySettingsNotAtomic
	}
	return updater.UpdateAtomic(ctx, func(current map[string]string) (map[string]string, error) {
		view := make(map[string]string, len(current))
		for key, value := range current {
			view[key] = value
		}
		for _, key := range relayCredentialKeys {
			view[key] = current[bloemRelayKey(key)]
		}
		if strings.TrimSpace(view[SettingPushRelayURL]) == "" {
			view[SettingPushRelayURL] = s.defaultURL
		}
		writes, err := update(view)
		if err != nil {
			return nil, err
		}
		return s.rename(writes), nil
	})
}

func (s bloemRelayStore) rename(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		if isRelayCredentialKey(key) {
			key = bloemRelayKey(key)
		}
		out[key] = value
	}
	return out
}

var errRelayOriginRefused = errors.New("push relay origin not allowed for this sender")

// relayOriginGuard lets a sender's requests reach only its own relay. Every
// request, each redirect hop included, passes through it.
type relayOriginGuard struct {
	next    http.RoundTripper
	allowed map[string]bool
}

func (g relayOriginGuard) RoundTrip(req *http.Request) (*http.Response, error) {
	origin := req.URL.Scheme + "://" + req.URL.Host
	if req.URL.Scheme != schemeHTTPS || !g.allowed[origin] {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("%w: %s", errRelayOriginRefused, origin)
	}
	return g.next.RoundTrip(req)
}

// newBloemRelaySender is the sender for Bloem's apps: Silo's relay client
// code, aimed at Bloem's relay with Bloem's credential.
func newBloemRelaySender(silo *pushSender) *pushSender {
	if silo == nil || silo.settings == nil || silo.settings.reader == nil {
		return nil
	}
	return newBloemRelaySenderFor(silo.cipher, silo.settings.reader, os.Getenv(bloemPushRelayDevelopmentEnv), nil)
}

// newBloemRelaySenderFor builds it over reader, the raw settings store. base
// is the transport under the origin guard; nil keeps the notification
// transport (public addresses only).
func newBloemRelaySenderFor(cipher *secret.Cipher, reader SettingReader, developmentURL string, base http.RoundTripper) *pushSender {
	relayURL := BloemPushRelayURL
	if dev := strings.TrimRight(strings.TrimSpace(developmentURL), "/"); dev != "" {
		relayURL = dev
	}
	sender := newPushSender(nil, nil, cipher, NewSettings(bloemRelayStore{inner: reader, defaultURL: relayURL}))
	// NormalizePushRelayURL accepts Silo's origin plus this one; the origin
	// guard below refuses Silo's.
	sender.developmentRelayURL = relayURL
	sender.logger = slog.Default().With("component", "notifications.bloem_relay")
	if base == nil {
		base = sender.client.Transport
	}
	sender.client.Transport = relayOriginGuard{next: base, allowed: bloemRelayOrigins(developmentURL)}
	return sender
}

// capabilityIssuer reads the iss claim of a relay capability without
// verifying it. It only tells which relay issued a stored credential.
func capabilityIssuer(capability string) string {
	parts := strings.Split(strings.TrimSpace(capability), ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return strings.TrimRight(claims.Issuer, "/")
}

// bloemIssuers are the issuers of Bloem's production and staging relays.
var bloemIssuers = map[string]bool{BloemPushRelayURL: true, "https://push.dev.bloem-studios.com": true}

// heldByBloemRelay reports whether Silo's relay settings hold a credential of
// Bloem's relay, as every Bloem server before the split kept it there.
func heldByBloemRelay(values map[string]string, origins map[string]bool) bool {
	if url := strings.TrimRight(strings.TrimSpace(values[SettingPushRelayURL]), "/"); url != "" {
		return origins[url]
	}
	key := strings.TrimSpace(values[SettingPushRelayAPIKey])
	return key != "" && !IsLegacyPushRelayKey(key) && bloemIssuers[capabilityIssuer(key)]
}

// separateRelayCredentials moves Bloem's relay credential off Silo's relay
// settings, onto Bloem's. Silo's settings are left as on a server that never
// registered, so the first push to a Silo app registers with Silo's relay;
// an administrator's clear stays a clear. When Bloem's settings already hold
// a credential, Silo's copy is dropped (relay registration is stateless).
// It reports whether it changed anything, and is a no-op once done.
func (s *AdminPushService) separateRelayCredentials(ctx context.Context) (bool, error) {
	if s == nil || s.settings == nil || s.settings.reader == nil || s.sender == nil {
		return false, nil
	}
	reader := s.settings.reader
	origins := bloemRelayOrigins(s.sender.developmentRelayURL)
	// A cheap look first: this runs on a timer and is nearly always a no-op.
	url, err := reader.Get(ctx, SettingPushRelayURL)
	if err != nil {
		return false, err
	}
	if url = strings.TrimRight(strings.TrimSpace(url), "/"); url != "" && !origins[url] {
		return false, nil
	}
	if url == "" {
		key, err := reader.Get(ctx, SettingPushRelayAPIKey)
		if err != nil || strings.TrimSpace(key) == "" {
			return false, err
		}
	}
	updater, ok := reader.(settingsAtomicUpdater)
	if !ok {
		return false, errRelaySettingsNotAtomic
	}
	moved := false
	err = updater.UpdateAtomic(ctx, func(current map[string]string) (map[string]string, error) {
		if !heldByBloemRelay(current, origins) {
			return nil, nil
		}
		siloCredential := credentialFromValues(current)
		writes := map[string]string{}
		bloemHeld := strings.TrimSpace(current[BloemSettingPushRelayAPIKey]) != "" ||
			parseRelayBool(current[BloemSettingPushRelayReregister])
		if siloCredential.APIKey != "" && !bloemHeld {
			for _, key := range relayCredentialKeys {
				writes[bloemRelayKey(key)] = current[key]
			}
			if strings.TrimSpace(writes[BloemSettingPushRelayURL]) == "" {
				writes[BloemSettingPushRelayURL] = BloemPushRelayURL
			}
		}
		for key, value := range relayCredentialValues(PushRelayCredential{ReregistrationRequired: siloCredential.Parked()}) {
			writes[key] = value
		}
		moved = true
		return writes, nil
	})
	if err != nil || !moved {
		return false, err
	}
	s.settings.Invalidate(relayCredentialKeys...)
	s.sender.settings.Invalidate(relayCredentialKeys...)
	if s.logger != nil {
		s.logger.InfoContext(ctx, "moved Bloem's push relay credential to Bloem's settings; Silo apps' push now registers with Silo's relay")
	}
	return true, nil
}

// adminRelaySeparateEvery is how often a worker checks Silo's relay settings
// for Bloem's credential. During a blue/green overlap the older build can
// register Bloem's relay under Silo's names again; this moves it back off.
const adminRelaySeparateEvery = 5 * time.Minute

// separateRelays runs separateRelayCredentials and logs a failure.
func (s *AdminPushService) separateRelays(ctx context.Context) {
	if _, err := s.separateRelayCredentials(ctx); err != nil && ctx.Err() == nil && s.logger != nil {
		s.logger.WarnContext(ctx, "moving Bloem's push relay credential off Silo's settings failed", "error", err)
	}
}

func parseRelayBool(value string) bool {
	parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
	return parsed
}
