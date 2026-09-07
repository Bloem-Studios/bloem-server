package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// BoundProgressiveAdapterV3 supplies initial/successor producer callbacks. The
// flow owner publishes the prepared recipe before Start and retains uncertain
// commands. It also owns token/recipe resolution before invoking Serve.
type BoundProgressiveAdapterV3 struct {
	Registry               *playback.BoundProgressiveRegistryV3
	OutputRoot, FFmpegPath string
	Worker                 transcodenode.BoundProgressiveClientV3
	Acquire                playback.ExecutorGrantProviderV3
	Transfer               playback.ExecutorOutputTransferProviderV3
}

func (a *BoundProgressiveAdapterV3) Prepare(ctx context.Context, card playback.RecipeCard) (playback.RecipeCard, *playback.PreparedBoundProgressiveV3, error) {
	if card.RoutingExecution == "transcode" {
		prepared, err := a.Worker.Prepare(ctx, card)
		return prepared, nil, err
	}
	p, err := playback.PrepareBoundProgressiveV3(ctx, card, a.OutputRoot, a.FFmpegPath)
	if err != nil {
		return playback.RecipeCard{}, nil, err
	}
	return p.Recipe(), p, nil
}

func (a *BoundProgressiveAdapterV3) Start(ctx context.Context, card playback.RecipeCard, prepared *playback.PreparedBoundProgressiveV3) error {
	if card.RoutingExecution == "transcode" {
		return a.Worker.Start(ctx, card)
	}
	if prepared == nil || a.Registry == nil {
		return errors.New("prepared progressive runtime missing")
	}
	expected, err := playback.BoundProgressiveRecipeDigestV3(card)
	if err != nil {
		return err
	}
	actual, err := playback.BoundProgressiveRecipeDigestV3(prepared.Recipe())
	if err != nil || actual != expected {
		return errors.New("prepared progressive recipe changed")
	}
	return a.Registry.Start(ctx, prepared)
}

func (a *BoundProgressiveAdapterV3) Stop(ctx context.Context, card playback.RecipeCard) error {
	if card.RoutingExecution == "transcode" {
		return errors.New("remote progressive cleanup requires durable grant revocation")
	}
	if a.Registry == nil || card.Executor == nil {
		return errors.New("progressive runtime missing")
	}
	return a.Registry.Stop(ctx, card.TranscodeTransportID, *card.Executor)
}

func (a *BoundProgressiveAdapterV3) Serve(w http.ResponseWriter, r *http.Request, card playback.RecipeCard) error {
	if err := playback.ValidateBoundProgressiveRecipeV3(card); err != nil {
		return err
	}
	if card.RoutingEgress != "api" || card.RoutingEgressNodeID != 0 {
		return errors.New("progressive API egress not selected")
	}
	if card.RoutingExecution == "transcode" {
		endpoint, err := transcodenode.BoundProgressiveOutputEndpointV3(card)
		if err != nil {
			return err
		}
		return playback.ServeRemoteBoundProgressiveV3(w, r, card, endpoint, a.Worker.Bearer, a.Acquire, a.Transfer)
	}
	if a.Registry == nil {
		return errors.New("progressive runtime missing")
	}
	return a.Registry.ServeHTTP(w, r, card.TranscodeTransportID, *card.Executor, a.Acquire, playback.AttemptGrantServeV3)
}
