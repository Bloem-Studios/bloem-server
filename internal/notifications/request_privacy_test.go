package notifications

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/requests"
)

func TestGlobalRequestBroadcastOmitsRequester(t *testing.T) {
	req := requests.Request{ID: "request-1", Title: "Movie", RequestedByUserID: 7, RequesterUsername: "private-name", HideRequesterInBroadcast: true}
	info := requestEventInfoFor(req)
	if info.RequesterName != "" || info.RequesterUserID != 0 || info.RequestID != "" {
		t.Fatalf("global broadcast exposed identity: %+v", info)
	}
	if info.Title != "Movie" {
		t.Fatal("lost public title")
	}
	req.HideRequesterInBroadcast = false
	info = requestEventInfoFor(req)
	if info.RequesterName != "private-name" || info.RequesterUserID != 7 {
		t.Fatal("local broadcast changed")
	}
}
