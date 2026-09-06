package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/imagesize"
)

// CompatConnectInfoService exposes only the settings a signed-in account needs
// to connect a compatibility client; implementation details remain admin-only.
type CompatConnectInfoService interface {
	GetConnectInfo(context.Context) handlers.CompatConnectInfoResponse
}

type CompatConnectInfoOutput struct {
	Body handlers.CompatConnectInfoResponse
}

type ImageCapabilities struct {
	Capability
	Param              string                              `json:"param"`
	Sizes              []imagesize.Size                    `json:"sizes"`
	Widths             map[string]handlers.ImageSizeWidths `json:"widths"`
	OriginalMaxWidthPx int                                 `json:"original_max_width_px"`
}

type ImageCapabilitiesOutput struct{ Body ImageCapabilities }

func registerOperationalDiscovery(reg *Registry) {
	Register(reg, Operation{Operation: humaOp(http.MethodGet, Prefix+"/compat/connect-info", "getCompatConnectInfo", "compat", "Connection information for compatibility clients."), Class: ClassAuthenticated, ServiceBacked: true},
		func(ctx context.Context, _ *struct{}) (*CompatConnectInfoOutput, error) {
			if reg.deps.CompatConnectInfo == nil {
				return nil, NewProblem(TypeDependencyUnavailable, "Compatibility connection information is not configured.")
			}
			return &CompatConnectInfoOutput{Body: reg.deps.CompatConnectInfo.GetConnectInfo(ctx)}, nil
		})
	Register(reg, Operation{Operation: humaOp(http.MethodGet, Prefix+"/images/capabilities", "getImageCapabilities", "images", "Available image sizes and their pixel widths."), Class: ClassProfileScoped, ProfileOptional: true},
		func(context.Context, *struct{}) (*ImageCapabilitiesOutput, error) {
			view := handlers.GetImagesCapability()
			return &ImageCapabilitiesOutput{Body: ImageCapabilities{
				Capability: Capability{Revision: "1", State: StateAvailable}, Param: view.Param, Sizes: view.Sizes, Widths: view.Widths, OriginalMaxWidthPx: view.OriginalMaxWidthPx,
			}}, nil
		})
}
