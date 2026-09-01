package notifications

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPayloadForRowProjectsAlertBody(t *testing.T) {
	expires := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	dismissed := expires.Add(-time.Hour)
	announcement := "ann-1"
	row := DeliveryRow{Delivery: Delivery{
		ID:             "d1",
		Type:           DeliveryTypeSystemAlert,
		ProfileID:      "p1",
		Body:           []byte(`{"title":"Outage","body":"Back soon","severity":"critical","deeplink":"/status","image_url":"https://x/y.png","dismissible":false,"cta":{"label":"Status","url":"https://status.example"}}`),
		ExpiresAt:      &expires,
		DismissedAt:    &dismissed,
		AnnouncementID: &announcement,
	}}
	raw, err := json.Marshal(PayloadForRow(row))
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, want := range []string{`"title":"Outage"`, `"body":"Back soon"`, `"severity":"critical"`, `"deeplink":"/status"`,
		`"image_url":"https://x/y.png"`, `"dismissible":false`, `"cta":{"label":"Status","url":"https://status.example"}`,
		`"expires_at":"2026-09-02T00:00:00Z"`, `"dismissed_at":"2026-09-01T23:00:00Z"`, `"reason_flags":{}`} {
		if !strings.Contains(encoded, want) {
			t.Errorf("payload missing %s: %s", want, encoded)
		}
	}

	// Release rows carry none of the alert keys (dismissible included).
	raw, _ = json.Marshal(PayloadForRow(DeliveryRow{Delivery: Delivery{ID: "d2", Type: DeliveryTypeEpisodeAvailable}}))
	for _, absent := range []string{`"title"`, `"severity"`, `"dismissible"`, `"expires_at"`, `"dismissed_at"`} {
		if strings.Contains(string(raw), absent) {
			t.Errorf("episode payload should omit %s: %s", absent, raw)
		}
	}
}

func TestBuildNotificationDisplayForSystemTypes(t *testing.T) {
	row := DeliveryRow{Delivery: Delivery{
		ID:   "d1",
		Type: DeliveryTypeSystemAnnouncement,
		Body: []byte(`{"title":"New feature","body":"Try it","severity":"info","deeplink":"/settings"}`),
	}}
	display := BuildNotificationDisplay(row)
	if display.Category != "system_announcement" || display.Title != "New feature" || display.Body != "Try it" || display.URL != "/settings" {
		t.Fatalf("unexpected display: %+v", display)
	}
	if !IsSystemDeliveryType(DeliveryTypeSystemAlert) || IsSystemDeliveryType(DeliveryTypeEpisodeAvailable) {
		t.Fatal("IsSystemDeliveryType misclassifies")
	}
	types := SupportedDeliveryTypes()
	if types[len(types)-1] != DeliveryTypeSystemAnnouncement {
		t.Fatalf("supported types should end with the system types: %v", types)
	}
}
