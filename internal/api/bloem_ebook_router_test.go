package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

type ebookRouterFixture struct {
	requests.RouterClient
	input  *pluginv1.FulfillRequest
	target *pluginv1.FulfillmentTarget
}

func (f *ebookRouterFixture) Fulfill(_ context.Context, in *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	f.input = in
	return &pluginv1.FulfillResponse{Targets: []*pluginv1.FulfillmentTarget{f.target}}, nil
}

type ebookConnectionFixture struct{ integration *requests.Integration }

func (f ebookConnectionFixture) GetIntegration(context.Context, string) (*requests.Integration, error) {
	return f.integration, nil
}

type ebookResolverFixture struct{ client requests.RouterClient }

func (f ebookResolverFixture) RequestRouterClient(context.Context, int, string) (requests.RouterClient, error) {
	return f.client, nil
}

type ebookValidatorFixture struct{ err error }

func (f ebookValidatorFixture) ValidateEbookIntegration(context.Context, bookrequests.EbookIntegration) error {
	return f.err
}

func TestEbookRouterPassesCanonicalIdentityAndChecksReceipt(t *testing.T) {
	input := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "host-request", ISBN: "9780306406157", MediaType: "book", Title: "Book"}, Integration: bookrequests.EbookIntegration{ID: "connection", InstallationID: 7, CapabilityID: "bookwarehouse"}, IdempotencyKey: "host-request"}
	raw, err := json.Marshal(map[string]string{"kind": "monitoring", "id": "12", "isbn": input.Request.ISBN, "format": "epub", "request_id": input.Request.ID})
	if err != nil {
		t.Fatal(err)
	}
	receipt := "bwh1." + base64.RawURLEncoding.EncodeToString(raw)
	client := &ebookRouterFixture{target: &pluginv1.FulfillmentTarget{ConnectionId: "connection", Quality: "epub", ExternalId: receipt, Status: "queued"}}
	installation := 7
	adapter := bloemEbookRouter{integrations: ebookConnectionFixture{&requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "bookwarehouse", SupportedMediaTypes: []string{"book"}, BaseURL: "https://books.example.test", APIKeyRef: "synthetic-key", PluginConfig: map[string]any{}}}, resolver: ebookResolverFixture{client}, validator: ebookValidatorFixture{}}
	out, err := adapter.Fulfill(t.Context(), input)
	if err != nil || out.ExternalID != receipt {
		t.Fatalf("receipt: %+v %v", out, err)
	}
	if client.input.GetRequest().GetExternalIds()["isbn"] != input.Request.ISBN || client.input.GetRequest().GetExternalIds()["bloem_request_id"] != input.Request.ID || len(client.input.Connections) != 1 || client.input.Connections[0].GetApiKey() != "synthetic-key" {
		t.Fatal("wrong provider request")
	}
	client.target.ConnectionId = "other"
	if _, err = adapter.Fulfill(t.Context(), input); err == nil {
		t.Fatal("foreign connection receipt accepted")
	}
	client.input = nil
	adapter.validator = ebookValidatorFixture{errors.New("changed")}
	if _, err = adapter.Fulfill(t.Context(), input); err == nil || client.input != nil {
		t.Fatal("changed integration reached provider")
	}
}

func TestEbookRouterRecoveryDoesNotSubmit(t *testing.T) {
	installation := 7
	client := &ebookRouterFixture{}
	adapter := bloemEbookRouter{integrations: ebookConnectionFixture{&requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "bookwarehouse", SupportedMediaTypes: []string{"book"}, BaseURL: "https://books.example.test", APIKeyRef: "synthetic-key", PluginConfig: map[string]any{}}}, resolver: ebookResolverFixture{client}, validator: ebookValidatorFixture{}}
	input := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "host-request", ISBN: "9780306406157", MediaType: "book"}, Integration: bookrequests.EbookIntegration{ID: "connection", InstallationID: 7, CapabilityID: "bookwarehouse"}}
	receipt, err := adapter.Recover(t.Context(), input)
	if err != nil || receipt.Status != "unknown" || !strings.HasPrefix(receipt.ExternalID, "bwh1.") || client.input != nil {
		t.Fatalf("recovery submitted: %+v %v", receipt, err)
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(receipt.ExternalID, "bwh1."))
	if err != nil {
		t.Fatal(err)
	}
	var bound map[string]string
	if err = json.Unmarshal(data, &bound); err != nil {
		t.Fatal(err)
	}
	if bound["kind"] != "unknown" || bound["isbn"] != input.Request.ISBN || bound["request_id"] != input.Request.ID {
		t.Fatalf("unbound recovery: %s", data)
	}
}

func TestEbookRouterRefilingRetainsOriginalSubmissionIdentity(t *testing.T) {
	in := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "new-request", MediaType: "book", ISBN: "9780306406157"}, IdempotencyKey: "original-submission"}
	if got := ebookRequestDescriptor(in).GetExternalIds()["bloem_request_id"]; got != "original-submission" {
		t.Fatalf("refiling changed provider identity: %q", got)
	}
	raw, err := json.Marshal(ebookProviderReceipt{Kind: "unknown", ISBN: in.Request.ISBN, Format: "epub", RequestID: in.IdempotencyKey})
	if err != nil {
		t.Fatal(err)
	}
	receipt := "bwh1." + base64.RawURLEncoding.EncodeToString(raw)
	if !validateEbookReceipt(receipt, in, "epub") {
		t.Fatal("original uncertain submission receipt rejected")
	}
}
