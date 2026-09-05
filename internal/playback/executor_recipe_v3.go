package playback

import (
	"context"
	"encoding/hex"
	"fmt"
)

// ExecutorRecipeLocatorV3 identifies immutable recipe bytes. It is a locator,
// never permission to execute: only the authority row publishes the current one.
type ExecutorRecipeLocatorV3 struct {
	Executor ExecutorNamespaceV3 `json:"executor"`
	Digest   string              `json:"digest"`
}

func (l ExecutorRecipeLocatorV3) Validate() error {
	if err := l.Executor.Validate(); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(l.Digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != l.Digest {
		return fmt.Errorf("invalid executor recipe digest")
	}
	return nil
}

// ExecutorRecipePlanStoreV3 publishes a locator only after immutable storage has
// accepted its bytes. Losing that CAS leaves an unreferenced object, never a
// stale mutable session-key overwrite. Namespace replacement remains a distinct
// future lifecycle operation; this CAS cannot alter the staged executor binding.
type ExecutorRecipePlanStoreV3 interface {
	GrantPlanStoreV3
	PublishAttemptRecipeLocator(context.Context, AttemptAuthorityV3, *ExecutorRecipeLocatorV3, ExecutorRecipeLocatorV3) error
	GetAttemptRecipeLocator(context.Context, AttemptAuthorityV3) (*ExecutorRecipeLocatorV3, error)
}
