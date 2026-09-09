package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

type SubtitleProviderStatusService interface {
	SubtitleProviderStatus() handlers.SubtitleProviderStatusView
}

type SubtitleAIStatusService interface {
	SubtitleAIStatus() handlers.SubtitleAIStatusView
}

type SubtitleProviderStatus struct {
	SchemaVersion int      `json:"schema_version"`
	Enabled       bool     `json:"enabled"`
	Providers     []string `json:"providers"`
}

type SubtitleAIStatus struct {
	Enabled           bool `json:"enabled"`
	TranscribeEnabled bool `json:"transcribe_enabled"`
}

type SubtitleProviderStatusOutput struct{ Body SubtitleProviderStatus }
type SubtitleAIStatusOutput struct{ Body SubtitleAIStatus }

func registerSubtitleCapabilities(reg *Registry) {
	op := func(path, id string) Operation {
		return Operation{Operation: humaOp(http.MethodGet, Prefix+path, id, "subtitles", "Read configured subtitle capabilities without starting work."), Class: ClassProfileScoped, ProfileOptional: true}
	}
	Register(reg, op("/subtitles/providers/status", "getSubtitleProviderStatus"), func(_ context.Context, _ *struct{}) (*SubtitleProviderStatusOutput, error) {
		view := handlers.SubtitleProviderStatusView{SchemaVersion: 1}
		if reg.deps.SubtitleProviders != nil {
			view = reg.deps.SubtitleProviders.SubtitleProviderStatus()
		}
		return &SubtitleProviderStatusOutput{Body: SubtitleProviderStatus{SchemaVersion: view.SchemaVersion, Enabled: view.Enabled, Providers: append([]string{}, view.Providers...)}}, nil
	})
	Register(reg, op("/subtitles/ai/status", "getSubtitleAIStatus"), func(_ context.Context, _ *struct{}) (*SubtitleAIStatusOutput, error) {
		var view handlers.SubtitleAIStatusView
		if reg.deps.SubtitleAI != nil {
			view = reg.deps.SubtitleAI.SubtitleAIStatus()
		}
		return &SubtitleAIStatusOutput{Body: SubtitleAIStatus{Enabled: view.Enabled, TranscribeEnabled: view.TranscribeEnabled}}, nil
	})
}
