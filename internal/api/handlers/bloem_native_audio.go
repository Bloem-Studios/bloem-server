package handlers

import (
	"context"
	"strings"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// NativeAudioSupported is structural admission of a native audiobook track.
func NativeAudioSupported(file *models.MediaFile) bool {
	return file != nil && storagesource.IsNativeLocation(file.FilePath) && file.BaseType == "audiobook" && file.IsAudioOnly() && nativeAudioContentType(file.Container) != ""
}

func nativeAudioContentType(container string) string {
	switch strings.ToLower(strings.TrimSpace(container)) {
	case "mp4", "m4a", "m4b":
		return "audio/mp4"
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "ogg", "opus":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	default:
		return ""
	}
}

func nativeAudioPlanRefusal(file *models.MediaFile, result playback.PlannerResultV3) *transportErrorV3 {
	if NativeAudioSupported(file) && (result.Plan == nil || result.Plan.Delivery != playback.DeliveryOriginalHTTPV3 || result.PlayMethod != playback.PlayDirect) {
		return &transportErrorV3{reason: routeCapabilityUnavailableReasonV3, message: "Remote audiobook tracks play only as original files.", retryable: false}
	}
	return nil
}

// pinNativeAudioRequest keeps the requested recording fixed while start
// evaluates candidates. Persisted fixed-file semantics also protect replans.
func pinNativeAudioRequest(ctx context.Context, file *models.MediaFile) error {
	if file == nil {
		return catalog.ErrItemNotFound
	}
	state, ok := ctx.Value(nativeVideoRequestKey{}).(*nativeVideoRequestSnapshots)
	if !ok {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.audioPin != 0 && state.audioPin != file.ID {
		return catalog.ErrItemNotFound
	}
	if NativeAudioSupported(file) {
		state.audioPin = file.ID
	}
	return nil
}
