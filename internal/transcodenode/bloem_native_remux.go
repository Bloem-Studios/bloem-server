package transcodenode

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/nodesessions"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// handleNativeSourceRemux shares the node's existing route authority, stop
// fences, lifecycle locks and shutdown registry; only source execution differs.
func (s *Server) handleNativeSourceRemux(w http.ResponseWriter, r *http.Request, candidate *NativeExecutionRequest, grant *NativeExecutionGrant) {
	claims, transport := candidate.Claims, candidate.TransportID
	cfg := s.watcher.Config()
	if cfg == nil || claims == nil || candidate.Recipe == nil {
		http.Error(w, "native remux recipe unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.claimsDenied(r.Context(), claims, transport) {
		writeStreamDenied(w)
		return
	}
	if claims.TranscodeNode == "" || claims.RoutingWorkload != string(noderouting.WorkloadRemux) || claims.RoutingExecution != string(noderouting.ExecutionTranscode) || claims.RoutingEgress != string(noderouting.EgressProxy) || !s.progressiveRemuxRunsOnThisNode(claims) ||
		(claims.PlayMethod != string(playback.PlayRemux) && claims.PlayMethod != streamtoken.PlayMethodAudioDownmixRemux) {
		http.Error(w, "routing policy unsatisfied", http.StatusServiceUnavailable)
		return
	}
	if claims.PlayMethod == streamtoken.PlayMethodAudioDownmixRemux && (!claims.TranscodeAudio || claims.TargetAudioChannels != 2 || !playback.IsAudioToAACStereoDownmixV3(claims.SourceAudioChannels, claims.TargetCodecAudio, claims.TargetAudioChannels)) {
		http.NotFound(w, r)
		return
	}
	seek := 0.0
	if value := r.URL.Query().Get("seek"); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			http.Error(w, "invalid seek", http.StatusBadRequest)
			return
		}
		seek = parsed
	}
	s.reloadMu.RLock()
	if s.shuttingDown || s.watcher.Config() != cfg {
		s.reloadMu.RUnlock()
		http.Error(w, "node configuration changed", http.StatusServiceUnavailable)
		return
	}
	unlock := s.lockSessionLifecycle(transport)
	if !s.progressiveRemuxAuthorityActive(r.Context(), transport, claims) {
		unlock()
		s.reloadMu.RUnlock()
		http.Error(w, "session stopped", http.StatusGone)
		return
	}
	s.mu.Lock()
	now := time.Now()
	s.pruneStoppedProgressiveRemuxesLocked(now)
	stopped := s.stoppedProgressiveRemuxes[transport].After(now)
	_, active := s.progressiveRemuxes[transport]
	s.mu.Unlock()
	if stopped || active && r.Method != http.MethodHead {
		unlock()
		s.reloadMu.RUnlock()
		if stopped {
			http.Error(w, "session stopped", http.StatusGone)
		} else {
			http.Error(w, "session already active", http.StatusConflict)
		}
		return
	}
	opts := candidate.Recipe.TranscodeOpts("", cfg.Playback.FFmpegPath, nil)
	opts.InputPath, opts.InputSource, opts.InputFormat, opts.SessionID, opts.SeekSeconds = grant.InputPath, grant.InputSource, grant.InputFormat, transport, seek
	if r.Method == http.MethodHead {
		// Admission/pinned Stat happen in the factory, then Run joins an empty
		// consumer before HEAD succeeds; no FFmpeg process or object read.
		input, err := grant.InputSource.Open(r.Context(), opts)
		if err == nil && input != nil {
			err = input.Run(func(context.Context, string) error { return nil })
			if closeErr := input.Close(); err == nil {
				err = closeErr
			}
		}
		unlock()
		s.reloadMu.RUnlock()
		if err != nil || input == nil {
			http.Error(w, "native source unavailable", http.StatusServiceUnavailable)
			return
		}
		contentType := playback.RemuxContentType(claims.AudioOnly)
		if contentType == "" {
			contentType = "video/mp4"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	abort, done := make(chan struct{}), make(chan struct{})
	stop := sync.OnceFunc(func() { cancel(); close(abort) })
	requestID := s.progressiveRemuxSequence.Add(1)
	s.mu.Lock()
	if s.progressiveRemuxes == nil {
		s.progressiveRemuxes = make(map[string]progressiveRemuxRequest)
	}
	s.progressiveRemuxes[transport] = progressiveRemuxRequest{id: requestID, playbackSessionID: claims.SessionID, cancel: stop, done: done}
	s.mu.Unlock()
	unlock()
	s.reloadMu.RUnlock()
	defer func() {
		cancel()
		s.mu.Lock()
		if current, ok := s.progressiveRemuxes[transport]; ok && current.id == requestID {
			delete(s.progressiveRemuxes, transport)
		}
		s.mu.Unlock()
		close(done)
	}()
	s.activeJobs.Add(1)
	defer s.activeJobs.Add(-1)
	if s.tracker != nil {
		s.tracker.Track(ctx, nodesessions.SessionInfo{SessionID: claims.SessionID, NodeURL: s.tracker.NodeURL(), NodeName: s.tracker.NodeName(), Type: "remux", AuthUserID: claims.UserID, ProfileID: claims.ProfileID, MediaFileID: claims.MediaFileID})
		defer s.tracker.Remove(context.WithoutCancel(ctx), claims.SessionID)
	}
	request := r.WithContext(ctx)
	s.attachTelemetrySession(request, transport)
	if err := playback.ServeSourceRemux(w, request, opts, "mp4", claims.TranscodeAudio, claims.DVProfile, playback.RemuxServeOptions{
		DVMode: playback.RemuxDVMode(claims.RemuxDVMode), FFmpegPath: cfg.Playback.FFmpegPath, DropResumeLeadingPictures: claims.RemuxResumeLeadingPictureDrop,
		ContentType: playback.RemuxContentType(claims.AudioOnly), AudioOnly: claims.AudioOnly, SourceAudioChannels: claims.SourceAudioChannels, TargetAudioChannels: claims.TargetAudioChannels, TargetAudioBitrateKbps: claims.TargetAudioBitrateKbps, Abort: abort,
	}); err != nil {
		http.Error(w, "native remux unavailable", http.StatusServiceUnavailable)
	}
}
