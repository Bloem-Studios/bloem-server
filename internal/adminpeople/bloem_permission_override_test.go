package adminpeople

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

// An account whose stored permissions are the column default '{}' has no
// permission override, as the account snapshot reports. The preview must read
// it the same way, or an unchanged permission ceiling shows up as a change.
func TestPreviewTreatsEmptyAccountPermissionsAsNoOverride(t *testing.T) {
	target := entitlements.Policy{PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5}
	current := target // the snapshot's view: allowed_permissions nil

	effective := effectivePolicyTargetForAccount(target, []string{}, nil)
	if effective.AllowedPermissions != nil {
		t.Fatalf("empty stored permissions became %#v, want nil (no override)", effective.AllowedPermissions)
	}

	counts := map[string]int64{}
	accumulatePolicyDiff(counts, current, effective)
	if counts["allowed_permissions"] != 0 {
		t.Fatalf("allowed_permissions counted as changed: %v", counts)
	}
}

func TestPreviewKeepsExplicitAccountPermissions(t *testing.T) {
	target := entitlements.Policy{PlaybackAllowed: true, MaxProfiles: 5}
	effective := effectivePolicyTargetForAccount(target, []string{"metadata_curation"}, nil)
	if len(effective.AllowedPermissions) != 1 || effective.AllowedPermissions[0] != "metadata_curation" {
		t.Fatalf("explicit permissions lost: %#v", effective.AllowedPermissions)
	}
}
