package auth

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/models"
)

type reauthenticationAccountStore interface {
	GetByID(context.Context, int) (*models.User, error)
}

// AccountCredentialVerifier verifies a local account password without minting
// a session or retaining the credential.
type AccountCredentialVerifier struct {
	accounts reauthenticationAccountStore
}

func NewAccountCredentialVerifier(accounts reauthenticationAccountStore) *AccountCredentialVerifier {
	return &AccountCredentialVerifier{accounts: accounts}
}

func (v *AccountCredentialVerifier) VerifyPassword(ctx context.Context, accountID int, password string) (bool, error) {
	if v == nil || v.accounts == nil || accountID <= 0 || password == "" {
		return false, nil
	}
	account, err := v.accounts.GetByID(ctx, accountID)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return account != nil && account.Enabled && account.LocalPasswordLoginEnabled && CheckPassword(account, password), nil
}
