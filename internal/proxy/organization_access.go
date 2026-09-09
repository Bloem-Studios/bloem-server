package proxy

import (
	"context"
	"net/http"
)

// SetMediaLibraryAccess adds a current library ceiling to existing signed
// recipes and media grants. The standalone server wires its database authority.
func (s *Server) SetMediaLibraryAccess(check func(context.Context, int, int) (bool, error)) {
	s.mediaLibraryAccess = check
}

// mediaLibraryAccessStatus leaves each route's existing error encoding intact.
func (s *Server) mediaLibraryAccessStatus(ctx context.Context, userID, fileID int) int {
	if s.mediaLibraryAccess == nil {
		return 0
	}
	allowed, err := s.mediaLibraryAccess(ctx, userID, fileID)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if !allowed {
		return http.StatusNotFound
	}
	return 0
}
