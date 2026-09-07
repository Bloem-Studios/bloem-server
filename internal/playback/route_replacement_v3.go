package playback

import (
	"context"
	"encoding/json"
	"time"
)

// RouteReplacementKeyV3 preserves the original command and lease across an
// uncertain prepare or commit. None of these identities may be regenerated.
type RouteReplacementKeyV3 struct {
	RequestID    string `json:"request_id"`
	Digest       string `json:"digest"`
	LeaseToken   string `json:"lease_token"`
	BaseReplanID string `json:"base_replan_id"`
}

type RouteReplacementPhaseV3 string

const (
	RouteReplacementStagedV3    RouteReplacementPhaseV3 = "staged"
	RouteReplacementReadyV3     RouteReplacementPhaseV3 = "ready"
	RouteReplacementRetiringV3  RouteReplacementPhaseV3 = "retiring"
	RouteReplacementCommittedV3 RouteReplacementPhaseV3 = "committed"
	RouteReplacementCancelledV3 RouteReplacementPhaseV3 = "cancelled"
)

// RouteReplacementReadyReceiptV3 is supplied only by trusted preparation code.
// Storage binds it to the candidate; it does not attest worker readiness.
type RouteReplacementReadyReceiptV3 struct {
	Route     AttemptGrantRouteV3     `json:"route"`
	Locator   ExecutorRecipeLocatorV3 `json:"locator"`
	ReceiptID string                  `json:"receipt_id"`
}

type RouteReplacementV3 struct {
	Key             RouteReplacementKeyV3           `json:"key"`
	PreviousPlanID  string                          `json:"previous_plan_id"`
	PreviousRoute   AttemptGrantRouteV3             `json:"previous_route"`
	PreviousLocator ExecutorRecipeLocatorV3         `json:"previous_locator"`
	Next            AttemptRecordV3                 `json:"next"`
	Route           AttemptGrantRouteV3             `json:"route"`
	Locator         ExecutorRecipeLocatorV3         `json:"locator"`
	Response        json.RawMessage                 `json:"response"`
	Phase           RouteReplacementPhaseV3         `json:"phase"`
	Ready           *RouteReplacementReadyReceiptV3 `json:"ready,omitempty"`
	DrainNotBefore  time.Time                       `json:"drain_not_before,omitzero"`
}

// BoundRouteReplacementStoreV3 is storage only. Stage/read never authorize
// execution; candidate grants and exact worker cleanup require integration.
type BoundRouteReplacementStoreV3 interface {
	StageBoundRouteReplacement(context.Context, InitialActivationBindingV3, RouteReplacementV3) (RouteReplacementV3, error)
	ReadBoundRouteReplacement(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3) (RouteReplacementV3, error)
	AcknowledgeBoundRouteReplacement(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3, RouteReplacementReadyReceiptV3) (RouteReplacementV3, error)
	BeginBoundRouteRetirement(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3) (RouteReplacementV3, error)
	CompleteBoundRouteReplacement(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3) (RouteReplacementV3, error)
	CancelBoundRouteReplacement(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3) (RouteReplacementV3, error)
	ConfirmBoundRouteCancellation(context.Context, InitialActivationBindingV3, RouteReplacementKeyV3) (RouteReplacementV3, error)
}
