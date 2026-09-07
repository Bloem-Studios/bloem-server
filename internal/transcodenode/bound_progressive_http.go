package transcodenode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// BoundProgressiveHTTPV3 is wired by the configured worker owner. The owner
// supplies node authentication, registration, approved paths and shutdown hooks.
// None of these handlers is mounted on the legacy remux GET path.
type BoundProgressiveHTTPV3 struct {
	mu                     sync.Mutex
	prepared               map[BoundProgressiveCommandV3]*playback.PreparedBoundProgressiveV3
	NodeID                 func() (int, bool)
	ApproveSource          func(context.Context, string) error
	Resolve                func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error)
	Registry               *playback.BoundProgressiveRegistryV3
	OutputRoot, FFmpegPath string
	Transfers              playback.ExecutorOutputTransferGrantProviderV3
}

type BoundProgressiveCommandV3 struct {
	TransportID  string                       `json:"transport_id"`
	Executor     playback.ExecutorNamespaceV3 `json:"executor"`
	RecipeDigest string                       `json:"recipe_digest"`
}

type BoundProgressiveReadyV3 struct {
	BoundProgressiveCommandV3
	Status string `json:"status"`
}

func BoundProgressiveCommandForRecipeV3(card playback.RecipeCard) (BoundProgressiveCommandV3, error) {
	if err := playback.ValidateBoundProgressiveRecipeV3(card); err != nil {
		return BoundProgressiveCommandV3{}, err
	}
	digest, err := playback.BoundProgressiveRecipeDigestV3(card)
	return BoundProgressiveCommandV3{TransportID: card.TranscodeTransportID, Executor: *card.Executor, RecipeDigest: digest}, err
}

func ValidateBoundProgressiveReadyV3(command BoundProgressiveCommandV3, ready BoundProgressiveReadyV3) error {
	if ready.BoundProgressiveCommandV3 != command || ready.Status != "ready" {
		return errors.New("worker did not confirm exact progressive readiness")
	}
	return nil
}

func decodeProgressiveRequest(w http.ResponseWriter, r *http.Request, body any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing progressive request data")
	}
	return nil
}

func (h *BoundProgressiveHTTPV3) prepare(ctx context.Context, card playback.RecipeCard) (*playback.PreparedBoundProgressiveV3, error) {
	if h.NodeID == nil || h.ApproveSource == nil || h.Resolve == nil || h.Registry == nil {
		return nil, errors.New("progressive worker unavailable")
	}
	node, ok := h.NodeID()
	if !ok || node <= 0 || card.RoutingExecution != "transcode" || card.RoutingExecutionNodeID != node {
		return nil, errors.New("progressive worker identity mismatch")
	}
	if err := h.ApproveSource(ctx, card.InputPath); err != nil {
		return nil, err
	}
	return playback.PrepareBoundProgressiveV3(ctx, card, h.OutputRoot, h.FFmpegPath)
}

func (h *BoundProgressiveHTTPV3) Prepare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method unavailable", 405)
		return
	}
	var body ExecutorPreparation
	if decodeProgressiveRequest(w, r, &body) != nil {
		http.Error(w, "invalid progressive preparation", 400)
		return
	}
	prepared, err := h.prepare(r.Context(), body.Recipe)
	if err != nil {
		http.Error(w, "progressive preparation unavailable", 422)
		return
	}
	command, err := BoundProgressiveCommandForRecipeV3(prepared.Recipe())
	if err != nil {
		http.Error(w, "invalid prepared recipe", 422)
		return
	}
	h.mu.Lock()
	if h.prepared == nil {
		h.prepared = make(map[BoundProgressiveCommandV3]*playback.PreparedBoundProgressiveV3)
	}
	for existing := range h.prepared {
		if existing.Executor == command.Executor {
			h.mu.Unlock()
			http.Error(w, "progressive preparation already retained", 409)
			return
		}
	}
	h.prepared[command] = prepared
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecutorPreparation{Recipe: prepared.Recipe()})
}

func (h *BoundProgressiveHTTPV3) Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method unavailable", 405)
		return
	}
	var command BoundProgressiveCommandV3
	if decodeProgressiveRequest(w, r, &command) != nil || command.Executor.Validate() != nil || command.TransportID == "" {
		http.Error(w, "invalid progressive start", 400)
		return
	}
	if h.Resolve == nil || h.Registry == nil {
		http.Error(w, "progressive authority unavailable", 503)
		return
	}
	card, err := h.Resolve(r.Context(), command.TransportID, command.Executor)
	if err != nil || card == nil {
		http.Error(w, "progressive authority unavailable", 503)
		return
	}
	expected, err := BoundProgressiveCommandForRecipeV3(*card)
	if err != nil || expected != command {
		http.Error(w, "progressive recipe mismatch", 409)
		return
	}
	if h.NodeID == nil || h.ApproveSource == nil {
		http.Error(w, "progressive worker unavailable", 503)
		return
	}
	node, known := h.NodeID()
	if !known || node <= 0 || card.RoutingExecutionNodeID != node || h.ApproveSource(r.Context(), card.InputPath) != nil {
		http.Error(w, "progressive worker authority changed", 503)
		return
	}
	h.mu.Lock()
	prepared := h.prepared[command]
	delete(h.prepared, command)
	h.mu.Unlock()
	if prepared == nil {
		http.Error(w, "progressive preparation unavailable", 503)
		return
	}
	if err = h.Registry.Start(r.Context(), prepared); err != nil {
		http.Error(w, "progressive readiness unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(BoundProgressiveReadyV3{BoundProgressiveCommandV3: command, Status: "ready"})
}

// BoundProgressiveOutputQueryV3 carries only the exact namespace on the internal
// worker hop. The permit and retained registry independently establish authority.
func BoundProgressiveOutputQueryV3(ns playback.ExecutorNamespaceV3) string {
	values := url.Values{"incarnation": {ns.Incarnation}, "epoch": {strconv.FormatInt(ns.Epoch, 10)}, "executor_id": {ns.ExecutorID}}
	return values.Encode()
}

func (h *BoundProgressiveHTTPV3) Output(w http.ResponseWriter, r *http.Request) {
	epoch, err := strconv.ParseInt(r.URL.Query().Get("epoch"), 10, 64)
	ns := playback.ExecutorNamespaceV3{Incarnation: r.URL.Query().Get("incarnation"), Epoch: epoch, ExecutorID: r.URL.Query().Get("executor_id")}
	permit := r.Header.Get(playback.OutputTransferHeaderV3)
	if err != nil || ns.Validate() != nil || permit == "" || h.Registry == nil || h.Transfers == nil {
		http.Error(w, "progressive transfer unavailable", 503)
		return
	}
	acquire := func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		grant, err := h.Transfers(ctx, transport, executor, permit)
		if grant != nil && grant.Request().OutputTransferID != permit {
			grant.Close()
			return nil, errors.New("progressive permit mismatch")
		}
		return grant, err
	}
	tracked := &progressiveOutputWriter{ResponseWriter: w}
	if err := h.Registry.ServeHTTP(tracked, r, r.PathValue("transport"), ns, acquire, playback.AttemptGrantTransferV3); err != nil {
		if tracked.started {
			panic(http.ErrAbortHandler)
		}
		http.Error(w, "progressive output unavailable", 503)
	}
}

// Preserve deadline traversal while distinguishing pre-header refusal from an
// interrupted media response, which must be aborted rather than append text.
type progressiveOutputWriter struct {
	http.ResponseWriter
	started bool
}

func (w *progressiveOutputWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *progressiveOutputWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *progressiveOutputWriter) Write(data []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(data)
}
