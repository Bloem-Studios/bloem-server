package adminpeople

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuditEvent is one row of admin_audit_events, the organization-visible ledger
// of administrative writes. Silo's own audit does not record a request under
// /api/bloem/v2, so every route that writes calls RecordAudit itself, in the
// transaction that makes the change.
//
// Callers must keep secrets out of Before and After: no reset link, token,
// digest, password, PIN or PIN hash, and no email address.
type AuditEvent struct {
	Actor            MutationActor
	Action           string
	TargetType       string
	TargetID         string
	OrganizationID   uuid.UUID
	SubjectAccountID int
	BeforeRevision   int64
	AfterRevision    int64
	Outcome          string
	Before           map[string]any
	After            map[string]any
}

// RecordAudit writes the event in tx. A request under support access carries
// the access id, so the organization sees every write made under it.
func RecordAudit(ctx context.Context, tx pgx.Tx, event AuditEvent) error {
	beforeJSON, err := json.Marshal(orEmpty(event.Before))
	if err != nil {
		return fmt.Errorf("encode audit before state: %w", err)
	}
	afterJSON, err := json.Marshal(orEmpty(event.After))
	if err != nil {
		return fmt.Errorf("encode audit after state: %w", err)
	}
	subject := ""
	if event.SubjectAccountID > 0 {
		subject = strconv.Itoa(event.SubjectAccountID)
	}
	var supportAccess *uuid.UUID
	if event.Actor.SupportAccessID != uuid.Nil {
		supportAccess = &event.Actor.SupportAccessID
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit_events (actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,subject_id,before_revision,after_revision,outcome,request_id,before_state,after_state,support_access_id) VALUES ($1,$2,'organization',$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,NULLIF($11,''),$12,$13,$14)`,
		event.Actor.AccountID, event.Actor.Authority, event.Action, event.TargetType, event.TargetID, event.OrganizationID, subject, event.BeforeRevision, event.AfterRevision, event.Outcome, event.Actor.RequestID, beforeJSON, afterJSON, supportAccess)
	if err != nil {
		return fmt.Errorf("record admin audit: %w", err)
	}
	return nil
}

func orEmpty(state map[string]any) map[string]any {
	if state == nil {
		return map[string]any{}
	}
	return state
}

// Refusal is a refusal a support route audits and still answers: the
// "<action>_refused" conflict row is committed with the request's transaction
// and the cause is returned to the caller. WrapRefusal makes one.
type Refusal struct{ cause error }

// WrapRefusal marks err so RunSupportTx commits the audit row written before
// it and then returns err.
func WrapRefusal(err error) error {
	if err == nil {
		return nil
	}
	return &Refusal{cause: err}
}

func (e *Refusal) Error() string { return e.cause.Error() }
func (e *Refusal) Unwrap() error { return e.cause }

// RefuseAudited writes a "<action>_refused" conflict event for a refusal and
// returns it wrapped for RunSupportTx. reason is a code, never free text.
func RefuseAudited(ctx context.Context, tx pgx.Tx, event AuditEvent, reason string, cause error) error {
	event.Outcome = "conflict"
	event.After = map[string]any{"reason": reason}
	event.Before = nil
	if err := RecordAudit(ctx, tx, event); err != nil {
		return err
	}
	return WrapRefusal(cause)
}

// RunSupportTx runs fn in one transaction. fn returns (result, error); a
// *Refusal commits what fn wrote (its refusal audit row) and returns the
// cause. A deadlock is retried up to twice, because two administrators acting
// on each other lock in opposite order.
func RunSupportTx[T any](ctx context.Context, begin func(context.Context) (pgx.Tx, error), fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	for attempt := 1; ; attempt++ {
		result, err := runSupportTxOnce(ctx, begin, fn)
		if attempt < 3 && isDeadlock(err) {
			continue
		}
		if err != nil {
			return zero, err
		}
		return result, nil
	}
}

func runSupportTxOnce[T any](ctx context.Context, begin func(context.Context) (pgx.Tx, error), fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	result, err := fn(tx)
	if err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return zero, commitErr
			}
			return zero, refusal.cause
		}
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return result, nil
}

// Budget is a named rate limit on support actions, counted from
// admin_audit_events so it holds across nodes without new infrastructure.
// Refusals are audited as conflict rows with the "_refused" action and count
// against it, so a compromised administrator cannot probe for protected
// targets faster than the limit.
type Budget struct {
	Name string
	// Actions are the audited actions the budget counts, refusals included.
	Actions []string
	// PerTarget counts events about one target (the audit row's target id for
	// streams, its subject account otherwise); zero Max skips the limit.
	PerTarget Limit
	PerActor  Limit
	PerOrg    Limit
	// TargetBySubject selects the per-target key: the subject account (true) or
	// the audit row's target id (false).
	TargetBySubject bool
}

// Limit is Max events in Window.
type Limit struct {
	Max    int
	Window time.Duration
}

// The support budgets. All numbers are the design's proposals, not measurements.
var (
	BudgetDisclose = Budget{
		Name:            "support_disclose",
		Actions:         []string{"people.password_reset_issued", "people.password_reset_refused"},
		TargetBySubject: true,
		PerTarget:       Limit{1, 2 * time.Minute},
		PerActor:        Limit{10, 10 * time.Minute},
		PerOrg:          Limit{30, time.Hour},
	}
	BudgetMutate = Budget{
		Name: "support_mutate",
		Actions: []string{
			"people.sessions_revoked", "people.sessions_revoke_refused",
			"people.session_revoked", "people.session_revoke_refused",
			"people.device_signed_out", "people.device_sign_out_refused",
			"people.profile_created", "people.profile_create_refused",
			"people.profile_renamed", "people.profile_rename_refused",
			"people.profile_pin_cleared", "people.profile_pin_clear_refused",
			"people.profile_removed", "people.profile_remove_refused",
		},
		TargetBySubject: true,
		PerTarget:       Limit{6, 10 * time.Minute},
		PerActor:        Limit{30, 10 * time.Minute},
		PerOrg:          Limit{120, time.Hour},
	}
	BudgetStream = Budget{
		Name:      "support_stream",
		Actions:   []string{"stream.stopped", "stream.stop_refused", "stream.message_sent", "stream.message_refused"},
		PerTarget: Limit{6, time.Minute},
		PerActor:  Limit{30, 10 * time.Minute},
		PerOrg:    Limit{60, time.Hour},
	}
)

// RateLimitedError is a budget that is spent. RetryAfter is when the oldest
// counted event leaves its window.
type RateLimitedError struct {
	Budget     string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string { return "support rate limit reached: " + e.Budget }

// ErrRateLimited is matched by errors.Is for every *RateLimitedError.
var ErrRateLimited = errors.New("support rate limit reached")

func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

// Querier is the read surface a budget check needs; pgx.Tx and *pgxpool.Pool satisfy it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CheckBudget counts the actor's, the target's and the organization's recent
// events against budget. target is the account id (as text) or the stream
// session id, per budget.TargetBySubject. Inside a transaction, take
// LockBudget first so concurrent requests cannot all pass the same count.
func CheckBudget(ctx context.Context, q Querier, budget Budget, organizationID uuid.UUID, actorID int, target string) error {
	type scope struct {
		limit  Limit
		filter string
		args   []any
	}
	targetColumn := "target_id"
	if budget.TargetBySubject {
		targetColumn = "subject_id"
	}
	scopes := []scope{
		{budget.PerTarget, " AND " + targetColumn + " = $4", []any{target}},
		{budget.PerActor, " AND actor_account_id = $4", []any{actorID}},
		{budget.PerOrg, "", nil},
	}
	var retry time.Duration
	for _, sc := range scopes {
		if sc.limit.Max <= 0 {
			continue
		}
		args := append([]any{organizationID, budget.Actions, sc.limit.Window.Seconds()}, sc.args...)
		var count int
		var oldest *time.Time
		var now time.Time
		err := q.QueryRow(ctx, `SELECT count(*), min(created_at), now() FROM admin_audit_events
			WHERE organization_id = $1 AND action = ANY($2) AND created_at > now() - make_interval(secs => $3)`+sc.filter, args...).Scan(&count, &oldest, &now)
		if err != nil {
			return fmt.Errorf("count support budget %s: %w", budget.Name, err)
		}
		if count >= sc.limit.Max && oldest != nil {
			wait := oldest.Add(sc.limit.Window).Sub(now)
			if wait < time.Second {
				wait = time.Second
			}
			if wait > retry {
				retry = wait
			}
		}
	}
	if retry > 0 {
		return &RateLimitedError{Budget: budget.Name, RetryAfter: retry}
	}
	return nil
}

// LockBudget serializes the checks and writes of one budget in one
// organization for the rest of the transaction.
func LockBudget(ctx context.Context, tx pgx.Tx, budget Budget, organizationID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "bloem:"+budget.Name+":"+organizationID.String()); err != nil {
		return fmt.Errorf("lock support budget %s: %w", budget.Name, err)
	}
	return nil
}

func isDeadlock(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == sqlstateDeadlockDetected
}
