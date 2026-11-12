package api

import (
	"context"
	"errors"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/Silo-Server/silo-server/internal/requests"
)

// nativeRequestRouters serves request_router.v1 for native storage
// installations from their storage sessions (nativestorage.Host).
type nativeRequestRouters interface {
	IsNativeInstallation(ctx context.Context, installationID int) (bool, error)
	NativeRequestRouter(ctx context.Context, installationID int, capabilityID string) (pluginv1.RequestRouterClient, error)
	NativeRequestRouterDescriptor(ctx context.Context, installationID int, capabilityID string) (*pluginv1.RequestRouterDescriptor, error)
}

// fallbackRequestRouters is the ordinary plugin runtime's resolver, with the
// feature reader it also implements.
type fallbackRequestRouters interface {
	requests.RouterClientResolver
	requests.RouterFeatureReader
}

// bloemRequestRouterResolver sends request_router.v1 calls for a native
// storage installation to that installation's storage session, and every other
// installation to the ordinary plugin runtime. The ordinary runtime refuses
// native installations, so neither side can serve the other's.
type bloemRequestRouterResolver struct {
	fallback fallbackRequestRouters
	native   nativeRequestRouters
}

func newBloemRequestRouterResolver(deps Dependencies) bloemRequestRouterResolver {
	resolver := bloemRequestRouterResolver{fallback: PluginRequestRouterAdapter{Svc: deps.PluginService}}
	if deps.NativeStorage != nil && deps.NativeStorage.Registry != nil {
		resolver.native = deps.NativeStorage
	}
	return resolver
}

func (r bloemRequestRouterResolver) isNative(ctx context.Context, installationID int) (bool, error) {
	if r.native == nil {
		return false, nil
	}
	return r.native.IsNativeInstallation(ctx, installationID)
}

func (r bloemRequestRouterResolver) RequestRouterClient(ctx context.Context, installationID int, capabilityID string) (requests.RouterClient, error) {
	native, err := r.isNative(ctx, installationID)
	if err != nil {
		return nil, err
	}
	if !native {
		if r.fallback == nil {
			return nil, errors.New("request router plugin service is not configured")
		}
		return r.fallback.RequestRouterClient(ctx, installationID, capabilityID)
	}
	client, err := r.native.NativeRequestRouter(ctx, installationID, capabilityID)
	if err != nil {
		return nil, err
	}
	return nativeRouterClient{client}, nil
}

func (r bloemRequestRouterResolver) RouterFeatures(ctx context.Context, installationID int, capabilityID string) (requests.RouterFeatures, error) {
	native, err := r.isNative(ctx, installationID)
	if err != nil {
		return requests.RouterFeatures{}, err
	}
	if !native {
		if r.fallback == nil {
			return requests.RouterFeatures{}, errors.New("request router plugin service is not configured")
		}
		return r.fallback.RouterFeatures(ctx, installationID, capabilityID)
	}
	descriptor, err := r.native.NativeRequestRouterDescriptor(ctx, installationID, capabilityID)
	if err != nil {
		return requests.RouterFeatures{}, err
	}
	return requests.RouterFeatures{
		SupportsSeasons:         descriptor.GetSupportsSeasons(),
		ReportsDownloadProgress: descriptor.GetReportsDownloadProgress(),
		APIKeyOptional:          descriptor.GetApiKeyOptional(),
		BaseURLOptional:         descriptor.GetBaseUrlOptional(),
	}, nil
}

// nativeRouterClient adapts a storage session's gRPC client to the request
// service's client shape.
type nativeRouterClient struct{ client pluginv1.RequestRouterClient }

func (c nativeRouterClient) Fulfill(ctx context.Context, req *pluginv1.FulfillRequest) (*pluginv1.FulfillResponse, error) {
	return c.client.Fulfill(ctx, req)
}
func (c nativeRouterClient) CheckStatus(ctx context.Context, req *pluginv1.CheckStatusRequest) (*pluginv1.CheckStatusResponse, error) {
	return c.client.CheckStatus(ctx, req)
}
func (c nativeRouterClient) ListConfigOptions(ctx context.Context, req *pluginv1.ListConfigOptionsRequest) (*pluginv1.ListConfigOptionsResponse, error) {
	return c.client.ListConfigOptions(ctx, req)
}
func (c nativeRouterClient) TestConnection(ctx context.Context, req *pluginv1.TestConnectionRequest) (*pluginv1.TestConnectionResponse, error) {
	return c.client.TestConnection(ctx, req)
}
func (c nativeRouterClient) Validate(ctx context.Context, req *pluginv1.ValidateRequest) (*pluginv1.ValidateResponse, error) {
	return c.client.Validate(ctx, req)
}
