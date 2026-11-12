package transcodenode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A worker answers the native-execution readiness probe only when it runs
// Bloem's native execution wrapper with approved storage. The API sends a
// native HLS start only to a worker that answers it.
func TestNativeExecutionReadinessAnswersOnlyWhenConfigured(t *testing.T) {
	resolve := func(context.Context, NativeExecutionRequest) (*NativeExecutionGrant, error) {
		return nil, errors.New("not used")
	}
	ordinary := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	server := newTestServer(t)
	wrapped := server.WrapNativeExecution(ordinary, NativeExecutionConfig{NodeID: 7, Resolve: resolve})

	request := httptest.NewRequest(http.MethodGet, NativeExecutionReadinessPath, nil)
	response := httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated probe = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, NativeExecutionReadinessPath, nil)
	request.Header.Set("Authorization", "Bearer "+testSecret)
	response = httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	var body NativeExecutionReadiness
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.NativeExecution || body.NodeID != 7 {
		t.Fatalf("configured probe = %d %q", response.Code, response.Body.String())
	}

	unconfigured := server.WrapNativeExecution(ordinary, NativeExecutionConfig{})
	request = httptest.NewRequest(http.MethodGet, NativeExecutionReadinessPath, nil)
	request.Header.Set("Authorization", "Bearer "+testSecret)
	response = httptest.NewRecorder()
	unconfigured.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("a worker without native execution authority reported itself ready")
	}

	// A worker without the wrapper (no approvals) serves only its ordinary
	// routes, which do not know the probe.
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("ordinary worker answered the readiness probe with %d", response.Code)
	}
}
