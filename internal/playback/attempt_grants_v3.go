package playback

import (
	"context"
	"time"
)

// AttemptGrantPolicyV3 is an internal deployment policy, not a wire-contract TTL.
// An unconfigured store cannot issue grants.
type AttemptGrantPolicyV3 struct{ MaxDuration time.Duration }

type AttemptGrantPurposeV3 string

const (
	AttemptGrantExecuteV3 AttemptGrantPurposeV3 = "execute"
	AttemptGrantServeV3   AttemptGrantPurposeV3 = "serve"
	// Transfer authorizes only a selected execution node's output hop to the
	// selected egress. It requires a permit opened by that egress; it never
	// grants permission to emit client bytes.
	AttemptGrantTransferV3 AttemptGrantPurposeV3 = "output_transfer"
)

// AttemptGrantRouteV3 freezes the selected transport and node identities beside
// the existing plan/recipe. Node zero denotes the owning API's local role.
type AttemptGrantRouteV3 struct {
	Executor        ExecutorNamespaceV3 `json:"executor"`
	TransportID     string              `json:"transport_id"`
	ExecutionNodeID int                 `json:"execution_node_id"`
	EgressNodeID    int                 `json:"egress_node_id"`
}

type AttemptGrantRequestV3 struct {
	Executor         ExecutorNamespaceV3
	SessionID        string
	PlanID           string
	TransportID      string
	Purpose          AttemptGrantPurposeV3
	NodeID           int
	Duration         time.Duration
	OutputTransferID string
	EgressNodeID     int
}

type AttemptGrantV3 struct {
	Authority AttemptAuthorityV3
	Request   AttemptGrantRequestV3
	IssuedAt  time.Time
	NotAfter  time.Time
}

type AttemptDrainV3 struct{ NotBefore time.Time }

// GrantPlanStoreV3 records grant validity before replying, including responses
// subsequently lost in transit. It does not enforce execution or HTTP writes.
type GrantPlanStoreV3 interface {
	AuthoritativePlanStoreV3
	StageAttemptRoute(context.Context, AttemptAuthorityV3, AttemptRecordV3, AttemptGrantRouteV3) error
	IssueAttemptGrant(context.Context, AttemptAuthorityV3, AttemptGrantRequestV3) (AttemptGrantV3, error)
	BeginAttemptDrain(context.Context, AttemptAuthorityV3) (AttemptDrainV3, error)
	CompleteAttemptDrain(context.Context, AttemptAuthorityV3) error
}
