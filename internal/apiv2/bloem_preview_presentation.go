package apiv2

import (
	"context"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/preview"
)

type PreviewPresentationSource interface {
	AccountPresentation(context.Context, *auth.Claims) (*preview.Presentation, bool, error)
}

// BloemPreviewPresentation is the API projection of the stored display facts.
type BloemPreviewPresentation struct {
	preview.PresentationFacts
	FreePlay *BloemPreviewFreePlay `json:"free_play,omitempty" doc:"The guest's free play while it lasts; absent for every other session and once it is used."`
}

// BloemPreviewFreePlay is the API projection of a guest's live free-play grant.
type BloemPreviewFreePlay preview.FreePlayView

func bloemPreviewPresentationView(view *preview.Presentation) *BloemPreviewPresentation {
	if view == nil {
		return nil
	}
	return &BloemPreviewPresentation{PresentationFacts: view.PresentationFacts, FreePlay: (*BloemPreviewFreePlay)(view.FreePlay)}
}

type BloemAccount struct {
	Account
	Preview         *BloemPreviewPresentation `json:"preview"`
	PlaybackAllowed bool                      `json:"playback_allowed"`
}

// Schema retains the stable Account schema and adds only the Bloem properties.
func (BloemAccount) Schema(r huma.Registry) *huma.Schema {
	ref := r.Schema(reflect.TypeFor[Account](), true, "")
	base := r.SchemaFromRef(ref.Ref)
	base.Properties["preview"] = &huma.Schema{AnyOf: []*huma.Schema{r.Schema(reflect.TypeFor[BloemPreviewPresentation](), true, ""), {Type: "null"}}}
	base.Properties["playback_allowed"] = &huma.Schema{Type: "boolean", Description: "Whether the effective account policy permits playback."}
	return ref
}

type BloemPreviewDenial struct {
	Code     string `json:"code" enum:"preview_only"`
	CTAURL   string `json:"cta_url" format:"uri-reference"`
	CTALabel string `json:"cta_label"`
}
type BloemPlaybackCapabilities struct {
	PlaybackCapabilities
	Denial *BloemPreviewDenial `json:"denial,omitempty"`
}

func (BloemPlaybackCapabilities) Schema(r huma.Registry) *huma.Schema {
	ref := r.Schema(reflect.TypeFor[PlaybackCapabilities](), true, "")
	r.SchemaFromRef(ref.Ref).Properties["denial"] = r.Schema(reflect.TypeFor[BloemPreviewDenial](), true, "")
	return ref
}

type BloemAccountOutput struct{ Body BloemAccount }
type BloemPlaybackCapabilitiesOutput struct {
	Status       int
	ETag         string `header:"ETag"`
	CacheControl string `header:"Cache-Control"`
	Body         BloemPlaybackCapabilities
}

func (reg *Registry) previewPresentation(ctx context.Context, fallback bool) (*preview.Presentation, bool, error) {
	if reg.deps.PreviewPresentation != nil {
		view, allowed, err := reg.deps.PreviewPresentation.AccountPresentation(ctx, claimsFrom(ctx))
		if err != nil {
			return nil, false, unavailable("account policy")
		}
		return view, allowed, nil
	}
	if apimw.IsPreviewPrincipal(ctx) {
		return nil, false, unavailable("preview presentation")
	}
	return nil, fallback, nil
}

// registerBloemPreviewProjection lifts only these two projections before the
// host's normal registration and capability hashing. Other handlers are unchanged.
func registerBloemPreviewProjection[I, O any](reg *Registry, op Operation, handler func(context.Context, *I) (*O, error)) bool {
	// With no extension installed, keep the host projection and response bytes.
	// The additive optional properties remain discoverable in the contract.
	if reg.deps.PreviewPresentation == nil {
		switch op.OperationID {
		case "getCurrentUser":
			BloemAccount{}.Schema(reg.api.OpenAPI().Components.Schemas)
		case opGetPlaybackCapabilities:
			BloemPlaybackCapabilities{}.Schema(reg.api.OpenAPI().Components.Schemas)
		}
		return false
	}

	if op.OperationID == "getCurrentUser" {
		original, ok := any(handler).(func(context.Context, *struct{}) (*AccountOutput, error))
		if !ok {
			return false
		}
		Register(reg, op, func(ctx context.Context, in *struct{}) (*BloemAccountOutput, error) {
			out, err := original(ctx, in)
			if err != nil || out == nil {
				return nil, err
			}
			view, allowed, err := reg.previewPresentation(ctx, false)
			if err != nil {
				return nil, err
			}
			return &BloemAccountOutput{Body: BloemAccount{Account: out.Body, Preview: bloemPreviewPresentationView(view), PlaybackAllowed: allowed}}, nil
		})
		return true
	}
	if op.OperationID == opGetPlaybackCapabilities {
		original, ok := any(handler).(func(context.Context, *CapabilityInput) (*PlaybackCapabilitiesOutput, error))
		if !ok {
			return false
		}
		Register(reg, op, func(ctx context.Context, in *CapabilityInput) (*BloemPlaybackCapabilitiesOutput, error) {
			out, err := original(ctx, in)
			if err != nil || out == nil {
				return nil, err
			}
			fallback := out.Body.Allowed != nil && *out.Body.Allowed
			view, allowed, err := reg.previewPresentation(ctx, fallback)
			if err != nil {
				return nil, err
			}
			out.Body.Allowed = &allowed
			result := &BloemPlaybackCapabilitiesOutput{Status: out.Status, ETag: out.ETag, CacheControl: out.CacheControl, Body: BloemPlaybackCapabilities{PlaybackCapabilities: out.Body}}
			if view != nil {
				result.Body.Denial = &BloemPreviewDenial{Code: apimw.CodePreviewOnly, CTAURL: view.CTAURL, CTALabel: view.CTALabel}
			}
			return result, nil
		})
		return true
	}
	return false
}
