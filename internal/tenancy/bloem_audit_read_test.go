package tenancy_test

import (
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/Silo-Server/silo-server/internal/tenancy"
	"strings"
	"testing"
)

func TestOrganizationAuditScopedRedactedStablePagination(t *testing.T) {
	store, f := newTenancyFixture(t)
	org, err := store.CreateOrganization(adminMutationContext(f), CreateOrganizationInput{Name: "Audit owner", Slug: "audit-owner", OwnerAccountID: f.otherID})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateOrganization(adminMutationContext(f), CreateOrganizationInput{Name: "Other audit", Slug: "other-audit", OwnerAccountID: f.adminID})
	if err != nil {
		t.Fatal(err)
	}
	// Identical timestamps and overlapping IDs across both sources exercise every
	// member of the keyset, not just the common unique-timestamp case.
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO admin_audit_events(created_at,actor_account_id,actor_platform_role,authority_context,action,target_type,target_id,organization_id,before_revision,after_revision,outcome,before_state)
 SELECT '2040-01-01T00:00:00Z',$1,'platform_admin','platform','audit_fixture','organization',($2::uuid)::text,$2::uuid,1,2,'success','{"private":"never-expose"}'::jsonb FROM generate_series(1,60)`, f.adminID, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO entitlement_audit_events(created_at,actor_account_id,action,organization_id) SELECT '2040-01-01T00:00:00Z',$1,'entitlement_fixture',$2 FROM generate_series(1,20)`, f.adminID, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO entitlement_audit_events(actor_account_id,action,organization_id) VALUES($1,'foreign-never-expose',$2)`, f.adminID, foreign.ID); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	fixtures := 0
	pages := 0
	for {
		page, err := store.ListOrganizationAudit(f.ctx, org.ID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Events) > 50 {
			t.Fatal("unbounded page")
		}
		raw, _ := json.Marshal(page)
		if strings.Contains(string(raw), "never-expose") || strings.Contains(string(raw), "before_state") {
			t.Fatalf("private audit data: %s", raw)
		}
		for _, event := range page.Events {
			key := fmt.Sprintf("%s:%d", event.Source, event.ID)
			if seen[key] {
				t.Fatalf("duplicate %s", key)
			}
			seen[key] = true
			if event.Action == "audit_fixture" || event.Action == "entitlement_fixture" {
				fixtures++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 4 {
			t.Fatal("pagination did not converge")
		}
	}
	if fixtures != 80 || pages != 2 {
		t.Fatalf("fixtures=%d pages=%d", fixtures, pages)
	}
	if _, err := store.ListOrganizationAudit(f.ctx, org.ID, "not-a-valid-cursor"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor: %v", err)
	}
	// A cursor is not tenant authority and can never expose the other ledger.
	page, err := store.ListOrganizationAudit(f.ctx, foreign.ID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		if event.Action == "audit_fixture" || event.Action == "entitlement_fixture" {
			t.Fatal("cursor crossed organization boundary")
		}
	}
}
