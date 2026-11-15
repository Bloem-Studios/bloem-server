package main

// Bloem-owned adapters between Silo's Jellyfin-compatible server and Bloem's
// tenancy. Silo's jellycompat package stays upstream bytes; everything Bloem
// needs from it is wired here.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/audiobooks/abs"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// compatDownloadTenants validates the authenticated account/profile's
// authoritative organization and current membership.
type compatDownloadTenants interface {
	ResolveSubjectTenant(context.Context, int, string) (tenancy.Context, error)
}

// newBloemCompatDownloadServer shares the native download service's policy,
// configuration and bandwidth limits with the Jellyfin listener. Bloem's
// download policy is per profile and needs the validated tenant, which
// upstream's account-level ServeDirect does not carry. A nil result keeps
// upstream's refusal when the router builds no download service.
func newBloemCompatDownloadServer(svc *downloads.Service, pool *pgxpool.Pool) jellycompat.DownloadServer {
	if svc == nil || pool == nil {
		return nil
	}
	store := tenancy.NewStore(pool)
	return bloemCompatDownloadServer{service: svc, tenants: tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)}
}

// bloemCompatDownloadsHook runs Silo's download-service hook, then replaces
// the Jellyfin listener's download server with Bloem's per-profile one and
// the Audiobookshelf listener's with one that resolves the account's tenant.
func bloemCompatDownloadsHook(next func(*downloads.Service), target *jellycompat.DownloadServer, absH *abs.Handler, pool *pgxpool.Pool) func(*downloads.Service) {
	return func(svc *downloads.Service) {
		if next != nil {
			next(svc)
		}
		*target = newBloemCompatDownloadServer(svc, pool)
		if absH != nil {
			absH.SetDownloads(newBloemABSDownloads(svc, pool))
		}
	}
}

// newBloemABSDownloads gives the Audiobookshelf listener the download
// service under the account's validated tenant. Its requests carry no
// tenant, and Bloem's tenant-scoped group policy refuses a download check
// without one, so every account would be refused. The listener names no
// profile to the download service, so the check is the account's, as in
// Silo. A nil result refuses every download.
func newBloemABSDownloads(svc *downloads.Service, pool *pgxpool.Pool) abs.Downloads {
	if svc == nil || pool == nil {
		return nil
	}
	store := tenancy.NewStore(pool)
	return bloemABSDownloads{service: svc, tenants: tenancy.NewSubjectResolver(tenancy.NewResolver(store), store)}
}

type bloemABSDownloads struct {
	service abs.Downloads
	tenants compatDownloadTenants
}

func (d bloemABSDownloads) tenantContext(ctx context.Context, userID int) (context.Context, error) {
	if tenant, ok := tenancy.FromContext(ctx); ok && tenant.AccountID == userID {
		return ctx, nil
	}
	if d.service == nil || d.tenants == nil || userID <= 0 {
		return ctx, downloads.ErrDownloadNotAllowed
	}
	tenant, err := d.tenants.ResolveSubjectTenant(ctx, userID, "")
	if err != nil {
		return ctx, fmt.Errorf("%w: resolve download tenant: %w", downloads.ErrDownloadNotAllowed, err)
	}
	return tenancy.WithContext(ctx, tenant), nil
}

func (d bloemABSDownloads) CheckDownloadAllowed(ctx context.Context, userID int) error {
	ctx, err := d.tenantContext(ctx, userID)
	if err != nil {
		return err
	}
	return d.service.CheckDownloadAllowed(ctx, userID)
}

func (d bloemABSDownloads) ServeDirect(ctx context.Context, w http.ResponseWriter, r *http.Request, userID, fileID int, format string, filter catalog.AccessFilter) error {
	ctx, err := d.tenantContext(ctx, userID)
	if err != nil {
		return err
	}
	return d.service.ServeDirect(ctx, w, r, userID, fileID, format, filter)
}

type bloemCompatDownloadServer struct {
	service *downloads.Service
	tenants compatDownloadTenants
}

func (s bloemCompatDownloadServer) ServeDirect(ctx context.Context, w http.ResponseWriter, r *http.Request, userID, fileID int, format string, filter catalog.AccessFilter) error {
	session := jellycompat.SessionFromContext(ctx)
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
	// A native-storage or .strm original is read under this session's
	// authority, rechecked around every read.
	serveCtx = api.WithNativeVideoCompatDownload(serveCtx, session.Token, userID, session.ProfileID)
	return s.service.ServeDirectForProfile(serveCtx, w, r, userID, session.ProfileID, fileID, format, filter)
}

// bloemProfileSessionRevoker ends the Jellyfin-compatible sessions bound to
// the named profiles of an account. A Jellyfin sign-in opens an account-level
// login session, so Bloem's profile-scoped revocation (an organization
// removing its profiles) does not reach it through the login-session check;
// the rows are deleted here. announce then has every replica drop its cached
// copies, as for an account-wide revocation.
func bloemProfileSessionRevoker(pool *pgxpool.Pool, announce func(context.Context, int) error) func(ctx context.Context, userID int, profileIDs []string) error {
	return func(ctx context.Context, userID int, profileIDs []string) error {
		if pool == nil || len(profileIDs) == 0 {
			return nil
		}
		if _, err := pool.Exec(ctx, `DELETE FROM jellycompat_sessions WHERE streamapp_user_id = $1 AND profile_id = ANY($2::text[])`, userID, profileIDs); err != nil {
			return fmt.Errorf("delete profile compatibility sessions: %w", err)
		}
		return announce(ctx, userID)
	}
}

// bloemGuardNativeStorageFiles hides native storage identities from the
// Jellyfin handlers that need an OS filename. A stream request that carries
// the native media host (bloemCompatNativeMedia) gets the file: its path
// reaches bytes only through that host, which authorizes them.
func bloemGuardNativeStorageFiles(files jellycompat.FilePathResolver) jellycompat.FilePathResolver {
	if files == nil {
		return nil
	}
	return bloemNativeStorageFiles{FilePathResolver: files}
}

type bloemNativeStorageFiles struct{ jellycompat.FilePathResolver }

func (r bloemNativeStorageFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FilePathResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) && !playback.HasContextMediaHost(ctx) {
		return nil, scanner.ErrFileNotFound
	}
	return file, err
}
