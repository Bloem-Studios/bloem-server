package ambience

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// EvaluationRequest contains only presentation candidates authorized by the host.
// Its order preserves the registry priority (starts_at, then id). Recurrence
// metadata stays here; it is removed from the concrete client-facing result.
type EvaluationRequest struct {
	Now        time.Time `json:"now"`
	Candidates []Wire    `json:"candidates"`
}

// EvaluationResponse contains the active occurrences at the requested instant.
type EvaluationResponse struct {
	Packs []Wire `json:"packs"`
}

// Evaluator selects active presentation packs without database or asset access.
type Evaluator interface {
	Evaluate(context.Context, EvaluationRequest) (EvaluationResponse, error)
}

// Engine is the pure evaluator used by the standalone ambience worker.
type Engine struct{}

func (Engine) Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
	if err := ctx.Err(); err != nil {
		return EvaluationResponse{}, err
	}
	if req.Now.IsZero() {
		return EvaluationResponse{}, invalid("evaluation instant is required")
	}
	out := EvaluationResponse{Packs: make([]Wire, 0, len(req.Candidates))}
	for _, candidate := range req.Candidates {
		if err := ctx.Err(); err != nil {
			return EvaluationResponse{}, err
		}
		if window, ok := candidate.Window.At(req.Now); ok {
			candidate.Window = Window{StartsAt: window.StartsAt, EndsAt: window.EndsAt}
			candidate.Surfaces = slices.Clone(candidate.Surfaces)
			if candidate.Surfaces == nil {
				candidate.Surfaces = []string{}
			}
			candidate.Assets.Sprites = slices.Clone(candidate.Assets.Sprites)
			out.Packs = append(out.Packs, candidate)
		}
	}
	return out, nil
}

var ErrInvalidEvaluation = errors.New("ambience: invalid evaluator response")

// evaluateActive is the host boundary: it sends only the already scoped snapshot
// and checks that the worker cannot introduce content or alter asset references.
func (s *Service) evaluateActive(ctx context.Context, packs []Pack, now time.Time) ([]Wire, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.evaluator == nil {
		return nil, errors.New("ambience: evaluator unavailable")
	}
	candidates := make([]Wire, 0, len(packs))
	authorized := make(map[string]int, len(packs))
	for position, p := range packs {
		candidate := p.Wire()
		candidate.Window = p.Window
		candidate.Surfaces = slices.Clone(candidate.Surfaces)
		candidate.Assets.Sprites = slices.Clone(candidate.Assets.Sprites)
		candidates = append(candidates, candidate)
		authorized[p.ID] = position
	}
	result, err := s.evaluator.Evaluate(ctx, EvaluationRequest{Now: now, Candidates: candidates})
	if err != nil {
		return nil, fmt.Errorf("ambience: evaluate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]Wire, 0, len(result.Packs))
	// A worker may select a subset, but cannot change registry priority or
	// duplicate an entry. Candidate positions must remain strictly increasing.
	previousPosition := -1
	for _, got := range result.Packs {
		position, ok := authorized[got.ID]
		if !ok || position <= previousPosition {
			return nil, ErrInvalidEvaluation
		}
		previousPosition = position
		p := packs[position]
		want := p.Wire()
		if got.EffectID != want.EffectID || got.Intensity != want.Intensity || !slices.Equal(got.Surfaces, want.Surfaces) || got.Assets.BannerURL != want.Assets.BannerURL || !slices.Equal(got.Assets.Sprites, want.Assets.Sprites) || !validOccurrence(p.Window, got.Window, now) {
			return nil, ErrInvalidEvaluation
		}
		// Project the authoritative host snapshot, using only the checked occurrence.
		want.Window = Window{StartsAt: got.Window.StartsAt.UTC(), EndsAt: got.Window.EndsAt.UTC()}
		out = append(out, want)
	}
	return out, nil
}

// validOccurrence checks a returned calendar occurrence without selecting the
// active season again. The worker chooses occurrences; the host constrains them
// to the stored schedule and inclusive-start/exclusive-end expiry contract.
func validOccurrence(schedule, occurrence Window, now time.Time) bool {
	if occurrence.RepeatYearly || occurrence.Timezone != "" || !occurrence.StartsAt.Before(occurrence.EndsAt) || !occurrence.Contains(now) || occurrence.StartsAt.Before(schedule.StartsAt) {
		return false
	}
	if !schedule.RepeatYearly {
		return occurrence.StartsAt.Equal(schedule.StartsAt) && occurrence.EndsAt.Equal(schedule.EndsAt)
	}
	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return false
	}
	start, end := schedule.StartsAt.In(loc), schedule.EndsAt.In(loc)
	a, b := occurrence.StartsAt.In(loc), occurrence.EndsAt.In(loc)
	return sameWallDate(start, a) && sameWallDate(end, b) && b.Year()-a.Year() == end.Year()-start.Year() &&
		calendarInstant(start, a.Year()).Equal(occurrence.StartsAt) && calendarInstant(end, b.Year()).Equal(occurrence.EndsAt)
}

func calendarInstant(original time.Time, year int) time.Time {
	return time.Date(year, original.Month(), original.Day(), original.Hour(), original.Minute(), original.Second(), original.Nanosecond(), original.Location())
}

func sameWallDate(a, b time.Time) bool {
	return a.Month() == b.Month() && a.Day() == b.Day() && a.Hour() == b.Hour() && a.Minute() == b.Minute() && a.Second() == b.Second() && a.Nanosecond() == b.Nanosecond()
}

// evaluatePresentation isolates optional ambience worker failures from login and
// discovery. Repository errors have already been returned by the caller. Never
// log worker payloads or error text, which could contain protected asset data.
func (s *Service) evaluatePresentation(ctx context.Context, packs []Pack, now time.Time) ([]Wire, error) {
	result, err := s.evaluateActive(ctx, packs, now)
	if err == nil {
		return result, nil
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return nil, cancellation
	}
	slog.WarnContext(ctx, "optional presentation unavailable", "operation", EvaluationOperation)
	return []Wire{}, nil
}
