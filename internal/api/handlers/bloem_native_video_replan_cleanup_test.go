package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

type nativeVideoReplanFile struct {
	NativeVideoFile
	ctx context.Context
}

func (f *nativeVideoReplanFile) Context() context.Context { return f.ctx }

func TestNativeVideoIdentityReplanOwnsPreviousExecutionCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("blocking child uses a Unix shell")
	}
	for _, delivery := range []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryRemuxProgressiveV3} {
		for _, commit := range []bool{false, true} {
			name := string(delivery) + "/rollback"
			if commit {
				name = string(delivery) + "/commit"
			}
			t.Run(name, func(t *testing.T) {
				service, file, session, _, _ := nativeVideoFixture(t)
				service.OpenDelegated = func(ctx context.Context, current *models.MediaFile, _ NativeVideoBinding) (NativeVideoFile, error) {
					opened, err := service.Open(ctx, current)
					if err != nil {
						return nil, err
					}
					return &nativeVideoReplanFile{NativeVideoFile: opened, ctx: ctx}, nil
				}
				if err := service.Bind(t.Context(), session, file); err != nil {
					t.Fatal(err)
				}
				source, format, err := service.InputSource(t.Context(), session, file)
				if err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(t.TempDir(), "blocking-child")
				if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				previous, err := playback.StartTranscode(t.Context(), playback.TranscodeOpts{SessionID: session.ID, InputPath: file.FilePath, InputSource: source, InputFormat: format, FFmpegPath: bin, OutputDir: t.TempDir(), TargetCodecVideo: "h264", TargetCodecAudio: "aac", HWAccel: "none"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = previous.Close() })
				manager := service.Sessions.(*playback.SessionManager)
				if err := manager.SetTranscodeRoute(session.ID, playback.TranscodeRoute{TransportID: session.ID + "-previous"}); err != nil {
					t.Fatal(err)
				}
				if err := manager.SetNodeRoutingAssignment(session.ID, playback.NodeRoutingAssignment{Workload: string(noderouting.WorkloadVideoTranscode), Execution: string(noderouting.ExecutionTranscode), ExecutionNodeID: 7, ExecutionNodeURL: "http://stale-worker.example", Egress: string(noderouting.EgressProxy), EgressNodeID: 8, EgressNodeURL: "http://proxy.example"}); err != nil {
					t.Fatal(err)
				}
				session, err = manager.GetSession(session.ID)
				if err != nil {
					t.Fatal(err)
				}
				h := NewPlaybackHandler(manager, service.Files)
				h.NativeVideo, h.JWTSecret = service, "fixture-secret"
				if _, accepted := h.tm.SwapTranscodeSession(session.ID, previous); !accepted {
					t.Fatal("prior child was not installed")
				}
				planner := &recordingNodePlannerV3{}
				h.NodePlanner = planner
				h.ProxyGrantStore = &recordingRecipeCardStoreV3{cards: map[string]playback.RecipeCard{session.ID: {SessionID: session.ID}}}
				method := playback.PlayDirect
				if delivery == playback.DeliveryRemuxProgressiveV3 {
					method = playback.PlayRemux
				}
				result := playback.PlannerResultV3{PlayMethod: method, Plan: &playback.PlanV3{SessionID: session.ID, EffectiveMediaFileID: file.ID, Delivery: delivery}}
				transport, failure := h.prepareNativeVideoTransportV3(httptest.NewRequest(http.MethodPost, "/", nil), session, file, result, mediaAuthModeV3{}, config.DefaultPlaybackRoutingPolicy())
				if failure != nil {
					t.Fatal(failure)
				}
				u, err := url.Parse(transport.url)
				if err != nil {
					t.Fatal(err)
				}
				claims, err := streamtoken.Verify(u.Query().Get(streamTokenParam), h.JWTSecret)
				if err != nil {
					transport.rollback()
					t.Fatal(err)
				}
				if claims.TranscodeNode != "" || claims.TranscodeTransportID != "" || claims.RoutingExecutionNodeID != 0 || claims.RoutingEgressNodeID != 0 {
					t.Errorf("new local recipe retains old node identity: node=%q transport=%q executor=%d egress=%d", claims.TranscodeNode, claims.TranscodeTransportID, claims.RoutingExecutionNodeID, claims.RoutingEgressNodeID)
				}
				if commit {
					if err := transport.commit(); err != nil {
						t.Fatal(err)
					}
					if h.tm.GetTranscodeSession(session.ID) != nil || previous.IsRunning() {
						t.Errorf("commit left previous execution: local=%v running=%v", h.tm.GetTranscodeSession(session.ID) != nil, previous.IsRunning())
					}
					if _, found := h.ProxyGrantStore.Get(t.Context(), session.ID); found {
						t.Error("commit left previous proxy grant")
					}
					if len(planner.released) != 1 || planner.released[0] != session.ID {
						t.Errorf("previous reservation not released: %v", planner.released)
					}
				} else {
					transport.rollback()
					_, proxy := h.ProxyGrantStore.Get(t.Context(), session.ID)
					if h.tm.GetTranscodeSession(session.ID) != previous || !previous.IsRunning() || !proxy || len(planner.released) != 0 {
						t.Fatal("rollback destroyed previous execution or authority")
					}
				}
			})
		}
	}
}
