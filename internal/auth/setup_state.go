package auth

import (
	"context"
	"fmt"
)

// AccountCounter counts the deployment's login accounts. *UserRepository
// satisfies it.
type AccountCounter interface {
	Count(ctx context.Context) (int, error)
}

// SetupState answers the one question "does this deployment still need its
// first account". It exists so a caller that needs only that answer — the
// public server-identity probe, for instance — can have it without
// constructing the whole *Service, and without a second copy of the rule
// deciding what "needs setup" means.
type SetupState struct {
	users AccountCounter
}

// NewSetupState builds a setup-state reporter over an account counter.
func NewSetupState(users AccountCounter) *SetupState {
	return &SetupState{users: users}
}

// NeedsSetup reports whether the system still needs its initial user account.
func (s *SetupState) NeedsSetup(ctx context.Context) (bool, error) {
	if s == nil || s.users == nil {
		return false, fmt.Errorf("counting users: no account repository")
	}
	return needsSetup(ctx, s.users)
}

// needsSetup is the rule itself, shared by SetupState and *Service so the two
// can never disagree about what an unconfigured deployment looks like.
func needsSetup(ctx context.Context, users AccountCounter) (bool, error) {
	count, err := users.Count(ctx)
	if err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	return count == 0, nil
}
