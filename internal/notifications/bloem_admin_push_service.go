package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"

	"github.com/Silo-Server/silo-server/internal/secret"
)

// Bloem admin push (docs/architecture/bloem-admin-push.md).
//
// Silo's push sends to profile devices with one Apple topic. An administrator's
// phone is an account-level endpoint with its own topic and FCM app, so this
// service keeps its own devices and deliveries and reuses only Silo's relay
// credential, HTTP client and retry timing. Nothing here changes how Silo
// pushes: a server with no admin device sends exactly what it sent before.

const (
	// AdminPushAppleTopic is the only Apple topic an admin device may register.
	AdminPushAppleTopic = "com.bloemstudios.admin"
	// AdminPushCapabilityToken is the feature token that tells an app this
	// server can push to it.
	AdminPushCapabilityToken = "admin_push_v1"
	// AdminWidgetWakesCapabilityToken tells an app this server accepts its widget report and
	// sends widget wakes. It is advertised with AdminPushCapabilityToken, never without it.
	AdminWidgetWakesCapabilityToken = "admin_widget_wakes_v1"

	adminPushBatch       = 50
	adminPushTick        = 5 * time.Second
	adminPushPurgeEvery  = time.Hour
	adminPushWireIDMax   = 128
	adminInstallationMax = 128
)

var (
	// ErrAdminPushUnavailable means push delivery is off or has no backing store.
	ErrAdminPushUnavailable = errors.New("admin push is unavailable")
	// ErrAdminPushInvalid wraps a registration or switch the server refuses.
	ErrAdminPushInvalid = errors.New("invalid admin push request")
	// ErrAdminPushUnauthorized means the display token is missing or unknown.
	ErrAdminPushUnauthorized = errors.New("display token is not valid")
	// ErrAdminPushForbidden means the token's account is no longer an enabled admin.
	ErrAdminPushForbidden = errors.New("account is no longer an administrator")
	// ErrAdminPushNotFound means no such device or delivery for the caller.
	ErrAdminPushNotFound = errors.New("not found")

	adminInstallationPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
)

// AdminPushRegistration is a device's registration after transport decoding.
type AdminPushRegistration struct {
	InstallationID  string
	Platform        string
	Token           string
	APNsEnvironment string
	APNsTopic       string
	// DisabledKinds replaces the device's switches when non-nil.
	DisabledKinds *[]string
	// WidgetsPlaced and WidgetChannels replace what the app reported about its widgets when non-nil.
	WidgetsPlaced  *bool
	WidgetChannels *[]string
	LoginSessionID string
}

// AdminPushKindState is one kind and whether this device receives it.
type AdminPushKindState struct {
	Kind          string `json:"kind"`
	Enabled       bool   `json:"enabled"`
	ServerEmitted bool   `json:"server_emitted"`
}

// AdminPushRegistered is what a registration returns.
type AdminPushRegistered struct {
	// DeviceID prefixes every delivery id this device is sent, so the app can
	// tell which saved server a push belongs to.
	DeviceID string
	// DisplayToken reads this device's notification text and retires it. Shown
	// once per registration.
	DisplayToken string
	Kinds        []AdminPushKindState
	// WidgetsPlaced and WidgetChannels echo what the server now holds for this device.
	WidgetsPlaced  bool
	WidgetChannels []string
}

// AdminPushContent is a notification's text and behavior, read by the phone
// after a push wakes it. The relay never sees any of it.
type AdminPushContent struct {
	DeliveryID  string
	Kind        string
	Channel     string
	Title       string
	Body        string
	LockScreen  string
	Page        string
	ItemID      string
	SubjectType string
	Actions     []string
	Badge       *int
	CreatedAt   time.Time
}

// AdminPushService registers admin devices, turns events into deliveries and
// sends them through the Bloem relay.
type AdminPushService struct {
	store    *adminPushStore
	sender   *pushSender
	settings *Settings
	cipher   *secret.Cipher
	logger   *slog.Logger
	now      func() time.Time
	wake     chan struct{}

	// ServerName names this server in copy such as "Backup failed on {server}".
	serverName func(context.Context) string
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// BloemAdminPush returns the admin push service, or nil when this build has no
// push backing (no database, relay sender or cipher). Safe to call repeatedly.
func (s *System) BloemAdminPush() *AdminPushService {
	if s == nil || s.pool == nil || s.pushSender == nil {
		return nil
	}
	s.adminPushOnce.Do(func() {
		s.adminPush = &AdminPushService{
			store:    newAdminPushStore(s.pool),
			sender:   s.pushSender,
			settings: s.Settings,
			cipher:   s.pushSender.cipher,
			logger:   slog.Default().With("component", "notifications.admin_push"),
			now:      time.Now,
			wake:     make(chan struct{}, 1),
		}
	})
	return s.adminPush
}

// SetServerName supplies the name used in copy; without it copy says "this server".
func (s *AdminPushService) SetServerName(name func(context.Context) string) {
	if s != nil {
		s.serverName = name
	}
}

// ServerName reports the configured name, or "".
func (s *AdminPushService) ServerName(ctx context.Context) string {
	if s == nil || s.serverName == nil {
		return ""
	}
	return s.serverName(ctx)
}

// Available reports whether registration can work.
func (s *AdminPushService) Available() bool {
	return s != nil && s.store != nil && s.cipher != nil && s.sender != nil
}

// Enabled reports whether the operator has push delivery on, for either platform.
func (s *AdminPushService) Enabled(ctx context.Context) bool {
	return s.Available() && s.settings != nil && s.settings.PushDeliveryEnabled(ctx)
}

func kindStates(disabled []string) []AdminPushKindState {
	off := map[string]bool{}
	for _, kind := range disabled {
		off[kind] = true
	}
	out := make([]AdminPushKindState, 0, len(adminPushKinds))
	for _, kind := range adminPushKinds {
		out = append(out, AdminPushKindState{Kind: kind.ID, Enabled: !off[kind.ID], ServerEmitted: kind.ServerEmitted})
	}
	return out
}

func validKindIDs(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := AdminPushKindByID(id); !ok {
			return nil, fmt.Errorf("%w: unknown kind %q", ErrAdminPushInvalid, id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// Register adds or refreshes the caller's device. The caller must already be
// an enabled administrator; the transport checks that before calling.
func (s *AdminPushService) Register(ctx context.Context, userID int, reg AdminPushRegistration) (*AdminPushRegistered, error) {
	if !s.Available() || userID <= 0 {
		return nil, ErrAdminPushUnavailable
	}
	// The token the request came in on may name a role the account has since
	// lost; admin-only is judged on the account as it is now.
	if admin, err := s.store.isAdminAccount(ctx, userID); err != nil {
		return nil, err
	} else if !admin {
		return nil, ErrAdminPushForbidden
	}
	in := adminPushUpsert{UserID: userID, LoginSessionID: reg.LoginSessionID}
	in.InstallationID = strings.TrimSpace(reg.InstallationID)
	if !adminInstallationPattern.MatchString(in.InstallationID) || len(in.InstallationID) > adminInstallationMax {
		return nil, fmt.Errorf("%w: installation_id must be 8-128 letters, digits or . _ : -", ErrAdminPushInvalid)
	}
	in.Platform = strings.TrimSpace(reg.Platform)
	switch in.Platform {
	case PushPlatformApple:
		if !s.settings.ApplePushDeliveryEnabled(ctx) {
			return nil, ErrAdminPushUnavailable
		}
		in.Token = strings.ToLower(strings.TrimSpace(reg.Token))
		if len(in.Token) < 64 || len(in.Token) > 256 || !apnsTokenHexPattern.MatchString(in.Token) {
			return nil, fmt.Errorf("%w: token must be a hex APNs token", ErrAdminPushInvalid)
		}
		in.TokenHash = apnsTokenHash(in.Token)
		in.APNsEnvironment = strings.ToLower(strings.TrimSpace(reg.APNsEnvironment))
		if in.APNsEnvironment != APNsEnvironmentProd && in.APNsEnvironment != APNsEnvironmentSandbox {
			return nil, fmt.Errorf("%w: apns_environment must be production or sandbox", ErrAdminPushInvalid)
		}
		in.APNsTopic = strings.TrimSpace(reg.APNsTopic)
		if in.APNsTopic != AdminPushAppleTopic {
			return nil, fmt.Errorf("%w: apns_topic must be %s", ErrAdminPushInvalid, AdminPushAppleTopic)
		}
	case PushPlatformAndroid:
		if !s.settings.AndroidPushDeliveryEnabled(ctx) {
			return nil, ErrAdminPushUnavailable
		}
		in.Token = strings.TrimSpace(reg.Token)
		if !fcmTokenPattern.MatchString(in.Token) {
			return nil, fmt.Errorf("%w: token is not a plausible FCM registration token", ErrAdminPushInvalid)
		}
		in.TokenHash = fcmTokenHash(in.Token)
	default:
		return nil, fmt.Errorf("%w: platform must be apple or android", ErrAdminPushInvalid)
	}
	if reg.DisabledKinds != nil {
		kinds, err := validKindIDs(*reg.DisabledKinds)
		if err != nil {
			return nil, err
		}
		in.DisabledKinds = &kinds
	}
	if reg.WidgetChannels != nil {
		channels, err := validAdminWidgetChannels(*reg.WidgetChannels)
		if err != nil {
			return nil, err
		}
		in.WidgetChannels = &channels
	}
	in.WidgetsPlaced = reg.WidgetsPlaced
	if in.WidgetsPlaced != nil && !*in.WidgetsPlaced {
		none := []string{}
		in.WidgetChannels = &none
	}
	device, token, err := s.store.upsertDevice(ctx, s.cipher, in)
	if err != nil {
		return nil, err
	}
	return &AdminPushRegistered{
		DeviceID: device.ServerDeviceID, DisplayToken: token, Kinds: kindStates(device.DisabledKinds),
		WidgetsPlaced: device.WidgetsPlaced, WidgetChannels: nonNil(device.WidgetChannels),
	}, nil
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// SetWidgets records which widget channels the app's placed widgets show, for one of the caller's
// devices. The app calls it whenever a widget is added or removed. Reporting no widgets stops wakes.
func (s *AdminPushService) SetWidgets(ctx context.Context, userID int, deviceID string, placed bool, channels []string) (bool, []string, error) {
	if !s.Available() {
		return false, nil, ErrAdminPushUnavailable
	}
	clean, err := validAdminWidgetChannels(channels)
	if err != nil {
		return false, nil, err
	}
	if placed && len(clean) == 0 {
		return false, nil, fmt.Errorf("%w: widgets_placed needs at least one channel", ErrAdminPushInvalid)
	}
	device, err := s.store.setWidgets(ctx, userID, deviceID, placed, clean)
	if err != nil {
		if isNoRows(err) {
			return false, nil, ErrAdminPushNotFound
		}
		return false, nil, err
	}
	return device.WidgetsPlaced, nonNil(device.WidgetChannels), nil
}

// SetKinds applies per-kind switches to one of the caller's devices.
func (s *AdminPushService) SetKinds(ctx context.Context, userID int, deviceID string, kinds map[string]bool) ([]AdminPushKindState, error) {
	if !s.Available() {
		return nil, ErrAdminPushUnavailable
	}
	for kind := range kinds {
		if _, ok := AdminPushKindByID(kind); !ok {
			return nil, fmt.Errorf("%w: unknown kind %q", ErrAdminPushInvalid, kind)
		}
	}
	device, err := s.store.setKinds(ctx, userID, deviceID, kinds)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrAdminPushNotFound
		}
		return nil, err
	}
	return kindStates(device.DisabledKinds), nil
}

// Unregister removes one of the caller's devices.
func (s *AdminPushService) Unregister(ctx context.Context, userID int, deviceID string) error {
	if !s.Available() {
		return ErrAdminPushUnavailable
	}
	found, err := s.store.deleteDevice(ctx, userID, deviceID)
	if err != nil {
		return err
	}
	if !found {
		return ErrAdminPushNotFound
	}
	return nil
}

// Retire removes the device a display token was issued to. It needs no login,
// so an app that already signed out can still stop its pushes.
func (s *AdminPushService) Retire(ctx context.Context, displayToken, deviceID string) error {
	if !s.Available() {
		return ErrAdminPushUnavailable
	}
	if !strings.HasPrefix(displayToken, adminDisplayTokenPrefix) {
		return ErrAdminPushUnauthorized
	}
	found, err := s.store.retireByDisplayToken(ctx, adminDisplayTokenHash(displayToken), deviceID)
	if err != nil {
		return err
	}
	if !found {
		return ErrAdminPushUnauthorized
	}
	return nil
}

// Content returns the text of one delivery to the device it was sent to. It
// refuses a token that is unknown, an account that is no longer an admin, and
// any delivery that is not this device's: another account's, another device's,
// or an expired one.
func (s *AdminPushService) Content(ctx context.Context, displayToken, deliveryID string) (*AdminPushContent, error) {
	if !s.Available() {
		return nil, ErrAdminPushUnavailable
	}
	if !strings.HasPrefix(displayToken, adminDisplayTokenPrefix) {
		return nil, ErrAdminPushUnauthorized
	}
	device, admin, err := s.store.deviceByDisplayToken(ctx, adminDisplayTokenHash(displayToken))
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, ErrAdminPushUnauthorized
	}
	if !admin {
		return nil, ErrAdminPushForbidden
	}
	// A push names its delivery as {device_id}.{delivery}; the app hands that
	// over as it received it. The prefix must be this token's own device.
	if prefix, rest, wire := strings.Cut(deliveryID, "."); wire {
		if prefix != device.ServerDeviceID {
			return nil, ErrAdminPushNotFound
		}
		deliveryID = rest
	}
	row, err := s.store.deliveryForDevice(ctx, device.ID, device.UserID, deliveryID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrAdminPushNotFound
	}
	kind, ok := AdminPushKindByID(row.Kind)
	if !ok {
		return nil, ErrAdminPushNotFound
	}
	content := &AdminPushContent{
		DeliveryID: row.ID, Kind: row.Kind, Channel: kind.Channel, Title: row.Title, Body: row.Body, LockScreen: row.LockText,
		Page: kind.Page, SubjectType: row.SubjectType, Actions: append([]string(nil), kind.Actions...), Badge: row.Badge, CreatedAt: row.CreatedAt,
	}
	if kind.Page != "" {
		content.ItemID = row.SubjectID
	}
	return content, nil
}

// Notify turns one event into a delivery for every admin it concerns and
// queues a send to each of their devices that wants this kind. The delivery id
// is derived from the event, so a repeat, or another replica seeing the same
// event, adds nothing.
func (s *AdminPushService) Notify(ctx context.Context, n AdminPushNotice) error {
	if !s.Enabled(ctx) {
		return nil
	}
	kind, ok := AdminPushKindByID(n.Kind)
	if !ok || !kind.ServerEmitted {
		return fmt.Errorf("%w: %q is not a server kind", ErrAdminPushInvalid, n.Kind)
	}
	accounts, err := s.store.adminAccountIDs(ctx, n.Audience, n.RequesterUserID)
	if err != nil {
		return fmt.Errorf("resolve admins: %w", err)
	}
	ttl := n.TTL
	if ttl <= 0 {
		ttl = adminPushDefaultTTL
	}
	queued := false
	for _, userID := range accounts {
		devices, err := s.store.recipientDevices(ctx, userID, n.Kind)
		if err != nil {
			return fmt.Errorf("list admin devices: %w", err)
		}
		if len(devices) == 0 {
			continue
		}
		now := s.now()
		_, inserted, err := s.store.insertDelivery(ctx, adminDeliveryRow{
			ID: adminDeliveryID(n, userID), UserID: userID, Kind: n.Kind, CollapseKey: adminCollapseKey(n),
			SubjectType: n.SubjectType, SubjectID: n.SubjectID, Title: n.Title, Body: n.Body, LockText: kind.LockText,
			Badge: n.Badge, ExpiresAt: now.Add(ttl),
		}, devices, now)
		if err != nil {
			return fmt.Errorf("store delivery: %w", err)
		}
		queued = queued || inserted
	}
	if queued {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

// Run sends queued deliveries and retries failed ones until ctx ends. Every
// API replica may run it: attempts are claimed with SKIP LOCKED.
func (s *AdminPushService) Run(ctx context.Context) {
	if !s.Available() {
		return
	}
	tick := time.NewTicker(adminPushTick)
	defer tick.Stop()
	purge := time.NewTicker(adminPushPurgeEvery)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			s.Drain(ctx)
			s.FlushWakes(ctx)
		case <-tick.C:
			s.Drain(ctx)
			s.FlushWakes(ctx)
		case <-purge.C:
			if _, err := s.store.purgeExpired(ctx); err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "purge expired admin push deliveries failed", "error", err)
			}
		}
	}
}

// Drain claims and sends the attempts that are due, returning how many ran.
func (s *AdminPushService) Drain(ctx context.Context) int {
	total := 0
	for ctx.Err() == nil {
		claimed, err := s.store.claimDue(ctx, adminPushBatch)
		if err != nil {
			if ctx.Err() == nil {
				s.logger.WarnContext(ctx, "claim admin push attempts failed", "error", err)
			}
			return total
		}
		if len(claimed) == 0 {
			return total
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5)
		for _, attempt := range claimed {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				s.process(ctx, attempt)
			}()
		}
		wg.Wait()
		total += len(claimed)
		if len(claimed) < adminPushBatch {
			return total
		}
	}
	return total
}

func (s *AdminPushService) fail(ctx context.Context, a adminPushAttempt, reason, message string) {
	if err := s.store.finalize(ctx, a.ID, PushOutcomeFailed, a.AttemptNumber+1, "", nil, reason, message, nil); err != nil && ctx.Err() == nil {
		s.logger.WarnContext(ctx, "finalize admin push attempt failed", "attempt_id", a.ID, "error", err)
	}
}

// process sends one attempt, rechecking everything that may have changed since
// it was queued: the device, the account's admin standing, the login, the
// device's switch for this kind and the operator's delivery setting.
func (s *AdminPushService) process(ctx context.Context, a adminPushAttempt) {
	device, live, err := s.store.deviceByID(ctx, a.DeviceID)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push device lookup failed", "attempt_id", a.ID, "error", err)
		}
		return
	}
	if device == nil {
		s.fail(ctx, a, "device_deleted", "push device deleted")
		return
	}
	delivery, err := s.store.delivery(ctx, a.DeliveryID)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "admin push delivery lookup failed", "attempt_id", a.ID, "error", err)
		}
		return
	}
	switch {
	case delivery == nil:
		s.fail(ctx, a, "delivery_missing", "delivery missing")
		return
	case !device.Enabled || !live:
		s.fail(ctx, a, "device_unavailable", "device or account no longer eligible")
		return
	case !device.kindEnabled(delivery.Kind):
		s.fail(ctx, a, "kind_disabled", "device switched this kind off")
		return
	case !s.now().Before(delivery.ExpiresAt):
		s.fail(ctx, a, "expired", "delivery expired")
		return
	}
	enabled := s.settings.ApplePushDeliveryEnabled(ctx)
	if device.Platform == PushPlatformAndroid {
		enabled = s.settings.AndroidPushDeliveryEnabled(ctx)
	}
	if !enabled {
		s.fail(ctx, a, "delivery_disabled", "push delivery disabled")
		return
	}
	token, err := s.cipher.Decrypt(device.TokenCiphertext, adminPushTokenAAD(device.ID))
	if err != nil {
		s.fail(ctx, a, "decrypt_failed", "push token decrypt failed")
		return
	}

	result := s.send(ctx, a, device, delivery, token)
	number := a.AttemptNumber + 1
	if result.OK {
		_ = s.store.finalize(ctx, a.ID, PushOutcomeDelivered, number, result.RelayRequestID, &result.HTTPStatus, result.UpstreamReason, "", nil)
		s.store.recordSuccess(ctx, device.ID)
		return
	}
	var status *int
	if result.HTTPStatus > 0 {
		status = &result.HTTPStatus
	}
	code := result.UpstreamReason
	if code == "" {
		code = result.Message
	}
	if code == "" {
		code = "push_delivery_failed"
	}
	s.store.recordFailure(ctx, device.ID, code, result.TerminalDevice)
	if delay, more := pushRetryDelayWithHint(number, result.RetryAfter); more && !result.TerminalDevice && retryableHTTPStatus(result.HTTPStatus) {
		next := s.now().Add(delay)
		_ = s.store.finalize(ctx, a.ID, PushOutcomeRetrying, number, result.RelayRequestID, status, result.UpstreamReason, result.Message, &next)
		return
	}
	_ = s.store.finalize(ctx, a.ID, PushOutcomeFailed, number, result.RelayRequestID, status, result.UpstreamReason, result.Message, nil)
}

func (s *AdminPushService) send(ctx context.Context, a adminPushAttempt, device *AdminPushDevice, delivery *adminDeliveryRow, token string) pushSendResult {
	credential, err := s.sender.prepareRelayCredential(ctx)
	if err != nil {
		return pushSendResult{HTTPStatus: http.StatusServiceUnavailable, Message: err.Error(), UpstreamReason: "relay_credential_unavailable"}
	}
	result := s.post(ctx, a, device, delivery, token, credential.RelayURL, credential.APIKey)
	if result.HTTPStatus == http.StatusUnauthorized {
		recovered, err := s.sender.recoverRelayCredential(ctx, credential, result.UpstreamReason)
		if err != nil {
			if result.UpstreamReason == relayCodeDeploymentDisabled {
				return result
			}
			return pushSendResult{HTTPStatus: http.StatusServiceUnavailable, Message: err.Error(), UpstreamReason: "relay_credential_unavailable"}
		}
		result = s.post(ctx, a, device, delivery, token, recovered.RelayURL, recovered.APIKey)
	}
	return result
}

// adminRelayApple and adminRelayFCM are the relay's send contracts. The relay
// refuses fields it does not know, so server_id and profile_id are not sent:
// the phone finds its server from the device id that prefixes delivery_id.
// relayModeBackgroundWake is the relay's silent push: on APNs a background push at priority 5 with
// no alert, on FCM a data-only message. It has no title, no sound and nothing visible.
const relayModeBackgroundWake = "background_wake"

type adminRelayApple struct {
	Token          string  `json:"token"`
	Environment    string  `json:"environment"`
	Topic          string  `json:"topic"`
	Mode           string  `json:"mode"`
	ServerDeviceID string  `json:"server_device_id"`
	DeliveryID     string  `json:"delivery_id"`
	CollapseID     *string `json:"collapse_id,omitempty"`
	Badge          *int    `json:"badge,omitempty"`
}

type adminRelayFCM struct {
	Token          string  `json:"token"`
	Mode           string  `json:"mode"`
	ServerDeviceID string  `json:"server_device_id"`
	DeliveryID     string  `json:"delivery_id"`
	CollapseID     *string `json:"collapse_id,omitempty"`
}

// adminWireDeliveryID is what the push carries: the device id, so the app can
// choose among its saved servers, and the delivery id the content read needs.
func adminWireDeliveryID(device *AdminPushDevice, deliveryID string) string {
	id := device.ServerDeviceID + "." + deliveryID
	if len(id) > adminPushWireIDMax {
		return id[:adminPushWireIDMax]
	}
	return id
}

func adminRelayPayload(device *AdminPushDevice, delivery *adminDeliveryRow, token string) (path string, body any) {
	collapse := delivery.CollapseKey
	wire := adminWireDeliveryID(device, delivery.ID)
	mode, badge := relayModePrivateAlert, delivery.Badge
	if delivery.Wake {
		// A wake is silent: no badge, and the relay builds no alert.
		mode, badge = relayModeBackgroundWake, nil
	}
	if device.Platform == PushPlatformAndroid {
		return relayFcmSendPath, adminRelayFCM{Token: token, Mode: mode, ServerDeviceID: device.ServerDeviceID, DeliveryID: wire, CollapseID: &collapse}
	}
	return relayAppleSendPath, adminRelayApple{
		Token: token, Environment: device.APNsEnvironment, Topic: device.APNsTopic, Mode: mode,
		ServerDeviceID: device.ServerDeviceID, DeliveryID: wire, CollapseID: &collapse, Badge: badge,
	}
}

func (s *AdminPushService) post(ctx context.Context, a adminPushAttempt, device *AdminPushDevice, delivery *adminDeliveryRow, token, relayURL, apiKey string) pushSendResult {
	path, payload := adminRelayPayload(device, delivery, token)
	body, err := json.Marshal(payload)
	if err != nil {
		return pushSendResult{Message: "relay payload build failed", UpstreamReason: "payload_build_failed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+path, bytes.NewReader(body))
	if err != nil || req.URL.Scheme != schemeHTTPS {
		return pushSendResult{Message: "invalid push relay URL", UpstreamReason: "invalid_relay_url"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Bloem-Admin-Push/1.0")
	// One attempt keeps one key across transport retries, so the relay can tell
	// a safe retry from a new delivery.
	req.Header.Set("Idempotency-Key", a.ID)

	resp, err := s.sender.client.Do(req)
	if err != nil {
		return pushSendResult{Message: classifyWebhookError(err), UpstreamReason: "relay_unreachable"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var parsed pushRelayAppleResponse
		_ = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&parsed)
		if parsed.RequestID == "" {
			parsed.RequestID = resp.Header.Get("X-Request-ID")
		}
		return pushSendResult{OK: true, HTTPStatus: resp.StatusCode, RelayRequestID: parsed.RequestID}
	}
	var parsed pushRelayErrorResponse
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	_ = json.Unmarshal(data, &parsed)
	code := parsed.Error.Code
	if code == "" {
		code = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	message := strings.TrimSpace(parsed.Error.Message)
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return pushSendResult{
		HTTPStatus: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		RelayRequestID: parsed.Error.RequestID, UpstreamReason: code, Message: message,
		TerminalDevice: terminalDeviceRejection(device.Platform, resp.StatusCode, code, message),
	}
}

// adminWakeCollapse is the relay collapse id of every wake to a device: a wake still
// waiting at the push service when a newer one arrives is replaced, so the newest wins.
var adminWakeCollapse = func() string {
	sum := sha256.Sum256([]byte("admin.widgets"))
	return hex.EncodeToString(sum[:16])
}()

// Wake notes that something a widget shows changed in channel. Devices with a widget
// showing that channel get one silent wake, after the rate limit: changes gather for
// 30 seconds, a device is woken at most once every 10 minutes, and not within 2 minutes
// after a visible push. requesterUserID limits the requests channel to the admins whose
// authority covers that requester, as the visible request notice is; it is ignored for
// the other channels, which describe the whole server. It never blocks on the relay.
func (s *AdminPushService) Wake(ctx context.Context, channel string, requesterUserID int) error {
	if !s.Enabled(ctx) {
		return nil
	}
	if _, err := validAdminWidgetChannels([]string{channel}); err != nil {
		return err
	}
	var accounts []int
	if channel == AdminWidgetRequests {
		ids, err := s.store.adminAccountIDs(ctx, AudienceRequestAdmins, requesterUserID)
		if err != nil {
			return fmt.Errorf("resolve admins: %w", err)
		}
		accounts = ids
		if accounts == nil {
			accounts = []int{}
		}
	}
	if _, err := s.store.markWakePending(ctx, channel, s.now(), accounts); err != nil {
		return fmt.Errorf("mark wake pending: %w", err)
	}
	return nil
}

// FlushWakes sends the wakes that are due and returns how many it sent or tried. Every API
// replica may call it: each wake is claimed once, in the database.
func (s *AdminPushService) FlushWakes(ctx context.Context) int {
	if !s.Enabled(ctx) {
		return 0
	}
	claims, err := s.store.claimDueWakes(ctx, s.now(), adminPushBatch)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.WarnContext(ctx, "claim widget wakes failed", "error", err)
		}
		return 0
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, claim := range claims {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.sendWake(ctx, claim)
		}()
	}
	wg.Wait()
	return len(claims)
}

// sendWake sends one device its wake. A wake is best effort: a transient relay failure puts
// the channels back to be tried after the minimum gap, and a token the push service says is
// gone switches the device off, as for a visible push.
func (s *AdminPushService) sendWake(ctx context.Context, claim wakeClaim) {
	device := claim.Device
	enabled := s.settings.ApplePushDeliveryEnabled(ctx)
	if device.Platform == PushPlatformAndroid {
		enabled = s.settings.AndroidPushDeliveryEnabled(ctx)
	}
	if !enabled {
		return
	}
	token, err := s.cipher.Decrypt(device.TokenCiphertext, adminPushTokenAAD(device.ID))
	if err != nil {
		return
	}
	now := s.now()
	row := &adminDeliveryRow{ID: adminWakeID(claim.Channels, now), CollapseKey: adminWakeCollapse, Wake: true}
	result := s.send(ctx, adminPushAttempt{ID: ulid.Make().String(), DeliveryID: row.ID, DeviceID: device.ID}, &device, row, token)
	switch {
	case result.OK:
		s.store.recordSuccess(ctx, device.ID)
	case result.TerminalDevice:
		s.store.recordFailure(ctx, device.ID, result.UpstreamReason, true)
	default:
		code := result.UpstreamReason
		if code == "" {
			code = result.Message
		}
		s.store.recordFailure(ctx, device.ID, code, false)
		if retryableHTTPStatus(result.HTTPStatus) || result.HTTPStatus == 0 {
			s.store.requeueWake(ctx, device.ID, claim.Channels, now)
		}
	}
}
