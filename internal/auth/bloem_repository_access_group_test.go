package auth

// Bloem coverage moved out of Silo's repository_access_group_test.go so that
// file stays byte-identical to upstream.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUserRepositoryCreateUsesDeploymentDefaultOrganizationGroupDB(t *testing.T) {
	// A disposable database: the second organization default this test seeds
	// must not be visible to concurrently running packages on the shared DB.
	ctx := context.Background()
	pool := NewBloemAuthTestDatabase(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var deploymentDefaultGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT g.id
		FROM access_groups g
		JOIN organizations o ON o.id = g.organization_id
		WHERE o.is_default
		  AND g.is_default`).Scan(&deploymentDefaultGroupID); err != nil {
		t.Fatalf("load deployment default organization group: %v", err)
	}

	foreignOrganizationID := uuid.New()
	foreignSlug := "auth-access-group-test-" + suffix
	foreignGroupName := "Auth Access Group Test " + suffix + " foreign default"
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, slug, name, status)
		VALUES ($1, $2, $3, 'initializing')`,
		foreignOrganizationID, foreignSlug, "Auth Access Group Test "+suffix); err != nil {
		t.Fatalf("insert foreign organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, $2, true)`, foreignOrganizationID, foreignGroupName); err != nil {
		t.Fatalf("insert foreign organization default group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE organization_id = $1`, foreignOrganizationID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, foreignOrganizationID)
	})

	created, err := NewUserRepository(pool).Create(ctx, createAuthAccessGroupUserInput(suffix, "two-org-defaults", nil))
	if err != nil {
		t.Fatalf("Create(two organization defaults) error: %v", err)
	}
	if created.AccessGroupID == nil || *created.AccessGroupID != deploymentDefaultGroupID {
		t.Fatalf("AccessGroupID = %#v, want deployment default organization group %d", created.AccessGroupID, deploymentDefaultGroupID)
	}
}
