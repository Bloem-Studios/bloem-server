package apiv2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Bloem's native organization and account routes answer with exactly the
// objects /api/v2 answers with, and decode group writes exactly as /api/v2's
// access-group operations do.
func TestBloemV2WireServesTheV2Shapes(t *testing.T) {
	wire := bloemV2Wire{}
	at := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	group := access.Group{ID: 21, Name: "Teens", LibraryIDs: []int{4}, MaxProfiles: 5, MemberCount: 3, CreatedAt: at, UpdatedAt: at}
	if got, want := wire.AccessGroup(group), adminAccessGroupOf(&group); !reflect.DeepEqual(got, want) {
		t.Fatalf("group = %#v, want the v2 AdminAccessGroup %#v", got, want)
	}
	// The organization list adds profile_count, the profiles a delete
	// reassigns, beside /api/v2's account member_count.
	tenantGroup := access.TenantGroup{Group: group, ProfileCount: 5}
	item, ok := wire.AccessGroupListItem(tenantGroup).(BloemOrganizationGroupListItem)
	if !ok || !reflect.DeepEqual(item.AdminAccessGroupListItem, AdminAccessGroupListItem{AdminAccessGroup: adminAccessGroupOf(&group), MemberCount: 3}) || item.ProfileCount != 5 {
		t.Fatalf("list item = %#v, want the v2 AdminAccessGroupListItem with profile_count", wire.AccessGroupListItem(tenantGroup))
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil || fields["member_count"] != float64(3) || fields["profile_count"] != float64(5) || fields["id"] != "21" {
		t.Fatalf("list item JSON = %s, want member_count 3 and profile_count 5 beside the v2 fields", encoded)
	}
	invitation := &models.Invitation{ID: 7, Email: "new@example.test", Role: "user", ExpiresAt: at, CreatedAt: at}
	if got, want := wire.Invitation(invitation), invitationOf(invitation); !reflect.DeepEqual(got, want) {
		t.Fatalf("invitation = %#v, want the v2 AdminInvitation %#v", got, want)
	}
	devices := wire.UserDevices([]handlers.AdminUserDeviceView{{AdminDeviceSummaryView: handlers.AdminDeviceSummaryView{DeviceID: "tv"}}})
	if got, ok := devices.([]AdminUserDevice); !ok || len(got) != 1 || got[0].DeviceID != "tv" {
		t.Fatalf("devices = %#v, want v2 AdminUserDevice values", devices)
	}

	created, err := wire.AccessGroupCreate([]byte(`{"name":"Teens","library_ids":["4"],"max_profiles":2}`))
	if err != nil || created.Name != "Teens" || !reflect.DeepEqual(created.LibraryIDs, []int{4}) || created.MaxProfiles != 2 || !created.DownloadAllowed || !created.RequestsAllowed {
		t.Fatalf("create = %#v, %v; want v2 decoding with v2's defaults", created, err)
	}
	if _, err := wire.AccessGroupCreate([]byte(`{"library_ids":["4"]}`)); err == nil {
		t.Fatal("create without a name was accepted")
	}
	update, err := wire.AccessGroupUpdate([]byte(`{"library_ids":null}`))
	if err != nil || update.LibraryIDs == nil || *update.LibraryIDs != nil {
		t.Fatalf("update = %#v, %v; want v2's explicit-null reset", update, err)
	}
	if _, err := wire.AccessGroupUpdate([]byte(`{"library_ids":["x"]}`)); err == nil {
		t.Fatal("update with an invalid library ID was accepted")
	} else if apiErr, ok := errors.AsType[*handlers.APIError](err); !ok || apiErr.Status != 422 || apiErr.Code != "validation_failed" {
		t.Fatalf("update error = %#v, want the v2 validation problem", err)
	}
	if body, _ := json.Marshal(wire.AccessGroup(group)); !json.Valid(body) {
		t.Fatal("group does not serialize")
	}
}
