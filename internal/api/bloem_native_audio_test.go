package api

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/models"
)

func TestNativeAudioCapabilityRequiresFullComposition(t *testing.T) {
	for _, missing := range []string{"none", "scanner", "gate", "presentation", "stream"} {
		t.Run(missing, func(t *testing.T) {
			service := &handlers.NativeVideoService{Presentation: func(context.Context, *models.MediaFile) error { return nil }}
			h := &handlers.PlaybackHandler{NativeVideo: service}
			stream := &handlers.StreamHandler{NativeVideo: service}
			caps := &nativeStorageCapabilities{}
			deps := Dependencies{BloemDependencies: BloemDependencies{NativeAudioScannerReady: true, NativeAudioGate: new(atomic.Bool), NativeStorageManagement: &handlers.BloemNativeStorageManagementHandler{Capabilities: caps}}}
			switch missing {
			case "scanner":
				deps.NativeAudioScannerReady = false
			case "gate":
				deps.NativeAudioGate = nil
			case "presentation":
				service.Presentation = nil
			case "stream":
				stream.NativeVideo = nil
			}
			attachNativeAudioReadiness(deps, h, stream, nil)
			want := missing == "none"
			if service.AudioReady != want || caps.audio != want || deps.NativeAudioGate != nil && deps.NativeAudioGate.Load() != want {
				t.Fatalf("service=%v caps=%v want=%v", service.AudioReady, caps.audio, want)
			}
			// Even a composed audio adapter cannot advertise through an uncomposed
			// native video/runtime capability.
			if caps.NativeAudioReady(context.Background()) {
				t.Fatal("audio bypassed native runtime readiness")
			}
		})
	}
}
