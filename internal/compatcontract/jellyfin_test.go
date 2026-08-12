package compatcontract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRunRejectsUnredactedSecrets(t *testing.T) {
	report, err := Run(context.Background(), Target{
		BaseURL: "http://127.0.0.1:8096?token=secret",
		Client:  http.DefaultClient,
	}, JellyfinBaseline())
	if err == nil || strings.Contains(report.JSON(), "secret") {
		t.Fatalf("err=%v report=%s", err, report.JSON())
	}
}

func TestRunChecksHTTPContracts(t *testing.T) {
	binary := []byte("fixture-bytes")
	checksum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("X-Contract", "present")
			_, _ = w.Write([]byte(`{"items":[2,1],"name":"fixture"}`))
		case "/binary":
			_, _ = w.Write(binary)
		case "/denied":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "http",
		Cases: []Case{
			{Name: "semantic json", Method: http.MethodGet, Path: "/json", WantStatus: http.StatusOK, WantHeaders: map[string]string{"X-Contract": "present"}, WantJSON: []byte(`{"name":"fixture","items":[2,1]}`)},
			{Name: "binary checksum", Method: http.MethodGet, Path: "/binary", WantStatus: http.StatusOK, WantSHA256: hex.EncodeToString(checksum[:])},
			{Name: "named unauthenticated exception", Method: http.MethodGet, Path: "/denied", Exception: ExceptionUnauthenticated},
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; report=%s", err, report.JSON())
	}
	if len(report.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(report.Results))
	}
}

func TestRunChecksRequiredAndExcludedFixtureIdentifiers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":["ordinary-item-001"]}`))
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "visibility",
		Cases: []Case{{
			Name:           "ordinary catalog excludes adult item",
			Path:           "/items",
			WantStatus:     http.StatusOK,
			PresentStrings: []string{"ordinary-item-001"},
			AbsentStrings:  []string{"adult-item-001"},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunPreservesFixtureQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "fixture" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "query",
		Cases: []Case{{
			Name:       "query path",
			Method:     http.MethodGet,
			Path:       "/resource?query=fixture",
			WantStatus: http.StatusNoContent,
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunChecksWebSocketMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"MessageType":"KeepAlive"}`)); err != nil {
			t.Errorf("WriteMessage: %v", err)
		}
	}))
	defer server.Close()

	report, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "websocket",
		Cases: []Case{{
			Name:              "keepalive",
			Path:              "/socket",
			WantWebSocketJSON: []json.RawMessage{json.RawMessage(`{"MessageType":"KeepAlive"}`)},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v; report=%s", err, report.JSON())
	}
}

func TestRunBoundsNegativeTimingDistribution(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, err := Run(context.Background(), Target{BaseURL: server.URL, Client: server.Client()}, Suite{
		Name: "timing",
		Cases: []Case{{
			Name:       "adult and random missing IDs are indistinguishable",
			Method:     http.MethodGet,
			Path:       "/missing-adult-item-001",
			WantStatus: http.StatusNotFound,
			Timing: &TimingDistribution{
				ControlPath: "/missing-random-item-002",
				Samples:     3,
				MaxRatio:    20,
			},
		}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
