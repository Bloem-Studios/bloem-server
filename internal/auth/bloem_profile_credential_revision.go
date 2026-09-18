package auth

import (
	"context"
	"errors"
)

var (
	ErrProfileCredentialRevisionConflict = errors.New("profile credentials changed; reload and review before trying again")
	ErrInvalidProfileCredentialRevision  = errors.New("expected credential revision must be positive")
)

// SetAtRevision binds a Bloem management write to the credential the caller
// reviewed. The shared transaction compares the revision while holding the
// profile row lock, before updating credentials or triggering session revocation.
// Existing Set callers retain their unconditional behavior.
func (s *ProfileCredentialService) SetAtRevision(ctx context.Context, accountID int, profileID, email, password string, expectedRevision int64) error {
	if expectedRevision <= 0 {
		return ErrInvalidProfileCredentialRevision
	}
	return s.set(ctx, accountID, profileID, email, password, &expectedRevision)
}

// ClearAtRevision disables only the reviewed credential, using the same locked
// revision comparison as SetAtRevision.
func (s *ProfileCredentialService) ClearAtRevision(ctx context.Context, accountID int, profileID string, expectedRevision int64) error {
	if expectedRevision <= 0 {
		return ErrInvalidProfileCredentialRevision
	}
	return s.clear(ctx, accountID, profileID, &expectedRevision)
}
