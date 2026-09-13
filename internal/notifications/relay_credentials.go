package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	relayRegisterPath = "/v1/deployments/register"
	relayRotatePath   = "/v1/deployments/rotate"
	relayRenewPath    = "/v1/deployments/renew"
	relayRenewBefore  = 7 * 24 * time.Hour
	relayRenewJitter  = 24 * time.Hour
)

type RelayHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type RelayCredentialResult struct {
	Credential PushRelayCredential
	RequestID  string
	APNsTopics []string
}

type relayCredentialResponse struct {
	RequestID    string   `json:"request_id"`
	DeploymentID string   `json:"deployment_id"`
	APIKey       string   `json:"api_key"`
	KeyPrefix    string   `json:"key_prefix"`
	APNsTopics   []string `json:"apns_topics"`
	ExpiresAt    string   `json:"expires_at"`
}

type relayNestedError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

type RelayCredentialError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e RelayCredentialError) Error() string { return e.Code }

// NormalizePushRelayURL accepts the official production origin or one exact
// operator-configured development/staging origin. Capabilities and device
// tokens must never be sent to an arbitrary URL supplied through the admin UI.
func NormalizePushRelayURL(raw, developmentOrigin string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		value = DefaultPushRelayURL
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", errors.New("relay_url must be an HTTPS origin")
	}
	allowed := map[string]bool{DefaultPushRelayURL: true}
	if override := strings.TrimRight(strings.TrimSpace(developmentOrigin), "/"); override != "" {
		allowed[override] = true
	}
	if !allowed[value] {
		return "", errors.New("relay_url is not an allowed Silo relay origin")
	}
	return value, nil
}

func IsLegacyPushRelayKey(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "rk_")
}

func LoadPushRelayCredential(ctx context.Context, settings *Settings) PushRelayCredential {
	return PushRelayCredential{
		RelayURL:               settings.PushRelayURL(ctx),
		DeploymentID:           settings.PushRelayDeploymentID(ctx),
		APIKey:                 settings.PushRelayAPIKey(ctx),
		ExpiresAt:              settings.PushRelayExpiresAt(ctx),
		KeyPrefix:              settings.PushRelayKeyPrefix(ctx),
		ReregistrationRequired: settings.PushRelayReregistrationRequired(ctx),
	}
}

func RelayCredentialNeedsRenewal(now, expiresAt time.Time, deploymentID string) bool {
	if expiresAt.IsZero() {
		return false
	}
	digest := sha256.Sum256([]byte(deploymentID))
	jitterSeconds := int64(digest[0])<<8 | int64(digest[1])
	jitter := time.Duration(jitterSeconds) * relayRenewJitter / 65535
	return !now.Before(expiresAt.Add(-(relayRenewBefore + jitter)))
}

func RelayRotationIdempotencyKey(deploymentID, capability string) string {
	digest := sha256.Sum256([]byte("silo-relay-rotation\x00" + deploymentID + "\x00" + capability))
	return "silo-rotate-" + hex.EncodeToString(digest[:])
}

func RequestRelayCredential(ctx context.Context, client RelayHTTPDoer, relayURL, path, capability, idempotencyKey string) (RelayCredentialResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		return RelayCredentialResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Silo-Server/PushRelayCredential")
	if capability != "" {
		req.Header.Set("Authorization", "Bearer "+capability)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return RelayCredentialResult{}, RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_unreachable", Message: "Push relay could not be reached"}
	}
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if readErr != nil {
		return RelayCredentialResult{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var parsed relayNestedError
		_ = json.Unmarshal(data, &parsed)
		code := strings.TrimSpace(parsed.Error.Code)
		if code == "" {
			code = fmt.Sprintf("relay_http_%d", resp.StatusCode)
		}
		message := strings.TrimSpace(parsed.Error.Message)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return RelayCredentialResult{}, RelayCredentialError{
			Status:     resp.StatusCode,
			Code:       code,
			Message:    message,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	var parsed relayCredentialResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return RelayCredentialResult{}, RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_bad_response", Message: "Push relay returned invalid JSON"}
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(parsed.ExpiresAt))
	if err != nil || strings.TrimSpace(parsed.DeploymentID) == "" || strings.TrimSpace(parsed.APIKey) == "" || strings.TrimSpace(parsed.KeyPrefix) == "" {
		return RelayCredentialResult{}, RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_bad_response", Message: "Push relay returned an incomplete credential response"}
	}
	return RelayCredentialResult{
		Credential: PushRelayCredential{
			RelayURL:     relayURL,
			DeploymentID: strings.TrimSpace(parsed.DeploymentID),
			APIKey:       strings.TrimSpace(parsed.APIKey),
			ExpiresAt:    expiresAt,
			KeyPrefix:    strings.TrimSpace(parsed.KeyPrefix),
		},
		RequestID:  parsed.RequestID,
		APNsTopics: parsed.APNsTopics,
	}, nil
}

// relayRegistrationAdvisoryLock serializes relay registration across API
// replicas and the admin endpoint. Process-local mutexes only cover one
// process; without a database-wide winner every writer that sees an empty
// credential would register its own relay deployment and the last writer
// would win.
const relayRegistrationAdvisoryLock int64 = 0x53494C4F52454C59 // "SILORELY"

// ErrRelayRegistrationBusy reports that another node holds the registration
// lock. Callers retry later and pick up the winner's stored credential.
var ErrRelayRegistrationBusy = errors.New("push relay registration in progress on another node")

// WithRelayRegistrationLock runs fn while holding the cross-replica
// registration lock. It drops the settings read cache first and hands fn the
// credential as stored, so fn can decide whether registration is still
// needed. A nil pool runs fn unlocked (single-node tests and tools).
func WithRelayRegistrationLock(ctx context.Context, pool *pgxpool.Pool, settings *Settings, fn func(current PushRelayCredential) error) error {
	lock, acquired, err := pglock.TryAcquire(ctx, pool, relayRegistrationAdvisoryLock)
	if err != nil {
		return err
	}
	if pool != nil && !acquired {
		return ErrRelayRegistrationBusy
	}
	defer func() { _ = lock.Release(ctx) }()

	settings.Invalidate(SettingPushRelayAPIKey, SettingPushRelayDeploymentID, SettingPushRelayExpiresAt,
		SettingPushRelayKeyPrefix, SettingPushRelayReregister, SettingPushRelayURL)
	return fn(LoadPushRelayCredential(ctx, settings))
}

func RegisterRelayCredential(ctx context.Context, settings *Settings, client RelayHTTPDoer, relayURL string) (RelayCredentialResult, error) {
	result, err := RequestRelayCredential(ctx, client, relayURL, relayRegisterPath, "", "")
	if err != nil {
		return RelayCredentialResult{}, err
	}
	if err := settings.UpdatePushRelayCredential(ctx, result.Credential); err != nil {
		return RelayCredentialResult{}, err
	}
	return result, nil
}

func RotateRelayCredential(ctx context.Context, settings *Settings, client RelayHTTPDoer, current PushRelayCredential) (RelayCredentialResult, error) {
	key := RelayRotationIdempotencyKey(current.DeploymentID, current.APIKey)
	result, err := RequestRelayCredential(ctx, client, current.RelayURL, relayRotatePath, current.APIKey, key)
	if err != nil {
		return RelayCredentialResult{}, err
	}
	if result.Credential.DeploymentID != current.DeploymentID {
		return RelayCredentialResult{}, RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_bad_response", Message: "Push relay changed the deployment during rotation"}
	}
	if err := settings.UpdatePushRelayCredential(ctx, result.Credential); err != nil {
		return RelayCredentialResult{}, err
	}
	return result, nil
}

func RenewRelayCredential(ctx context.Context, settings *Settings, client RelayHTTPDoer, current PushRelayCredential) (RelayCredentialResult, error) {
	result, err := RequestRelayCredential(ctx, client, current.RelayURL, relayRenewPath, current.APIKey, "")
	if err != nil {
		return RelayCredentialResult{}, err
	}
	if result.Credential.DeploymentID != current.DeploymentID {
		return RelayCredentialResult{}, RelayCredentialError{Status: http.StatusBadGateway, Code: "relay_bad_response", Message: "Push relay changed the deployment during renewal"}
	}
	if err := settings.UpdatePushRelayCredential(ctx, result.Credential); err != nil {
		return RelayCredentialResult{}, err
	}
	return result, nil
}

func MarkRelayReregistrationRequired(ctx context.Context, settings *Settings, current PushRelayCredential) error {
	current.ReregistrationRequired = true
	return settings.UpdatePushRelayCredential(ctx, current)
}
