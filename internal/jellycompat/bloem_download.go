package jellycompat

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// HandleBloemDownload checks Bloem's transport permissions before the
// upstream download handler decodes an ID or queries the catalog.
func (h *PlaybackHandler) HandleBloemDownload(w http.ResponseWriter, r *http.Request) {
	session := SessionFromContext(r.Context())
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "Missing authentication token")
		return
	}
	if h.downloadDenied(w, r, session) {
		return
	}
	h.HandleDownload(w, r)
}

// DownloadTenantResolver validates the authenticated account/profile's
// authoritative organization and current membership.
type DownloadTenantResolver interface {
	ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error)
}

// NewProfileDownloadServer adapts the shared native download service to a
// compat session. Missing dependencies keep the upstream nil-service refusal.
func NewProfileDownloadServer(svc *downloads.Service, tenants DownloadTenantResolver) DownloadServer {
	if svc == nil || tenants == nil {
		return nil
	}
	return profileDownloadServer{service: svc, tenants: tenants}
}

type profileDownloadServer struct {
	service *downloads.Service
	tenants DownloadTenantResolver
}

func (s profileDownloadServer) ServeDirect(ctx context.Context, w http.ResponseWriter, r *http.Request, userID, fileID int, format string, filter catalog.AccessFilter) error {
	session := SessionFromContext(ctx)
	if s.service == nil || s.tenants == nil || session == nil || userID <= 0 || session.StreamAppUserID != userID {
		return downloads.ErrDownloadNotAllowed
	}
	tenant, err := s.tenants.ResolveSubjectTenant(ctx, userID, session.ProfileID)
	if err != nil {
		return fmt.Errorf("%w: resolve download tenant: %w", downloads.ErrDownloadNotAllowed, err)
	}
	serveCtx := tenancy.WithContext(ctx, tenant)
	if _, err := policy.TenantFactsFromContext(serveCtx, userID); err != nil {
		return fmt.Errorf("%w: download tenant facts: %w", downloads.ErrDownloadNotAllowed, err)
	}
	return s.service.ServeDirectForProfile(serveCtx, w, r, userID, session.ProfileID, fileID, format, filter)
}
