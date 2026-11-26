// Package bloemtestdevice gives Bloem's tests a device sign-in that an
// account has approved and no device has collected yet, to show that a
// revocation withdraws it: without that, the device could still collect a
// fresh session after the account was signed out everywhere.
package bloemtestdevice

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// SignIn is one approved device sign-in.
type SignIn struct {
	pool    *pgxpool.Pool
	devices *auth.DeviceLoginService
	name    string
	code    string
}

var started atomic.Int64

// Approve starts a device sign-in and approves it as userID, as a trusted
// caller does (auth.DeviceLoginService.Approve without claims).
func Approve(t testing.TB, pool *pgxpool.Pool, userID int) SignIn {
	t.Helper()
	return approve(t, pool, userID, "")
}

// ApproveForProfile is Approve for a remote-playback sign-in bound to one of
// the account's profiles, which collects a session as that profile.
func ApproveForProfile(t testing.TB, pool *pgxpool.Pool, userID int, profileID string) SignIn {
	t.Helper()
	return approve(t, pool, userID, profileID)
}

func approve(t testing.TB, pool *pgxpool.Pool, userID int, profileID string) SignIn {
	t.Helper()
	ctx := context.Background()
	devices := auth.NewDeviceLoginService(pool, auth.NewUserRepository(pool),
		auth.NewJWTService("bloem-test-device-sign-in-secret", time.Hour, 24*time.Hour),
		auth.NewSessionRepository(pool), pgstore.NewPostgresProvider(pool),
		access.NewProfileTokenService("bloem-test-device-profile-secret", time.Hour))
	name := fmt.Sprintf("bloem-test-device-%d-%d", time.Now().UnixNano(), started.Add(1))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_login_requests WHERE device_name = $1`, name)
	})
	input := auth.DeviceLoginStartInput{DeviceName: name, ClientPurpose: auth.DeviceLoginPurposeLogin}
	if profileID != "" {
		input.ClientPurpose, input.Temporary = auth.DeviceLoginPurposeRemote, true
	}
	start, err := devices.Start(ctx, input)
	if err != nil {
		t.Fatalf("starting a device sign-in: %v", err)
	}
	lookup := auth.DeviceLoginLookupInput{UserCode: start.UserCode}
	if profileID == "" {
		err = devices.Approve(ctx, lookup, userID)
	} else {
		err = devices.ApproveForProfile(ctx, lookup, userID, profileID)
	}
	if err != nil {
		t.Fatalf("approving a device sign-in as account %d: %v", userID, err)
	}
	return SignIn{pool: pool, devices: devices, name: name, code: start.DeviceCode}
}

// Collect polls the sign-in as the device does.
func (s SignIn) Collect(ctx context.Context) (*auth.DeviceLoginPollResult, error) {
	return s.devices.Poll(ctx, s.code, "")
}

// RequireDenied fails t unless the approval itself was withdrawn: the request
// is denied, as Silo's revocation leaves it, so it cannot be collected even
// once whatever else refuses the account no longer does.
func (s SignIn) RequireDenied(t testing.TB) {
	t.Helper()
	var status string
	if err := s.pool.QueryRow(context.Background(), `SELECT status FROM device_login_requests WHERE device_name = $1`, s.name).Scan(&status); err != nil {
		t.Fatalf("reading the device sign-in: %v", err)
	}
	if status != auth.DeviceLoginStatusDenied {
		t.Fatalf("the device sign-in is %s after the revocation, want %s", status, auth.DeviceLoginStatusDenied)
	}
}

// RequireWithdrawn fails t when the device can still collect a session.
func (s SignIn) RequireWithdrawn(t testing.TB) {
	t.Helper()
	poll, err := s.Collect(context.Background())
	if err == nil && (poll.TokenPair != nil || poll.Status == auth.DeviceLoginStatusApproved) {
		t.Fatalf("a device approved before the revocation collected a session after it: status %s", poll.Status)
	}
}
