package api

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// A start retried on another worker, or with a fallback recipe, replaces the
// earlier grant.
func TestNativeVideoCompatWorkerStartRetryReplacesGrant(t *testing.T) {
	client := compatWorkerRedis(t)
	f := newCompatServeFixtureFor(t, uuid.NewString())
	f.service.WorkerGrants = handlers.NewNativeVideoWorkerGrantStore(client)
	probes := &atomic.Int32{}
	first, second := compatWorker(t, 7, probes), compatWorker(t, 9, probes)
	start, body := f.start()
	if err := f.host.SealWorkerStart(f.workerContext(), first.URL, body); err != nil {
		t.Fatal(err)
	}
	retry := start
	retry.TargetCodecVideo = "hevc"
	retryBody, _ := json.Marshal(retry)
	if err := f.host.SealWorkerStart(f.workerContext(), second.URL, retryBody); err != nil {
		t.Fatalf("retry on another worker: %v", err)
	}
	grants := f.service.WorkerGrants
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 7, TransportID: start.SessionID, InputPath: start.InputPath, Start: &start}, f.service); err == nil {
		t.Fatal("the replaced worker's grant still resolves")
	}
	if _, err := grants.Resolve(t.Context(), transcodenode.NativeExecutionRequest{WorkerID: 9, TransportID: retry.SessionID, InputPath: retry.InputPath, Start: &retry}, f.service); err != nil {
		t.Fatalf("retried start refused: %v", err)
	}
}
