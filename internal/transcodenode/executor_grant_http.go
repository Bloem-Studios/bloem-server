package transcodenode

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// WithExecutorGrantProvider installs durable grant acquisition for bound worker
// execution. Output delivery uses separate execution-to-egress permits.
func (s *Server) WithExecutorGrantProvider(provider playback.ExecutorGrantProviderV3) *Server {
	s.executorGrants = provider
	return s
}

// WithExecutorOutputTransferProvider installs internal worker-to-egress grants.
// The selected egress creates the permit; the worker cannot impersonate it.
func (s *Server) WithExecutorOutputTransferProvider(provider playback.ExecutorOutputTransferGrantProviderV3) *Server {
	s.executorOutputTransfers = provider
	return s
}

func (s *Server) grantExecutorResponse(w http.ResponseWriter, r *http.Request, transportID string, session *playback.TranscodeSession) (http.ResponseWriter, *http.Request, func(), error) {
	namespace := session.ExecutorNamespace()
	if namespace == nil {
		return playback.GuardExecutorResponseV3(w, r, nil, transportID, nil)
	}
	permit := r.Header.Get(playback.OutputTransferHeaderV3)
	if permit == "" || s.executorOutputTransfers == nil {
		return nil, r, nil, errors.New("executor output transfer authority unavailable")
	}
	provider := func(ctx context.Context, transport string, executor playback.ExecutorNamespaceV3, purpose playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error) {
		grant, err := s.executorOutputTransfers(ctx, transport, executor, permit)
		if err != nil || grant == nil {
			return grant, err
		}
		if grant.Request().OutputTransferID != permit {
			grant.Close()
			return nil, errors.New("executor output transfer permit mismatch")
		}
		return grant, nil
	}
	return playback.GuardExecutorOutputV3(w, r, provider, transportID, namespace, playback.AttemptGrantTransferV3)
}
