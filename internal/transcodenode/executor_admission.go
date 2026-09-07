package transcodenode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

// ExecutorPreparation is input-only. A successful reply never grants execution
// or reserves an output directory. The caller must publish the returned recipe
// immutably before making its one bound start request.
type ExecutorPreparation struct {
	Recipe playback.RecipeCard `json:"recipe"`
}

func validInitialWorkerRecipe(card playback.RecipeCard) bool {
	return card.Executor != nil && card.Executor.Validate() == nil && card.SessionID != "" && card.TranscodeTransportID != "" &&
		card.RoutingExecution == string(noderouting.ExecutionTranscode) && card.RoutingExecutionNodeID > 0 &&
		card.RoutingWorkload == string(noderouting.WorkloadVideoTranscode) &&
		((card.RoutingEgress == string(noderouting.EgressAPI) && card.RoutingEgressNodeID == 0) ||
			(card.RoutingEgress == string(noderouting.EgressProxy) && card.RoutingEgressNodeID > 0)) &&
		card.PlayMethod == playback.PlayTranscode && !card.VideoStreamCopy() && recipeIsComplete(card) && !card.AudioOnly
}

// ValidateExecutorPreparation accepts only worker-local policy resolution.
// All source facts, attribution, routing and playback identities stay captured.
func ValidateExecutorPreparation(proposed, prepared playback.RecipeCard) error {
	if !validInitialWorkerRecipe(proposed) || !validInitialWorkerRecipe(prepared) {
		return errors.New("invalid initial worker recipe")
	}
	if proposed.ToneMapPolicy != prepared.ToneMapPolicy && !(proposed.ToneMapPolicy == tonemap.PolicyNone && prepared.ToneMapPolicy == "") {
		return errors.New("worker preparation changed tone-map policy")
	}
	expected := proposed
	expected.HWAccel, expected.HWDevice = prepared.HWAccel, prepared.HWDevice
	expected.SoftwareVideoDecode = prepared.SoftwareVideoDecode
	expected.ToneMapPolicy, expected.ToneMapFilter = prepared.ToneMapPolicy, prepared.ToneMapFilter
	// Match the published JSON representation: time.Time carries a process-local
	// monotonic component that correctly disappears at the worker boundary.
	expectedJSON, expectedErr := json.Marshal(expected)
	preparedJSON, preparedErr := json.Marshal(prepared)
	if expectedErr != nil || preparedErr != nil || !bytes.Equal(expectedJSON, preparedJSON) || prepared.HWAccel == "" || prepared.HWAccel == "auto" {
		return errors.New("worker preparation changed captured recipe")
	}
	return nil
}

func (s *Server) handlePrepareExecutor(w http.ResponseWriter, r *http.Request) {
	if s.executorGrants == nil || s.executorRecipeResolver == nil {
		http.Error(w, "executor preparation is not configured", http.StatusServiceUnavailable)
		return
	}
	var request ExecutorPreparation
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || !validInitialWorkerRecipe(request.Recipe) {
		http.Error(w, "invalid executor recipe", http.StatusBadRequest)
		return
	}
	card := request.Recipe
	if s.nodeRowID == nil {
		http.Error(w, "node identity unavailable", http.StatusServiceUnavailable)
		return
	}
	nodeID, ok := s.nodeRowID()
	if !ok || nodeID != card.RoutingExecutionNodeID {
		http.Error(w, "recipe selects another execution node", http.StatusConflict)
		return
	}
	if !s.requireApprovedInputPath(w, r, card.InputPath) {
		return
	}
	cfg := s.watcher.Config()
	if cfg == nil {
		http.Error(w, "node configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	output, err := card.Executor.OutputDir(cfg.Playback.TranscodeDir)
	if err != nil {
		http.Error(w, "invalid output namespace", http.StatusBadRequest)
		return
	}
	// Hold the configuration generation stable through preparation. Hardware
	// probes/source validation are allowed; no playback process or output claim
	// is made here.
	s.reloadMu.RLock()
	defer s.reloadMu.RUnlock()
	if s.shuttingDown || s.watcher.Config() != cfg || !s.gpu.beginWork() {
		http.Error(w, "node preparation unavailable", http.StatusServiceUnavailable)
		return
	}
	defer s.gpu.endWork()
	opts := card.TranscodeOpts(output, cfg.Playback.FFmpegPath, s.ffmpegSink)
	opts.HWAccel, opts.HWDevice = cfg.Playback.HWAccel, cfg.Playback.HWDevice
	prepared, err := playback.PrepareFrozenTranscodeOpts(r.Context(), opts)
	if err != nil {
		http.Error(w, "executor recipe preparation failed", http.StatusUnprocessableEntity)
		return
	}
	card.HWAccel, card.HWDevice = prepared.HWAccel, prepared.HWDevice
	card.SoftwareVideoDecode = prepared.SoftwareVideoDecode
	card.ToneMapPolicy, card.ToneMapFilter = prepared.ToneMapPolicy, prepared.ToneMapFilter
	if err := ValidateExecutorPreparation(request.Recipe, card); err != nil {
		http.Error(w, "executor policy was not frozen", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecutorPreparation{Recipe: card})
}

// BoundTranscodeStartRequest projects a published recipe onto the existing
// worker command. The digest covers the complete recipe, including identity and
// worker-local device policy not carried in the legacy start fields.
func BoundTranscodeStartRequest(card playback.RecipeCard) (TranscodeStartRequest, error) {
	if !validInitialWorkerRecipe(card) || card.HWAccel == "" || card.HWAccel == "auto" {
		return TranscodeStartRequest{}, errors.New("frozen initial worker recipe required")
	}
	data, err := json.Marshal(card)
	if err != nil {
		return TranscodeStartRequest{}, err
	}
	digest := sha256.Sum256(data)
	request := TranscodeStartRequest{
		Executor: new(*card.Executor), ExecutorRecipeDigest: hex.EncodeToString(digest[:]), SessionID: card.TranscodeTransportID, RequireReady: true,
		InputPath: card.InputPath, SourceVideoCodec: card.SourceVideoCodec, SourceVideoProfile: card.SourceVideoProfile, SourceVideoBitDepth: card.SourceVideoBitDepth,
		SourceAudioChannels: card.SourceAudioChannels, CopyFMP4RecipeVersion: card.CopyFMP4RecipeVersion, SoftwareVideoDecode: card.SoftwareVideoDecode,
		ToneMapPolicy: card.ToneMapPolicy, ToneMapMode: card.ToneMapMode, ToneMapSourceKind: card.ToneMapSourceKind, ToneMapRecipeVersion: card.ToneMapRecipeVersion,
		ToneMapPreflightRequired: card.ToneMapPreflightRequired, ToneMapSourceRevision: card.ToneMapSourceRevision, ToneMapDVConfigPresent: card.ToneMapDVConfigPresent,
		ToneMapDVBLCompatIDPresent: card.ToneMapDVBLCompatIDPresent, ToneMapDVBLPresent: card.ToneMapDVBLPresent, ToneMapDVRPUPresent: card.ToneMapDVRPUPresent,
		VideoBitstreamFilter: card.VideoBitstreamFilter, VideoSampleEntry: card.VideoSampleEntry, CopyVideoMPEGTS: card.CopyVideoMPEGTS,
		SeekSeconds: card.SeekSeconds, StreamOriginSeconds: card.StreamOriginSeconds, CopySeekAnchorResolved: card.CopySeekAnchorResolved,
		StartSegmentNumber: card.StartSegmentNumber, TargetResolution: card.TargetResolution, TargetCodecVideo: card.TargetCodecVideo, TargetCodecAudio: card.TargetCodecAudio,
		TargetAudioChannels: card.TargetAudioChannels, TargetAudioBitrateKbps: card.TargetAudioBitrateKbps, TargetBitrateKbps: card.TargetBitrateKbps,
		SegmentDuration: card.SegmentDuration, HWAccel: card.HWAccel, AudioTrackIndex: card.AudioTrackIndex, SubtitleTrackIndex: card.SubtitleTrackIndex,
		SubtitleBurnIn: card.SubtitleBurnIn, SubtitleCodec: card.SubtitleCodec, TotalDuration: card.TotalDuration, ThrottleSeconds: card.ThrottleSeconds,
	}
	if playback.IsAudioToAACStereoDownmixV3(card.SourceAudioChannels, card.TargetCodecAudio, card.TargetAudioChannels) {
		request.AudioRecipeVersion = playback.TransformationAudioToAACRecipeVersionV3
		request.TargetAudioChannels = 2
	} else {
		request.SourceAudioChannels = 0
	}
	return request, nil
}

func ValidateBoundTranscodeStartResponse(request TranscodeStartRequest, response TranscodeStartResponse) error {
	if request.Executor == nil || request.ExecutorRecipeDigest == "" || !request.RequireReady ||
		playback.MatchExecutorNamespace(response.Executor, request.Executor) != nil || response.ExecutorRecipeDigest != request.ExecutorRecipeDigest ||
		response.SessionID != request.SessionID || response.Status != "started" || response.HWAccel != request.HWAccel || response.ToneMapMode != request.ToneMapMode {
		return errors.New("worker did not confirm exact ready executor recipe")
	}
	return errors.Join(ValidateAudioRecipeAttestation(request, response), ValidateCopyFMP4RecipeAttestation(request, response), ValidateThrottleAttestation(request, response))
}

func (s *Server) authorizeExecutorStart(w http.ResponseWriter, r *http.Request, request TranscodeStartRequest) (*playback.RecipeCard, bool) {
	s.mu.RLock()
	existing := s.sessions[request.SessionID]
	s.mu.RUnlock()
	if existing != nil {
		http.Error(w, "executor replacement requires a new identity", http.StatusConflict)
		return nil, false
	}
	if s.executorGrants == nil || s.executorRecipeResolver == nil {
		http.Error(w, "executor admission is not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	card, err := s.executorRecipeResolver(r.Context(), request.SessionID, *request.Executor)
	if err != nil || card == nil {
		http.Error(w, "executor recipe authority unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	if s.nodeRowID == nil {
		http.Error(w, "node identity unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	nodeID, ok := s.nodeRowID()
	if !ok || nodeID != card.RoutingExecutionNodeID {
		http.Error(w, "recipe selects another execution node", http.StatusConflict)
		return nil, false
	}
	expected, err := BoundTranscodeStartRequest(*card)
	if err != nil || !reflect.DeepEqual(expected, request) {
		http.Error(w, "submitted recipe differs from authority", http.StatusConflict)
		return nil, false
	}
	return card, true
}
