package policy

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// NewBloemPlaybackAdmissionDecider adds trusted tenant facts to the upstream
// playback admission adapter before policy evaluation.
func NewBloemPlaybackAdmissionDecider(checker ActionChecker) playback.AdmissionDecider {
	return NewPlaybackAdmissionDecider(bloemPlaybackActionChecker{checker: checker})
}

type bloemPlaybackActionChecker struct {
	checker ActionChecker
}

func (c bloemPlaybackActionChecker) CheckAction(ctx context.Context, input ActionInput) (ActionDecision, Meta, error) {
	tenantFacts, err := TenantFactsFromContext(ctx, input.UserID)
	if err != nil {
		return ActionDecision{}, Meta{}, fmt.Errorf("playback admission tenant facts: %w", err)
	}
	input.Tenant = tenantFacts
	return c.checker.CheckAction(ctx, input)
}
