package apiv2

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

type fakeAdminDevices struct {
	calls        int
	user, device string
}

func (f *fakeAdminDevices) ReadAdminDevices(context.Context) ([]handlers.AdminDeviceSummaryView, error) {
	f.calls++
	return []handlers.AdminDeviceSummaryView{{UserID: 2, Username: "same", DeviceID: "shared", DeviceName: "Screen", LastUpdated: "2026-09-01T01:02:03.123456Z"}, {UserID: 1, Username: "same", DeviceID: "shared", DeviceName: "Screen", LastUpdated: "2026-09-01T01:02:03.123456Z"}}, nil
}
func (f *fakeAdminDevices) ReadAdminDevice(_ context.Context, user, device string) (*handlers.AdminDeviceDetailView, error) {
	f.calls++
	f.user = user
	f.device = device
	var view handlers.AdminDeviceDetailView
	err := json.Unmarshal([]byte(`{"user_id":2,"device_id":"screen/one","profiles":[{"profile_id":"child","profile_name":"Child","override_count":1,"last_updated":"2026-09-01T01:02:03Z"}],"settings":[{"user_id":2,"profile_id":"child","key":"legacy","value":"true","updated_at":"2026-09-01T01:02:03Z"}]}`), &view)
	return &view, err
}
func TestAdminDevicesProjectionAndPaging(t *testing.T) {
	deps := pilotDeps(nil, nil)
	f := new(fakeAdminDevices)
	deps.AdminDevices = f
	h := NewHandler(deps)
	path := Prefix + "/admin/devices"
	requireProblem(t, do(t, h, "GET", path, "", bearer(memberToken)), TypePermissionDenied)
	if f.calls != 0 {
		t.Fatal("unauthorized read reached service")
	}
	rec := do(t, h, "GET", path+"?limit=1", "", bearer(adminToken))
	var body Collection[AdminDeviceMetadata]
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(body.Items) != 1 || body.Items[0].UserID != "1" || body.Page == nil || !body.Page.HasMore {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"last_updated":"2026-09-01T01:02:03.123Z"`) || body.Items[0].Profiles == nil {
		t.Fatal(rec.Body.String())
	}
	cursor := url.QueryEscape(body.Page.NextCursor)
	rec = do(t, h, "GET", path+"?limit=1&cursor="+cursor, "", bearer(adminToken))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user_id":"2"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	requireProblem(t, do(t, h, "GET", path+"?limit=2&cursor="+cursor, "", bearer(adminToken)), TypeInvalidCursor)
	requireProblem(t, do(t, h, "GET", path+"?limit=1&cursor="+cursor, "", actingRequestAdmin), TypeInvalidCursor)
	rec = do(t, h, "GET", path+"/2/screen%2Fone", "", bearer(adminToken))
	if rec.Code != 200 || f.user != "2" || f.device != "screen/one" || !strings.Contains(rec.Body.String(), `"profile_id":"child"`) || !strings.Contains(rec.Body.String(), `"value":"true"`) {
		t.Fatal(rec.Code, rec.Body.String(), f)
	}
	requireProblem(t, do(t, h, "GET", path+"/0/screen", "", bearer(adminToken)), TypeValidationFailed)
}
func adminDeviceFixtureCases() []fixtureCase {
	return []fixtureCase{
		{name: "admin_devices", operationID: "listAdminDevices", method: "GET", path: Prefix + "/admin/devices", headers: bearer(adminToken), status: 200, assertHeaders: []string{"Content-Type", "Cache-Control"}, schema: "#/components/schemas/CollectionAdminDeviceMetadata", scenario: "Account/device composite identity distinguishes a shared device ID; absent profile metadata is an empty array."},
		{name: "admin_device", operationID: "getAdminDevice", method: "GET", path: Prefix + "/admin/devices/2/screen%2Fone", headers: bearer(adminToken), status: 200, assertHeaders: []string{"Content-Type", "Cache-Control"}, schema: "#/components/schemas/AdminDeviceDetail", scenario: "Device detail preserves profile identities and legacy compatibility settings; canonical overrides use the settings-values API."},
	}
}

func (f *fakeAdminDevices) AdminDevicesAvailable() bool { return true }
