package auth

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

type ProfileCredentialStatus struct {
	ProfileID          string `json:"profile_id"`
	LoginEmail         string `json:"login_email"`
	Configured         bool   `json:"configured"`
	CredentialRevision int64  `json:"credential_revision"`
}

// Status never reads the password hash into application memory or returns it.
func (s *ProfileCredentialService) Status(ctx context.Context, accountID int, profileID string) (ProfileCredentialStatus, error) {
	var result ProfileCredentialStatus
	if s == nil || s.pool == nil {
		return result, ErrProfileCredentialNotFound
	}
	err := s.pool.QueryRow(ctx, `SELECT id,COALESCE(login_email,''),login_email IS NOT NULL AND password_hash IS NOT NULL,credential_revision
 FROM user_profiles WHERE user_id=$1 AND id=$2`, accountID, profileID).Scan(&result.ProfileID, &result.LoginEmail, &result.Configured, &result.CredentialRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrProfileCredentialNotFound
	}
	return result, err
}
