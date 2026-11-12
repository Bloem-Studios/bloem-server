package api

import (
	"context"
	"encoding/json"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// Worker HLS for Jellyfin clients. Silo's Jellyfin server starts a remote
// transcode by posting a start request to a worker; for a native-storage
// input the worker runs it only with a grant the API sealed for that exact
// worker, transport and start (handlers.NativeVideoWorkerGrantStore). The
// start hook (playback.SealContextWorkerStart) seals it, and the stored
// recipe the worker rebuilds and serves segments from is accepted into the
// grant when Silo's Jellyfin server stores it (AcceptWorkerRecipe).

// SealWorkerStart seals the worker grant for a native start request before
// it is sent to nodeURL. Other inputs pass through. The worker must answer
// the native-execution readiness probe; its answer names the worker the
// grant is sealed for.
func (c *NativeVideoCompat) SealWorkerStart(ctx context.Context, nodeURL string, body []byte) error {
	var start transcodenode.TranscodeStartRequest
	if err := json.Unmarshal(body, &start); err != nil {
		return nil //nolint:nilerr // An unreadable start names no native input; the worker judges it as before.
	}
	if !storagesource.IsNativeLocation(start.InputPath) {
		return nil
	}
	delivery := c.delivery.Load()
	if delivery == nil || delivery.secret == nil || !delivery.service.WorkerGrants.Enabled() {
		return errNativeVideoCompatUnavailable
	}
	grants := delivery.service.WorkerGrants
	node, err := delivery.service.WorkerNativeNode(ctx, nodeURL, delivery.secret())
	if err != nil {
		return err
	}
	_, session, _, err := c.bind(ctx, start.InputPath)
	if err != nil {
		return nativeVideoCompatError(err)
	}
	if session.ID != start.SessionID {
		return middleware.ErrCurrentCredentialForbidden
	}
	// Silo's Jellyfin server retries a failed start, on another worker or with
	// a fallback recipe, under the same transport. Each start replaces the
	// earlier one, so the earlier grant is revoked first.
	if prior, err := grants.TransportNode(ctx, start.SessionID); err == nil {
		if err := grants.Revoke(ctx, prior, start.SessionID); err != nil {
			return err
		}
	}
	return grants.Prepare(ctx, node, delivery.service, session, start)
}

// AcceptWorkerRecipe accepts the recipe Silo's Jellyfin server stores for a
// native worker transport into its grant, so the worker can rebuild and
// serve it. The recipe is stored as returned: a recipe stored before the
// route was recorded names the grant's worker.
func (c *NativeVideoCompat) AcceptWorkerRecipe(ctx context.Context, card playback.RecipeCard) (playback.RecipeCard, error) {
	if !storagesource.IsNativeLocation(card.InputPath) {
		return card, nil
	}
	delivery := c.delivery.Load()
	if delivery == nil || !delivery.service.WorkerGrants.Enabled() {
		return card, errNativeVideoCompatUnavailable
	}
	transport := card.TranscodeTransportID
	if transport == "" {
		transport = card.SessionID
	}
	node, err := delivery.service.WorkerGrants.TransportNode(ctx, transport)
	if err != nil {
		return card, err
	}
	if card.RoutingExecutionNodeID == 0 {
		card.RoutingExecutionNodeID = node
	} else if card.RoutingExecutionNodeID != node {
		return card, storagesource.ErrReferenceConflict
	}
	if err := delivery.service.WorkerGrants.Accept(ctx, node, transport, card); err != nil {
		return card, err
	}
	return card, nil
}
