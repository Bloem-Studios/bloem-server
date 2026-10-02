package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
)

type bloemSignupEnabledSettings struct{}

func (bloemSignupEnabledSettings) Get(context.Context, string) (string, error) { return "true", nil }
func TestBloemTransactionalSignupRefusesDisabledLocalPasswordsBeforeInviteRedemption(t *testing.T) {
	pool := NewBloemAuthTestDatabase(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO server_settings(key,value) VALUES($1,'false') ON CONFLICT(key) DO UPDATE SET value='false'`, config.AuthLocalPasswordLoginSettingKey); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	// No invite-code store: reaching redemption would return ErrInviteCodeNotFound.
	svc := &Service{settings: bloemSignupEnabledSettings{}}
	pair, created, err := svc.SignupInTransaction(t.Context(), tx, "blocked", "blocked@example.invalid", "fixture-password", "unused", false, "", "test", "")
	if !errors.Is(err, ErrLocalLoginDisabled) || pair != nil || created.User != nil {
		t.Fatalf("blocked signup: pair=%v account=%v error=%v", pair != nil, created.User != nil, err)
	}
}
