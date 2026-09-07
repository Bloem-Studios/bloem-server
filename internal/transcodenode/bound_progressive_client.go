package transcodenode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/nodepool"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// BoundProgressiveClientV3 performs single internal command exchanges with the
// selected worker only. The caller owns request deadlines and uncertain outcomes.
type BoundProgressiveClientV3 struct{ Bearer string }

func (c BoundProgressiveClientV3) exchange(ctx context.Context, card playback.RecipeCard, path string, body any, status int, result any) error {
	if card.RoutingExecution != "transcode" || card.TranscodeNodeURL == "" {
		return errors.New("selected progressive worker required")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, nodepool.NodeEndpoint(card.TranscodeNodeURL, path), bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid progressive command endpoint")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Bearer)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return errors.New("progressive command outcome uncertain")
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errors.New("progressive command not confirmed")
	}
	if result == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 128<<10))
	if err := decoder.Decode(result); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid progressive command response")
	}
	return nil
}

func (c BoundProgressiveClientV3) Prepare(ctx context.Context, card playback.RecipeCard) (playback.RecipeCard, error) {
	if err := playback.ValidateBoundProgressiveRecipeV3(card); err != nil {
		return playback.RecipeCard{}, err
	}
	var response ExecutorPreparation
	if err := c.exchange(ctx, card, "/remux/prepare", ExecutorPreparation{Recipe: card}, http.StatusOK, &response); err != nil {
		return playback.RecipeCard{}, err
	}
	before, err := playback.BoundProgressiveRecipeDigestV3(card)
	if err != nil {
		return playback.RecipeCard{}, err
	}
	after, err := playback.BoundProgressiveRecipeDigestV3(response.Recipe)
	if err != nil || before != after {
		return playback.RecipeCard{}, errors.New("worker changed progressive recipe")
	}
	return response.Recipe, nil
}

func (c BoundProgressiveClientV3) Start(ctx context.Context, card playback.RecipeCard) error {
	command, err := BoundProgressiveCommandForRecipeV3(card)
	if err != nil {
		return err
	}
	var ready BoundProgressiveReadyV3
	if err := c.exchange(ctx, card, "/remux/start", command, http.StatusAccepted, &ready); err != nil {
		return err
	}
	return ValidateBoundProgressiveReadyV3(command, ready)
}

func BoundProgressiveOutputEndpointV3(card playback.RecipeCard) (string, error) {
	if err := playback.ValidateBoundProgressiveRecipeV3(card); err != nil {
		return "", err
	}
	if card.RoutingExecution != "transcode" {
		return "", errors.New("selected progressive worker required")
	}
	return nodepool.NodeEndpoint(card.TranscodeNodeURL, "/remux/output/"+url.PathEscape(card.TranscodeTransportID)) + "?" + BoundProgressiveOutputQueryV3(*card.Executor), nil
}
