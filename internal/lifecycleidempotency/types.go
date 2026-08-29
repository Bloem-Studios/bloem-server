package lifecycleidempotency

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Digest [32]byte

type ActorKind string

const (
	ActorAuthenticatedAccount ActorKind = "authenticated_account"
	ActorPreauthIntent        ActorKind = "preauth_intent"
)

type TargetSource string

const (
	TargetPathAccount      TargetSource = "path_account"
	TargetPathTenantMember TargetSource = "path_tenant_member"
	TargetBodyAccount      TargetSource = "body_account"
	TargetStoredSelection  TargetSource = "stored_selection"
	TargetExactMembership  TargetSource = "exact_membership"
)

type Phase string

const (
	PhaseOptional Phase = "optional"
	PhaseRequired Phase = "required"
)

type State string

const (
	StateBindingUnresolved State = "binding_unresolved"
	StateBound             State = "bound"
	StateCommittedPending  State = "committed_pending"
	StateCompleted         State = "completed"
)

var (
	ErrConflict       = errors.New("lifecycle idempotency key conflicts with its original request")
	ErrKeyRequired    = errors.New("lifecycle idempotency key is required")
	ErrKeyMalformed   = errors.New("lifecycle idempotency key is malformed")
	ErrInvalidBinding = errors.New("lifecycle idempotency binding is invalid")
	ErrTargetNotFound = errors.New("lifecycle idempotency target not found")
	// ErrTargetUnavailable means the live resource exists but cannot yet be
	// represented by the receipt target schema (for example, an empty tenant).
	ErrTargetUnavailable = errors.New("lifecycle idempotency target is temporarily unavailable")
	ErrPending           = errors.New("lifecycle request is committed and pending completion")
)

type TargetBinding struct {
	OrganizationID       uuid.UUID
	MembershipID         uuid.UUID
	AccountID            int
	AccountIncarnationID uuid.UUID
	ProfileID            string
	ResourceID           string
}

type Binding struct {
	ActorKind                 ActorKind
	ActorAccountID            *int
	ActorAccountIncarnationID *uuid.UUID
	ActorSubjectDigest        Digest
	Method                    string
	RouteID                   string
	RequestHash               Digest
	TargetSource              TargetSource
	TargetSetDigest           Digest
	Targets                   []TargetBinding
}

type Request struct {
	IdempotencyKey string
	Binding        Binding
	ResolveTargets func(context.Context, pgx.Tx) ([]TargetBinding, error)
}

type Result struct {
	Status      int
	Body        []byte
	Headers     map[string][]string
	OperationID string
	Replayed    bool
}

type Receipt struct {
	KeyDigest Digest
	Binding   Binding
	State     State
	Result    Result
}

type Mutator func(context.Context, pgx.Tx, Binding) (Result, error)
type CreateMutator func(context.Context, pgx.Tx) ([]TargetBinding, Result, error)

type Coordinator interface {
	Execute(context.Context, Request, Mutator) (Result, error)
	ExecuteCreate(context.Context, Request, CreateMutator) (Result, error)
}
