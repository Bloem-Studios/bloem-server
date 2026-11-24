package notifications

import (
	"testing"
	"time"
)

func TestLimitKindsReachOrganizationAdmins(t *testing.T) {
	for _, id := range []string{AdminKindStreamLimitReached, AdminKindTranscodeLimitReached} {
		kind, ok := AdminPushKindByID(id)
		if !ok || !kind.OrganizationAdmins || !kind.ServerEmitted || kind.Channel != AdminChannelLimits {
			t.Fatalf("kind %s = %+v, want a server-sent limits kind for organization admins", id, kind)
		}
	}
	for _, kind := range AdminPushKinds() {
		if kind.OrganizationAdmins && kind.Channel != AdminChannelLimits {
			t.Fatalf("kind %s reaches organization admins; only the limit kinds may", kind.ID)
		}
	}
}

func TestLimitNotices(t *testing.T) {
	at := time.Date(2026, 11, 24, 21, 37, 0, 0, time.FixedZone("CET", 3600))
	stream := StreamLimitNotice(7, "anna", 2, "person", at)
	if stream.Kind != AdminKindStreamLimitReached || stream.SubjectType != "person" || stream.SubjectID != "7" {
		t.Fatalf("stream notice subject = %+v", stream)
	}
	if stream.Title != "Stream limit reached" || stream.Body != "anna was turned away: all 2 streams in use." {
		t.Fatalf("stream notice text = %q / %q", stream.Title, stream.Body)
	}
	if stream.DedupeKey != "2026-11-24T20" {
		t.Fatalf("dedupe key = %q, want the UTC hour", stream.DedupeKey)
	}
	for scope, word := range map[string]string{"person": "personal", "household": "household", "organization": "organization's"} {
		n := TranscodeLimitNotice(7, "anna", 3, scope, at)
		want := "anna couldn't start a transcode: the " + word + " limit of 3 is in use."
		if n.Kind != AdminKindTranscodeLimitReached || n.Title != "Transcode limit reached" || n.Body != want {
			t.Fatalf("transcode notice for %s = %q / %q, want %q", scope, n.Title, n.Body, want)
		}
	}
	if n := StreamLimitNotice(7, "", 1, "person", at); n.Body != "Someone was turned away: all 1 streams in use." {
		t.Fatalf("nameless body = %q", n.Body)
	}
	later := StreamLimitNotice(7, "anna", 2, "person", at.Add(10*time.Minute))
	if adminDeliveryID(stream, 1) != adminDeliveryID(later, 1) {
		t.Fatal("two refusals in the same hour make two deliveries for one recipient")
	}
	nextHour := StreamLimitNotice(7, "anna", 2, "person", at.Add(time.Hour))
	if adminDeliveryID(stream, 1) == adminDeliveryID(nextHour, 1) {
		t.Fatal("a refusal an hour later is folded into the earlier delivery")
	}
}
