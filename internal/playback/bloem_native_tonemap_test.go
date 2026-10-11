package playback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
)

func TestNativeToneMapProbesExactVideoSignature(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	track, err := playback.ProbeSourcePrimaryVideoTrack(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.ToneMapSourceRevision = tonemap.SourceRevision{MediaFileID: 1, FileSize: 1, StreamSignature: tonemap.StreamSignatureForTrack(track)}
	if err := playback.ValidateSourceToneMap(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.ToneMapSourceRevision.StreamSignature = "different-live-track"
	if err := playback.ValidateSourceToneMap(context.Background(), opts); !errors.Is(err, tonemap.ErrSourceRevisionChanged) {
		t.Fatalf("wrong video accepted: %v", err)
	}
	if opens.Load() != released.Load() {
		t.Fatalf("unjoined probes: %d %d", opens.Load(), released.Load())
	}
}

func TestNativeToneMapExecutionRequiresHostRevisionValidation(t *testing.T) {
	opts, _, _ := nativeInputFixture(t)
	opts.TargetCodecVideo = "h264"
	opts.ToneMapMode = tonemap.ModeSoftware
	opts.ToneMapPolicy = tonemap.PolicySoftwareOnly
	opts.ToneMapSourceKind = tonemap.SourcePQ
	opts.ToneMapFilter = tonemap.SoftwareFilterBT2390
	opts.ToneMapRecipeVersion = playback.TransformationHDRToSDRToneMapRecipeVersionV3
	opts.ToneMapSourceRevision = tonemap.SourceRevision{MediaFileID: 1, FileSize: 1, StreamSignature: "catalog-video"}
	revoked := errors.New("host rejected frozen source revision")
	called := false
	opts.InputSource.Validate = func(context.Context, playback.TranscodeOpts) error { called = true; return revoked }
	if _, err := playback.StartTranscode(context.Background(), opts); !errors.Is(err, revoked) {
		t.Fatalf("did not enforce host revision: %v", err)
	}
	if !called {
		t.Fatal("typed input did not reach host validation")
	}
}
