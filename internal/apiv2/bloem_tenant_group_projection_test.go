package apiv2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/google/uuid"
)

func TestTenantGroupV2ProjectionRetainsArrayDistinctions(t *testing.T) {
	for _, empty := range []bool{false, true} {
		group := access.TenantGroup{Group: access.Group{ID: 1, Revision: 9, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}, OrganizationID: uuid.New(), PlaybackAllowed: false, MaxProfiles: 3}
		if empty {
			group.LibraryIDs = []int{}
			group.AllowedPermissions = []string{}
		}
		wire, err := json.Marshal(adminAccessGroupOf(&group.Group))
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"library_ids", "allowed_permissions"} {
			value, present := fields[key]
			if !present {
				t.Fatalf("omitted %s: %s", key, wire)
			}
			if empty {
				array, ok := value.([]any)
				if !ok || len(array) != 0 {
					t.Fatalf("empty %s became null: %s", key, wire)
				}
			} else if value != nil {
				t.Fatalf("nil %s became array: %s", key, wire)
			}
		}
		for _, key := range []string{"organization_id", "playback_allowed", "max_profiles", "managed_template_key"} {
			if _, ok := fields[key]; ok {
				t.Fatalf("storage field %s reached v2 wire", key)
			}
		}
	}
}
