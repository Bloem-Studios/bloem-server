package storageplugin

import (
	"errors"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/capability"
)

// ErrNativeSurface rejects a native manifest that declares a public surface
// the native runtime does not host.
var ErrNativeSurface = errors.New("native-only provider must not declare public capabilities other than request_router.v1, or routes")

// ValidateNativeSurfaces admits a request_router.v1 declaration beside the
// storage provider, since the Bookwarehouse storage release also ships a
// request router, but the host never calls it: native installations write no
// capability rows and serve no request integration. Every other capability
// type and every HTTP route stays refused, so the ordinary plugin runtime
// never has a reason to load the provider.
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
