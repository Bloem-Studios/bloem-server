package notifications

import (
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

func TestEbookRequestNoticesUseEbookLabelsAndScopedLinks(t *testing.T) {
	r := bookrequests.Request{MediaType: "book", Title: "A requested book"}
	for _, event := range []string{"submitted", "approve", "decline"} {
		body, err := bookRequestAlert(r, event)
		if err != nil || strings.Contains(strings.ToLower(body.Title), "audiobook") || !strings.Contains(strings.ToLower(body.Title), "ebook") || !strings.HasSuffix(body.Deeplink, "kind=ebook") {
			t.Fatalf("wrong ebook notice: %s %+v %v", event, body, err)
		}
		if event == "submitted" && !strings.HasPrefix(body.Deeplink, "/admin/requests") {
			t.Fatal("submission was not directed to the organization queue")
		}
	}
	if _, err := bookRequestAlert(r, "fulfilled"); err == nil {
		t.Fatal("ebook manual fulfillment notice claimed completion without a library item")
	}
}
