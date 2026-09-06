package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/google/uuid"
)

func TestPrepareInitialLocalTranscodeFreezesWithoutStarting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	h := &PlaybackHandler{PlaybackConfig: func() config.PlaybackConfig {
		return config.PlaybackConfig{TranscodeDir: root, HWAccel: playback.HWAccelNone, FFmpegPath: "/not/a/real/ffmpeg"}
	}}
	session := &playback.Session{ID: uuid.NewString(), UserID: 1, ProfileID: "p", TranscodeTransportID: uuid.NewString(), Executor: &playback.ExecutorNamespaceV3{Incarnation: uuid.NewString(), Epoch: 1, ExecutorID: uuid.NewString()}, AudioTrackIndex: 3}
	file := &models.MediaFile{ID: 42, FilePath: "/not/a/real/movie.mkv", CodecVideo: "hevc", CodecAudio: "aac", Duration: 100}
	plan := &playback.PlanV3{SessionID: session.ID, Delivery: playback.DeliveryTranscodeHLSV3, Timeline: playback.TimelineV3{SourceStartSeconds: 13}}
	result := playback.PlannerResultV3{Plan: plan, PlayMethod: playback.PlayTranscode, TargetVideoCodec: "h264", TargetAudioCodec: "aac", TargetAudioChannels: 2, TargetAudioBitrateKbps: 192, SourceAudioChannels: 6, TargetResolution: "720p", TargetBitrateKbps: 2500, SubtitleTransportTrackIndex: 4, SubtitleBurnIn: true, SubtitleCodec: "ass"}
	card, opts, err := h.prepareInitialLocalTranscodeV3(t.Context(), session, file, result)
	if err != nil {
		t.Fatal(err)
	}
	expectedSubdir, _ := session.Executor.OutputSubdir()
	if opts.OutputSubdir != expectedSubdir || opts.OutputDir != filepath.Join(root, expectedSubdir) || opts.Executor == session.Executor || *opts.Executor != *session.Executor || opts.TranscodeTransportID != session.TranscodeTransportID {
		t.Fatalf("namespace not frozen: %+v", opts)
	}
	if opts.SeekSeconds != 12 || opts.StartSegmentNumber != 6 || opts.StreamOriginSeconds != 0 || opts.CopySeekAnchorResolved {
		t.Fatalf("timeline: seek=%v segment=%v origin=%v copy=%v", opts.SeekSeconds, opts.StartSegmentNumber, opts.StreamOriginSeconds, opts.CopySeekAnchorResolved)
	}
	if opts.SubtitleTrackIndex != 4 || !opts.SubtitleBurnIn || opts.SubtitleCodec != "ass" || opts.AudioTrackIndex != 3 || opts.TargetAudioBitrateKbps != 192 || opts.TargetAudioChannels != 2 {
		t.Fatalf("tracks not preserved: %+v", opts)
	}
	if card.RoutingWorkload != string(noderouting.WorkloadVideoTranscode) || card.RoutingExecution != "api" || card.RoutingEgress != "api" || card.TranscodeTransportID != session.TranscodeTransportID || card.TargetCodecAudio != "aac" {
		t.Fatalf("card: %+v", card)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("preparation allocated output: %v", err)
	}
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryRemuxHLSV3, playback.DeliveryRemuxProgressiveV3} {
		plan.Delivery = delivery
		if _, _, err := h.prepareInitialLocalTranscodeV3(t.Context(), session, file, result); err == nil {
			t.Fatal("unsupported remux accepted")
		}
	}
	plan.Delivery = playback.DeliveryTranscodeHLSV3
	result.TargetVideoCodec = "copy"
	if _, _, err := h.prepareInitialLocalTranscodeV3(t.Context(), session, file, result); err == nil {
		t.Fatal("copy accepted")
	}
	result.TargetVideoCodec = "h264"
	result.ToneMapMode = tonemap.ModeHardware
	if _, _, err := h.prepareInitialLocalTranscodeV3(t.Context(), session, file, result); err == nil {
		t.Fatal("incomplete tone-map policy accepted")
	}
	session.Executor = nil
	if _, _, err := h.prepareInitialLocalTranscodeV3(t.Context(), session, file, result); err == nil {
		t.Fatal("unbound session accepted")
	}
}
