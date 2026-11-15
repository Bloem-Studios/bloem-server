package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// relayRotationSecretSetting is the server-held secret the rotation
// Idempotency-Key is derived from: the signing secret every process of a
// deployment shares (internal/auth.JWTSecretSettingKey), encrypted at rest.
// It is spelled out here, as other packages do, so notifications does not
// import auth.
const relayRotationSecretSetting = "auth.jwt_secret" //nolint:gosec // G101: a setting name, not a credential

// relayRotationKeyLabel domain-separates this use of the secret from every
// other. Change it only together with the version in the key prefix.
const relayRotationKeyLabel = "bloem/relay-rotation/v1"

// bloemRelayRotationKey is the Idempotency-Key for rotating current's
// capability: an HMAC of the deployment and the capability under a secret only
// this server holds.
//
// The relay keeps a rotation record under this key and, for a repeat of the
// same key, hands back the capability the first rotation produced. Silo derived
// the key from the deployment ID and the old capability alone, so anyone who
// held a superseded capability could compute it and ask the relay for the
// current one, which undoes rotating after a leak. Keying the derivation
// closes that: the old capability is no longer enough.
//
// The key is stable for a given capability, so a retry after a lost response
// repeats it and the relay answers with the same new capability instead of
// rotating twice. It changes with the capability, so the next generation's
// rotation gets its own.
//
// It fails closed. Without the secret the only available key would be one a
// leaked capability can reproduce, so the rotation is not attempted.
func bloemRelayRotationKey(ctx context.Context, settings *Settings, current PushRelayCredential) (string, error) {
	secret := ""
	if settings != nil && settings.reader != nil {
		// Read the store directly: the facade's cache is for hot paths, and a
		// secret that was just provisioned must be visible.
		stored, err := settings.reader.Get(ctx, relayRotationSecretSetting)
		if err == nil {
			secret = strings.TrimSpace(stored)
		}
	}
	if secret == "" {
		return "", RelayCredentialError{
			Status:  http.StatusServiceUnavailable,
			Code:    "relay_rotation_secret_unavailable",
			Message: "The server's signing secret is unavailable, so the push relay credential was not rotated",
		}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	// Length-prefixed, so one field's end cannot be mistaken for another's start.
	for _, field := range []string{relayRotationKeyLabel, current.DeploymentID, current.APIKey} {
		mac.Write([]byte(strconv.Itoa(len(field))))
		mac.Write([]byte{':'})
		mac.Write([]byte(field))
	}
	return "bloem-rotate-" + hex.EncodeToString(mac.Sum(nil)), nil
}
