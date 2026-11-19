package fspath

// Bloem's process launches that do not go through CommandContext, added to
// Silo's launchesOutsideFspath (launch_guard_test.go) so that file stays as
// Silo wrote it. None of them gets a library path or a relay URL: native
// inputs run only on the API servers, from a private tool input argument
// with Bloem's own demuxer restriction (restrictSourceInputArgs,
// sourceInputOptions), and Live TV reads its tuner or provider stream.
var bloemLaunchesOutsideFspath = map[string]string{
	"internal/bloemtestsdk/sdk.go:BuildStorageFixture":                    "go mod download and go build of the storage plugin test fixture",
	"internal/bloemtestsdk/sdk.go:StorageDir":                             "go mod download of the storage plugin test fixture",
	"internal/playback/bloem_native_tonemap.go:sourceVideoTool":           "FFmpeg on a native tool input argument, restricted to the source's demuxer",
	"internal/playback/bloem_native_tools.go:ResolveSourceCopySeekAnchor": "FFmpeg on a native tool input argument, restricted to the source's demuxer",
	"internal/playback/bloem_native_tools.go:ServeSourceRemux":            "FFmpeg on a native tool input argument, restricted to the source's demuxer",
	"internal/playback/bloem_native_tools.go:SourceDVRPUStrippable":       "FFmpeg on a native tool input argument, restricted to the source's demuxer",
	"internal/playback/bloem_native_tools.go:sourceCapabilityOutput":      "FFmpeg capability queries (-bsfs, -h bsf=noise, tone-map checks that name no input)",
	"internal/playback/live_hls.go:startLiveHLSOnce":                      "FFmpeg on a Live TV tuner or provider stream",
	"internal/playback/live_record.go:StartLiveRecord":                    "FFmpeg on a Live TV tuner or provider stream",
	"internal/scanner/bloem_native_probe.go:probeNative":                  "ffprobe on a native tool input argument, restricted to the source's demuxer",
	"internal/storageplugin/manager.go:start":                             "a storage plugin binary",
}

func init() {
	for launch, reason := range bloemLaunchesOutsideFspath {
		launchesOutsideFspath[launch] = reason
	}
}
