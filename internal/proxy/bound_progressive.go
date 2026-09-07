package proxy

import (
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// ServeBoundProgressiveV3 is called only after the owning proxy has validated
// signed claims against the immutable recipe. It cannot reconstruct or execute.
func ServeBoundProgressiveV3(w http.ResponseWriter, r *http.Request, card playback.RecipeCard, nodeID int, bearer string, acquire playback.ExecutorGrantProviderV3, transfer playback.ExecutorOutputTransferProviderV3) error {
	if card.RoutingEgress != "proxy" || nodeID <= 0 || card.RoutingEgressNodeID != nodeID {
		return errors.New("progressive proxy egress not selected")
	}
	endpoint, err := transcodenode.BoundProgressiveOutputEndpointV3(card)
	if err != nil {
		return err
	}
	return playback.ServeRemoteBoundProgressiveV3(w, r, card, endpoint, bearer, acquire, transfer)
}
