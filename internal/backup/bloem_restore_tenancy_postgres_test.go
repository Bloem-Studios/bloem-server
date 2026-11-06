package backup

import (
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestBloemRestoredAccountsJoinTheirOrganizationDB: a selective restore puts
// each new account back in the organization it was in at the backup, or in
// the default organization when that one is gone, with a membership there;
// its access group and its profiles' groups are matched by name within that
// organization, never another organization's namesake. The fixture triggers
// that place bare test writes in the default organization are off while
// the restore runs, so the restore has to do it itself, as in production.
func TestBloemRestoredAccountsJoinTheirOrganizationDB(t *testing.T) {
	f := newSelectiveFixture(t)
	ctx := t.Context()
	s := seedBloemTenancy(t, f.pool)

	source, err := backupUnderLock(t, f.svc, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	dbExec(t, f.pool, `DELETE FROM users WHERE id = ANY($1)`, []int{s.ann, s.ben, s.cat})
	dbExec(t, f.pool, `DELETE FROM resource_owners WHERE organization_id = $1`, s.gone)
	dbExec(t, f.pool, `DELETE FROM organizations WHERE id = $1`, s.gone)
	dbExec(t, f.pool, `ALTER TABLE users DISABLE TRIGGER a_bloem_fixture_account_membership`)
	dbExec(t, f.pool, `ALTER TABLE user_profiles DISABLE TRIGGER a_bloem_fixture_profile_org`)

	preview, err := f.svc.Preview(ctx, source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.Itoa
	profile := func(account, name string) ProfileSelection {
		return ProfileSelection{Profile: account + "-" + s.suffix + "-" + name, Target: TargetNew}
	}
	report := f.restoreUsers(t, source.ID, preview.Digest, []UserSelection{
		{Account: id(s.ann), Target: TargetNew, Profiles: []ProfileSelection{profile("ann", "main"), profile("ann", "kids")}},
		{Account: id(s.ben), Target: TargetNew, Profiles: []ProfileSelection{profile("ben", "main")}},
		{Account: id(s.cat), Target: TargetNew, Profiles: []ProfileSelection{profile("cat", "main")}},
	})
	restored := map[int]int{}
	for _, source := range []int{s.ann, s.ben, s.cat} {
		item := reportItem(t, report, SectionUsers, id(source))
		if item.Outcome != OutcomeRestored || item.Account == nil || !item.Account.Created {
			t.Fatalf("account %d restored = %+v", source, item)
		}
		restored[source] = item.Account.LocalID
	}
	checkBloemPlacement(t, f.pool, s, restored)
}

// bloemTenancySeed is the accounts TestBloemRestoredAccountsJoinTheirOrganizationDB
// backs up: Ann in a tenant organization whose group shares a name with one
// of the default organization's, Ben in the default organization, and Cat in
// an organization deleted after the backup.
type bloemTenancySeed struct {
	suffix                                  string
	defaultOrg, tenant, gone                string
	defaultFamily, tenantFamily, tenantKids int
	defaultGroup                            int
	ann, ben, cat                           int
}

func seedBloemTenancy(t *testing.T, pool *pgxpool.Pool) bloemTenancySeed {
	t.Helper()
	ctx := t.Context()
	s := bloemTenancySeed{suffix: randomHex(t, 4)}
	scan := func(out any, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(out); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	scan(&s.defaultOrg, `SELECT id::text FROM organizations WHERE is_default`)
	var owner int
	scan(&owner, `INSERT INTO users (role, username) VALUES ('admin', $1) RETURNING id`, "owner-"+s.suffix)
	org := func(slug string) (id string) {
		scan(&id, `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ($1, $1, 'active', $2) RETURNING id::text`, slug, owner)
		return id
	}
	group := func(organization, name string, isDefault bool) (id int) {
		scan(&id, `INSERT INTO access_groups (organization_id, name, is_default) VALUES ($1, $2, $3) RETURNING id`, organization, name, isDefault)
		return id
	}
	s.tenant, s.gone = org("tenant-"+s.suffix), org("gone-"+s.suffix)
	family := "Family " + s.suffix
	s.defaultFamily = group(s.defaultOrg, family, false)
	s.tenantFamily = group(s.tenant, family, true)
	s.tenantKids = group(s.tenant, "Kids "+s.suffix, false)
	goneDefault := group(s.gone, "Gone default "+s.suffix, true)
	scan(&s.defaultGroup, `SELECT coalesce(max(id), 0) FROM access_groups WHERE organization_id = $1 AND is_default`, s.defaultOrg)
	if s.defaultGroup == 0 {
		s.defaultGroup = group(s.defaultOrg, "Default "+s.suffix, true)
	}
	s.ann = bloemTenancyAccount(t, pool, "ann-"+s.suffix, s.tenant, &s.tenantFamily, []int{s.tenantFamily, s.tenantKids})
	s.ben = bloemTenancyAccount(t, pool, "ben-"+s.suffix, s.defaultOrg, nil, []int{s.defaultGroup})
	s.cat = bloemTenancyAccount(t, pool, "cat-"+s.suffix, s.gone, nil, []int{goneDefault})
	return s
}

// bloemTenancyAccount writes an account, its membership and its profiles
// ("main", then "kids", one per group) in one transaction, as Bloem's
// account creation writes them.
func bloemTenancyAccount(t *testing.T, pool *pgxpool.Pool, name, organization string, groupID *int, profileGroups []int) int {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int
	if err := tx.QueryRow(ctx, `INSERT INTO users (role, username, access_group_id) VALUES ('user', $1, $2) RETURNING id`, name, groupID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role) VALUES ($1, $2, 'active', 'user')`,
		organization, id); err != nil {
		t.Fatal(err)
	}
	for i, g := range profileGroups {
		profile := []string{"main", "kids"}[i]
		if _, err := tx.Exec(ctx, `INSERT INTO user_profiles (id, user_id, name, is_primary, organization_id, access_group_id) VALUES ($1, $2, $3, $4, $5, $6)`,
			name+"-"+profile, id, profile, i == 0, organization, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// checkBloemPlacement checks each restored account's membership and group
// and each restored profile's organization and group.
func checkBloemPlacement(t *testing.T, pool *pgxpool.Pool, s bloemTenancySeed, restored map[int]int) {
	t.Helper()
	ctx := t.Context()
	for source, want := range map[int]string{s.ann: s.tenant, s.ben: s.defaultOrg, s.cat: s.defaultOrg} {
		var got string
		if err := pool.QueryRow(ctx, `SELECT organization_id::text FROM organization_memberships WHERE account_id = $1`, restored[source]).Scan(&got); err != nil {
			t.Fatalf("membership of account %d: %v", source, err)
		}
		if got != want {
			t.Errorf("account %d joined organization %s, want %s", source, got, want)
		}
	}
	var annGroup int
	if err := pool.QueryRow(ctx, `SELECT coalesce(access_group_id, 0) FROM users WHERE id = $1`, restored[s.ann]).Scan(&annGroup); err != nil {
		t.Fatal(err)
	}
	if annGroup != s.tenantFamily {
		t.Errorf("Ann's account group = %d, want the tenant's (%d), not the default organization's namesake (%d)", annGroup, s.tenantFamily, s.defaultFamily)
	}
	type placement struct {
		org   string
		group int
	}
	for _, c := range []struct {
		account int
		profile string
		want    placement
	}{
		{s.ann, "main", placement{s.tenant, s.tenantFamily}}, {s.ann, "kids", placement{s.tenant, s.tenantKids}},
		{s.ben, "main", placement{s.defaultOrg, s.defaultGroup}}, {s.cat, "main", placement{s.defaultOrg, s.defaultGroup}},
	} {
		var got placement
		if err := pool.QueryRow(ctx, `SELECT organization_id::text, access_group_id FROM user_profiles WHERE user_id = $1 AND name = $2`,
			restored[c.account], c.profile).Scan(&got.org, &got.group); err != nil {
			t.Fatalf("profile %s of account %d: %v", c.profile, c.account, err)
		}
		if got != c.want {
			t.Errorf("profile %s of account %d restored in %+v, want %+v", c.profile, c.account, got, c.want)
		}
	}
}
