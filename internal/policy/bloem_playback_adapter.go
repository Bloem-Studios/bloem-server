package policy

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// NewBloemPlaybackAdmissionDecider adds trusted tenant facts to the upstream
// playback admission adapter before policy evaluation.
func NewBloemPlaybackAdmissionDecider(checker ActionChecker) playback.AdmissionDecider {
	decide := NewPlaybackAdmissionDecider(bloemPlaybackActionChecker{checker: checker})
	return func(ctx context.Context, req playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		decision, err := decide(ctx, req)
		if err == nil && !decision.Allowed {
			// Stream insights hears a refusal at a limit, such as a
			// mid-stream switch to a transcode, that no other hook sees.
			playback.NoteAdmissionRefusal(ctx, req.UserID, decision.ReasonCode)
		}
		return decision, err
	}
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
