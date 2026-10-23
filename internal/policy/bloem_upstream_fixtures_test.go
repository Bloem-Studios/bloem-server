package policy

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertDecisionLogRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, entry Entry) Entry {
	t.Helper()
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	if entry.InputDigest == "" {
		entry.InputDigest = "digest"
	}
	if entry.OrganizationID == uuid.Nil {
		if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&entry.OrganizationID); err != nil {
			t.Fatalf("load default organization: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO policy_decisions (
			"timestamp", organization_id, membership_id, decision_name, policy_generation, user_id, profile_id,
			session_id, request_id, node_id, allowed, eval_time_ns, input_digest,
			input_sample, result_sample, error
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb, $15::jsonb, $16)
		RETURNING id, "timestamp"
	`,
		entry.Timestamp,
		entry.OrganizationID,
		nullableUUID(entry.MembershipID),
		string(entry.DecisionName),
		entry.PolicyGeneration,
		entry.UserID,
		nullableString(entry.ProfileID),
		nullableString(entry.SessionID),
		nullableString(entry.RequestID),
		nullableString(entry.NodeID),
		entry.Allowed,
		entry.EvalTimeNS,
		entry.InputDigest,
		nullableJSON(entry.InputSample),
		nullableJSON(entry.ResultSample),
		nullableString(entry.Error),
	).Scan(&entry.ID, &entry.Timestamp); err != nil {
		t.Fatalf("insert policy decision row: %v", err)
	}
	return entry
}
