package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

const notificationFixtureID = "00000000-0000-0000-0000-000000000002"

type fakeNotificationInbox struct {
	cutoff notifications.Cursor
	writes int
	patch  notifications.PreferencePatch
	empty  bool
}

func fixtureNotificationInbox() *fakeNotificationInbox {
	return &fakeNotificationInbox{cutoff: notifications.Cursor{CreatedAt: fixedTime().Add(123 * time.Microsecond), ID: notificationFixtureID}}
}
func (*fakeNotificationInbox) NotificationCapabilities(context.Context) handlers.NotificationCapabilitiesView {
	var out handlers.NotificationCapabilitiesView
	_ = json.Unmarshal([]byte(`{"in_app":{"enabled":true},"apple_push":{"available":false,"provider":"off","supported_modes":["in_app_only"]},"android_push":{"available":false,"provider":"off","supported_modes":["in_app_only"]},"web_push":{"available":false},"webhooks":{"available":false,"max_per_profile":0,"supported_types":[]},"email":{"available":false,"modes":[],"digest_hour":0},"discord":{"available":false,"modes":[],"digest_hour":0}}`), &out)
	return out
}
func (f *fakeNotificationInbox) row(profile string) notifications.DeliveryRowPayload {
	return notifications.DeliveryRowPayload{ID: notificationFixtureID, Type: "request.approved", ProfileID: profile, CreatedAt: f.cutoff.CreatedAt, ReasonFlags: json.RawMessage(`{"request_id":"request-1"}`)}
}
func (f *fakeNotificationInbox) ListNotificationInbox(_ context.Context, profile string, _ bool, _ int, before, through *notifications.Cursor) (handlers.NotificationInboxPageView, error) {
	boundary := f.cutoff
	if through != nil {
		boundary = *through
	}
	rows := []notifications.DeliveryRowPayload{f.row(profile)}
	if before != nil {
		rows[0].ID = "00000000-0000-0000-0000-000000000001"
	}
	if f.empty {
		rows = nil
		boundary = notifications.Cursor{}
	}
	return handlers.NotificationInboxPageView{Items: rows, Through: boundary, More: before == nil && !f.empty}, nil
}
func (f *fakeNotificationInbox) SyncNotificationInbox(_ context.Context, profile string, _ int, _ *notifications.Cursor) ([]notifications.DeliveryRowPayload, bool, int, error) {
	if f.empty {
		return nil, false, 0, nil
	}
	return []notifications.DeliveryRowPayload{f.row(profile)}, false, 1, nil
}
func (f *fakeNotificationInbox) GetNotificationInboxItem(_ context.Context, profile, id string) (notifications.DeliveryRowPayload, error) {
	if id != notificationFixtureID {
		return notifications.DeliveryRowPayload{}, &handlers.APIError{Status: 404, Code: "not_found", Message: "Notification not found"}
	}
	return f.row(profile), nil
}
func (*fakeNotificationInbox) NotificationUnreadCount(context.Context, string) (int, error) {
	return 1, nil
}
func (f *fakeNotificationInbox) MarkNotificationRead(context.Context, int, string, string) error {
	f.writes++
	return nil
}
func (f *fakeNotificationInbox) MarkNotificationInboxThrough(_ context.Context, _ int, _ string, through notifications.Cursor) error {
	f.cutoff = through
	f.writes++
	return nil
}
func (*fakeNotificationInbox) NotificationPreferences(_ context.Context, profile string) (notifications.Preferences, error) {
	return notifications.DefaultPreferences(profile), nil
}
func (f *fakeNotificationInbox) PatchNotificationPreferences(_ context.Context, profile string, patch notifications.PreferencePatch) (notifications.Preferences, error) {
	f.patch = patch
	f.writes++
	p := notifications.DefaultPreferences(profile)
	if patch.Enabled != nil {
		p.Enabled = *patch.Enabled
	}
	return p, nil
}
func TestNotificationInboxScopedCursorAndReadCutoff(t *testing.T) {
	f := fixtureNotificationInbox()
	deps := pilotDeps(nil, nil)
	deps.NotificationInbox = f
	h := NewHandler(deps)
	path := Prefix + "/notifications"
	first := do(t, h, http.MethodGet, path+"?limit=1", "", profileOwner())
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var page struct {
		Items      []NotificationItem `json:"items"`
		Page       PageInfo           `json:"page"`
		ReadCutoff string             `json:"read_cutoff"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.Page.HasMore || page.ReadCutoff == "" {
		t.Fatal(first.Body.String())
	}
	requireProblem(t, do(t, h, http.MethodGet, path+"/invalid-id", "", profileOwner()), TypeValidationFailed)
	original := f.cutoff
	f.cutoff.CreatedAt = f.cutoff.CreatedAt.Add(time.Hour)
	next := do(t, h, http.MethodGet, path+"?limit=1&cursor="+url.QueryEscape(page.Page.NextCursor), "", profileOwner())
	if next.Code != 200 {
		t.Fatal(next.Code, next.Body.String())
	}
	requireProblem(t, do(t, h, http.MethodGet, path+"?limit=2&cursor="+url.QueryEscape(page.Page.NextCursor), "", profileOwner()), TypeInvalidCursor)
	requireProblem(t, do(t, h, http.MethodPost, path+"/read-all", `{}`, profileOwner()), TypeValidationFailed)
	body, _ := json.Marshal(map[string]string{"through": page.ReadCutoff})
	saved := do(t, h, http.MethodPost, path+"/read-all", string(body), profileOwner())
	if saved.Code != 204 || f.cutoff != original {
		t.Fatal(saved.Code, saved.Body.String(), f.cutoff)
	}
	requireProblem(t, do(t, h, http.MethodPost, path+"/read-all", string(body), actingRequestAdmin), TypeInvalidCursor)
	requireProblem(t, do(t, h, http.MethodPut, path+"/preferences", `{"enabled":null}`, profileOwner()), TypeValidationFailed)
	saved = do(t, h, http.MethodPut, path+"/preferences", `{"enabled":false}`, profileOwner())
	if saved.Code != 200 || f.patch.Enabled == nil || *f.patch.Enabled || f.patch.NotifyFavorites != nil {
		t.Fatal(saved.Code, saved.Body.String(), f.patch)
	}
}
func TestNotificationEmptySyncHasStableCheckpoint(t *testing.T) {
	f := fixtureNotificationInbox()
	f.empty = true
	deps := pilotDeps(nil, nil)
	deps.NotificationInbox = f
	h := NewHandler(deps)
	r := do(t, h, http.MethodGet, Prefix+"/notifications/sync?limit=1", "", profileOwner())
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var first struct {
		SyncCursor string `json:"sync_cursor"`
		Initial    bool   `json:"initial_snapshot"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil || first.SyncCursor == "" || !first.Initial {
		t.Fatal(first, err)
	}
	r = do(t, h, http.MethodGet, Prefix+"/notifications/sync?limit=1&cursor="+url.QueryEscape(first.SyncCursor), "", profileOwner())
	var second struct {
		Initial bool `json:"initial_snapshot"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &second); err != nil || second.Initial || r.Code != 200 {
		t.Fatal(second, err, r.Code)
	}
}
