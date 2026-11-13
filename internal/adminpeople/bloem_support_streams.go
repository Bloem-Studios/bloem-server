package adminpeople

// Bloem-owned. Authority over the organization's live streams.
//
// Stopping a stream is not account takeover, so the rules are narrower than
// for a reset or a sign-out: any person of the organization may have a stream
// stopped, administrators and the owner included. Only the actor's own stream
// and a platform-level account's stream are refused (a platform account stays
// behind Silo's Owner rule), so an organization cannot reach the platform
// operator through its console.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// ReadActorFacts re-reads the acting account for a stream route: the
// organization must be active and the actor must still hold the authority the
// pass was issued under.
func ReadActorFacts(ctx context.Context, q Querier, organizationID uuid.UUID, actor MutationActor) (ActorFacts, error) {
	var ownerAccountID *int
	err := q.QueryRow(ctx, `SELECT owner_account_id FROM organizations WHERE id=$1 AND status='active'`, organizationID).Scan(&ownerAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActorFacts{}, ErrAuthorizationStateChanged
	}
	if err != nil {
		return ActorFacts{}, err
	}
	facts := ActorFacts{AccountID: actor.AccountID, Authority: actor.Authority, IsOrganizationOwner: ownerAccountID != nil && *ownerAccountID == actor.AccountID}
	switch actor.Authority {
	case AuthorityPlatformAdmin:
		var enabled bool
		var role string
		err = q.QueryRow(ctx, `SELECT enabled,role,is_owner FROM users WHERE id=$1`, actor.AccountID).Scan(&enabled, &role, &facts.IsServerOwner)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!enabled || role != models.RoleAdmin)) {
			return facts, ErrAuthorizationStateChanged
		}
	case AuthorityOrganizationAdmin:
		var status tenancy.MembershipStatus
		var role string
		var revision int64
		var enabled bool
		err = q.QueryRow(ctx, `SELECT m.status,m.legacy_role,m.security_revision,u.enabled FROM organization_memberships m JOIN users u ON u.id=m.account_id WHERE m.organization_id=$1 AND m.account_id=$2`, organizationID, actor.AccountID).Scan(&status, &role, &revision, &enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return facts, ErrAuthorizationStateChanged
		}
		if err == nil && (status != tenancy.MembershipActive || role != models.RoleAdmin || !enabled || (actor.SecurityRevision > 0 && actor.SecurityRevision != revision)) {
			return facts, ErrAuthorizationStateChanged
		}
	default:
		return facts, ErrAuthorizationStateChanged
	}
	return facts, err
}

// StreamTarget is the stream owner as the stream rules see them.
type StreamTarget struct {
	AccountID     int
	IsPlatform    bool
	IsServerOwner bool
}

// ReadStreamTargets says, for each account, whether it is a platform-level
// account.
func ReadStreamTargets(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, accountIDs []int) (map[int]StreamTarget, error) {
	targets := make(map[int]StreamTarget, len(accountIDs))
	for _, id := range accountIDs {
		targets[id] = StreamTarget{AccountID: id}
	}
	if len(accountIDs) == 0 {
		return targets, nil
	}
	rows, err := q.Query(ctx, `SELECT id, role = 'admin' OR is_owner, is_owner FROM users WHERE id = ANY($1)`, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("read stream owners: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var target StreamTarget
		if err := rows.Scan(&target.AccountID, &target.IsPlatform, &target.IsServerOwner); err != nil {
			return nil, err
		}
		targets[target.AccountID] = target
	}
	return targets, rows.Err()
}

// JudgeStream applies the stream rules: it returns a *TargetProtectedError
// for the actor's own stream and for a platform-level account's stream the
// actor may not touch.
func JudgeStream(actor ActorFacts, target StreamTarget) error {
	if target.AccountID == actor.AccountID {
		return &TargetProtectedError{Reason: TargetSelf}
	}
	if !target.IsPlatform {
		return nil
	}
	refuse := &TargetProtectedError{Reason: TargetPlatformAccount}
	if actor.Authority != AuthorityPlatformAdmin {
		return refuse
	}
	owner := auth.OwnerActor{ID: actor.AccountID, IsOwner: actor.IsServerOwner}
	if auth.CheckOwnerTarget(owner, &models.User{ID: target.AccountID, Role: models.RoleAdmin, IsOwner: target.IsServerOwner}) != nil {
		return refuse
	}
	return nil
}
