package adminpeople

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

const sqlstateDeadlockDetected = "40P01"

// TargetClass says what a person is to the actor who wants to act on them.
// Every organization write that touches a person (People, password reset,
// sessions, profiles) asks Judge, so one table governs all of them:
//
//	target                OA   OO   PA
//	member                yes  yes  yes
//	organization_admin    no   yes  yes
//	organization_owner    no   -    yes
//	platform_account      no   no   only where Silo's Owner rule allows
//	self                  no   no   no
//
// OA is an organization administrator, OO the organization owner, PA a
// platform administrator (through a membership or through support access).
type TargetClass string

const (
	TargetSelf              TargetClass = "self"
	TargetPlatformAccount   TargetClass = "platform_account"
	TargetOrganizationOwner TargetClass = "organization_owner"
	TargetOrganizationAdmin TargetClass = "organization_admin"
	TargetMember            TargetClass = "member"
)

var (
	// ErrTargetProtected is matched by errors.Is for every *TargetProtectedError.
	ErrTargetProtected = errors.New("target is protected from this actor")
	// ErrAccountShared means the account belongs to more than one organization.
	// Account-wide effects (a password reset, signing out everywhere) must not
	// let one organization act on another's member.
	ErrAccountShared = errors.New("account belongs to more than one organization")
)

// TargetProtectedError is the per-target refusal. Handlers answer it with
// 409 target_protected and Reason, never 403: clients read a 403 on an
// organization route as loss of access to the whole organization.
type TargetProtectedError struct{ Reason TargetClass }

func (e *TargetProtectedError) Error() string {
	return fmt.Sprintf("target is protected from this actor (%s)", e.Reason)
}

func (e *TargetProtectedError) Is(target error) bool { return target == ErrTargetProtected }

// ActorFacts is the acting account as the rules see it, read under lock.
type ActorFacts struct {
	AccountID           int
	Authority           string
	IsOrganizationOwner bool
	IsServerOwner       bool
}

// TargetFacts is the person being acted on, read under lock.
type TargetFacts struct {
	AccountID           int
	MembershipID        uuid.UUID
	Status              tenancy.MembershipStatus
	LegacyRole          string
	SecurityRevision    int64
	IsOrganizationOwner bool
	Enabled             bool
	UserRole            string
	IsServerOwner       bool
	// MembershipCount is how many organizations the account belongs to.
	MembershipCount int
}

// Classify names the target's class for the actor. The order is fixed: self,
// platform account, organization owner, organization administrator, member.
func Classify(actorAccountID int, target TargetFacts) TargetClass {
	switch {
	case target.AccountID == actorAccountID:
		return TargetSelf
	case target.UserRole == models.RoleAdmin || target.IsServerOwner:
		return TargetPlatformAccount
	case target.IsOrganizationOwner:
		return TargetOrganizationOwner
	case target.LegacyRole == models.RoleAdmin:
		return TargetOrganizationAdmin
	default:
		return TargetMember
	}
}

// Judge applies the table above. It is pure: callers read the facts under
// row locks and pass them in.
func Judge(actor ActorFacts, target TargetFacts) (TargetClass, error) {
	class := Classify(actor.AccountID, target)
	platform := actor.Authority == AuthorityPlatformAdmin
	refuse := &TargetProtectedError{Reason: class}
	switch class {
	case TargetSelf:
		return class, refuse
	case TargetPlatformAccount:
		if !platform {
			return class, refuse
		}
		owner := auth.OwnerActor{ID: actor.AccountID, IsOwner: actor.IsServerOwner}
		if auth.CheckOwnerTarget(owner, &models.User{ID: target.AccountID, Role: target.UserRole, IsOwner: target.IsServerOwner}) != nil {
			return class, refuse
		}
	case TargetOrganizationOwner:
		if !platform {
			return class, refuse
		}
	case TargetOrganizationAdmin:
		if !platform && !actor.IsOrganizationOwner {
			return class, refuse
		}
	case TargetMember:
	}
	return class, nil
}

// LockedAuthority is what LockAuthority read.
type LockedAuthority struct {
	Actor  ActorFacts
	Target TargetFacts
	Class  TargetClass
}

// LockAuthority re-reads and locks, in one transaction, everything the rules
// depend on, then judges them. Lock order: organization (shared), actor
// membership or account (shared), target membership, target account. A
// promotion that commits first is seen; one that commits later cannot change
// what this transaction already wrote.
//
// It returns ErrAuthorizationStateChanged when the organization is no longer
// active or the actor no longer holds the authority the request was issued
// under, ErrNotFound when the account is not a member of the organization,
// and *TargetProtectedError (with the facts) when the rules refuse. The
// returned LockedAuthority is filled in for a refusal too, so a caller can
// audit it.
func LockAuthority(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, actor MutationActor, accountID int) (LockedAuthority, error) {
	var out LockedAuthority
	var ownerAccountID *int
	err := tx.QueryRow(ctx, `SELECT owner_account_id FROM organizations WHERE id=$1 AND status='active' FOR SHARE`, organizationID).Scan(&ownerAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrAuthorizationStateChanged
	}
	if err != nil {
		return out, err
	}
	out.Actor = ActorFacts{AccountID: actor.AccountID, Authority: actor.Authority}
	out.Actor.IsOrganizationOwner = ownerAccountID != nil && *ownerAccountID == actor.AccountID

	switch actor.Authority {
	case AuthorityPlatformAdmin:
		var enabled bool
		var role string
		err = tx.QueryRow(ctx, `SELECT enabled,role,is_owner FROM users WHERE id=$1 FOR SHARE`, actor.AccountID).Scan(&enabled, &role, &out.Actor.IsServerOwner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!enabled || role != models.RoleAdmin)) {
			return out, ErrAuthorizationStateChanged
		}
		if err != nil {
			return out, err
		}
	case AuthorityOrganizationAdmin:
		var status tenancy.MembershipStatus
		var role string
		var revision int64
		var enabled bool
		err = tx.QueryRow(ctx, `SELECT m.status,m.legacy_role,m.security_revision,u.enabled FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2 FOR SHARE OF m`, organizationID, actor.AccountID).Scan(&status, &role, &revision, &enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrAuthorizationStateChanged
		}
		if err != nil {
			return out, err
		}
		if status != tenancy.MembershipActive || role != models.RoleAdmin || !enabled || (actor.SecurityRevision > 0 && actor.SecurityRevision != revision) {
			return out, ErrAuthorizationStateChanged
		}
	default:
		return out, ErrAuthorizationStateChanged
	}

	target := TargetFacts{AccountID: accountID}
	err = tx.QueryRow(ctx, `SELECT id,status,legacy_role,security_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2 FOR UPDATE`, organizationID, accountID).Scan(&target.MembershipID, &target.Status, &target.LegacyRole, &target.SecurityRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT enabled,role,is_owner FROM users WHERE id=$1 FOR UPDATE`, accountID).Scan(&target.Enabled, &target.UserRole, &target.IsServerOwner); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE account_id=$1`, accountID).Scan(&target.MembershipCount); err != nil {
		return out, err
	}
	target.IsOrganizationOwner = ownerAccountID != nil && *ownerAccountID == accountID
	out.Target = target
	out.Class, err = Judge(out.Actor, target)
	return out, err
}

// RequireUnshared refuses an account-wide effect on an account that belongs to
// more than one organization.
func (a LockedAuthority) RequireUnshared() error {
	if a.Target.MembershipCount > 1 {
		return ErrAccountShared
	}
	return nil
}

// auditedRefusal marks an error whose refusal audit row must be committed
// even though the request failed.
type auditedRefusal struct{ cause error }

func (e *auditedRefusal) Error() string { return e.cause.Error() }
func (e *auditedRefusal) Unwrap() error { return e.cause }

// auditRefusal turns a per-target refusal into an audited one: it writes the
// "<action>_refused" conflict row in the open transaction and marks the error
// so writePerson commits the row. Every other error passes through.
func auditRefusal(ctx context.Context, tx pgx.Tx, err error, actorID int, action, targetType, targetID string, organizationID uuid.UUID, accountID int, revision int64) error {
	var refusal *TargetProtectedError
	if !errors.As(err, &refusal) {
		return err
	}
	if auditErr := recordPeopleAudit(ctx, tx, actorID, action, targetType, targetID, organizationID, accountID, revision, revision, "conflict", nil, map[string]any{"reason": string(refusal.Reason)}); auditErr != nil {
		return auditErr
	}
	return &auditedRefusal{cause: err}
}

// writePerson runs one person write in its own transaction. A refusal commits
// its audit row; a deadlock with a concurrent write on the same people is
// retried, because two administrators acting on each other lock in opposite
// order.
func (s *Service) writePerson(ctx context.Context, fn func(pgx.Tx) (PersonSummary, error)) (PersonSummary, error) {
	for attempt := 1; ; attempt++ {
		person, err := s.writePersonOnce(ctx, fn)
		var pgErr *pgconn.PgError
		if attempt < 3 && errors.As(err, &pgErr) && pgErr.Code == sqlstateDeadlockDetected {
			continue
		}
		return person, err
	}
}

func (s *Service) writePersonOnce(ctx context.Context, fn func(pgx.Tx) (PersonSummary, error)) (PersonSummary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PersonSummary{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	person, err := fn(tx)
	if err != nil {
		var refusal *auditedRefusal
		if errors.As(err, &refusal) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return PersonSummary{}, commitErr
			}
			return PersonSummary{}, refusal.cause
		}
		return PersonSummary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PersonSummary{}, err
	}
	return person, nil
}

// ReasonTargetProtected is the bulk skip reason for a person the actor may not
// act on (anything Judge refuses other than the actor itself).
const ReasonTargetProtected = "target_protected"

// judgeLockedBulkTarget judges one bulk target. The batch already holds the
// organization, the actor and (for this record) the target's membership and
// account locks, so the facts are read without locking again.
func judgeLockedBulkTarget(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) error {
	actor, ok := MutationActorFromContext(ctx)
	if !ok {
		return nil
	}
	facts := ActorFacts{AccountID: actor.AccountID, Authority: actor.Authority}
	target := TargetFacts{AccountID: accountID}
	var ownerAccountID *int
	err := tx.QueryRow(ctx, `SELECT m.legacy_role,u.role,u.is_owner,o.owner_account_id,COALESCE((SELECT is_owner FROM users WHERE id=$3),false)
		FROM organization_memberships m JOIN users u ON u.id=m.account_id JOIN organizations o ON o.id=m.organization_id
		WHERE m.organization_id=$1 AND m.account_id=$2`, organizationID, accountID, actor.AccountID).Scan(&target.LegacyRole, &target.UserRole, &target.IsServerOwner, &ownerAccountID, &facts.IsServerOwner)
	if err != nil {
		return err
	}
	facts.IsOrganizationOwner = ownerAccountID != nil && *ownerAccountID == actor.AccountID
	target.IsOrganizationOwner = ownerAccountID != nil && *ownerAccountID == accountID
	_, err = Judge(facts, target)
	return err
}

// bulkTargetProtection turns a refusal into the bulk record's skip. Anything
// but a refusal that is not a protected target is an error for the record.
func bulkTargetProtection(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (string, string, error) {
	err := judgeLockedBulkTarget(ctx, tx, organizationID, accountID)
	var refusal *TargetProtectedError
	switch {
	case err == nil:
		return "", "", nil
	case errors.As(err, &refusal) && refusal.Reason == TargetSelf:
		return "skipped", ReasonActorAuthorityTarget, nil
	case errors.As(err, &refusal):
		return "skipped", ReasonTargetProtected, nil
	default:
		return "", "", err
	}
}
