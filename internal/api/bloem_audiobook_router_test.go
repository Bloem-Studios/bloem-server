package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/requests"
)

type audiobookValidatorFixture struct{ err error }

func (v audiobookValidatorFixture) ValidateAudiobookIntegration(context.Context, bookrequests.AudiobookIntegration) error {
	return v.err
}

type audiobookRouterClientFixture struct {
	requests.RouterClient
	input    *pluginv1.FulfillRequest
	response *pluginv1.FulfillResponse
	err      error
}

func (f *audiobookRouterClientFixture) Fulfill(_ context.Context, in *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	f.input = in
	return f.response, f.err
}
func audiobookAdapterFixture() (bloemAudiobookRouter, bookrequests.AudiobookDispatchInput, *audiobookRouterClientFixture) {
	installation := 7
	in := bookrequests.AudiobookDispatchInput{Request: bookrequests.Request{ID: "request", MediaType: "audiobook", Title: "Synthetic Saga", Author: "Test Author"}, Integration: bookrequests.AudiobookIntegration{ID: "connection", InstallationID: installation, CapabilityID: "audiobooks", Language: "en"}, SubmissionID: "12345678-1234-1234-1234-123456789abc", Attempt: 2, SearchPass: 3, AICalls: 1, ExcludedRefs: []string{"abook:123", "nzbking:abcdef12"}}
	raw, _ := json.Marshal(bookrequests.AudiobookReceipt{Kind: "qbittorrent", ID: "0123456789012345678901234567890123456789", RequestID: in.SubmissionID, Attempt: 2, Client: "0123456789abcdef", Confirmed: true})
	decision, _ := json.Marshal(bookrequests.AcquisitionDecision{V: 1, Phase: "select", Outcome: "handed_off"})
	client := &audiobookRouterClientFixture{response: &pluginv1.FulfillResponse{Message: "abd1." + base64.RawURLEncoding.EncodeToString(decision), Targets: []*pluginv1.FulfillmentTarget{{ConnectionId: "connection", Quality: "audiobook", ExternalId: "abr1." + base64.RawURLEncoding.EncodeToString(raw), Status: "queued", ExternalStatus: "queued"}}}}
	adapter := bloemAudiobookRouter{integrations: ebookConnectionFixture{&requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "audiobooks", SupportedMediaTypes: []string{"audiobook"}, APIKeyRef: "abk1.e30", PluginConfig: map[string]any{}}}, resolver: ebookResolverFixture{client}, validator: audiobookValidatorFixture{}}
	return adapter, in, client
}
func TestAudiobookRouterPassesOnlyAcquisitionIdentity(t *testing.T) {
	a, in, c := audiobookAdapterFixture()
	out, err := a.Fulfill(t.Context(), in)
	if err != nil || out.Kind != bookrequests.AudiobookHandedOff {
		t.Fatalf("outcome %+v %v", out, err)
	}
	ids := c.input.Request.ExternalIds
	if ids["bloem_request_id"] != in.SubmissionID || ids["bloem_attempt"] != "2" || ids["bloem_search_pass"] != "3" || ids["bloem_ai_calls"] != "1" || ids["bloem_excluded_refs"] != "abook:123,nzbking:abcdef12" || ids["author"] != in.Request.Author || len(c.input.Connections) != 1 || c.input.Connections[0].ApiKey != "abk1.e30" {
		t.Fatalf("descriptor %+v", c.input)
	}
	c.response.Targets[0].ConnectionId = "other"
	if _, err = a.Fulfill(t.Context(), in); err == nil {
		t.Fatal("foreign receipt accepted")
	}
}
func TestAudiobookRouterPreRPCFailureAndRecoveryNeverSubmit(t *testing.T) {
	a, in, c := audiobookAdapterFixture()
	a.validator = audiobookValidatorFixture{errors.New("disabled")}
	if _, err := a.Fulfill(t.Context(), in); !errors.Is(err, bookrequests.ErrProviderNotCalled) || c.input != nil {
		t.Fatalf("pre-RPC error %v", err)
	}
	receipt, err := a.Recover(t.Context(), in)
	if err != nil || c.input != nil {
		t.Fatalf("recovery %v", err)
	}
	if _, err = bookrequests.ParseAudiobookReceipt(receipt.ExternalID, in.SubmissionID, in.Attempt); err != nil {
		t.Fatal(err)
	}
}

func TestAudiobookRouterRefusesCredentialsBoundToAnotherURL(t *testing.T) {
	bundle, err := audiobookCredentialBundle(map[string]string{"qbittorrent_password": "synthetic-pass"}, map[string]any{"qbittorrent_url": "https://client.example.test/qb"})
	if err != nil {
		t.Fatal(err)
	}
	for name, stored := range map[string]struct {
		key    string
		config map[string]any
	}{
		"moved URL":         {bundle, map[string]any{"qbittorrent_url": "https://elsewhere.example.test/qb"}},
		"cleared URL":       {bundle, map[string]any{}},
		"unreadable bundle": {"abk1.not-base64!", map[string]any{"qbittorrent_url": "https://client.example.test/qb"}},
	} {
		t.Run(name, func(t *testing.T) {
			a, in, c := audiobookAdapterFixture()
			fixture := a.integrations.(ebookConnectionFixture)
			moved := *fixture.integration
			moved.APIKeyRef, moved.PluginConfig = stored.key, stored.config
			a.integrations = ebookConnectionFixture{&moved}
			if _, err := a.Fulfill(t.Context(), in); !errors.Is(err, bookrequests.ErrProviderNotCalled) || !errors.Is(err, bookrequests.ErrConflict) || c.input != nil {
				t.Fatalf("fulfill with an unbound credential: %v", err)
			}
			// The fixture client has no CheckStatus, so reaching it panics.
			if _, err := a.CheckStatus(t.Context(), in, bookrequests.RecoveryReceipt(in.SubmissionID, in.Attempt)); !errors.Is(err, bookrequests.ErrConflict) {
				t.Fatalf("status check with an unbound credential: %v", err)
			}
		})
	}
	// The same credential at its bound URL is sent.
	a, in, c := audiobookAdapterFixture()
	bound := *a.integrations.(ebookConnectionFixture).integration
	bound.APIKeyRef, bound.PluginConfig = bundle, map[string]any{"qbittorrent_url": "https://client.example.test/qb/"}
	a.integrations = ebookConnectionFixture{&bound}
	if _, err := a.Fulfill(t.Context(), in); err != nil || c.input == nil || c.input.Connections[0].ApiKey != bundle {
		t.Fatalf("bound credential refused: %v", err)
	}
}

type audiobookStatusClientFixture struct {
	requests.RouterClient
	status *pluginv1.TargetStatus
}

func (f *audiobookStatusClientFixture) CheckStatus(context.Context, *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	return &pluginv1.CheckStatusResponse{Statuses: []*pluginv1.TargetStatus{f.status}}, nil
}

// Release plugin builds answer in the host's vocabulary; pre-release builds
// answered "queued" for a verification hold, a lost download and an
// unreadable client. Both map onto the same dispatch states: a review reaches
// an administrator, a lost or never-added download is retried and searched
// again, and an unreadable client waits without counting toward lost. A
// completed answer needs a verified external status and decision.
func TestAudiobookRouterMapsPluginStatusVocabulary(t *testing.T) {
	a, in, fulfill := audiobookAdapterFixture()
	receipt := fulfill.response.Targets[0].ExternalId
	envelope := func(outcome string) string {
		raw, _ := json.Marshal(bookrequests.AcquisitionDecision{V: 1, Phase: "verify", Outcome: outcome, Issues: []string{"too_short"}})
		return "abd1." + base64.RawURLEncoding.EncodeToString(raw)
	}
	for _, tc := range []struct {
		status, external, message, want string
	}{
		{"queued", "queued", "", "queued"},
		{"downloading", "stalled", "", "downloading"},
		{"review", "review", envelope("review"), "review"},
		{"unknown", "missing", "", "unknown"},
		{"unknown", "absent", "", "unknown"},
		{"unknown", "unknown", "", bookrequests.AudiobookProviderUnreadable},
		{"completed", "verified", envelope("verified"), "completed"},
		{"failed", "client_changed", "", "failed"},
		// Pre-release answers.
		{"queued", "review", envelope("review"), "review"},
		{"queued", "missing", "", "unknown"},
		{"queued", "absent", "", "unknown"},
		{"queued", "unknown", "", bookrequests.AudiobookProviderUnreadable},
		// Rejected answers.
		{"queued", "review", "", ""},
		{"queued", "review", envelope("verified"), ""},
		{"review", "review", "", ""},
		{"completed", "verified", "", ""},
		{"completed", "verified", envelope("review"), ""},
		{"completed", "downloading", envelope("verified"), ""},
	} {
		client := &audiobookStatusClientFixture{status: &pluginv1.TargetStatus{Quality: "audiobook", ConnectionId: "connection", Status: tc.status, ExternalStatus: tc.external, Message: tc.message}}
		a.resolver = ebookResolverFixture{client}
		out, err := a.CheckStatus(t.Context(), in, bookrequests.Receipt{ExternalID: receipt, Status: "queued"})
		if tc.want == "" {
			if !errors.Is(err, bookrequests.ErrInvalid) {
				t.Fatalf("%s/%s with message %q accepted: %+v %v", tc.status, tc.external, tc.message, out, err)
			}
			continue
		}
		if err != nil || out.Kind != bookrequests.AudiobookStatus || out.ProviderStatus != tc.want || out.ExternalStatus != tc.external {
			t.Fatalf("%s/%s: %+v %v, want provider status %s", tc.status, tc.external, out, err, tc.want)
		}
	}
}
