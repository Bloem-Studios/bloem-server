package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestProfileCredentialStatusOwnershipAndRedaction(t *testing.T) {
	ctx := context.Background()
	service := newProfileCredentialService(t)
	account, profile := newProfileCredentialFixture(t, service.pool, "credential-status")
	status, err := service.Status(ctx, account, profile)
	if err != nil || status.Configured || status.LoginEmail != "" {
		t.Fatalf("initial: %+v %v", status, err)
	}
	if err := service.Set(ctx, account, profile, "status-reader@example.test", "secret-profile-password"); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(ctx, account, profile)
	if err != nil || !status.Configured || status.LoginEmail != "status-reader@example.test" || status.CredentialRevision < 2 {
		t.Fatalf("configured: %+v %v", status, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "password") || strings.Contains(string(raw), "hash") {
		t.Fatalf("credential material in status: %s", raw)
	}
	if _, err := service.Status(ctx, account+10000, profile); !errors.Is(err, ErrProfileCredentialNotFound) {
		t.Fatalf("foreign owner status: %v", err)
	}
	if err := service.Clear(ctx, account, profile); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(ctx, account, profile)
	if err != nil || status.Configured || status.LoginEmail != "" {
		t.Fatalf("cleared: %+v %v", status, err)
	}
}
