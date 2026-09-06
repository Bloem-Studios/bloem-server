package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

type fakeNotificationDestinations struct {
	profile string
	limit   int
}

func (f *fakeNotificationDestinations) ListNotificationWebPushPage(_ context.Context, profile string, limit int, after *notifications.Cursor) ([]notifications.WebPushSubscription, error) {
	f.profile, f.limit = profile, limit
	rows := []notifications.WebPushSubscription{{ID: "01", Endpoint: "https://push.example.test/1", P256dh: "sensitive-p256dh", Auth: "sensitive-auth", CreatedAt: fixedTime()}, {ID: "02", Endpoint: "https://push.example.test/2", CreatedAt: fixedTime()}}
	if after != nil {
		rows = rows[1:]
	}
	return rows, nil
}
func (f *fakeNotificationDestinations) ListNotificationWebhookPage(_ context.Context, profile string, limit int, after *notifications.Cursor) ([]notifications.Webhook, error) {
	f.profile, f.limit = profile, limit
	rows := []notifications.Webhook{{ID: "01", Name: "Example", Type: "generic", URLHost: "example.test", URLCiphertext: "sensitive-url", SigningSecretCiphertext: new("sensitive-secret"), CreatedAt: fixedTime()}, {ID: "02", Name: "Other", Type: "generic", URLHost: "example.test", CreatedAt: fixedTime()}}
	if after != nil {
		rows = rows[1:]
	}
	return rows, nil
}
func (f *fakeNotificationDestinations) ListNotificationServerChannelPage(_ context.Context, limit int, after *notifications.Cursor) ([]notifications.ServerChannel, error) {
	f.limit = limit
	rows := []notifications.ServerChannel{{ID: "01", Name: "Example", Type: "generic", URLHost: "example.test", URLCiphertext: "sensitive-url", SigningSecretCiphertext: new("sensitive-secret"), CreatedAt: fixedTime()}, {ID: "02", Name: "Other", Type: "generic", URLHost: "example.test", CreatedAt: fixedTime()}}
	if after != nil {
		rows = rows[1:]
	}
	return rows, nil
}

func TestNotificationDestinationCursors(t *testing.T) {
	fake := new(fakeNotificationDestinations)
	deps := pilotDeps(nil, nil)
	deps.NotificationDestinations = fake
	h := NewHandler(deps)
	for _, tc := range []struct {
		path    string
		headers map[string]string
		profile string
	}{
		{Prefix + "/notifications/web-push/subscriptions", profileOwner(), "p-owner"},
		{Prefix + "/notifications/webhooks", profileOwner(), "p-owner"},
		{Prefix + "/admin/notifications/server-channels", bearer(adminToken), ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tc.path+"?limit=1", "", tc.headers)
			if rec.Code != 200 || fake.limit != 2 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "sensitive") {
				t.Fatal("destination credential exposed")
			}
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				Page PageInfo `json:"page"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != "01" || !page.Page.HasMore || page.Page.NextCursor == "" {
				t.Fatalf("%+v", page)
			}
			query := "?limit=1&cursor=" + url.QueryEscape(page.Page.NextCursor)
			rec = do(t, h, http.MethodGet, tc.path+query, "", tc.headers)
			if rec.Code != 200 {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 || page.Items[0].ID != "02" || page.Page.HasMore {
				t.Fatalf("%+v", page)
			}
			requireProblem(t, do(t, h, http.MethodGet, tc.path+strings.Replace(query, "limit=1", "limit=2", 1), "", tc.headers), TypeInvalidCursor)
			if tc.profile != "" {
				requireProblem(t, do(t, h, http.MethodGet, tc.path+query, "", with(bearer(memberToken), "X-Profile-Id", "p-primary")), TypeInvalidCursor)
			}
		})
	}
	requireProblem(t, do(t, h, http.MethodGet, Prefix+"/admin/notifications/server-channels", "", profileOwner()), TypePermissionDenied)
}

func notificationDestinationFixtureCases() []fixtureCase {
	cases := []fixtureCase{
		{name: "notification_web_push_subscriptions", operationID: listNotificationWebPushOperation, path: Prefix + "/notifications/web-push/subscriptions", headers: profileOwner(), schema: "#/components/schemas/CollectionNotificationWebPushSubscription"},
		{name: "notification_webhooks", operationID: listNotificationWebhooksOperation, path: Prefix + "/notifications/webhooks", headers: profileOwner(), schema: "#/components/schemas/CollectionNotificationWebhookDestination"},
		{name: "notification_server_channels", operationID: listNotificationServerChannelsOperation, path: Prefix + "/admin/notifications/server-channels", headers: bearer(adminToken), schema: "#/components/schemas/CollectionNotificationServerChannel"},
	}
	for i := range cases {
		cases[i].method = "GET"
		cases[i].status = 200
		cases[i].scenario = "Bounded notification destination metadata excludes reusable keys and webhook URL ciphertext."
		cases[i].assertHeaders = []string{"Content-Type", "Cache-Control"}
	}
	return cases
}
