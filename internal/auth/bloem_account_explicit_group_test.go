package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestBloemAccountExplicitOrganizationGroup(t *testing.T) {
	pool := bloemAccountTransactionDatabase(t)
	ctx := t.Context()
	users := auth.NewUserRepository(pool)
	tenants := tenancy.NewStore(pool)
	owner, err := users.Create(ctx, models.CreateUserInput{
		Username: "owner", Email: "owner@example.test", Password: "fixture-password", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ProvisionDefaultMembership(ctx, owner.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := tenants.ActivateInitialOwnership(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, slug, name, status, owner_account_id)
		VALUES ($1, $2, 'Explicit group organization', 'active', $3)`, organizationID, organizationID.String(), owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Default', true)`, organizationID); err != nil {
		t.Fatal(err)
	}
	var intendedGroupID, foreignGroupID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Explicit non-default group', false) RETURNING id`, organizationID).Scan(&intendedGroupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT g.id FROM access_groups g JOIN organizations o ON o.id = g.organization_id
		WHERE o.is_default AND g.is_default`).Scan(&foreignGroupID); err != nil {
		t.Fatal(err)
	}
	accounts := auth.NewAccountProvisioner(users, pgstore.NewPostgresProvider(pool))
	accounts.SetMembershipProvisioner(&bloemAccountMemberships{store: tenants})

	for _, tc := range []struct {
		name    string
		groupID int64
		foreign bool
	}{
		{name: "same-organization", groupID: intendedGroupID},
		{name: "foreign-organization", groupID: foreignGroupID, foreign: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			before := bloemAccountTransactionCounts(t, tx)
			maxStreams, maxProfiles := 2, 3
			downloadAllowed, requestsAllowed := false, true
			input := auth.CreateAccountInput{
				User: models.CreateUserInput{
					Username: tc.name, Email: tc.name + "@example.test", Password: "fixture-password", Role: models.RoleUser,
					AccessGroupID: &tc.groupID, MaxStreams: &maxStreams, MaxProfiles: &maxProfiles,
					DownloadAllowed: &downloadAllowed, RequestsAllowed: &requestsAllowed,
				},
				DefaultProfile: auth.DefaultProfileOptions{Enabled: true, Name: "Home"},
			}
			created, err := accounts.CreateAccountForOrganizationInTransaction(ctx, tx, organizationID, input)
			if tc.foreign {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "organization_memberships_organization_access_group_fkey" {
					t.Fatalf("foreign group error = %v, want organization/group foreign key rejection", err)
				}
				if created != (auth.CreatedAccount{}) {
					t.Fatal("foreign group rejection returned account identities")
				}
				if err := tx.Rollback(ctx); err != nil {
					t.Fatal(err)
				}
				var after [4]int
				if err := pool.QueryRow(ctx, `SELECT
					(SELECT count(*) FROM users),
					(SELECT count(*) FROM organization_memberships),
					(SELECT count(*) FROM user_profiles),
					(SELECT count(*) FROM login_email_registry)`).Scan(&after[0], &after[1], &after[2], &after[3]); err != nil {
					t.Fatal(err)
				}
				if after != before {
					t.Fatalf("foreign group rollback left account/membership/profile/email rows: before=%v after=%v", before, after)
				}
				var accountsLeft, emailsLeft int
				if err := pool.QueryRow(ctx, `SELECT
					(SELECT count(*) FROM users WHERE username = $1 OR email = $2),
					(SELECT count(*) FROM login_email_registry WHERE normalized_email = $2)`, input.User.Username, input.User.Email).Scan(&accountsLeft, &emailsLeft); err != nil {
					t.Fatal(err)
				}
				if accountsLeft != 0 || emailsLeft != 0 {
					t.Fatalf("rejected identity survived rollback: accounts=%d emails=%d", accountsLeft, emailsLeft)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if created.User == nil || created.User.AccountIncarnationID == uuid.Nil || created.OrganizationID != organizationID || created.MembershipID == uuid.Nil || created.ProfileID == "" {
				t.Fatal("successful provisioning omitted or changed an intended identity")
			}
			user := created.User
			if user.AccessGroupID == nil || *user.AccessGroupID != intendedGroupID ||
				user.MaxStreams == nil || *user.MaxStreams != maxStreams || user.MaxProfiles != maxProfiles ||
				user.DownloadAllowed == nil || *user.DownloadAllowed != downloadAllowed ||
				user.RequestsAllowed == nil || *user.RequestsAllowed != requestsAllowed || user.MaxTranscodes != nil {
				t.Fatal("returned user lost explicit group, policy overrides, or inherited policy")
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var accountCount, membershipCount, profileCount, emailCount int
			if err := pool.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM users WHERE id = $1),
				(SELECT count(*) FROM organization_memberships WHERE account_id = $1),
				(SELECT count(*) FROM user_profiles WHERE user_id = $1),
				(SELECT count(*) FROM login_email_registry WHERE account_id = $1 AND normalized_email = $2)`, user.ID, input.User.Email).Scan(&accountCount, &membershipCount, &profileCount, &emailCount); err != nil {
				t.Fatal(err)
			}
			if accountCount != 1 || membershipCount != 1 || profileCount != 1 || emailCount != 1 {
				t.Fatalf("committed account/membership/profile/email counts = %d/%d/%d/%d, want 1/1/1/1", accountCount, membershipCount, profileCount, emailCount)
			}
			var membershipOrganization, profileOrganization uuid.UUID
			var membershipGroup, profileGroup int64
			var status, role string
			var storedMaxStreams, storedMaxTranscodes *int
			var storedMaxProfiles int
			var storedDownload, storedRequests *bool
			if err := pool.QueryRow(ctx, `
				SELECT organization_id, access_group_id, status, legacy_role,
				       max_streams, max_transcodes, max_profiles, download_allowed, requests_allowed
				FROM organization_memberships WHERE id = $1 AND account_id = $2`, created.MembershipID, user.ID).Scan(
				&membershipOrganization, &membershipGroup, &status, &role,
				&storedMaxStreams, &storedMaxTranscodes, &storedMaxProfiles, &storedDownload, &storedRequests); err != nil {
				t.Fatal(err)
			}
			if membershipOrganization != organizationID || membershipGroup != intendedGroupID || status != "active" || role != models.RoleUser {
				t.Fatal("committed membership changed organization, explicit group, active status, or role")
			}
			if storedMaxStreams == nil || *storedMaxStreams != maxStreams || storedMaxProfiles != maxProfiles ||
				storedDownload == nil || *storedDownload != downloadAllowed ||
				storedRequests == nil || *storedRequests != requestsAllowed || storedMaxTranscodes != nil {
				t.Fatal("committed membership lost policy overrides or inherited policy")
			}
			var primary bool
			if err := pool.QueryRow(ctx, `
				SELECT organization_id, access_group_id, is_primary
				FROM user_profiles WHERE id = $1 AND user_id = $2`, created.ProfileID, user.ID).Scan(&profileOrganization, &profileGroup, &primary); err != nil {
				t.Fatal(err)
			}
			if profileOrganization != organizationID || profileGroup != intendedGroupID || !primary {
				t.Fatal("committed primary profile changed organization or explicit group")
			}
		})
	}
}
