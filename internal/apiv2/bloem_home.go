package apiv2

import (
	"context"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// HomeLayoutInput opts a home request in to promoted sections.
type HomeLayoutInput struct {
	Promoted string `query:"promoted" enum:"1" doc:"Opt in to promoted home sections"`
}

// bloemPromotedViewer applies the promoted-sections opt-in to a section viewer.
func bloemPromotedViewer(viewer handlers.SectionViewer, in HomeLayoutInput) handlers.SectionViewer {
	viewer.Extensions = url.Values{"promoted": []string{in.Promoted}}
	return viewer
}

func bloemHomeLayoutContext(ctx context.Context, in HomeLayoutInput) context.Context {
	return handlers.WithHomePromotions(ctx, in.Promoted == "1")
}
