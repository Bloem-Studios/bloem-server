package proxy

import (
	"context"
	"net/http"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// WithExecutorRuntime wires this configured egress node's immutable recipe and
// grant callbacks. Stream requests never supply the runtime's node identity.
func (s *Server) WithExecutorRuntime(acquire playback.ExecutorGrantProviderV3, resolve func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error), transfer playback.ExecutorOutputTransferProviderV3) *Server {
	s.executorGrants, s.executorRecipeResolver, s.executorOutputTransfers = acquire, resolve, transfer
	return s
}

func (s *Server) guardExecutorDelivery(w http.ResponseWriter, r *http.Request, claims *streamtoken.Claims) (http.ResponseWriter, *http.Request, func(), bool) {
	if claims == nil {
		http.Error(w, "playback authority unavailable", http.StatusServiceUnavailable)
		return nil, nil, nil, false
	}
	if !claims.ExecutorBound {
		return w, r, func() {}, true
	}
	refuse := func() (http.ResponseWriter, *http.Request, func(), bool) {
		http.Error(w, "playback authority unavailable", http.StatusServiceUnavailable)
		return nil, nil, nil, false
	}
	if s.executorRecipeResolver == nil || s.executorGrants == nil {
		return refuse()
	}
	card := playback.RecipeCardFromClaims(claims)
	transport := transcodeTransportIDFromClaims(claims)
	if card.Executor == nil || card.Executor.Validate() != nil {
		return refuse()
	}
	resolved, err := s.executorRecipeResolver(r.Context(), transport, *card.Executor)
	if err != nil || resolved == nil {
		return refuse()
	}
	// Compare the complete signed projection, including source and byte-affecting
	// fields. JWT validity is checked by the caller; issuance metadata is not part
	// of the immutable playback recipe.
	expected := resolved.ToClaims()
	expected.RegisteredClaims, expected.Version = claims.RegisteredClaims, claims.Version
	nodeID, known := s.currentNodeRowID()
	if !reflect.DeepEqual(expected, *claims) || proxyEgressStatusV3(resolved.RoutingWorkload, resolved.RoutingExecution, resolved.RoutingEgress, resolved.RoutingEgressNodeID, nodeID, known) != 0 {
		return refuse()
	}
	direct := resolved.PlayMethod == playback.PlayDirect && resolved.RoutingWorkload == string(noderouting.WorkloadDirectPlay) && resolved.RoutingExecution == string(noderouting.ExecutionNone) && resolved.InputPath != "" && resolved.TranscodeNodeURL == ""
	transcode := resolved.PlayMethod == playback.PlayTranscode && resolved.RoutingWorkload == string(noderouting.WorkloadVideoTranscode) && resolved.RoutingExecution == string(noderouting.ExecutionTranscode) && resolved.RoutingExecutionNodeID > 0 && resolved.TranscodeNodeURL != "" && !resolved.VideoStreamCopy() && !resolved.AudioOnly
	remux := resolved.IsTranscodeRecipe() && resolved.VideoStreamCopy() && playback.ValidateCopyFMP4RecipeCard(*resolved) == nil && resolved.RoutingWorkload == string(noderouting.WorkloadRemux) && resolved.RoutingExecution == string(noderouting.ExecutionTranscode) && resolved.RoutingExecutionNodeID > 0 && resolved.TranscodeNodeURL != "" && !resolved.AudioOnly
	if !direct && !transcode && !remux {
		return refuse()
	}
	writer, request, cleanup, err := playback.GuardExecutorResponseV3(w, r, s.executorGrants, transport, resolved.Executor)
	if err != nil {
		return refuse()
	}
	return writer, request, cleanup, true
}
