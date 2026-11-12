package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/logredact"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nodeauth"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
	"github.com/Silo-Server/silo-server/internal/transcodeproxy"
)

// A worker reads native storage only when it runs Bloem's native execution
// wrapper with approved storage (cmd/silo configureStandaloneNativeVideo).
// Any other worker would receive a start for an input it cannot open, so the
// API asks first and sends native HLS only to a worker that answers.
const (
	nativeVideoWorkerReadyTTL    = time.Minute
	nativeVideoWorkerNotReadyTTL = 15 * time.Second
	nativeVideoWorkerProbeWait   = 3 * time.Second
)

var errNativeVideoWorkerNotReady = errors.New("worker cannot read native storage")

// NativeVideoWorkerReadiness caches each worker's readiness answer.
type NativeVideoWorkerReadiness struct {
	mu      sync.Mutex
	entries map[nativeVideoWorkerReadinessKey]nativeVideoWorkerReadinessEntry
}

func NewNativeVideoWorkerReadiness() *NativeVideoWorkerReadiness {
	return &NativeVideoWorkerReadiness{entries: make(map[nativeVideoWorkerReadinessKey]nativeVideoWorkerReadinessEntry)}
}

type nativeVideoWorkerReadinessKey struct {
	url string
	id  int
}

type nativeVideoWorkerReadinessEntry struct {
	err   error
	until time.Time
}

// workerNativeReady reports whether node can execute this service's native
// inputs. Answers are cached briefly per worker.
func (s *NativeVideoService) workerNativeReady(ctx context.Context, node *nodepool.Node, secret string) error {
	if s == nil || s.WorkerReadiness == nil || node == nil || node.ID <= 0 || node.URL == "" {
		return errNativeVideoWorkerNotReady
	}
	key := nativeVideoWorkerReadinessKey{url: nodepool.NormalizeNodeURL(node.URL), id: node.ID}
	cache := s.WorkerReadiness
	cache.mu.Lock()
	entry, ok := cache.entries[key]
	cache.mu.Unlock()
	if ok && time.Now().Before(entry.until) {
		return entry.err
	}
	err := probeNativeVideoWorker(ctx, node, secret)
	if ctx.Err() != nil {
		// The caller gave up; that says nothing about the worker.
		return err
	}
	ttl := nativeVideoWorkerReadyTTL
	if err != nil {
		ttl = nativeVideoWorkerNotReadyTTL
	}
	cache.mu.Lock()
	cache.entries[key] = nativeVideoWorkerReadinessEntry{err: err, until: time.Now().Add(ttl)}
	cache.mu.Unlock()
	return err
}

func probeNativeVideoWorker(ctx context.Context, node *nodepool.Node, secret string) error {
	if secret == "" {
		return errNativeVideoWorkerNotReady
	}
	probeCtx, cancel := context.WithTimeout(ctx, nativeVideoWorkerProbeWait)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, nodepool.NodeEndpoint(node.URL, transcodenode.NativeExecutionReadinessPath), nil)
	if err != nil {
		return fmt.Errorf("%w: %w", errNativeVideoWorkerNotReady, logredact.SanitizeURLError(err))
	}
	if err := nodeauth.Sign(request, node.URL, secret); err != nil {
		return fmt.Errorf("%w: %w", errNativeVideoWorkerNotReady, err)
	}
	response, err := transcodeproxy.DoNode(transcodeproxy.NodeClient(), request, node.URL, "native_execution_readiness")
	if err != nil {
		return fmt.Errorf("%w: %w", errNativeVideoWorkerNotReady, logredact.SanitizeURLError(err))
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%w: readiness answered %d", errNativeVideoWorkerNotReady, response.StatusCode)
	}
	var answer transcodenode.NativeExecutionReadiness
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&answer); err != nil {
		return fmt.Errorf("%w: %w", errNativeVideoWorkerNotReady, err)
	}
	// Grants are sealed for the registered worker; a worker answering under
	// another identity would refuse every one of them.
	if !answer.NativeExecution || answer.NodeID != node.ID {
		return errNativeVideoWorkerNotReady
	}
	return nil
}

func nativeVideoWorkerNotReadyErrorV3(cause error) *transportErrorV3 {
	return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "The selected worker cannot read native storage.", retryable: true, cause: cause, executorIneligible: true}
}

// nativeVideoWorkerAdmissionV3 refuses a native HLS start on a worker that
// cannot read native storage, before any grant is sealed or start is sent.
// The route loop then excludes the worker and may fall back to the API.
func (h *PlaybackHandler) nativeVideoWorkerAdmissionV3(ctx context.Context, file *models.MediaFile, node *nodepool.Node) *transportErrorV3 {
	if h.NativeVideo == nil || !NativeVideoSupported(file) {
		return nil
	}
	if err := h.NativeVideo.workerNativeReady(ctx, node, h.JWTSecret); err != nil {
		return nativeVideoWorkerNotReadyErrorV3(err)
	}
	return nil
}
