package downloads

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// ServeDirectForProfile applies the selected profile's download policy while
// retaining the shared direct-download service and its serving limits.
func (s *Service) ServeDirectForProfile(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int, profileID string, fileID int, format string, filter catalog.AccessFilter) error {
	return s.ServeDirect(bloemWithDownloadProfile(ctx, userID, profileID), w, r, userID, fileID, format, filter)
}

// ResolveDirectFileForProfile authorizes the selected profile before an
// original file is served locally or delegated to a download proxy.
func (s *Service) ResolveDirectFileForProfile(ctx context.Context, userID int, profileID string, fileID int, format string, filter catalog.AccessFilter) (*FileTarget, error) {
	return s.ResolveDirectFile(bloemWithDownloadProfile(ctx, userID, profileID), userID, fileID, format, filter)
}
