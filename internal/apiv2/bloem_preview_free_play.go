package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
)

// PreviewFreePlayService is free play's storage side for the preview gate.
type PreviewFreePlayService interface {
	// Busy reports whether the free-play capacity is streaming for others.
	Busy(ctx context.Context, grantID string) (bool, error)
	// BindPlayback records that the grant started the playback session.
	BindPlayback(ctx context.Context, playbackSessionID, grantID string) error
	// StopPlayback ends a playback session free play will not serve.
	StopPlayback(playbackSessionID string)
	// Deliver meters a bearer-authorized media request; false means it wrote
	// the refusal.
	Deliver(w http.ResponseWriter, r *http.Request) (*http.Request, bool)
}

// previewFreePlayOperations open to a guest only while its session holds a
// live free-play grant. Replan stays shut: it could change the delivery and
// the session outside the start's HLS check.
var previewFreePlayOperations = map[string]bool{
	"startPlayback": true, "stopPlayback": true, "updatePlaybackProgress": true,
	"getPlaybackManifest": true, playbackSegmentOperation: true,
	playbackSubtitleOperation: true, playbackSubtitleHead: true,
}

// previewFreePlayMedia are the media reads the meter judges.
var previewFreePlayMedia = map[string]bool{
	"getPlaybackManifest": true, playbackSegmentOperation: true,
	playbackSubtitleOperation: true, playbackSubtitleHead: true,
}

// freePlayStartBodyLimit bounds the start body the gate rewrites.
const freePlayStartBodyLimit = 1 << 20

// serveFreePlay runs a free-play operation for a guest marked with a live
// grant.
func serveFreePlay(svc PreviewFreePlayService, operationID string, next http.Handler, w http.ResponseWriter, r *http.Request) {
	grant, _ := previewcontext.FreePlayGrant(r.Context())
	switch {
	case operationID == "startPlayback":
		startFreePlay(svc, grant, next, w, r)
	case previewFreePlayMedia[operationID]:
		if r, ok := svc.Deliver(w, r); ok {
			next.ServeHTTP(w, r)
		}
	default:
		next.ServeHTTP(w, r)
	}
}

// startFreePlay plans a free-play start that can only become HLS, then binds
// the HLS session to the grant. A plan that comes back otherwise is stopped
// and refused.
func startFreePlay(svc PreviewFreePlayService, grant string, next http.Handler, w http.ResponseWriter, r *http.Request) {
	busy, err := svc.Busy(r.Context(), grant)
	if err != nil {
		writeFreePlayUnavailable(w)
		return
	}
	if busy {
		apimw.WriteFreePlayBusy(w)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, freePlayStartBodyLimit+1))
	var body map[string]any
	if err != nil || len(raw) > freePlayStartBodyLimit || json.Unmarshal(raw, &body) != nil || body == nil {
		apimw.WritePreviewOnly(w)
		return
	}
	// Progress stays on the device; the server then requires the position
	// the player starts from.
	body["progress_persistence"] = "client"
	if body["start_position"] == nil {
		body["start_position"] = 0
	}
	if playback, ok := body["client_playback_context"].(map[string]any); ok {
		if deliveries, ok := playback["deliveries"].(map[string]any); ok {
			for class, value := range deliveries {
				if delivery, ok := value.(map[string]any); ok && class != "hls" {
					delivery["enabled"] = false
				}
			}
		}
	}
	rewritten, err := json.Marshal(body)
	if err != nil {
		apimw.WritePreviewOnly(w)
		return
	}
	r = r.Clone(r.Context())
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(rewritten)), int64(len(rewritten))
	r.Header.Del("Content-Length")
	captured := &capturedResponse{header: http.Header{}, status: http.StatusOK}
	next.ServeHTTP(captured, r)
	if captured.status == http.StatusCreated && !bindFreePlayDecision(svc, grant, captured.body.Bytes(), w, r) {
		return
	}
	for key, values := range captured.header {
		w.Header()[key] = values
	}
	w.WriteHeader(captured.status)
	_, _ = w.Write(captured.body.Bytes())
}

// bindFreePlayDecision binds a playable start decision's HLS session to the
// grant. A decision that is not an HLS transcode stream is stopped and
// refused; false means the refusal is written.
func bindFreePlayDecision(svc PreviewFreePlayService, grant string, body []byte, w http.ResponseWriter, r *http.Request) bool {
	var decision struct {
		SessionID string `json:"session_id"`
		Plan      struct {
			Stream struct {
				URL string `json:"url"`
			} `json:"stream"`
		} `json:"playback_plan"`
	}
	if json.Unmarshal(body, &decision) != nil || decision.SessionID == "" {
		return true // a terminal decision starts nothing
	}
	if !strings.Contains(decision.Plan.Stream.URL, "/playback/transcode/"+decision.SessionID+"/") {
		svc.StopPlayback(decision.SessionID)
		apimw.WritePreviewOnly(w)
		return false
	}
	if err := svc.BindPlayback(r.Context(), decision.SessionID, grant); err != nil {
		svc.StopPlayback(decision.SessionID)
		writeFreePlayUnavailable(w)
		return false
	}
	return true
}

func writeFreePlayUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"unavailable","message":"Free play is temporarily unavailable."}`))
}

// capturedResponse holds a start decision until the gate has judged it.
type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (c *capturedResponse) Header() http.Header { return c.header }
func (c *capturedResponse) WriteHeader(status int) {
	if !c.wrote {
		c.status, c.wrote = status, true
	}
}
func (c *capturedResponse) Write(p []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	return c.body.Write(p)
}
