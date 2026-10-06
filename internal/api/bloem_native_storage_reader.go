package api

import (
	"context"
	"errors"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func nativeStorageReader(deps Dependencies, local *handlers.EbookReaderHandler) *handlers.NativeEbookFileService {
	if deps.NativeStorage == nil || deps.DB == nil {
		return handlers.NewNativeEbookFileService(local, nil)
	}
	resources := resourcetenancy.NewStore(deps.DB)
	resolver := tenancy.NewResolver(tenancy.NewStore(deps.DB))
	coordinator := &nativestorage.Coordinator{AcquireOpen: deps.NativeStorage.AcquireOpen, References: storagesource.NewRepository(deps.DB), Registry: deps.NativeStorage.Registry, Runtime: nativestorage.ManagedRuntime{Manager: deps.NativeStorage.Manager}}
	coordinator.AuthorizeFile = func(ctx context.Context, file *models.MediaFile) (*models.MediaFile, error) {
		// Require gates to have supplied profile/PIN/library scope; an absent scope
		// must not degrade to an unrestricted empty catalog filter.
		if _, ok := access.GetScope(ctx); !ok {
			return nil, catalog.ErrItemNotFound
		}
		return local.ResolveReaderFile(ctx, file.ContentID, file.ID, handlers.AccessFilterFromContext(ctx, ""))
	}
	coordinator.AuthorizeSource = func(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig) error {
		return authorizeNativeStorageSource(ctx, file, source, resolver, resources)
	}

	return handlers.NewNativeEbookFileService(local, coordinator)
}
func nativeStorageAccessError(err error) error {
	if errors.Is(err, resourcetenancy.ErrResourceHidden) {
		return status.Error(codes.PermissionDenied, "native storage access denied")
	}
	return err
}

type nativeStorageTenantResolver interface {
	Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)
}
type nativeStorageResourcePolicy interface {
	RequireAccess(context.Context, tenancy.Context, resourcetenancy.RootRef) (resourcetenancy.Grant, error)
}

func authorizeNativeStorageSource(ctx context.Context, file *models.MediaFile, source storagesource.SourceConfig, resolver nativeStorageTenantResolver, resources nativeStorageResourcePolicy) error {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || source.InstallationID == nil {
		return status.Error(codes.PermissionDenied, "native storage access denied")
	}
	organization := &tenant.OrganizationID
	if tenant.Legacy {
		organization = nil
	}
	fresh, err := resolver.Resolve(ctx, tenant.AccountID, organization, tenant.Legacy)
	if err != nil {
		return err
	}
	if fresh != tenant {
		return status.Error(codes.PermissionDenied, "tenant authorization changed")
	}
	if _, err = resources.RequireAccess(ctx, fresh, resourcetenancy.RootRef{Kind: resourcetenancy.RootMediaFolder, ID: int64(file.MediaFolderID)}); err != nil {
		return nativeStorageAccessError(err)
	}
	grant, err := resources.RequireAccess(ctx, fresh, resourcetenancy.RootRef{Kind: resourcetenancy.RootPluginInstallation, ID: *source.InstallationID})
	if err != nil {
		return nativeStorageAccessError(err)
	}
	if grant.Owner.ID != source.OwnerID {
		return status.Error(codes.PermissionDenied, "source owner changed")
	}
	return nil
}
