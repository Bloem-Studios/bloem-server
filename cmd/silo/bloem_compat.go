package main

// Bloem-owned adapters between Silo's Jellyfin-compatible server and Bloem's
// tenancy. Silo's jellycompat package stays upstream bytes; everything Bloem
// needs from it is wired here.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/models"
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
// Jellyfin handlers that need an OS filename. Native attachment delivery
// through the compatibility server is not implemented.
func bloemGuardNativeStorageFiles(files jellycompat.FilePathResolver) jellycompat.FilePathResolver {
	if files == nil {
		return nil
	}
	return bloemNativeStorageFiles{FilePathResolver: files}
}

type bloemNativeStorageFiles struct{ jellycompat.FilePathResolver }

func (r bloemNativeStorageFiles) GetByID(ctx context.Context, id int) (*models.MediaFile, error) {
	file, err := r.FilePathResolver.GetByID(ctx, id)
	if err == nil && file != nil && storagesource.IsNativeLocation(file.FilePath) {
		return nil, scanner.ErrFileNotFound
	}
	return file, err
}

// compatStorageLibraries lists libraries by whether they read from a storage
// source (storagesource.Repository).
type compatStorageLibraries interface {
	LibrariesByStorage(context.Context) (without, with []int, err error)
}

// bloemCompatStorageLibrariesTTL bounds how long a new storage-backed library
// can stay visible to Jellyfin clients.
const bloemCompatStorageLibrariesTTL = 5 * time.Second

// bloemCompatHideNativeLibraries leaves every library that reads from a
// storage source out of a Jellyfin viewer's scope. Jellyfin clients cannot
// play native-storage files yet, so without this their items would look
// playable and then fail. Browse, search, latest, next up and item reads all
// filter by the scope. A mixed library is hidden whole until native playback
// reaches the compatibility server.
func bloemCompatHideNativeLibraries(next jellycompat.ScopeResolver, pool *pgxpool.Pool) jellycompat.ScopeResolver {
	if pool == nil {
		return next
	}
	return hideStorageLibraries(next, storagesource.NewRepository(pool))
}

func hideStorageLibraries(next jellycompat.ScopeResolver, libraries compatStorageLibraries) jellycompat.ScopeResolver {
	if next == nil || libraries == nil {
		return next
	}
	return &bloemCompatNativeLibraryHider{next: next, libraries: libraries, now: time.Now}
}

type bloemCompatNativeLibraryHider struct {
	next      jellycompat.ScopeResolver
	libraries compatStorageLibraries
	now       func() time.Time

	mu            sync.Mutex
	until         time.Time
	without, with []int
}

func (h *bloemCompatNativeLibraryHider) Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error) {
	scope, err := h.next.Resolve(ctx, input)
	if err != nil {
		return scope, err
	}
	without, with, err := h.storageLibraries(ctx)
	if err != nil {
		// Fail closed: an unknown split could show unplayable items.
		return access.Scope{}, fmt.Errorf("list storage-backed libraries: %w", err)
	}
	if len(with) == 0 {
		return scope, nil
	}
	if scope.AllowedLibraryIDs == nil {
		scope.AllowedLibraryIDs = slices.Clone(without)
	} else {
		scope.AllowedLibraryIDs = slices.DeleteFunc(slices.Clone(scope.AllowedLibraryIDs), func(id int) bool {
			_, stored := slices.BinarySearch(with, id)
			return stored
		})
	}
	if scope.AllowedLibraryIDs == nil {
		scope.AllowedLibraryIDs = []int{}
	}
	scope.LibrariesRestricted = true
	return scope, nil
}

func (h *bloemCompatNativeLibraryHider) storageLibraries(ctx context.Context) (without, with []int, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if now := h.now(); now.Before(h.until) {
		return h.without, h.with, nil
	}
	without, with, err = h.libraries.LibrariesByStorage(ctx)
	if err != nil {
		return nil, nil, err
	}
	without, with = slices.Clone(without), slices.Clone(with)
	slices.Sort(with)
	h.without, h.with, h.until = without, with, h.now().Add(bloemCompatStorageLibrariesTTL)
	return without, with, nil
}
