package promotions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloempresentation"
)

// campaignFacts omits repository ownership, author metadata, and artwork
// dimensions. The host supplies ordering and keeps tenant authority locally.
type campaignFacts struct {
	ID          string    `json:"id"`
	Surfaces    []string  `json:"surfaces"`
	Placement   Placement `json:"placement"`
	Kicker      string    `json:"kicker"`
	Headline    string    `json:"headline"`
	Subtitle    string    `json:"subtitle"`
	ImageURL    string    `json:"image_url"`
	Deeplink    string    `json:"deeplink"`
	CTA         *CTA      `json:"cta"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Targeting   Targeting `json:"targeting"`
	Dismissible bool      `json:"dismissible"`
}

func presentationFacts(promotions []Promotion) []campaignFacts {
	out := make([]campaignFacts, 0, len(promotions))
	for _, p := range promotions {
		out = append(out, campaignFacts{ID: p.ID, Surfaces: p.Surfaces, Placement: p.Placement, Kicker: p.Kicker, Headline: p.Headline, Subtitle: p.Subtitle, ImageURL: p.ImageURL, Deeplink: p.Deeplink, CTA: p.CTA, StartsAt: p.StartsAt, EndsAt: p.EndsAt, Targeting: p.Targeting, Dismissible: p.Dismissible})
	}
	return out
}

func (in CandidateInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Promotions []campaignFacts `json:"promotions"`
		Query      struct {
			Surface   string `json:"surface"`
			ContentID string `json:"content_id"`
		} `json:"query"`
		Now          time.Time `json:"now"`
		DismissedIDs []string  `json:"dismissed_ids"`
	}{Promotions: presentationFacts(in.Promotions), Query: struct {
		Surface   string `json:"surface"`
		ContentID string `json:"content_id"`
	}{Surface: in.Query.Surface, ContentID: in.Query.ContentID}, Now: in.Now, DismissedIDs: in.DismissedIDs})
}

func (in DeliveryInput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Promotions    []campaignFacts `json:"promotions"`
		Viewer        Viewer          `json:"viewer"`
		Role          string          `json:"role"`
		Organizations map[string]bool `json:"organizations"`
	}{Promotions: presentationFacts(in.Promotions), Viewer: in.Viewer, Role: in.Role, Organizations: in.Organizations})
}

// Caller is the supervised process bridge. The adapter never owns its lifecycle.
type Caller interface {
	Call(context.Context, string, any, any) error
}

type PluginEvaluator struct{ caller Caller }

func NewPluginEvaluator(caller Caller) *PluginEvaluator { return &PluginEvaluator{caller: caller} }

var ErrPluginResponse = errors.New("promotions: invalid plugin response")

func (e *PluginEvaluator) Candidates(ctx context.Context, in CandidateInput) (CandidateResult, error) {
	var out CandidateResult
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if e == nil || e.caller == nil {
		return out, fmt.Errorf("%w: no caller", ErrPluginResponse)
	}
	if err := e.caller.Call(ctx, OperationCandidates, in, &out); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return CandidateResult{}, err
	}
	if out.IDs == nil {
		return CandidateResult{}, fmt.Errorf("%w: missing ids", ErrPluginResponse)
	}
	// The plugin may choose fewer cards, but may not widen schedule, content,
	// dismissal, or surface bounds supplied by the host.
	allowed, err := (Engine{}).Candidates(ctx, in)
	if err != nil {
		return CandidateResult{}, err
	}
	if err := validateOrder(out.IDs, allowed.IDs); err != nil {
		return CandidateResult{}, err
	}
	return out, nil
}

func (e *PluginEvaluator) Deliver(ctx context.Context, in DeliveryInput) (DeliveryResult, error) {
	var out DeliveryResult
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if e == nil || e.caller == nil {
		return out, fmt.Errorf("%w: no caller", ErrPluginResponse)
	}
	if err := e.caller.Call(ctx, OperationDeliver, in, &out); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return DeliveryResult{}, err
	}
	if out.Cards == nil {
		return DeliveryResult{}, fmt.Errorf("%w: missing cards", ErrPluginResponse)
	}
	allowed := make([]string, 0, len(in.Promotions))
	byID := make(map[string]Promotion, len(in.Promotions))
	for _, p := range in.Promotions {
		// Permission authority stays in the host, including library/explicit
		// audiences. A plugin cannot promote a candidate past these facts.
		if Matches(p.Targeting, in.Viewer, in.Role, in.Organizations) {
			allowed = append(allowed, p.ID)
			byID[p.ID] = p
		}
	}
	selected := make([]string, len(out.Cards))
	for i, card := range out.Cards {
		selected[i] = card.ID
	}
	if err := validateOrder(selected, allowed); err != nil {
		return DeliveryResult{}, err
	}
	canonical := DeliveryResult{Cards: make([]Card, 0, len(out.Cards)), HomePosition: DefaultHomePosition}
	for _, card := range out.Cards {
		p := byID[card.ID]
		expected := p.Card()
		card.ExpiresAt = card.ExpiresAt.UTC()
		expected.ExpiresAt = expected.ExpiresAt.UTC()
		if !reflect.DeepEqual(card, expected) {
			return DeliveryResult{}, fmt.Errorf("%w: changed card fields for %s", ErrPluginResponse, card.ID)
		}
		if len(canonical.Cards) == 0 && p.Placement.HomePosition != nil {
			canonical.HomePosition = *p.Placement.HomePosition
		}
		canonical.Cards = append(canonical.Cards, expected)
	}
	if out.HomePosition != canonical.HomePosition {
		return DeliveryResult{}, fmt.Errorf("%w: changed home position", ErrPluginResponse)
	}
	return canonical, nil
}

func validateOrder(selected, allowed []string) error {
	next := 0
	for _, id := range selected {
		for next < len(allowed) && allowed[next] != id {
			next++
		}
		if next == len(allowed) {
			return fmt.Errorf("%w: unknown, duplicate, or out-of-order id %q", ErrPluginResponse, id)
		}
		next++
	}
	return nil
}

// RunPlugin serves the versioned process protocol over standard input/output.
// Its engine receives only JSON facts; it cannot query host data itself.
func RunPlugin(in io.Reader, out io.Writer) error {
	engine := Engine{}
	return bloempresentation.Serve(in, out, func(op string, payload json.RawMessage) (any, error) {
		switch op {
		case OperationCandidates:
			var input CandidateInput
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil, fmt.Errorf("promotions: decode candidates: %w", err)
			}
			return engine.Candidates(context.Background(), input)
		case OperationDeliver:
			var input DeliveryInput
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil, fmt.Errorf("promotions: decode delivery: %w", err)
			}
			return engine.Deliver(context.Background(), input)
		default:
			return nil, fmt.Errorf("promotions: unknown operation %q", op)
		}
	})
}
