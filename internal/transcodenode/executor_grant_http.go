package transcodenode

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// WithExecutorGrantProvider installs durable grant acquisition for bound worker
// execution and HTTP delivery. A namespace without this provider fails closed.
func (s *Server) WithExecutorGrantProvider(provider playback.ExecutorGrantProviderV3) *Server {
	s.executorGrants = provider
	return s
}

func (s *Server) grantExecutorResponse(w http.ResponseWriter, r *http.Request, transportID string, session *playback.TranscodeSession) (http.ResponseWriter, *http.Request, func(), error) {
	return playback.GuardExecutorResponseV3(w, r, s.executorGrants, transportID, session.ExecutorNamespace())
}
