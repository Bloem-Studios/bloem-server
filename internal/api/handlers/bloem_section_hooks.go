package handlers

import (
	"context"
	"net/url"

	"github.com/Silo-Server/silo-server/internal/sections"
)

// HomeSectionTransformer extends a resolved layout after host authorization.
type HomeSectionTransformer func(context.Context, []sections.ResolvedSection, url.Values) []sections.ResolvedSection

// SectionItemExtension projects host-owned extension data onto the stable card union.
type SectionItemExtension func(sections.SectionWithItems) []SectionItemView

type sectionOptionsKey struct{}

// WithSectionOptions carries request-specific extension options to layout reads.
func WithSectionOptions(ctx context.Context, options url.Values) context.Context {
	return context.WithValue(ctx, sectionOptionsKey{}, options)
}

func sectionOptions(ctx context.Context) url.Values {
	options, _ := ctx.Value(sectionOptionsKey{}).(url.Values)
	return options
}

func (h *SectionHandler) transformHomeSections(ctx context.Context, resolved []sections.ResolvedSection, options url.Values) []sections.ResolvedSection {
	for _, transform := range h.HomeTransformers {
		resolved = transform(ctx, resolved, options)
	}
	return resolved
}
