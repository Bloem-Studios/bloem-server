package access

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func TestTenantGroupPolicyRetainsTenantFieldsAndClonesSlices(t *testing.T) {
	group := TenantGroup{Group: Group{ID: 1, LibraryIDs: []int{4}, AllowedPermissions: []string{"request"}}, OrganizationID: uuid.New(), PlaybackAllowed: false, MaxProfiles: 3}
	policy := group.Policy()
	if policy.PlaybackAllowed || policy.MaxProfiles != 3 {
		t.Fatalf("tenant policy lost fields: %+v", policy)
	}
	policy.LibraryIDs[0] = 7
	policy.AllowedPermissions[0] = "marker_edit"
	if group.LibraryIDs[0] != 4 || group.AllowedPermissions[0] != "request" {
		t.Fatal("policy aliases persisted slices")
	}
	raw, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["PlaybackAllowed"] != false || fields["MaxProfiles"] != float64(3) || fields["ManagedTemplateKey"] != nil {
		t.Fatalf("model defaults changed: %s", raw)
	}
}

func TestTenantGroupCanonicalExtendedPolicyMutationDB(t *testing.T) {
	ctx, fixture := newOrganizationGroupStoreDBTest(t)
	group := fixture.createGroup(fixture.orgA, "Extended policy")
	account := fixture.createUser(&group.ID, 10)
	fixture.createProfile(account, fixture.orgA, &group.ID)
	var before int64
	if err := fixture.pool.QueryRow(ctx, "SELECT access_policy_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2", fixture.orgA, account).Scan(&before); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.store.UpdatePolicyConditional(ctx, fixture.orgA, group.ID, TenantUpdateGroupInput{PlaybackAllowed: new(false), MaxProfiles: new(3)}, GroupPrecondition{Revision: group.Revision})
	if err != nil || updated.PlaybackAllowed || updated.MaxProfiles != 3 {
		t.Fatalf("extended policy update: %+v %v", updated, err)
	}
	var after int64
	if err := fixture.pool.QueryRow(ctx, "SELECT access_policy_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2", fixture.orgA, account).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("extended fields revision=%d, want %d", after, before+1)
	}
	noop, err := fixture.store.UpdatePolicyConditional(ctx, fixture.orgA, group.ID, TenantUpdateGroupInput{PlaybackAllowed: new(false), MaxProfiles: new(3)}, GroupPrecondition{Revision: updated.Revision})
	if err != nil || noop.Revision <= updated.Revision {
		t.Fatalf("same-value write did not advance timestamp/configuration revision: %+v %v", noop, err)
	}
	if err := fixture.pool.QueryRow(ctx, "SELECT access_policy_revision FROM organization_memberships WHERE organization_id=$1 AND account_id=$2", fixture.orgA, account).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("same-value update invalidated account again: %d", after)
	}
	empty, err := fixture.store.Update(ctx, fixture.orgA, group.ID, UpdateGroupInput{})
	if err != nil || empty.Revision != noop.Revision {
		t.Fatalf("empty update changed revision: %+v %v", empty, err)
	}
	common, err := fixture.store.Update(ctx, fixture.orgA, group.ID, UpdateGroupInput{Description: new("Descriptive")})
	if err != nil || common.PlaybackAllowed || common.MaxProfiles != 3 {
		t.Fatalf("common update lost extended values: %+v %v", common, err)
	}
}
