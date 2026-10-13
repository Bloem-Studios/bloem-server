package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/imagesize"
)

type ImageCapabilities struct {
	Capability
	EpisodeStillProvenance bool                       `json:"episode_still_provenance" doc:"Responses may carry poster_is_episode_still, backdrop_is_episode_still, and still_is_episode_still where the server knows whether an episode image is the episode's own still; an absent field means unknown"`
	SeasonListArtworkParam string                     `json:"season_list_artwork_param" doc:"Season-list boolean query parameter; false omits poster URLs and thumbhashes"`
	Param                  string                     `json:"param"`
	Sizes                  []imagesize.Size           `json:"sizes"`
	Widths                 map[string]ImageSizeWidths `json:"widths"`
	OriginalMaxWidthPx     int                        `json:"original_max_width_px"`
	StorageBackend         string                     `json:"storage_backend" enum:"local,s3"`
	Delivery               string                     `json:"delivery" enum:"server,direct"`
}

type ImageCapabilitiesOutput struct {
	Status       int
	ETag         string `header:"ETag"`
	CacheControl string `header:"Cache-Control"`
	Body         ImageCapabilities
}

func registerOperationalDiscovery(reg *Registry) {
	Register(reg, Operation{Operation: humaOp(http.MethodGet, Prefix+"/images/capabilities", "getImageCapabilities", "images", "Available image sizes and their pixel widths."), Class: ClassProfileScoped, ProfileOptional: true},
		func(context.Context, *CapabilityInput) (*ImageCapabilitiesOutput, error) {
			view := handlers.GetImagesCapability(reg.deps.ArtworkBackend)
			widths := make(map[string]ImageSizeWidths, len(view.Widths))
			for key, value := range view.Widths {
				widths[key] = ImageSizeWidths(value)
			}
			return &ImageCapabilitiesOutput{Body: ImageCapabilities{
				EpisodeStillProvenance: true,
				SeasonListArtworkParam: view.SeasonListArtworkParam,
				StorageBackend:         view.StorageBackend, Delivery: view.Delivery,
				Capability: Capability{State: StateAvailable}, Param: view.Param, Sizes: view.Sizes, Widths: widths, OriginalMaxWidthPx: view.OriginalMaxWidthPx,
			}}, nil
		})
}

// CompatConnectAccountInfo is the native transport projection, independent of handler views.
// ImageSizeWidths is the native transport projection, independent of handler views.
type ImageSizeWidths struct {
	Small  int `json:"small"`
	Medium int `json:"medium"`
	Large  int `json:"large"`
}
