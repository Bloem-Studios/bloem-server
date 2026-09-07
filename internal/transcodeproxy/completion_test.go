package transcodeproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestAcknowledgeSurvivesCompletedDownstreamRequest(t *testing.T) {
	const (
		jwtSecret  = "test-secret"
		generation = "incarnation:generation"
	)

	acknowledged := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+jwtSecret {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get(GenerationHeader); got != generation {
			t.Errorf("generation = %q, want %q", got, generation)
		}
		acknowledged <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	if err := Acknowledge(requestCtx, server.Client(), server.URL+"/segment", jwtSecret, generation); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}
	select {
	case <-acknowledged:
	default:
		t.Fatal("acknowledgement request was not delivered")
	}
}

func TestAcknowledgeExecutorPreservesAuthorityAndRefusesRedirect(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get(playback.OutputTransferHeaderV3) != "permit" || r.Header.Get("X-Silo-Stream-Token") != "token" || r.Header.Get(GenerationHeader) != "generation" {
			t.Error("bound acknowledgement lost exact authority")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	if err := AcknowledgeExecutor(t.Context(), server.Client(), server.URL, "secret", "generation", "token", "permit"); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 1 || reached.Load() != 0 {
		t.Fatal("bound acknowledgement followed redirect or replayed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := AcknowledgeExecutor(ctx, server.Client(), server.URL, "secret", "generation", "token", "permit"); err == nil {
		t.Fatal("cancelled authority sent acknowledgement")
	}
	if calls.Load() != 1 {
		t.Fatal("cancelled authority reached worker")
	}
	for _, credentials := range [][2]string{{"", "permit"}, {"token", ""}} {
		if err := AcknowledgeExecutor(t.Context(), server.Client(), server.URL, "secret", "generation", credentials[0], credentials[1]); err == nil {
			t.Fatal("missing authority accepted")
		}
	}
}

func TestCopyResponseHeadersKeepsTransferPermitPrivate(t *testing.T) {
	source := http.Header{playback.OutputTransferHeaderV3: []string{"private"}, GenerationHeader: []string{"generation"}, "Content-Type": []string{"video/mp2t"}}
	destination := http.Header{}
	CopyResponseHeaders(destination, source)
	if destination.Get(playback.OutputTransferHeaderV3) != "" || destination.Get(GenerationHeader) != "" || destination.Get("Content-Type") != "video/mp2t" {
		t.Fatal("private header escaped or public metadata lost")
	}
}
