package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

// The storage plugin's ebook connection carries no URL or key: the plugin
// uses its installation's own Bookwarehouse connection.
func TestEbookRouterNativeConnectionNeedsNoKey(t *testing.T) {
	input := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "host-request", ISBN: "9780306406157", MediaType: "book", Title: "Book"}, Integration: bookrequests.EbookIntegration{ID: "connection", InstallationID: 30, CapabilityID: "ebooks", Native: true}, IdempotencyKey: "host-request"}
	raw, _ := json.Marshal(map[string]string{"kind": "monitoring", "id": "12", "isbn": input.Request.ISBN, "format": "epub", "request_id": input.Request.ID})
	receipt := "bwh1." + base64.RawURLEncoding.EncodeToString(raw)
	client := &ebookRouterFixture{target: &pluginv1.FulfillmentTarget{ConnectionId: "connection", Quality: "epub", ExternalId: receipt, Status: "queued"}}
	installation := 30
	stored := &requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "ebooks", SupportedMediaTypes: []string{"book"}, PluginConfig: map[string]any{"preferred_format": "epub"}}
	adapter := bloemEbookRouter{integrations: ebookConnectionFixture{stored}, resolver: ebookResolverFixture{client}, validator: ebookValidatorFixture{}}
	out, err := adapter.Fulfill(t.Context(), input)
	if err != nil || out.ExternalID != receipt {
		t.Fatalf("native ebook fulfill: %+v %v", out, err)
	}
	if c := client.input.GetConnections()[0]; c.GetApiKey() != "" || c.GetBaseUrl() != "" {
		t.Fatalf("native connection %+v", c)
	}
	// A request plugin's connection still needs its key.
	input.Integration.Native = false
	if _, err = adapter.Fulfill(t.Context(), input); err == nil {
		t.Fatal("keyless request plugin connection accepted")
	}
}

type nativeAudiobookStatusClient struct {
	requests.RouterClient
	status *pluginv1.TargetStatus
}

func (f nativeAudiobookStatusClient) CheckStatus(context.Context, *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	return &pluginv1.CheckStatusResponse{Statuses: []*pluginv1.TargetStatus{f.status}}, nil
}

// The storage plugin's audiobook connection carries no credential bundle, and
// its verified downloads must name the Bookwarehouse audiobook they became.
func TestAudiobookRouterNativeCompletionNamesAudiobook(t *testing.T) {
	installation := 36
	in := bookrequests.AudiobookDispatchInput{Request: bookrequests.Request{ID: "request", MediaType: "audiobook", Title: "Synthetic Saga"}, Integration: bookrequests.AudiobookIntegration{ID: "connection", InstallationID: installation, CapabilityID: "audiobooks", Language: "en", Native: true}, SubmissionID: "12345678-1234-1234-1234-123456789abc", Attempt: 1}
	raw, _ := json.Marshal(bookrequests.AudiobookReceipt{Kind: "qbittorrent", ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TorrentID: "6f1c0e8a-3b2d-4c1e-9f7a-0123456789ab", RequestID: in.SubmissionID, Attempt: 1, Client: "0123456789abcdef", Confirmed: true})
	receipt := bookrequests.Receipt{ExternalID: "abr1." + base64.RawURLEncoding.EncodeToString(raw)}
	envelope := func(d bookrequests.AcquisitionDecision) string {
		body, _ := json.Marshal(d)
		return "abd1." + base64.RawURLEncoding.EncodeToString(body)
	}
	stored := &requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "audiobooks", SupportedMediaTypes: []string{"audiobook"}, PluginConfig: map[string]any{}}
	status := &pluginv1.TargetStatus{ConnectionId: "connection", Quality: "audiobook", Status: "completed", ExternalStatus: "verified", Message: envelope(bookrequests.AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified", AudiobookID: "bw-audiobook"})}
	adapter := bloemAudiobookRouter{integrations: ebookConnectionFixture{stored}, resolver: ebookResolverFixture{nativeAudiobookStatusClient{status: status}}, validator: audiobookValidatorFixture{}}
	out, err := adapter.CheckStatus(t.Context(), in, receipt)
	if err != nil || out.ProviderStatus != "completed" || out.Decision == nil || out.Decision.AudiobookID != "bw-audiobook" {
		t.Fatalf("native completion: %+v %v", out, err)
	}
	status.Message = envelope(bookrequests.AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified"})
	if _, err = adapter.CheckStatus(t.Context(), in, receipt); err == nil {
		t.Fatal("native completion without an audiobook id accepted")
	}
	// A request plugin's connection still needs its credential bundle.
	in.Integration.Native = false
	status.Message = envelope(bookrequests.AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified", AudiobookID: "bw-audiobook"})
	if _, err = adapter.CheckStatus(t.Context(), in, receipt); err == nil {
		t.Fatal("keyless request plugin connection accepted")
	}
}
