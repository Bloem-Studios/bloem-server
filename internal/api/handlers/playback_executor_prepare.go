package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/logredact"
	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// prepareRemoteExecutorRecipeV3 contacts only the selected worker, once. The
// reply is checked against the captured proposal and remains input-only until
// the initial control flow publishes its immutable locator.
func (h *PlaybackHandler) prepareRemoteExecutorRecipeV3(ctx context.Context, proposed playback.RecipeCard) (playback.RecipeCard, error) {
	data, err := json.Marshal(transcodenode.ExecutorPreparation{Recipe: proposed})
	if err != nil {
		return playback.RecipeCard{}, err
	}
	deadline := h.remotePlaybackTransportTimeout(proposed.TranscodeNodeURL, transcodenode.TranscodeStartRequest{
		ToneMapMode: proposed.ToneMapMode, ToneMapPreflightRequired: proposed.ToneMapPreflightRequired, TotalDuration: proposed.TotalDuration,
	})
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, nodepool.NodeEndpoint(proposed.TranscodeNodeURL, "/transcode/prepare"), bytes.NewReader(data))
	if err != nil {
		return playback.RecipeCard{}, &executorPreparationErrorV3{class: "transport", cause: logredact.SanitizeURLError(err)}
	}
	request.Header.Set("Authorization", "Bearer "+h.JWTSecret)
	request.Header.Set("Content-Type", "application/json")
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return playback.RecipeCard{}, &executorPreparationErrorV3{class: "transport", cause: logredact.SanitizeURLError(err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return playback.RecipeCard{}, &executorPreparationErrorV3{class: "http_status", status: response.StatusCode}
	}
	var prepared transcodenode.ExecutorPreparation
	if err := json.NewDecoder(io.LimitReader(response.Body, 128<<10)).Decode(&prepared); err != nil {
		return playback.RecipeCard{}, &executorPreparationErrorV3{class: "response_decode", status: response.StatusCode, cause: err}
	}
	if err := transcodenode.ValidateExecutorPreparation(proposed, prepared.Recipe); err != nil {
		return playback.RecipeCard{}, &executorPreparationErrorV3{class: "response_identity", status: response.StatusCode, cause: err}
	}
	return prepared.Recipe, nil
}

// Classification preserves the internal cause without exposing the worker body.
type executorPreparationErrorV3 struct {
	class  string
	status int
	cause  error
}

func (e *executorPreparationErrorV3) Error() string {
	return fmt.Sprintf("selected worker preparation %s status %d", e.class, e.status)
}
func (e *executorPreparationErrorV3) Unwrap() error { return e.cause }
