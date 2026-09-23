package policy

// Bloem organization-scoping coverage moved out of Silo's decisionlog_repo_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDecisionRepositoryOrganizationMethodsNeverReturnForeignRows(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPolicyStoreTest(t, ctx)
	repo := NewDecisionRepository(pool)
	var localOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&localOrganizationID); err != nil {
		t.Fatal(err)
	}
	foreignOrganizationID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id,slug,name,status,is_default) VALUES ($1,$2,$3,'initializing',false)`, foreignOrganizationID, "decision-foreign-"+foreignOrganizationID.String(), "Decision foreign"); err != nil {
		t.Fatal(err)
	}
	local := insertDecisionLogRow(t, ctx, pool, Entry{OrganizationID: localOrganizationID, DecisionName: DecisionAction, PolicyGeneration: 1, EvalTimeNS: 1, InputDigest: "local"})
	foreign := insertDecisionLogRow(t, ctx, pool, Entry{OrganizationID: foreignOrganizationID, DecisionName: DecisionAction, PolicyGeneration: 1, EvalTimeNS: 1, InputDigest: "foreign"})

	page, err := repo.ListForOrganization(ctx, localOrganizationID, ListOptions{})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != local.ID {
		t.Fatalf("ListForOrganization = %+v, %v", page, err)
	}
	if _, err := repo.GetForOrganization(ctx, localOrganizationID, foreign.ID, nil); !errors.Is(err, ErrDecisionNotFound) {
		t.Fatalf("foreign GetForOrganization error = %v, want ErrDecisionNotFound", err)
	}
}
