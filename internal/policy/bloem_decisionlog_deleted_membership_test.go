package policy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Decisions are buffered and written a moment later, so deleting an account
// can remove its organization membership before its last decisions reach the
// table. Those rows keep no membership, as ON DELETE SET NULL leaves the ones
// already written, and the batch they share with everyone else's decisions
// is stored instead of dropped.
func TestDecisionLogKeepsABatchWhoseMembershipWasDeleted(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPolicyStoreTest(t, ctx)
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	var accountID int
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, role, enabled) VALUES ($1, 'user', true) RETURNING id`,
		"decision-deleted-"+uuid.NewString()).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	var membershipID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user') RETURNING id`, organizationID, accountID).Scan(&membershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, accountID); err != nil {
		t.Fatal(err)
	}

	logger := newDecisionLogger(pool, "decision-test")
	now := time.Now().UTC()
	err := logger.insertBatch(ctx, []Entry{
		{Timestamp: now, OrganizationID: organizationID, MembershipID: membershipID, DecisionName: DecisionAction, PolicyGeneration: 1, EvalTimeNS: 1, InputDigest: "deleted-member"},
		{Timestamp: now, OrganizationID: organizationID, DecisionName: DecisionAction, PolicyGeneration: 1, EvalTimeNS: 1, InputDigest: "someone-else"},
	})
	if err != nil {
		t.Fatalf("insertBatch: %v; want the batch stored", err)
	}
	var stored, withMembership int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(membership_id) FROM policy_decisions
		WHERE input_digest IN ('deleted-member', 'someone-else')`).Scan(&stored, &withMembership); err != nil {
		t.Fatal(err)
	}
	if stored != 2 || withMembership != 0 {
		t.Fatalf("stored %d rows, %d with a membership; want both rows and no dangling membership", stored, withMembership)
	}
}
