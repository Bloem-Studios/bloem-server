package plugins

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
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
