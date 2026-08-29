package database

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/migrations"
)

const membershipPolicyMigrationSuffix = "_membership_policy_isolation.sql"

type membershipPolicyFixture struct {
	pool         *pgxpool.Pool
	accountID    int
	defaultOrgID uuid.UUID
	otherOrgID   uuid.UUID
	defaultGroup int64
	otherGroup   int64
}

func TestMembershipPolicyMigrationBackfillsEveryOrganizationMembership(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)

	rows, err := fixture.pool.Query(context.Background(), `
SELECT organization_id, access_group_id, permissions, library_ids,
       max_playback_quality, max_streams, max_transcodes,
       transcode_allowed, audio_transcode_allowed, download_allowed,
       download_transcode_allowed, requests_allowed, max_profiles,
       access_policy_revision
FROM public.organization_memberships
WHERE account_id = $1
ORDER BY organization_id`, fixture.accountID)
	if err != nil {
		t.Fatalf("query membership policy: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var organizationID uuid.UUID
		var accessGroupID *int64
		var permissions []string
		var libraryIDs []int32
		var quality *string
		var maxStreams, maxTranscodes *int
		var transcodeAllowed, audioTranscodeAllowed, downloadAllowed *bool
		var downloadTranscodeAllowed, requestsAllowed *bool
		var maxProfiles int
		var revision int64
		if err := rows.Scan(
			&organizationID, &accessGroupID, &permissions, &libraryIDs,
			&quality, &maxStreams, &maxTranscodes, &transcodeAllowed,
			&audioTranscodeAllowed, &downloadAllowed, &downloadTranscodeAllowed,
			&requestsAllowed, &maxProfiles, &revision,
		); err != nil {
			t.Fatalf("scan membership policy: %v", err)
		}
		seen++
		if permissions == nil || strings.Join(permissions, ",") != "admin.users,media.play" {
			t.Errorf("permissions = %v", permissions)
		}
		if len(libraryIDs) != 2 || libraryIDs[0] != 11 || libraryIDs[1] != 22 {
			t.Errorf("library_ids = %v", libraryIDs)
		}
		if quality == nil || *quality != "720p" || maxStreams == nil || *maxStreams != 3 || maxTranscodes == nil || *maxTranscodes != 1 {
			t.Errorf("quality/caps = %v/%v/%v", quality, maxStreams, maxTranscodes)
		}
		if transcodeAllowed == nil || *transcodeAllowed || audioTranscodeAllowed == nil || *audioTranscodeAllowed {
			t.Errorf("transcode overrides = %v/%v", transcodeAllowed, audioTranscodeAllowed)
		}
		if downloadAllowed == nil || *downloadAllowed || downloadTranscodeAllowed == nil || !*downloadTranscodeAllowed || requestsAllowed == nil || *requestsAllowed {
			t.Errorf("download/request overrides = %v/%v/%v", downloadAllowed, downloadTranscodeAllowed, requestsAllowed)
		}
		if maxProfiles != 4 || revision != 7 {
			t.Errorf("max_profiles/revision = %d/%d", maxProfiles, revision)
		}
		wantGroup := fixture.defaultGroup
		if organizationID == fixture.otherOrgID {
			wantGroup = fixture.otherGroup
		}
		if accessGroupID == nil || *accessGroupID != wantGroup {
			t.Errorf("organization %s group = %v, want %d", organizationID, accessGroupID, wantGroup)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate membership policy: %v", err)
	}
	if seen != 2 {
		t.Fatalf("membership rows = %d, want 2", seen)
	}
}

func TestMembershipPolicyMigrationAddsCompatibilityAuthorityAndFencesLegacyWrites(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()

	var phase string
	var finalized bool
	if err := fixture.pool.QueryRow(ctx, `
SELECT phase, finalized_at IS NOT NULL
FROM public.membership_policy_authority
WHERE singleton`).Scan(&phase, &finalized); err != nil {
		t.Fatalf("read authority: %v", err)
	}
	if phase != "compatibility" || finalized {
		t.Fatalf("authority = %q finalized=%t", phase, finalized)
	}

	_, err := fixture.pool.Exec(ctx, `
UPDATE public.users
SET username = 'must-rollback', max_streams = 9
WHERE id = $1`, fixture.accountID)
	assertMembershipPolicyError(t, err, "membership_policy_fenced")
	var username string
	var maxStreams int
	if err := fixture.pool.QueryRow(ctx, `SELECT username, max_streams FROM public.users WHERE id=$1`, fixture.accountID).Scan(&username, &maxStreams); err != nil {
		t.Fatalf("read fenced account: %v", err)
	}
	if username != "membership-policy-user" || maxStreams != 3 {
		t.Fatalf("fenced update residue: username=%q max_streams=%d", username, maxStreams)
	}

	if _, err := fixture.pool.Exec(ctx, `UPDATE public.users SET username='identity-only' WHERE id=$1`, fixture.accountID); err != nil {
		t.Fatalf("identity-only update: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE public.users SET max_streams=max_streams WHERE id=$1`, fixture.accountID); err != nil {
		t.Fatalf("exact policy reassignment: %v", err)
	}
}

func TestMembershipPolicyMigrationFencesLegacyUsersPolicyWritesAtomically(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	secondID := insertMembershipPolicyAccount(t, fixture.pool, "membership-policy-second", "user")
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()

	_, err := fixture.pool.Exec(ctx, `
UPDATE public.users
SET username = username || '-changed',
    max_streams = CASE WHEN id=$1 THEN 12 ELSE max_streams END
WHERE id IN ($1,$2)`, secondID, fixture.accountID)
	assertMembershipPolicyError(t, err, "membership_policy_fenced")
	var changed int
	if err := fixture.pool.QueryRow(ctx, `
SELECT count(*) FROM public.users
WHERE id IN ($1,$2) AND username LIKE '%-changed'`, secondID, fixture.accountID).Scan(&changed); err != nil {
		t.Fatalf("read multi-row fence residue: %v", err)
	}
	if changed != 0 {
		t.Fatalf("multi-row fenced update changed %d rows", changed)
	}

	if _, err := fixture.pool.Exec(ctx, `
UPDATE public.users
SET role='admin', access_group_id=access_group_id
WHERE id=$1`, fixture.accountID); err != nil {
		t.Fatalf("role update with exact group reassignment: %v", err)
	}
	_, err = fixture.pool.Exec(ctx, `
UPDATE public.users
SET role='user', access_group_id=$2
WHERE id=$1`, fixture.accountID, fixture.otherGroup)
	assertMembershipPolicyError(t, err, "membership_policy_fenced")
	var role string
	var groupID int64
	if err := fixture.pool.QueryRow(ctx, `SELECT role,access_group_id FROM public.users WHERE id=$1`, fixture.accountID).Scan(&role, &groupID); err != nil {
		t.Fatalf("read role/group fence residue: %v", err)
	}
	if role != "admin" || groupID != fixture.defaultGroup {
		t.Fatalf("role/group residue = %q/%d", role, groupID)
	}
}

func TestMembershipPolicyMigrationEnforcesCompositeProfileMembership(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()

	var foreignOrgID uuid.UUID
	if err := fixture.pool.QueryRow(ctx, `
INSERT INTO public.organizations (slug,name,status,owner_account_id)
VALUES ('membership-policy-foreign','Foreign','active',$1)
RETURNING id`, fixture.accountID).Scan(&foreignOrgID); err != nil {
		t.Fatalf("create foreign organization: %v", err)
	}
	var foreignGroupID int64
	if err := fixture.pool.QueryRow(ctx, `
INSERT INTO public.access_groups (organization_id,name,is_default)
VALUES ($1,'Foreign Default',true)
RETURNING id`, foreignOrgID).Scan(&foreignGroupID); err != nil {
		t.Fatalf("create foreign group: %v", err)
	}
	_, err := fixture.pool.Exec(ctx, `
INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name)
VALUES ('foreign-profile',$1,$2,$3,'Foreign')`, fixture.accountID, foreignOrgID, foreignGroupID)
	if err == nil {
		t.Fatal("profile without exact organization membership succeeded")
	}

	if _, err := fixture.pool.Exec(ctx, `
	INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name)
VALUES ('default-profile',$1,$2,$4,'Default'), ('other-profile',$1,$3,$5,'Other')`,
		fixture.accountID, fixture.defaultOrgID, fixture.otherOrgID, fixture.defaultGroup, fixture.otherGroup); err != nil {
		t.Fatalf("create exact-membership profiles: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
DELETE FROM public.organization_memberships
WHERE organization_id=$1 AND account_id=$2`, fixture.defaultOrgID, fixture.accountID); err != nil {
		t.Fatalf("delete one membership: %v", err)
	}
	var defaultExists, otherExists bool
	if err := fixture.pool.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM public.user_profiles WHERE id='default-profile'),
       EXISTS(SELECT 1 FROM public.user_profiles WHERE id='other-profile')`).Scan(&defaultExists, &otherExists); err != nil {
		t.Fatalf("read cascaded profiles: %v", err)
	}
	if defaultExists || !otherExists {
		t.Fatalf("profile cascade default=%t other=%t", defaultExists, otherExists)
	}
}

func TestMembershipPolicyMigrationRejectsActiveNonPlatformMemberWithoutOrganizationGroup(t *testing.T) {
	t.Run("ordinary account is rejected", func(t *testing.T) {
		pool := newMembershipPolicyLegacyPool(t)
		ctx := context.Background()
		accountID := insertMembershipPolicyAccount(t, pool, "ungrouped-user", "user")
		organizationID := insertMembershipPolicyOrganization(t, pool, "ungrouped-user-org", accountID, false)
		if _, err := pool.Exec(ctx, `
INSERT INTO public.organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, organizationID, accountID); err != nil {
			t.Fatalf("seed ungrouped membership: %v", err)
		}
		err := RunMigrations(ctx, pool, migrations.FS, "sql")
		if err == nil || !strings.Contains(err.Error(), "active non-platform membership without organization access group") {
			t.Fatalf("migration error = %v", err)
		}
		assertMembershipPolicyMigrationVersion(t, pool, false)
	})

	t.Run("platform admin may remain ungrouped", func(t *testing.T) {
		pool := newMembershipPolicyLegacyPool(t)
		ctx := context.Background()
		accountID := insertMembershipPolicyAccount(t, pool, "ungrouped-admin", "admin")
		organizationID := insertMembershipPolicyOrganization(t, pool, "ungrouped-admin-org", accountID, false)
		if _, err := pool.Exec(ctx, `
INSERT INTO public.organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','admin')`, organizationID, accountID); err != nil {
			t.Fatalf("seed platform membership: %v", err)
		}
		migrateMembershipPolicyUp(t, pool)
		var groupID *int64
		if err := pool.QueryRow(ctx, `
SELECT access_group_id FROM public.organization_memberships
WHERE organization_id=$1 AND account_id=$2`, organizationID, accountID).Scan(&groupID); err != nil {
			t.Fatalf("read platform membership: %v", err)
		}
		if groupID != nil {
			t.Fatalf("platform group = %v, want nil", groupID)
		}
	})
}

func TestMembershipPolicyMigrationScopesPrimaryProfileAndProfileCapPerOrganization(t *testing.T) {
	pool := newMembershipPolicyLegacyPool(t)
	ctx := context.Background()
	accountID := insertMembershipPolicyAccount(t, pool, "profile-scope", "user")
	if _, err := pool.Exec(ctx, `UPDATE public.users SET max_profiles=5 WHERE id=$1`, accountID); err != nil {
		t.Fatalf("set account profile limit: %v", err)
	}
	organizationA := insertMembershipPolicyOrganization(t, pool, "profile-scope-a", accountID, true)
	organizationB := insertMembershipPolicyOrganization(t, pool, "profile-scope-b", accountID, true)
	groupA := membershipPolicyDefaultGroup(t, pool, organizationA)
	groupB := membershipPolicyDefaultGroup(t, pool, organizationB)
	if _, err := pool.Exec(ctx, `UPDATE public.access_groups SET max_profiles=1 WHERE id=$1`, groupA); err != nil {
		t.Fatalf("set group A limit: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.access_groups SET max_profiles=3 WHERE id=$1`, groupB); err != nil {
		t.Fatalf("set group B limit: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO public.organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$3,'active','user'),($2,$3,'active','user')`, organizationA, organizationB, accountID); err != nil {
		t.Fatalf("seed profile memberships: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP INDEX public.user_profiles_primary_per_user`); err != nil {
		t.Fatalf("drop legacy primary index for ambiguous fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name,is_primary,created_at)
VALUES ('profile-a',$1,$2,$4,'A',true,'2026-01-01T00:00:00Z'),
       ('profile-b',$1,$3,$5,'B',true,'2026-01-01T00:00:00Z')`,
		accountID, organizationA, organizationB, groupA, groupB); err != nil {
		t.Fatalf("seed cross-organization primaries: %v", err)
	}
	// The migration expects to replace the legacy index; recreate it with the
	// same name but no predicate so the intentionally ambiguous fixture remains.
	if _, err := pool.Exec(ctx, `CREATE INDEX user_profiles_primary_per_user ON public.user_profiles(user_id)`); err != nil {
		t.Fatalf("recreate legacy index: %v", err)
	}

	migrateMembershipPolicyUp(t, pool)
	var primaryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.user_profiles WHERE user_id=$1 AND is_primary`, accountID).Scan(&primaryCount); err != nil {
		t.Fatalf("count primaries: %v", err)
	}
	if primaryCount != 2 {
		t.Fatalf("cross-organization primaries = %d, want 2", primaryCount)
	}
	_, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name)
VALUES ('profile-a-second',$1,$2,$3,'A2')`, accountID, organizationA, groupA)
	if err == nil || !strings.Contains(err.Error(), "profile entitlement limit reached") {
		t.Fatalf("organization A second profile error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name)
VALUES ('profile-b-second',$1,$2,$3,'B2')`, accountID, organizationB, groupB); err != nil {
		t.Fatalf("organization B second profile: %v", err)
	}
}

func TestMembershipPolicyMigrationAddsCompatibilityAuthorityAndRolloutObservations(t *testing.T) {
	pool := newMembershipPolicyLegacyPool(t)
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	for _, node := range []struct{ id, kind string }{
		{id: "integrated-a", kind: "integrated"},
		{id: "api-a", kind: "api"},
		{id: "proxy-a", kind: "proxy"},
		{id: "transcode-a", kind: "transcode"},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO public.node_heartbeats (node_id,node_type,node_url,updated_at)
VALUES ($1,$2,'http://node.test',$3)`, node.id, node.kind, observedAt); err != nil {
			t.Fatalf("seed heartbeat %s: %v", node.id, err)
		}
	}
	migrateMembershipPolicyUp(t, pool)

	var observations int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.membership_policy_rollout_observations`).Scan(&observations); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	if observations != 2 {
		t.Fatalf("observations = %d, want 2", observations)
	}
	for _, nodeID := range []string{"integrated-a", "api-a"} {
		var state string
		var instanceID *uuid.UUID
		var observationID *int64
		var capabilities []string
		var gotObservedAt, lastSeenAt time.Time
		if err := pool.QueryRow(ctx, `
SELECT observations.state, observations.instance_id, observations.observation_id,
       observations.observed_at, observations.last_seen_at, heartbeats.schema_capabilities
FROM public.node_heartbeats AS heartbeats
JOIN public.membership_policy_rollout_observations AS observations
  ON observations.observation_id=heartbeats.membership_policy_rollout_observation_id
WHERE heartbeats.node_id=$1`, nodeID).Scan(
			&state, &instanceID, &observationID, &gotObservedAt, &lastSeenAt, &capabilities,
		); err != nil {
			t.Fatalf("read observation %s: %v", nodeID, err)
		}
		if state != "legacy" || instanceID != nil || observationID == nil || !gotObservedAt.Equal(observedAt) || !lastSeenAt.Equal(observedAt) || len(capabilities) != 0 {
			t.Fatalf("observation %s = state=%s instance=%v id=%v observed=%s seen=%s caps=%v", nodeID, state, instanceID, observationID, gotObservedAt, lastSeenAt, capabilities)
		}
	}
	for _, nodeID := range []string{"proxy-a", "transcode-a"} {
		var observationID *int64
		if err := pool.QueryRow(ctx, `SELECT membership_policy_rollout_observation_id FROM public.node_heartbeats WHERE node_id=$1`, nodeID).Scan(&observationID); err != nil {
			t.Fatalf("read nonparticipant %s: %v", nodeID, err)
		}
		if observationID != nil {
			t.Fatalf("nonparticipant %s observation = %v", nodeID, observationID)
		}
	}
}

func TestMembershipPolicyMigrationSeedsOnlyCompatibleLegacyMembershipInsert(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()
	organizationID := insertMembershipPolicyOrganization(t, fixture.pool, "compat-seed", fixture.accountID, true)
	wantGroup := membershipPolicyDefaultGroup(t, fixture.pool, organizationID)
	if _, err := fixture.pool.Exec(ctx, `
INSERT INTO public.organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$2,'active','user')`, organizationID, fixture.accountID); err != nil {
		t.Fatalf("legacy membership insert: %v", err)
	}
	var groupID int64
	var revision int64
	if err := fixture.pool.QueryRow(ctx, `
SELECT access_group_id,access_policy_revision
FROM public.organization_memberships WHERE organization_id=$1 AND account_id=$2`,
		organizationID, fixture.accountID).Scan(&groupID, &revision); err != nil {
		t.Fatalf("read seeded membership: %v", err)
	}
	if groupID != wantGroup || revision != 7 {
		t.Fatalf("seeded policy group/revision = %d/%d, want %d/7", groupID, revision, wantGroup)
	}

	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin marked insert: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer','v1',true)`); err != nil {
		t.Fatalf("mark writer: %v", err)
	}
	_, err = tx.Exec(ctx, `
UPDATE public.organization_memberships SET max_streams=8
WHERE organization_id=$1 AND account_id=$2`, organizationID, fixture.accountID)
	assertMembershipPolicyError(t, err, "membership_policy_not_finalized")
	_ = tx.Rollback(ctx)

	markedOrganizationID := insertMembershipPolicyOrganization(t, fixture.pool, "compat-marked-insert", fixture.accountID, true)
	markedTx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin marked membership insert: %v", err)
	}
	defer func() { _ = markedTx.Rollback(ctx) }()
	if _, err := markedTx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer','v1',true)`); err != nil {
		t.Fatalf("mark membership insert: %v", err)
	}
	_, err = markedTx.Exec(ctx, `
INSERT INTO public.organization_memberships (
    organization_id,account_id,status,legacy_role,max_profiles,access_policy_revision)
VALUES ($1,$2,'active','user',4,7)`, markedOrganizationID, fixture.accountID)
	assertMembershipPolicyError(t, err, "membership_policy_not_finalized")
}

func TestMembershipPolicyMigrationPreservesLegacyObservationAcrossCapableNodeIDReuse(t *testing.T) {
	pool := newMembershipPolicyLegacyPool(t)
	ctx := context.Background()
	initial := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
INSERT INTO public.node_heartbeats (node_id,node_type,node_url,updated_at)
VALUES ('shared-node','integrated','http://old.test',$1)`, initial); err != nil {
		t.Fatalf("seed legacy heartbeat: %v", err)
	}
	migrateMembershipPolicyUp(t, pool)
	var originalObservation int64
	if err := pool.QueryRow(ctx, `
SELECT membership_policy_rollout_observation_id FROM public.node_heartbeats WHERE node_id='shared-node'`).Scan(&originalObservation); err != nil {
		t.Fatalf("read original observation: %v", err)
	}
	refreshed := initial.Add(time.Minute)
	if _, err := pool.Exec(ctx, `
INSERT INTO public.node_heartbeats (node_id,node_type,node_url,updated_at)
VALUES ('shared-node','integrated','http://old.test',$1)
ON CONFLICT (node_id) DO UPDATE SET node_type=EXCLUDED.node_type,node_url=EXCLUDED.node_url,updated_at=EXCLUDED.updated_at`, refreshed); err != nil {
		t.Fatalf("refresh legacy heartbeat: %v", err)
	}
	var refreshedObservation int64
	var lastSeen time.Time
	if err := pool.QueryRow(ctx, `
SELECT heartbeats.membership_policy_rollout_observation_id,observations.last_seen_at
FROM public.node_heartbeats AS heartbeats
JOIN public.membership_policy_rollout_observations AS observations
  ON observations.observation_id=heartbeats.membership_policy_rollout_observation_id
WHERE heartbeats.node_id='shared-node'`).Scan(&refreshedObservation, &lastSeen); err != nil {
		t.Fatalf("read refreshed observation: %v", err)
	}
	if refreshedObservation != originalObservation || !lastSeen.Equal(refreshed) {
		t.Fatalf("legacy refresh observation/seen = %d/%s, want %d/%s", refreshedObservation, lastSeen, originalObservation, refreshed)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.node_heartbeats WHERE node_id='shared-node'`); err != nil {
		t.Fatalf("delete legacy heartbeat: %v", err)
	}

	capableInstance := uuid.New()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin capable beat: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.schema_capability_writer','v1',true)`); err != nil {
		t.Fatalf("mark capable beat: %v", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO public.node_heartbeats (
    node_id,node_type,node_url,updated_at,schema_capabilities,instance_id)
VALUES ('shared-node','integrated','http://new.test',$1,ARRAY['membership_policy_v1'],$2)`,
		refreshed.Add(time.Minute), capableInstance); err != nil {
		t.Fatalf("insert capable heartbeat: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit capable heartbeat: %v", err)
	}
	var capableObservation int64
	if err := pool.QueryRow(ctx, `
SELECT membership_policy_rollout_observation_id FROM public.node_heartbeats WHERE node_id='shared-node'`).Scan(&capableObservation); err != nil {
		t.Fatalf("read capable observation: %v", err)
	}
	if capableObservation == originalObservation {
		t.Fatal("capable replacement reused legacy observation")
	}

	legacyReturn := refreshed.Add(2 * time.Minute)
	if _, err := pool.Exec(ctx, `
INSERT INTO public.node_heartbeats (node_id,node_type,node_url,updated_at)
VALUES ('shared-node','integrated','http://stale.test',$1)
ON CONFLICT (node_id) DO UPDATE SET node_type=EXCLUDED.node_type,node_url=EXCLUDED.node_url,updated_at=EXCLUDED.updated_at`, legacyReturn); err != nil {
		t.Fatalf("replay legacy heartbeat: %v", err)
	}
	var currentInstance *uuid.UUID
	var currentCapabilities []string
	var currentObservation int64
	if err := pool.QueryRow(ctx, `
SELECT instance_id,schema_capabilities,membership_policy_rollout_observation_id
FROM public.node_heartbeats WHERE node_id='shared-node'`).Scan(&currentInstance, &currentCapabilities, &currentObservation); err != nil {
		t.Fatalf("read replayed heartbeat: %v", err)
	}
	if currentInstance != nil || len(currentCapabilities) != 0 || currentObservation == capableObservation || currentObservation == originalObservation {
		t.Fatalf("replayed heartbeat instance=%v caps=%v observation=%d", currentInstance, currentCapabilities, currentObservation)
	}
	var legacyCount, capableCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE state='legacy'), count(*) FILTER (WHERE state='capable')
FROM public.membership_policy_rollout_observations WHERE node_id='shared-node'`).Scan(&legacyCount, &capableCount); err != nil {
		t.Fatalf("count preserved observations: %v", err)
	}
	if legacyCount != 2 || capableCount != 1 {
		t.Fatalf("preserved observation counts legacy=%d capable=%d", legacyCount, capableCount)
	}
}

func TestMembershipPolicyMigrationDownRestoresRepresentableLegacyState(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	if err := MigrateDownTo(context.Background(), fixture.pool, migrations.FS, "sql", predecessor); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	var username string
	var maxStreams int
	var revision int64
	if err := fixture.pool.QueryRow(context.Background(), `
SELECT username,max_streams,access_policy_revision FROM public.users WHERE id=$1`, fixture.accountID).Scan(&username, &maxStreams, &revision); err != nil {
		t.Fatalf("read restored account: %v", err)
	}
	if username != "membership-policy-user" || maxStreams != 3 || revision != 7 {
		t.Fatalf("restored account = %q/%d/%d", username, maxStreams, revision)
	}
	assertMembershipPolicyMigrationVersion(t, fixture.pool, false)
}

func TestMembershipPolicyMigrationDownRestoresRepresentableFinalizedState(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()
	finalizeMembershipPolicySchemaForDownTest(t, fixture.pool)

	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	if err := MigrateDownTo(ctx, fixture.pool, migrations.FS, "sql", predecessor); err != nil {
		t.Fatalf("migrate finalized state down: %v", err)
	}
	var maxStreams int
	if err := fixture.pool.QueryRow(ctx, `SELECT max_streams FROM public.users WHERE id=$1`, fixture.accountID).Scan(&maxStreams); err != nil {
		t.Fatalf("read restored finalized account: %v", err)
	}
	if maxStreams != 3 {
		t.Fatalf("restored finalized max_streams = %d, want 3", maxStreams)
	}
	assertMembershipPolicyMigrationVersion(t, fixture.pool, false)
}

func TestMembershipPolicyMigrationDownCollapsesRepresentablePostMigrationState(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `ALTER TABLE public.organization_memberships DISABLE TRIGGER organization_memberships_policy_writer_guard`); err != nil {
		t.Fatalf("disable membership policy guard: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
DELETE FROM public.organization_memberships
WHERE organization_id=$1 AND account_id=$2`, fixture.otherOrgID, fixture.accountID); err != nil {
		t.Fatalf("remove non-collapsible membership: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
UPDATE public.organization_memberships
SET max_streams=44,access_policy_revision=8
WHERE organization_id=$1 AND account_id=$2`, fixture.defaultOrgID, fixture.accountID); err != nil {
		t.Fatalf("seed collapsible policy: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `ALTER TABLE public.organization_memberships ENABLE TRIGGER organization_memberships_policy_writer_guard`); err != nil {
		t.Fatalf("enable membership policy guard: %v", err)
	}
	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	if err := MigrateDownTo(ctx, fixture.pool, migrations.FS, "sql", predecessor); err != nil {
		t.Fatalf("collapse representable state: %v", err)
	}
	var maxStreams int
	var revision int64
	if err := fixture.pool.QueryRow(ctx, `
SELECT max_streams,access_policy_revision FROM public.users WHERE id=$1`, fixture.accountID).Scan(&maxStreams, &revision); err != nil {
		t.Fatalf("read collapsed account: %v", err)
	}
	if maxStreams != 44 || revision != 8 {
		t.Fatalf("collapsed account max_streams/revision = %d/%d, want 44/8", maxStreams, revision)
	}
}

func TestMembershipPolicyMigrationDownRejectsDivergentMembershipPolicyWithoutPartialRollback(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `ALTER TABLE public.organization_memberships DISABLE TRIGGER organization_memberships_policy_writer_guard`); err != nil {
		t.Fatalf("disable membership policy guard: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
UPDATE public.organization_memberships
SET max_streams=99
WHERE organization_id=$1 AND account_id=$2`, fixture.defaultOrgID, fixture.accountID); err != nil {
		t.Fatalf("seed divergent membership: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `ALTER TABLE public.organization_memberships ENABLE TRIGGER organization_memberships_policy_writer_guard`); err != nil {
		t.Fatalf("enable membership policy guard: %v", err)
	}
	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	err := MigrateDownTo(ctx, fixture.pool, migrations.FS, "sql", predecessor)
	if err == nil || !strings.Contains(err.Error(), "cannot represent changed membership state") {
		t.Fatalf("divergent Down error = %v", err)
	}
	assertMembershipPolicyMigrationVersion(t, fixture.pool, true)
	var maxStreams int
	if err := fixture.pool.QueryRow(ctx, `
SELECT max_streams FROM public.organization_memberships
WHERE organization_id=$1 AND account_id=$2`, fixture.defaultOrgID, fixture.accountID).Scan(&maxStreams); err != nil {
		t.Fatalf("read divergent membership after failed Down: %v", err)
	}
	if maxStreams != 99 {
		t.Fatalf("failed Down changed divergent membership to %d", maxStreams)
	}
	var authorityExists bool
	if err := fixture.pool.QueryRow(ctx, `SELECT to_regclass('public.membership_policy_authority') IS NOT NULL`).Scan(&authorityExists); err != nil {
		t.Fatalf("read authority after failed Down: %v", err)
	}
	if !authorityExists {
		t.Fatal("failed Down removed authority table")
	}
}

func TestMembershipPolicyMigrationDownRejectsDivergentPrimaryStateWithoutPartialRollback(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `
INSERT INTO public.user_profiles (id,user_id,organization_id,access_group_id,name,is_primary)
VALUES ('primary-down',$1,$2,$3,'Primary Down',false)`,
		fixture.accountID, fixture.defaultOrgID, fixture.defaultGroup); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	migrateMembershipPolicyUp(t, fixture.pool)
	if _, err := fixture.pool.Exec(ctx, `
UPDATE public.user_profiles SET is_primary=true
WHERE user_id=$1 AND id='primary-down'`, fixture.accountID); err != nil {
		t.Fatalf("change primary state: %v", err)
	}
	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	err := MigrateDownTo(ctx, fixture.pool, migrations.FS, "sql", predecessor)
	if err == nil || !strings.Contains(err.Error(), "cannot represent changed profile primary state") {
		t.Fatalf("divergent primary Down error = %v", err)
	}
	assertMembershipPolicyMigrationVersion(t, fixture.pool, true)
	var primary bool
	if err := fixture.pool.QueryRow(ctx, `
SELECT is_primary FROM public.user_profiles WHERE user_id=$1 AND id='primary-down'`, fixture.accountID).Scan(&primary); err != nil {
		t.Fatalf("read primary after failed Down: %v", err)
	}
	if !primary {
		t.Fatal("failed Down changed divergent primary state")
	}
}

func TestMembershipPolicyMigrationDownRejectsHalfRenamedProjectionWithoutPartialRollback(t *testing.T) {
	fixture := newMembershipPolicyFixture(t)
	migrateMembershipPolicyUp(t, fixture.pool)
	ctx := context.Background()
	if _, err := fixture.pool.Exec(ctx, `
DROP TRIGGER users_membership_policy_authority_fence ON public.users;
ALTER TABLE public.users RENAME COLUMN max_streams TO rollback_membership_max_streams`); err != nil {
		t.Fatalf("seed half-renamed projection: %v", err)
	}
	_, predecessor, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	err := MigrateDownTo(ctx, fixture.pool, migrations.FS, "sql", predecessor)
	if err == nil || !strings.Contains(err.Error(), "half-renamed compatibility projection") {
		t.Fatalf("half-renamed Down error = %v", err)
	}
	assertMembershipPolicyMigrationVersion(t, fixture.pool, true)
	var rollbackColumnExists bool
	if err := fixture.pool.QueryRow(ctx, `
SELECT EXISTS(
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public' AND table_name='users'
      AND column_name='rollback_membership_max_streams'
)`).Scan(&rollbackColumnExists); err != nil {
		t.Fatalf("read half-renamed schema after failed Down: %v", err)
	}
	if !rollbackColumnExists {
		t.Fatal("failed Down changed pre-existing half-renamed schema")
	}
}

func newMembershipPolicyFixture(t *testing.T) membershipPolicyFixture {
	t.Helper()
	ctx := context.Background()
	pool := newMembershipPolicyLegacyPool(t)

	var fixture membershipPolicyFixture
	fixture.pool = pool
	if err := pool.QueryRow(ctx, `
INSERT INTO public.users (
    username,email,password_hash,role,permissions,library_ids,
    max_playback_quality,max_streams,max_transcodes,transcode_allowed,
    audio_transcode_allowed,download_allowed,download_transcode_allowed,
    requests_allowed,max_profiles,access_policy_revision)
VALUES (
    'membership-policy-user','membership-policy-user@example.test','x','user',
    ARRAY['admin.users','media.play'],ARRAY[11,22],
    '720p',3,1,false,false,false,true,false,4,7)
	RETURNING id`).Scan(&fixture.accountID); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM public.organizations WHERE is_default`).Scan(&fixture.defaultOrgID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM public.access_groups WHERE organization_id=$1 AND is_default`, fixture.defaultOrgID).Scan(&fixture.defaultGroup); err != nil {
		t.Fatalf("load default group: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.users SET access_group_id=$1 WHERE id=$2`, fixture.defaultGroup, fixture.accountID); err != nil {
		t.Fatalf("assign account group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO public.organizations (slug,name,status,owner_account_id)
VALUES ('membership-policy-other','Other','active',$1)
RETURNING id`, fixture.accountID).Scan(&fixture.otherOrgID); err != nil {
		t.Fatalf("create other organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO public.access_groups (organization_id,name,is_default)
VALUES ($1,'Other Default',true)
RETURNING id`, fixture.otherOrgID).Scan(&fixture.otherGroup); err != nil {
		t.Fatalf("create other default group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO public.organization_memberships (organization_id,account_id,status,legacy_role)
VALUES ($1,$3,'active','user'),($2,$3,'active','user')`,
		fixture.defaultOrgID, fixture.otherOrgID, fixture.accountID); err != nil {
		t.Fatalf("create memberships: %v", err)
	}
	return fixture
}

func newMembershipPolicyLegacyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool := newDisposableMigrationDatabase(t)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest: %v", err)
	}
	if _, predecessor, found := membershipPolicyMigrationVersions(t); found {
		if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", predecessor); err != nil {
			t.Fatalf("migrate to membership-policy predecessor: %v", err)
		}
	}
	return pool
}

func insertMembershipPolicyAccount(t *testing.T, pool *pgxpool.Pool, name, role string) int {
	t.Helper()
	var accountID int
	if err := pool.QueryRow(context.Background(), `
INSERT INTO public.users (username,email,password_hash,role)
VALUES ($1,$2,'x',$3)
RETURNING id`, name, name+"@example.test", role).Scan(&accountID); err != nil {
		t.Fatalf("create account %s: %v", name, err)
	}
	return accountID
}

func insertMembershipPolicyOrganization(t *testing.T, pool *pgxpool.Pool, slug string, ownerID int, withDefaultGroup bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var organizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
INSERT INTO public.organizations (slug,name,status,owner_account_id)
VALUES ($1,$1,'active',$2)
RETURNING id`, slug, ownerID).Scan(&organizationID); err != nil {
		t.Fatalf("create organization %s: %v", slug, err)
	}
	if withDefaultGroup {
		if _, err := pool.Exec(ctx, `
INSERT INTO public.access_groups (organization_id,name,is_default)
VALUES ($1,$2,true)`, organizationID, slug+" default"); err != nil {
			t.Fatalf("create default group for %s: %v", slug, err)
		}
	}
	return organizationID
}

func membershipPolicyDefaultGroup(t *testing.T, pool *pgxpool.Pool, organizationID uuid.UUID) int64 {
	t.Helper()
	var groupID int64
	if err := pool.QueryRow(context.Background(), `
SELECT id FROM public.access_groups WHERE organization_id=$1 AND is_default`, organizationID).Scan(&groupID); err != nil {
		t.Fatalf("load default group for %s: %v", organizationID, err)
	}
	return groupID
}

func assertMembershipPolicyMigrationVersion(t *testing.T, pool *pgxpool.Pool, wantApplied bool) {
	t.Helper()
	target, _, found := membershipPolicyMigrationVersions(t)
	if !found {
		t.Fatal("membership policy migration missing")
	}
	var applied bool
	if err := pool.QueryRow(context.Background(), `
SELECT EXISTS(SELECT 1 FROM public.goose_db_version WHERE version_id=$1 AND is_applied)`, target).Scan(&applied); err != nil {
		t.Fatalf("read membership policy migration version: %v", err)
	}
	if applied != wantApplied {
		t.Fatalf("membership policy migration applied=%t, want %t", applied, wantApplied)
	}
}

func finalizeMembershipPolicySchemaForDownTest(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin finalized-state simulation: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_finalizer','v1',true)`); err != nil {
		t.Fatalf("mark finalized-state simulation: %v", err)
	}
	if _, err := tx.Exec(ctx, `DROP TRIGGER users_membership_policy_authority_fence ON public.users`); err != nil {
		t.Fatalf("drop compatibility users fence: %v", err)
	}
	for _, column := range []string{
		"access_group_id", "permissions", "library_ids", "max_playback_quality",
		"max_streams", "max_transcodes", "transcode_allowed",
		"audio_transcode_allowed", "download_allowed",
		"download_transcode_allowed", "requests_allowed", "max_profiles",
		"access_policy_revision",
	} {
		statement := `ALTER TABLE public.users RENAME COLUMN ` +
			pgxIdentifier(column) + ` TO ` + pgxIdentifier("rollback_membership_"+column)
		if _, err := tx.Exec(ctx, statement); err != nil {
			t.Fatalf("rename finalized policy column %s: %v", column, err)
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE public.membership_policy_authority
SET phase='finalized',finalized_at=now()
WHERE singleton`); err != nil {
		t.Fatalf("finalize authority: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit finalized-state simulation: %v", err)
	}
}

func pgxIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func migrateMembershipPolicyUp(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate membership policy: %v", err)
	}
}

func membershipPolicyMigrationVersions(t *testing.T) (target, predecessor int64, found bool) {
	t.Helper()
	files, err := fs.Glob(migrations.FS, "sql/*.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	for _, file := range files {
		versionText, _, ok := strings.Cut(path.Base(file), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			continue
		}
		if strings.HasSuffix(file, membershipPolicyMigrationSuffix) {
			target, found = version, true
		}
	}
	if !found {
		return 0, 0, false
	}
	for _, file := range files {
		versionText, _, ok := strings.Cut(path.Base(file), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err == nil && version < target && version > predecessor {
			predecessor = version
		}
	}
	return target, predecessor, true
}

func assertMembershipPolicyError(t *testing.T, err error, message string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected PostgreSQL error containing %q", message)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %T %v, want PostgreSQL error", err, err)
	}
	if pgErr.Code != "P0001" || !strings.Contains(pgErr.Message, message) {
		t.Fatalf("PostgreSQL error = %s %q, want P0001 containing %q", pgErr.Code, pgErr.Message, message)
	}
}
