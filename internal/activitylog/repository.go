package activitylog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// AdminEvent is a typed, redacted record of an authority-changing Platform
// operation. BeforeState and AfterState must contain only bounded lifecycle
// fields; request bodies, credentials, invite secrets, and tokens never belong
// in this record.
type AdminEvent struct {
	ActorAccountID    int            `json:"actor_account_id"`
	ActorPlatformRole string         `json:"actor_platform_role"`
	AuthorityContext  string         `json:"authority_context"`
	Action            string         `json:"action"`
	TargetType        string         `json:"target_type"`
	TargetID          string         `json:"target_id"`
	OrganizationID    uuid.UUID      `json:"organization_id"`
	SubjectID         string         `json:"subject_id,omitempty"`
	BeforeRevision    int64          `json:"before_revision"`
	AfterRevision     int64          `json:"after_revision"`
	Outcome           string         `json:"outcome"`
	RequestID         string         `json:"request_id,omitempty"`
	BeforeState       map[string]any `json:"before_state,omitempty"`
	AfterState        map[string]any `json:"after_state,omitempty"`
}

// RecordAdminEvent appends an immutable lifecycle audit event.
func (r *Repo) RecordAdminEvent(ctx context.Context, event AdminEvent) error {
	if r == nil || r.pool == nil {
		return errors.New("activity log repository unavailable")
	}
	beforeState, err := json.Marshal(event.BeforeState)
	if err != nil {
		return fmt.Errorf("marshal admin audit before state: %w", err)
	}
	afterState, err := json.Marshal(event.AfterState)
	if err != nil {
		return fmt.Errorf("marshal admin audit after state: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO admin_audit_events (
			actor_account_id, actor_platform_role, authority_context, action,
			target_type, target_id, organization_id, subject_id,
			before_revision, after_revision, outcome, request_id,
			before_state, after_state
		) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11, NULLIF($12, ''), $13, $14)`,
		event.ActorAccountID, event.ActorPlatformRole, event.AuthorityContext, event.Action,
		event.TargetType, event.TargetID, event.OrganizationID, event.SubjectID,
		event.BeforeRevision, event.AfterRevision, event.Outcome, event.RequestID,
		beforeState, afterState)
	if err != nil {
		return fmt.Errorf("record admin audit event: %w", err)
	}
	return nil
}
