package apiv2

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// BloemHomePromotionInput opts a home request in to promoted sections.
type BloemHomePromotionInput struct {
	Promoted string `query:"promoted" enum:"1" doc:"Opt in to promoted home sections"`
}

// bloemHomePromotionContext carries the promoted-sections opt-in to the home
// reads, which hand it to the section transformers.
func bloemHomePromotionContext(ctx context.Context, in BloemHomePromotionInput) context.Context {
	return handlers.WithHomePromotions(ctx, in.Promoted == "1")
}
