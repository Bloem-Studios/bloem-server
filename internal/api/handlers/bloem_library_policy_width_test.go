package handlers

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// A catalog library can have a bigint ID even while unrelated child workflows
// retain int4 limits. Merely discovering that library must not break dynamic
// policy materialization for an otherwise ordinary, small-ID account/tenant.
func TestBloemLibraryPolicyWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   int
	}{
		{"int32", 7},
		{"bigint", 2147483648},
	} {
		for _, dynamic := range []bool{true, false} {
			mode := "explicit"
			if dynamic {
				mode = "dynamic"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()
				pool := newInvitationLifecycleDatabase(t, ctx)
				if _, err := pool.Exec(ctx, `INSERT INTO media_folders(id,name,type,enabled) VALUES($1,'Width fixture','movies',true)`, tc.id); err != nil {
					t.Fatal(err)
				}
				ids := []int{tc.id}
				if dynamic {
					ids = []int{}
				}
				template, err := entitlements.NewTemplateStore(pool).Create(ctx, entitlements.CreateTemplateInput{
					Key: "width-fixture", Name: "Width fixture", Enabled: true,
					Policy: entitlements.Policy{LibraryIDs: ids, PlaybackAllowed: true, MaxStreams: 2,
						MaxProfiles: 3, TranscodeAllowed: true, MaxTranscodes: 1, DownloadAllowed: true,
						MaxPlaybackQuality: "1080p", RequestsAllowed: true},
				})
				if err != nil {
					t.Fatalf("create template: %v", err)
				}
				organization, err := tenancy.NewStore(pool).CreateTenantOrganization(ctx, tenancy.CreateTenantOrganizationInput{
					Name: "Width fixture", ExternalOperatorID: "fixture", ExternalServiceID: "width-fixture",
					Slots: 10, Transcodes: 4, EntitlementTemplateKey: template.Key, EntitlementTemplateRevision: template.Revision,
				})
				if err != nil {
					t.Fatalf("materialize library policy: %v", err)
				}
				var groupIDs []int
				if err := pool.QueryRow(ctx, `SELECT library_ids FROM access_groups
					WHERE organization_id=$1 AND is_default`, organization.ID).Scan(&groupIDs); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(groupIDs, []int{tc.id}) {
					t.Fatalf("materialized group IDs=%v, want the exact library", groupIDs)
				}

				users := auth.NewUserRepository(pool)
				account, err := users.Create(ctx, models.CreateUserInput{
					Username: "width-member", Email: "width-member@example.test", Password: "synthetic-width-password",
					Role: models.RoleUser, LibraryIDs: []int{tc.id},
				})
				if err != nil {
					t.Fatalf("create membership policy: %v", err)
				}
				account, err = users.GetByID(ctx, account.ID)
				if err != nil || !slices.Equal(account.LibraryIDs, []int{tc.id}) {
					t.Fatalf("membership policy round trip failed: %v", err)
				}
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
				cohort, _, err := entitlements.NewTemplateStore(pool).EnsureExactCohortInTx(ctx, tx, organization.ID, template.Key, template.Revision, account.ID)
				if err != nil {
					t.Fatalf("freeze cohort policy: %v", err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				var cohortIDs []int
				if err := pool.QueryRow(ctx, `SELECT library_ids FROM entitlement_policy_cohort_revisions WHERE id=$1`, cohort.ID).Scan(&cohortIDs); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(cohortIDs, []int{tc.id}) {
					t.Fatalf("persisted cohort IDs=%v, want the exact library", cohortIDs)
				}
				invitation, err := invitations.NewRepository(pool).CreateForOrganization(ctx, organization.ID, models.CreateInvitationInput{
					Email: "width-invitee@example.test", Role: models.RoleUser, LibraryIDs: []int{tc.id},
					InvitedBy: int64(account.ID), ExpiresAt: time.Now().Add(time.Hour),
				}, "synthetic-width-token-hash")
				if err != nil || !slices.Equal(invitation.LibraryIDs, []int{tc.id}) {
					t.Fatalf("invitation policy round trip failed: %v", err)
				}
			})
		}
	}
}
