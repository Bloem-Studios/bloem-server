package plugins

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPickConnectionConfigPrefersConnectionKey(t *testing.T) {
	configs := []*RuntimeConfig{{Key: "other", Value: map[string]any{"a": 1}}, {Key: "connection", Value: map[string]any{"bucket": "b"}}}
	if got := pickConnectionConfig(configs); got["bucket"] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := pickConnectionConfig(configs[:1]); got["a"] != 1 {
		t.Fatalf("fallback to first config failed: %v", got)
	}
	if pickConnectionConfig(nil) != nil {
		t.Fatal("expected nil")
	}
}

func TestPickConnectionConfigStrictRequiresExplicitConnectionKey(t *testing.T) {
	configs := []*RuntimeConfig{{Key: "other", Value: map[string]any{"a": 1}}, {Key: "connection", Value: map[string]any{"bucket": "b"}}}
	if got := pickConnectionConfigStrict(configs); got["bucket"] != "b" {
		t.Fatalf("got %v", got)
	}
	if got := pickConnectionConfigStrict(configs[:1]); got != nil {
		t.Fatalf("strict pick fell back to the first config: %v", got)
	}
	if pickConnectionConfigStrict(nil) != nil || pickConnectionConfigStrict([]*RuntimeConfig{nil}) != nil {
		t.Fatal("expected nil")
	}
}

func TestOverlayFromStrictPickRefusesFirstConfigFallback(t *testing.T) {
	// A source whose only runtime config is not `connection` must not be rotated.
	configs := []*RuntimeConfig{{Key: "other", Value: map[string]any{"bucket": "b"}}}
	if _, err := overlayS3Credentials(pickConnectionConfigStrict(configs), S3Credentials{AccessKey: "A", SecretKey: "B"}); err == nil {
		t.Fatal("rotation accepted a config that is not the connection")
	}
}

func TestRotationLookupErrorMapsOnlyNoRows(t *testing.T) {
	if err := rotationLookupError(pgx.ErrNoRows); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("no rows must be source unavailable, got %v", err)
	}
	if err := rotationLookupError(fmt.Errorf("lookup: %w", pgx.ErrNoRows)); !errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("wrapped no rows must be source unavailable, got %v", err)
	}
	boom := errors.New("connection reset")
	err := rotationLookupError(boom)
	if !errors.Is(err, boom) || errors.Is(err, storagesource.ErrSourceUnavailable) {
		t.Fatalf("other DB errors must pass through unchanged, got %v", err)
	}
}

func TestOverlayS3CredentialsChangesOnlyCredentialFields(t *testing.T) {
	current := map[string]any{"source_id": "s3_1", "bucket": "media", "prefix": "movies/", "region": "us-east-1", "endpoint": "https://s3.example.com",
		"revision_mode": "checked-etag", "access_key": "OLD", "secret_key": "OLD2", "session_token": "OLD3"}
	next, err := overlayS3Credentials(current, S3Credentials{AccessKey: "NEW", SecretKey: "NEW2"})
	if err != nil {
		t.Fatal(err)
	}
	conn := next["connection"]
	for _, k := range []string{"source_id", "bucket", "prefix", "region", "endpoint", "revision_mode"} {
		if conn[k] != current[k] {
			t.Fatalf("%s changed: %v", k, conn[k])
		}
	}
	if conn["access_key"] != "NEW" || conn["secret_key"] != "NEW2" {
		t.Fatalf("credentials not replaced: %v", conn)
	}
	if _, kept := conn["session_token"]; kept {
		t.Fatal("empty session token must clear the old one")
	}
	if current["access_key"] != "OLD" {
		t.Fatal("input map was mutated")
	}
}

func TestOverlayS3CredentialsRejectsMissingKeysAndMissingConnection(t *testing.T) {
	if _, err := overlayS3Credentials(map[string]any{"bucket": "b"}, S3Credentials{AccessKey: "A"}); err == nil {
		t.Fatal("secret key required")
	}
	if _, err := overlayS3Credentials(nil, S3Credentials{AccessKey: "A", SecretKey: "B"}); err == nil {
		t.Fatal("source without stored connection accepted")
	}
}

func TestRotateCredentialsRequiresRegistryAndAuthority(t *testing.T) {
	key, owner := uuid.New(), uuid.New()
	creds := S3Credentials{AccessKey: "A", SecretKey: "B"}
	var nilRegistry *NativeStorageRegistry
	for name, r := range map[string]*NativeStorageRegistry{"nil registry": nilRegistry, "no pool": {}} {
		_, err := r.RotateCredentials(context.Background(), key, owner, creds, 1, func(context.Context, pgx.Tx) error { return nil })
		var onboarding *catalog.NativeOnboardingError
		if !errors.As(err, &onboarding) || onboarding.Code != "native_storage_unavailable" {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

// An S3 edit replaces the whole configuration, but the request limits and the
// proxy that an edit form does not send keep their stored values. Credentials
// are never carried, and the proxy password only to the same proxy and user.
func TestCarryS3StoredSettings(t *testing.T) {
	stored := map[string]any{"source_id": "s3_1", "bucket": "media", "endpoint": "https://s3.example", "access_key": "OLD", "secret_key": "OLD2", "session_token": "OLD3",
		"auth_failure_budget": 3.0, "auth_failure_window_seconds": 60.0, "auth_pause_seconds": 600.0, "auth_pause_max_seconds": 3600.0,
		"max_concurrent_requests": 8.0, "max_listing_requests": 2.0, "max_read_mbps": 90.0,
		"http_proxy": "http://proxy.example:3128", "http_proxy_username": "u", "http_proxy_password": "p"}
	carried := []string{"auth_failure_budget", "auth_failure_window_seconds", "auth_pause_seconds", "auth_pause_max_seconds",
		"max_concurrent_requests", "max_listing_requests", "max_read_mbps", "http_proxy", "http_proxy_username", "http_proxy_password"}
	edit := func(connection map[string]any) map[string]any {
		submitted := map[string]map[string]any{"connection": connection}
		next := carryS3StoredSettings(stored, submitted)
		if len(submitted["connection"]) != len(connection) {
			t.Fatal("the submitted configuration was modified in place")
		}
		return next["connection"]
	}

	t.Run("omitted settings keep their stored values", func(t *testing.T) {
		conn := edit(map[string]any{"source_id": "s3_1", "bucket": "other", "access_key": "NEW", "secret_key": "NEW2"})
		for _, k := range carried {
			if conn[k] != stored[k] {
				t.Fatalf("%s = %v, want the stored value", k, conn[k])
			}
		}
		if conn["bucket"] != "other" || conn["access_key"] != "NEW" || conn["secret_key"] != "NEW2" {
			t.Fatalf("submitted fields not kept: %v", conn)
		}
		if _, ok := conn["session_token"]; ok {
			t.Fatal("a credential was carried from the stored configuration")
		}
		if _, ok := conn["endpoint"]; ok {
			t.Fatal("a field outside the carried set was carried")
		}
	})
	t.Run("submitted values win and null clears", func(t *testing.T) {
		conn := edit(map[string]any{"max_read_mbps": 50.0, "max_concurrent_requests": nil, "http_proxy_password": "p2"})
		if conn["max_read_mbps"] != 50.0 || conn["http_proxy_password"] != "p2" {
			t.Fatalf("submitted values lost: %v", conn)
		}
		if _, ok := conn["max_concurrent_requests"]; ok {
			t.Fatal("null did not clear the stored value")
		}
		if conn["max_listing_requests"] != 2.0 {
			t.Fatal("an omitted limit was not carried")
		}
	})
	t.Run("the proxy password follows only the same proxy and user", func(t *testing.T) {
		if conn := edit(map[string]any{"http_proxy": "http://elsewhere.example:3128"}); conn["http_proxy_password"] != nil {
			t.Fatal("password carried to a different proxy")
		}
		if conn := edit(map[string]any{"http_proxy_username": "v"}); conn["http_proxy_password"] != nil {
			t.Fatal("password carried to a different user")
		}
		if conn := edit(map[string]any{"http_proxy": "http://proxy.example:3128", "http_proxy_username": "u"}); conn["http_proxy_password"] != "p" {
			t.Fatal("password not carried when proxy and user are resubmitted unchanged")
		}
	})
	t.Run("clearing the proxy drops its omitted user and password", func(t *testing.T) {
		for _, proxy := range []any{nil, ""} {
			conn := edit(map[string]any{"http_proxy": proxy})
			if conn["http_proxy_username"] != nil || conn["http_proxy_password"] != nil {
				t.Fatalf("proxy %v: user or password carried without a proxy: %v", proxy, conn)
			}
		}
	})
	t.Run("a case variant counts as sent", func(t *testing.T) {
		conn := edit(map[string]any{"MAX_READ_MBPS": 5.0})
		if _, ok := conn["max_read_mbps"]; ok {
			t.Fatal("stored value carried beside a case variant")
		}
	})
	t.Run("nothing stored or no connection is unchanged", func(t *testing.T) {
		submitted := map[string]map[string]any{"connection": {"bucket": "b"}}
		if got := carryS3StoredSettings(nil, submitted); len(got["connection"]) != 1 {
			t.Fatalf("got %v", got)
		}
		other := map[string]map[string]any{"source": {"bucket": "b"}}
		if got := carryS3StoredSettings(stored, other); len(got) != 1 || len(got["source"]) != 1 {
			t.Fatalf("got %v", got)
		}
	})
}
