package storageplugin

import (
	"context"
	"errors"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/capability"
	"google.golang.org/grpc"
)

// ErrNativeSurface rejects a native manifest that declares a public surface
// the native runtime does not host.
var ErrNativeSurface = errors.New("native-only provider must not declare public capabilities other than request_router.v1, or routes")

// ValidateNativeSurfaces admits the one public capability a native storage
// provider may also serve: request_router.v1, which the host calls only over
// the provider's own session. Every other capability type and every HTTP route
// stays refused, so the ordinary plugin runtime never has a reason to load it.
func ValidateNativeSurfaces(manifest *publicv1.PluginManifest) error {
	if len(manifest.GetHttpRoutes()) != 0 {
		return ErrNativeSurface
	}
	for _, c := range manifest.GetCapabilities() {
		if c.GetType() != capability.RequestRouter || c.GetId() == "" {
			return ErrNativeSurface
		}
	}
	return nil
}

// DeclaresRequestRouter reports whether manifest declares the request_router.v1
// capability id.
func DeclaresRequestRouter(manifest *publicv1.PluginManifest, id string) bool {
	for _, c := range manifest.GetCapabilities() {
		if c.GetType() == capability.RequestRouter && c.GetId() == id && id != "" {
			return true
		}
	}
	return false
}

// RequestRouterRPCLimit bounds one request_router.v1 call on a storage
// session. Acquisition calls search and hand off over the network, so they get
// longer than the session's storage RPC deadline; the caller's own deadline
// still applies.
const RequestRouterRPCLimit = 90 * time.Second

// requestRouterClient raises the session's per-call deadline to
// RequestRouterRPCLimit for every request_router.v1 call.
type requestRouterClient struct{ publicv1.RequestRouterClient }

func (c requestRouterClient) Fulfill(ctx context.Context, in *publicv1.FulfillRequest, opts ...grpc.CallOption) (*publicv1.FulfillResponse, error) {
	return c.RequestRouterClient.Fulfill(ctx, in, append(opts, RPCLimit(RequestRouterRPCLimit))...)
}
func (c requestRouterClient) CheckStatus(ctx context.Context, in *publicv1.CheckStatusRequest, opts ...grpc.CallOption) (*publicv1.CheckStatusResponse, error) {
	return c.RequestRouterClient.CheckStatus(ctx, in, append(opts, RPCLimit(RequestRouterRPCLimit))...)
}
func (c requestRouterClient) ListConfigOptions(ctx context.Context, in *publicv1.ListConfigOptionsRequest, opts ...grpc.CallOption) (*publicv1.ListConfigOptionsResponse, error) {
	return c.RequestRouterClient.ListConfigOptions(ctx, in, append(opts, RPCLimit(RequestRouterRPCLimit))...)
}
func (c requestRouterClient) TestConnection(ctx context.Context, in *publicv1.TestConnectionRequest, opts ...grpc.CallOption) (*publicv1.TestConnectionResponse, error) {
	return c.RequestRouterClient.TestConnection(ctx, in, append(opts, RPCLimit(RequestRouterRPCLimit))...)
}
func (c requestRouterClient) Validate(ctx context.Context, in *publicv1.ValidateRequest, opts ...grpc.CallOption) (*publicv1.ValidateResponse, error) {
	return c.RequestRouterClient.Validate(ctx, in, append(opts, RPCLimit(RequestRouterRPCLimit))...)
}
