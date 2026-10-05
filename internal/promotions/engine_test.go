package promotions

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func enginePromotion(id string) Promotion {
	return Promotion{ID: id, Surfaces: []string{SurfaceHome, SurfaceDetail, SurfacePrePlayback, SurfaceInPlayback}, StartsAt: promoStart, EndsAt: promoEnd, Headline: id, ImageURL: "https://example.test/image.jpg", Dismissible: true}
}

func TestEngineCandidatesPreservesWindowsSurfacesContentAndDismissals(t *testing.T) {
	now := promoStart.Add(time.Hour)
	candidates := []Promotion{enginePromotion("active"), enginePromotion("expired"), enginePromotion("future"), enginePromotion("detail"), enginePromotion("restricted"), enginePromotion("dismissed")}
	candidates[1].EndsAt = now
	candidates[2].StartsAt = now.Add(time.Second)
	candidates[3].Surfaces = []string{SurfaceDetail}
	candidates[4].Placement.ContentIDs = []string{"movie"}
	got, err := (Engine{}).Candidates(context.Background(), CandidateInput{Promotions: candidates, Query: Query{Surface: SurfaceHome}, Now: now, DismissedIDs: []string{"dismissed"}})
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"active"}) {
		t.Fatalf("selection: %+v %v", got, err)
	}
	got, err = (Engine{}).Candidates(context.Background(), CandidateInput{Promotions: []Promotion{enginePromotion("boundary")}, Query: Query{Surface: SurfaceHome}, Now: promoStart})
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"boundary"}) {
		t.Fatalf("inclusive start: %+v %v", got, err)
	}
}

func TestEngineDeliveryPreservesTargetingPresentationAndHomePosition(t *testing.T) {
	first := enginePromotion("first")
	pos := 3
	first.Placement.HomePosition = &pos
	first.Placement.PlaybackStyle = "card"
	role := enginePromotion("role")
	role.Targeting = Targeting{Audience: "role", Role: "admin"}
	library := enginePromotion("library")
	library.Targeting = Targeting{Audience: "library", LibraryID: 8}
	explicit := enginePromotion("profile")
	explicit.Targeting = Targeting{Audience: "explicit", ProfileIDs: []string{"profile"}}
	org := enginePromotion("org")
	org.Targeting = Targeting{Audience: "organization", OrganizationID: "org"}
	in := DeliveryInput{Promotions: []Promotion{first, role, library, explicit, org}, Viewer: Viewer{UserID: 7, ProfileID: "profile", LibraryIDs: []int{8}}, Role: "user", Organizations: map[string]bool{"org": true}}
	got, err := (Engine{}).Deliver(context.Background(), in)
	if err != nil || !reflect.DeepEqual(ids(got.Cards), []string{"first", "library", "profile", "org"}) || got.HomePosition != 3 || got.Cards[0].DurationSeconds != 10 || !got.Cards[0].ExpiresAt.Equal(promoEnd) {
		t.Fatalf("delivery: %+v %v", got, err)
	}
	in.Promotions = []Promotion{enginePromotion("default"), first}
	got, err = (Engine{}).Deliver(context.Background(), in)
	if err != nil || got.HomePosition != DefaultHomePosition {
		t.Fatalf("first card exclusively chooses position: %+v %v", got, err)
	}
	in.Viewer.LibraryIDs = nil
	in.Promotions = []Promotion{library}
	got, err = (Engine{}).Deliver(context.Background(), in)
	if err != nil || len(got.Cards) != 1 {
		t.Fatalf("unrestricted library: %+v %v", got, err)
	}
	in.Viewer.LibraryIDs = []int{}
	got, err = (Engine{}).Deliver(context.Background(), in)
	if err != nil || len(got.Cards) != 0 {
		t.Fatalf("empty library restriction: %+v %v", got, err)
	}
}

func TestEngineCancellationAndInvalidSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Engine{}).Candidates(ctx, CandidateInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("candidates cancellation: %v", err)
	}
	if _, err := (Engine{}).Deliver(ctx, DeliveryInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("delivery cancellation: %v", err)
	}
	if _, err := (Engine{}).Candidates(context.Background(), CandidateInput{Query: Query{Surface: "unknown"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("surface validation: %v", err)
	}
}

type promotionCaller func(context.Context, string, any, any) error

func (f promotionCaller) Call(ctx context.Context, op string, in, out any) error {
	return f(ctx, op, in, out)
}

func TestPluginEvaluatorRejectsInvalidSelections(t *testing.T) {
	for _, selected := range [][]string{nil, {"foreign"}, {"allowed", "allowed"}} {
		evaluator := NewPluginEvaluator(promotionCaller(func(_ context.Context, op string, _ any, out any) error {
			out.(*CandidateResult).IDs = selected
			return nil
		}))
		_, err := evaluator.Candidates(context.Background(), CandidateInput{Promotions: []Promotion{enginePromotion("allowed")}, Query: Query{Surface: SurfaceHome}, Now: promoStart})
		// A nil IDs field is malformed; a genuine empty result is [].
		if err == nil {
			t.Fatalf("accepted malformed selection: %+v", selected)
		}
	}
}

func TestPluginEvaluatorRejectsForgedCardsAndPosition(t *testing.T) {
	for _, mutate := range []func(*DeliveryResult){
		func(r *DeliveryResult) { r.Cards[0].Headline = "forged" },
		func(r *DeliveryResult) { r.HomePosition = -1 },
		func(r *DeliveryResult) { r.Cards = append(r.Cards, r.Cards[0]) },
		func(r *DeliveryResult) { r.Cards[0].ID = "foreign" },
	} {
		p := enginePromotion("allowed")
		evaluator := NewPluginEvaluator(promotionCaller(func(_ context.Context, _ string, _ any, out any) error {
			r := out.(*DeliveryResult)
			r.Cards = []Card{p.Card()}
			r.HomePosition = DefaultHomePosition
			mutate(r)
			return nil
		}))
		if _, err := evaluator.Deliver(context.Background(), DeliveryInput{Promotions: []Promotion{p}}); err == nil {
			t.Fatal("accepted forged response")
		}
	}
}

func TestPluginEvaluatorPropagatesFailureAndCancellation(t *testing.T) {
	failure := errors.New("worker unavailable")
	evaluator := NewPluginEvaluator(promotionCaller(func(ctx context.Context, _ string, _ any, _ any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure
	}))
	if _, err := evaluator.Candidates(context.Background(), CandidateInput{}); !errors.Is(err, failure) {
		t.Fatalf("lost error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := evaluator.Deliver(ctx, DeliveryInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
