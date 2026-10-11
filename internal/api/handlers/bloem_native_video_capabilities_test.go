package handlers

import (
	"context"
	"testing"
)

type nativeVideoCapabilityFixture struct{ ready, movies, video, remote bool }

func (c nativeVideoCapabilityFixture) NativeStorageReady(context.Context) bool { return c.ready }
func (c nativeVideoCapabilityFixture) NativeMovieLibrariesReady(context.Context) bool {
	return c.movies
}
func (c nativeVideoCapabilityFixture) NativeVideoReady(context.Context) bool       { return c.video }
func (c nativeVideoCapabilityFixture) NativeRemoteVideoReady(context.Context) bool { return c.remote }

func TestNativeVideoCapabilitiesDoNotCertifyBackendOrOtherSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		cap                   nativeVideoCapabilityFixture
		movies, video, remote bool
	}{
		{"composed", nativeVideoCapabilityFixture{true, true, true, true}, true, true, true},
		{"scanner_absent", nativeVideoCapabilityFixture{true, false, true, true}, false, true, true},
		{"video_absent", nativeVideoCapabilityFixture{true, false, false, false}, false, false, false},
		{"storage_absent", nativeVideoCapabilityFixture{false, true, true, true}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &BloemNativeStorageManagementHandler{Capabilities: tc.cap}
			body := h.nativeStorageCapabilities(t.Context())
			if body["native_movie_libraries"] != tc.movies || body["native_video_playback"] != tc.video || body["native_video_remote_execution"] != tc.remote {
				t.Fatalf("incorrect native video composition: %v", body)
			}
			if body["backend_verified"] != false || body["native_video_downloads"] != false || body["native_video_jellyfin"] != false || body["schema"] != 2 {
				t.Fatal("native capability certified unsupported source or surface")
			}
		})
	}
}
