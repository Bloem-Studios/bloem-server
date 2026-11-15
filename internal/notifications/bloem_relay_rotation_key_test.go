package notifications

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

type failingRelaySettings struct{}

func (failingRelaySettings) Get(context.Context, string) (string, error) {
	return "", errors.New("settings store unavailable")
}

func rotationTestCredential() PushRelayCredential {
	return PushRelayCredential{
		RelayURL:     DefaultPushRelayURL,
		DeploymentID: "deployment-rotate",
		APIKey:       "old.capability.value",
		ExpiresAt:    time.Now().Add(30 * 24 * time.Hour),
		KeyPrefix:    "cap_v2_old",
	}
}

func rotationTestSettings(secret string) *Settings {
	return NewSettings(&lockedRelaySettings{values: map[string]string{relayRotationSecretSetting: secret}})
}

func TestRelayRotationKeyCannotBeComputedFromTheCapabilityAlone(t *testing.T) {
	current := rotationTestCredential()
	key, err := bloemRelayRotationKey(context.Background(), rotationTestSettings("server-secret-one"), current)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	// What a holder of the superseded capability could compute: the key the
	// server used before the secret was involved. The relay would have handed
	// the new capability back to anyone presenting it.
	if legacy := RelayRotationIdempotencyKey(current.DeploymentID, current.APIKey); key == legacy {
		t.Fatalf("rotation key equals the key derivable from the capability: %q", key)
	}
	other, err := bloemRelayRotationKey(context.Background(), rotationTestSettings("server-secret-two"), current)
	if err != nil {
		t.Fatalf("key under another secret: %v", err)
	}
	if other == key {
		t.Fatal("rotation key does not depend on the server secret")
	}
}

func TestRelayRotationKeyIsStableForARetryAndDistinctPerGeneration(t *testing.T) {
	settings := rotationTestSettings("server-secret-one")
	current := rotationTestCredential()
	first, err := bloemRelayRotationKey(context.Background(), settings, current)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	// A retry after a lost response must send the same key, or the relay would
	// treat it as a second rotation.
	again, err := bloemRelayRotationKey(context.Background(), settings, current)
	if err != nil || again != first {
		t.Fatalf("retry key = %q, %v; want %q", again, err, first)
	}
	// The relay remembers a key against the generation it rotated from, so the
	// next generation's rotation must not reuse it.
	next := current
	next.APIKey = "new.capability.value"
	nextKey, err := bloemRelayRotationKey(context.Background(), settings, next)
	if err != nil || nextKey == first {
		t.Fatalf("next generation key = %q, %v; want a different key", nextKey, err)
	}
	otherDeployment := current
	otherDeployment.DeploymentID = "another-deployment"
	otherKey, err := bloemRelayRotationKey(context.Background(), settings, otherDeployment)
	if err != nil || otherKey == first {
		t.Fatalf("other deployment key = %q, %v; want a different key", otherKey, err)
	}
}

func TestRelayRotationKeyFitsTheRelayHeaderLimit(t *testing.T) {
	key, err := bloemRelayRotationKey(context.Background(), rotationTestSettings("server-secret-one"), rotationTestCredential())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	// The relay refuses an Idempotency-Key over 255 characters.
	if !regexp.MustCompile(`^bloem-rotate-[0-9a-f]{64}$`).MatchString(key) || len(key) > 255 {
		t.Fatalf("rotation key = %q", key)
	}
}

func TestRotateRelayCredentialSendsTheSecretDerivedKeyAndReplaysIt(t *testing.T) {
	current := rotationTestCredential()
	settings := rotationTestSettings("server-secret-one")
	want, err := bloemRelayRotationKey(context.Background(), settings, current)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	var keys []string
	calls := 0
	doer := relayDoerFunc(func(req *http.Request) (*http.Response, error) {
		keys = append(keys, req.Header.Get("Idempotency-Key"))
		calls++
		if calls == 1 {
			return nil, errors.New("connection reset")
		}
		return relayResponse(http.StatusOK, credentialJSON(current.DeploymentID, "new.capability.value", time.Now().Add(30*24*time.Hour))), nil
	})
	if _, err := RotateRelayCredential(context.Background(), settings, doer, current); err == nil {
		t.Fatal("first rotation unexpectedly succeeded")
	}
	if _, err := RotateRelayCredential(context.Background(), settings, doer, current); err != nil {
		t.Fatalf("rotation replay: %v", err)
	}
	if len(keys) != 2 || keys[0] != want || keys[1] != want {
		t.Fatalf("Idempotency-Key = %#v, want %q twice", keys, want)
	}
	if legacy := RelayRotationIdempotencyKey(current.DeploymentID, current.APIKey); keys[0] == legacy {
		t.Fatal("rotation used the key derivable from the capability")
	}
}

func TestRotateRelayCredentialFailsClosedWithoutAServerSecret(t *testing.T) {
	cases := map[string]*Settings{
		"missing":     NewSettings(&lockedRelaySettings{values: map[string]string{}}),
		"blank":       rotationTestSettings(" \n\t "),
		"unreadable":  NewSettings(failingRelaySettings{}),
		"no settings": NewSettings(nil),
	}
	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			requests := 0
			doer := relayDoerFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return relayResponse(http.StatusOK, credentialJSON("deployment-rotate", "new.capability.value", time.Now().Add(time.Hour))), nil
			})
			_, err := RotateRelayCredential(context.Background(), settings, doer, rotationTestCredential())
			var relayErr RelayCredentialError
			if !errors.As(err, &relayErr) || relayErr.Code != "relay_rotation_secret_unavailable" || relayErr.Status != http.StatusServiceUnavailable {
				t.Fatalf("err = %#v, want relay_rotation_secret_unavailable (503)", err)
			}
			if requests != 0 {
				t.Fatalf("made %d relay requests; rotating with a guessable key defeats the point", requests)
			}
			if strings.Contains(relayErr.Message, "old.capability.value") {
				t.Fatal("error message leaks the capability")
			}
		})
	}
}
