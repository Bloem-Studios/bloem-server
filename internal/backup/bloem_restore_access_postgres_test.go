package backup

import (
	"strconv"
	"strings"
	"testing"
)

// TestBloemRestoreNeverGivesMoreAccessDB: a restore leaves out an account
// whose group here may play where its group in the backup could not, and a
// new profile whose group here gives it more than its group in the backup;
// the same groups unchanged restore both.
func TestBloemRestoreNeverGivesMoreAccessDB(t *testing.T) {
	bloemUserDataBackups(t)
	f := newSelectiveFixture(t)
	ctx := t.Context()
	pool := f.pool
	suffix := randomHex(t, 4)
	scanInt := func(sql string, args ...any) (out int) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return out
	}
	owner := scanInt(`INSERT INTO users (role, username) VALUES ('admin', $1) RETURNING id`, "owner-"+suffix)
	var org string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ($1, $1, 'active', $2) RETURNING id::text`,
		"tenant-"+suffix, owner).Scan(&org); err != nil {
		t.Fatal(err)
	}
	group := func(name string, isDefault, playback bool) int {
		return scanInt(`INSERT INTO access_groups (organization_id, name, is_default, playback_allowed) VALUES ($1, $2, $3, $4) RETURNING id`,
			org, name, isDefault, playback)
	}
	open := group("Open "+suffix, true, true)
	locked := group("Locked "+suffix, false, false)
	kids := group("Kids "+suffix, false, false)
	eve := bloemTenancyAccount(t, pool, "eve-"+suffix, org, &locked, []int{locked})
	fay := bloemTenancyAccount(t, pool, "fay-"+suffix, org, &open, []int{open, kids})

	source, err := backupUnderLock(t, f.svc, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	// After the backup Eve's account is gone and Fay's kids profile; both
	// groups may now play.
	dbExec(t, pool, `DELETE FROM users WHERE id = $1`, eve)
	dbExec(t, pool, `DELETE FROM user_profiles WHERE user_id = $1 AND id = $2`, fay, "fay-"+suffix+"-kids")
	preview, err := f.svc.Preview(ctx, source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	checkBloemSelectable(t, preview, eve, fay, suffix, true, true)
	dbExec(t, pool, `UPDATE access_groups SET playback_allowed = true WHERE id = ANY($1)`, []int{locked, kids})
	if preview, err = f.svc.Preview(ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	checkBloemSelectable(t, preview, eve, fay, suffix, false, false)
}

// checkBloemSelectable checks whether Eve's account and Fay's kids profile
// can be restored, and that each one left out says it would get playback.
// Fay's account is here, so it is selectable only while that profile is.
func checkBloemSelectable(t *testing.T, preview *RestorePreview, eve, fay int, suffix string, eveOK, kidsOK bool) {
	t.Helper()
	e := previewItem(t, preview, SectionUsers, strconv.Itoa(eve))
	if e.Selectable != eveOK {
		t.Errorf("Eve selectable = %v (%s: %s), want %v", e.Selectable, e.Reason, e.Message, eveOK)
	}
	if !eveOK && (e.Reason != ReasonPolicyWider || !strings.Contains(e.Message, "playback_allowed")) {
		t.Errorf("Eve left out with %s: %q, want %s naming playback_allowed", e.Reason, e.Message, ReasonPolicyWider)
	}
	f := previewItem(t, preview, SectionUsers, strconv.Itoa(fay))
	// Fay's account is here; it is selectable only while a profile is.
	if f.Account == nil || f.Selectable != kidsOK {
		t.Fatalf("Fay = %+v, want her account, selectable %v", f, kidsOK)
	}
	var kids *PreviewProfile
	for i := range f.Account.Profiles {
		if f.Account.Profiles[i].ID == "fay-"+suffix+"-kids" {
			kids = &f.Account.Profiles[i]
		}
	}
	if kids == nil {
		t.Fatal("Fay's kids profile is not in the preview")
	}
	if kids.Selectable != kidsOK {
		t.Errorf("Fay's kids profile selectable = %v (%s: %s), want %v", kids.Selectable, kids.Reason, kids.Message, kidsOK)
	}
	if !kidsOK && (kids.Reason != ReasonPolicyWider || !strings.Contains(kids.Message, "playback_allowed")) {
		t.Errorf("Fay's kids profile left out with %s: %q, want %s naming playback_allowed", kids.Reason, kids.Message, ReasonPolicyWider)
	}
}
