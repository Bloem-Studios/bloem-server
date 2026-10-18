package apiv2

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/imagesize"
)

type ImageCapabilities struct {
	Capability
	EpisodeStillProvenance bool                       `json:"episode_still_provenance" doc:"Responses may carry poster_is_episode_still, backdrop_is_episode_still, and still_is_episode_still where the server knows whether an episode image is the episode's own still; an absent field means unknown"`
	ExtendedItemArtwork    bool                       `json:"extended_item_artwork" doc:"Movie and series detail (getCatalogItem) may carry banner_url, thumb_url, clearart_url, disc_url and backdrop_urls, and widths lists banner, thumb, clearart and disc"`
	SeasonListArtworkParam string                     `json:"season_list_artwork_param" doc:"Season-list boolean query parameter; false omits poster URLs and thumbhashes"`
	Param                  string                     `json:"param"`
	SlotWidthParams        map[string]string          `json:"slot_width_params" doc:"Per-slot width query parameters, keyed by slot: poster (poster_url), backdrop (backdrop_url, backdrop_urls), logo (logo_url), still (an episode's still_url), photo (cast and crew photo_url). Each takes the width in device pixels the client draws that field at and overrides param for it; the server returns the narrowest cached size at least that wide, else the original. Accepted wherever param is"`
	Sizes                  []imagesize.Size           `json:"sizes"`
	Widths                 map[string]ImageSizeWidths `json:"widths"`
	OriginalMaxWidthPx     int                        `json:"original_max_width_px"`
	StorageBackend         string                     `json:"storage_backend" enum:"local,s3"`
	Delivery               string                     `json:"delivery" enum:"server,direct"`
}

// slotWidthParams names each ArtworkQuery slot width parameter by its slot.
var slotWidthParams = func() map[string]string {
	params := make(map[string]string, len(imagesize.Slots))
	for _, slot := range imagesize.Slots {
		params[slot.Name()] = slot.Name() + "_width"
	}
	return params
}()

// extendedItemArtworkTypes are the item artwork types beyond poster, backdrop
// and logo that movie and series detail can carry.
var extendedItemArtworkTypes = []string{artworkkey.ImageBanner, artworkkey.ImageThumb, artworkkey.ImageClearArt, artworkkey.ImageDisc}

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
			// The v1 capability view is frozen, so the extended artwork
			// types are listed here only.
			for _, imageType := range extendedItemArtworkTypes {
				widths[imageType] = ImageSizeWidths(handlers.ImageWidthsFor(imageType))
			}
			return &ImageCapabilitiesOutput{Body: ImageCapabilities{
				EpisodeStillProvenance: true,
				ExtendedItemArtwork:    true,
				SeasonListArtworkParam: view.SeasonListArtworkParam,
				SlotWidthParams:        slotWidthParams,
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
