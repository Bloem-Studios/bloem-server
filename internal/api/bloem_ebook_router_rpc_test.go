//go:build bloem

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/encoding/protojson"
)

type ebookRPCConnection struct{ row *requests.Integration }

func (f ebookRPCConnection) GetIntegration(context.Context, string) (*requests.Integration, error) {
	return f.row, nil
}

type ebookRPCResolver struct{ client requests.RouterClient }

func (f ebookRPCResolver) RequestRouterClient(context.Context, int, string) (requests.RouterClient, error) {
	return f.client, nil
}

type ebookRPCValidator struct{}

func (ebookRPCValidator) ValidateEbookIntegration(context.Context, bookrequests.EbookIntegration) error {
	return nil
}

// This opt-in acceptance test crosses the real go-plugin process boundary using
// the released Bloem SDK executable and host's Silo-compatible protobuf surface.
// The HTTP fixture contains only synthetic metadata and credentials.
func TestBloemEbookRouterRealPluginRPC(t *testing.T) {
	binary := os.Getenv("BLOEM_BOOKWAREHOUSE_ROUTER_TEST_BINARY")
	if binary == "" {
		t.Skip("set BLOEM_BOOKWAREHOUSE_ROUTER_TEST_BINARY to the local request plugin executable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "manifest").Output()
	if err != nil {
		t.Fatalf("plugin manifest: %v", err)
	}
	m := &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(raw, m); err != nil {
		t.Fatal(err)
	}
	if m.PluginId != "bloem.requests.bookwarehouse" || len(m.Capabilities) != 1 || m.Capabilities[0].Type != "request_router.v1" || m.Capabilities[0].Id != "bookwarehouse" {
		t.Fatalf("wrong plugin manifest: %v", m)
	}
	host := pluginhost.NewHost(pluginhost.Config{Logger: hclog.NewNullLogger()})
	process, err := host.Start(ctx, pluginhost.StartRequest{InstallationID: 7, BinaryPath: binary, Manifest: m})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Stop(7); err != nil {
			t.Errorf("stop plugin: %v", err)
		}
	}()
	rpc, err := process.RequestRouter("bookwarehouse")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, format   string
		pdf, ambiguous bool
	}{{name: "epub-default", format: "epub"}, {name: "pdf-opt-in", format: "pdf", pdf: true}, {name: "uncertain-read-only-recovery", format: "epub", ambiguous: true}} {
		t.Run(tc.name, func(t *testing.T) {
			var posts, reads atomic.Int32
			var exists, completed atomic.Bool
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-API-Key") != "synthetic-rpc-key" {
					t.Error("incorrect RPC credential")
				}
				reads.Add(1)
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				row := func() map[string]string {
					state := "downloading"
					if completed.Load() {
						state = "completed"
					}
					return map[string]string{"id": "provider-row", "isbn13": "9780306406157", "status": state, "selected_format": tc.format, "format": tc.format}
				}
				switch r.URL.Path {
				case "/api/v1/monitoring":
					rows := []any{}
					if exists.Load() {
						rows = append(rows, row())
					}
					write(map[string]any{"requests": rows, "page": 1, "limit": 100, "total": len(rows)})
				case "/api/v1/books":
					if r.URL.Query().Get("format") != tc.format {
						t.Error("wrong format reached backend")
					}
					write(map[string]any{"books": []any{}, "pagination": map[string]int{"page": 1, "limit": 100, "total_items": 0, "total_pages": 0}})
				case "/api/v1/monitoring/add":
					if r.Method != "POST" {
						t.Error("wrong method")
					}
					posts.Add(1)
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 2 || body["isbn"] != "9780306406157" || body["preferred_format"] != tc.format {
						t.Errorf("wrong submission body %v", body)
					}
					w.WriteHeader(202)
					if tc.ambiguous {
						_, _ = w.Write([]byte("invalid success response synthetic-rpc-key"))
					} else {
						exists.Store(true)
						write(map[string]string{"request_id": "provider-row", "status": "searching"})
					}
				case "/api/v1/monitoring/provider-row":
					write(row())
				default:
					t.Errorf("unexpected backend path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer fixture.Close()
			installation := 7
			configuration := map[string]any{"allow_http": true}
			if tc.pdf {
				configuration["preferred_format"] = "pdf"
				configuration["allow_pdf"] = true
			}
			connection := &requests.Integration{ID: "rpc-connection", Enabled: true, InstallationID: &installation, CapabilityID: "bookwarehouse", SupportedMediaTypes: []string{"book"}, BaseURL: fixture.URL, APIKeyRef: "synthetic-rpc-key", PluginConfig: configuration}
			adapter := bloemEbookRouter{integrations: ebookRPCConnection{connection}, resolver: ebookRPCResolver{rpc}, validator: ebookRPCValidator{}}
			input := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "stable-rpc-request", ISBN: "9780306406157", MediaType: "book", Title: "Synthetic ebook"}, Integration: bookrequests.EbookIntegration{ID: connection.ID, InstallationID: installation, CapabilityID: connection.CapabilityID}, IdempotencyKey: "stable-rpc-request"}
			receipt, err := adapter.Fulfill(ctx, input)
			if err != nil || receipt.ExternalID == "" || posts.Load() != 1 {
				t.Fatalf("Fulfill receipt=%+v err=%v posts=%d", receipt, err, posts.Load())
			}
			format := tc.format
			if !validateEbookReceipt(receipt.ExternalID, input, format) {
				t.Fatal("wire receipt identity mismatch")
			}
			if tc.ambiguous {
				if receipt.Status != "unknown" {
					t.Fatalf("ambiguous status %q", receipt.Status)
				}
				recovery, err := adapter.Recover(ctx, input)
				if err != nil || recovery != receipt {
					t.Fatalf("Recover does not match plugin unknown receipt: %+v %+v %v", receipt, recovery, err)
				}
				status, err := adapter.CheckStatus(ctx, input, recovery)
				if err != nil || status.Status != "unknown" || posts.Load() != 1 {
					t.Fatalf("unknown absence: %+v %v posts=%d", status, err, posts.Load())
				}
				exists.Store(true)
				receipt = recovery
			} else {
				status, err := adapter.CheckStatus(ctx, input, receipt)
				if err != nil || status.Status != "downloading" {
					t.Fatalf("durable CheckStatus=%+v err=%v", status, err)
				}
			}
			completed.Store(true)
			status, err := adapter.CheckStatus(ctx, input, receipt)
			if err != nil || status.Status != "completed" || posts.Load() != 1 {
				t.Fatalf("provider completion=%+v err=%v posts=%d", status, err, posts.Load())
			}
			foreign := input
			foreign.Request.ID = "another-rpc-request"
			foreign.IdempotencyKey = "foreign-submission"
			before := reads.Load()
			if _, err = adapter.CheckStatus(ctx, foreign, receipt); err == nil || reads.Load() != before {
				t.Fatal("foreign request receipt reached backend")
			}
			refiled := input
			refiled.Request.ID = "refiled-host-request"
			status, err = adapter.CheckStatus(ctx, refiled, receipt)
			if err != nil || status.Status != "completed" || posts.Load() != 1 {
				t.Fatalf("retained submission key was not reusable on refile: %+v %v", status, err)
			}
			if tc.pdf {
				connection.PluginConfig["allow_pdf"] = false
				before = reads.Load()
				if _, err = adapter.Fulfill(ctx, input); err == nil || reads.Load() != before || posts.Load() != 1 {
					t.Fatal("PDF admitted without connection opt-in")
				}
			}
		})
	}

	t.Run("cancellation-after-send-recovers-read-only", func(t *testing.T) {
		var posts atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})
		fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
			switch r.URL.Path {
			case "/api/v1/monitoring":
				rows := []any{}
				if posts.Load() > 0 {
					rows = append(rows, map[string]string{"id": "accepted-before-cancel", "isbn13": "9780306406157", "status": "searching_now", "selected_format": "epub"})
				}
				write(map[string]any{"requests": rows, "page": 1, "limit": 100, "total": len(rows)})
			case "/api/v1/books":
				write(map[string]any{"books": []any{}, "pagination": map[string]int{"page": 1, "limit": 100, "total_items": 0, "total_pages": 0}})
			case "/api/v1/monitoring/add":
				posts.Add(1)
				close(started)
				<-release
				w.WriteHeader(202)
				write(map[string]string{"request_id": "accepted-before-cancel"})
			default:
				t.Errorf("unexpected backend path %s", r.URL.Path)
				w.WriteHeader(404)
			}
		}))
		defer fixture.Close()
		defer close(release)
		installation := 7
		connection := &requests.Integration{ID: "cancel-connection", Enabled: true, InstallationID: &installation, CapabilityID: "bookwarehouse", SupportedMediaTypes: []string{"book"}, BaseURL: fixture.URL, APIKeyRef: "synthetic-rpc-key", PluginConfig: map[string]any{"allow_http": true}}
		adapter := bloemEbookRouter{integrations: ebookRPCConnection{connection}, resolver: ebookRPCResolver{rpc}, validator: ebookRPCValidator{}}
		input := bookrequests.DispatchInput{Request: bookrequests.Request{ID: "cancel-rpc-request", ISBN: "9780306406157", MediaType: "book"}, Integration: bookrequests.EbookIntegration{ID: connection.ID, InstallationID: installation, CapabilityID: connection.CapabilityID}}
		submitCtx, submitCancel := context.WithCancel(ctx)
		defer submitCancel()
		done := make(chan error, 1)
		go func() { _, err := adapter.Fulfill(submitCtx, input); done <- err }()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("no backend submission observed")
		}
		submitCancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("canceled RPC unexpectedly returned success")
			}
		case <-ctx.Done():
			t.Fatal("canceled RPC did not return")
		}
		receipt, err := adapter.Recover(ctx, input)
		if err != nil || receipt.Status != "unknown" {
			t.Fatalf("Recover=%+v err=%v", receipt, err)
		}
		status, err := adapter.CheckStatus(ctx, input, receipt)
		if err != nil || status.Status != "queued" || posts.Load() != 1 {
			t.Fatalf("recovery=%+v err=%v posts=%d", status, err, posts.Load())
		}
	})
}
