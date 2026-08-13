package auth

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"golang.org/x/crypto/bcrypt"
)

type adminReauthAccountStoreStub struct {
	account *models.User
	err     error
}

func (s adminReauthAccountStoreStub) GetByID(context.Context, int) (*models.User, error) {
	return s.account, s.err
}

func TestAccountCredentialVerifierRequiresCorrectEnabledLocalPassword(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	account := &models.User{ID: 7, PasswordHash: string(hash), Enabled: true, LocalPasswordLoginEnabled: true}
	verifier := NewAccountCredentialVerifier(adminReauthAccountStoreStub{account: account})
	for _, test := range []struct {
		name     string
		password string
		enabled  bool
		want     bool
	}{
		{name: "correct", password: "correct horse battery staple", enabled: true, want: true},
		{name: "wrong", password: "wrong", enabled: true},
		{name: "disabled account", password: "correct horse battery staple", enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			account.Enabled = test.enabled
			got, err := verifier.VerifyPassword(context.Background(), account.ID, test.password)
			if err != nil || got != test.want {
				t.Fatalf("VerifyPassword() = %t, %v, want %t", got, err, test.want)
			}
		})
	}
}
