package apiv2

import (
	"context"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// BloemHomePromotionInput opts a home request in to promoted sections.
type BloemHomePromotionInput struct {
	Promoted string `query:"promoted" enum:"1" doc:"Opt in to promoted home sections"`
}

// bloemPromotedViewer applies the promoted-sections opt-in to a section viewer.
func bloemPromotedViewer(viewer handlers.SectionViewer, in BloemHomePromotionInput) handlers.SectionViewer {
	viewer.Extensions = url.Values{"promoted": []string{in.Promoted}}
	return viewer
}

func bloemHomeLayoutContext(ctx context.Context, in BloemHomePromotionInput) context.Context {
	return handlers.WithHomePromotions(ctx, in.Promoted == "1")
}
