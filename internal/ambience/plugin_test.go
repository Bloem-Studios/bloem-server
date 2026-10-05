package ambience

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type callerFunc func(context.Context, string, any, any) error

func (f callerFunc) Call(ctx context.Context, op string, req, result any) error {
	return f(ctx, op, req, result)
}

type evaluatorFunc func(context.Context, EvaluationRequest) (EvaluationResponse, error)

func (f evaluatorFunc) Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
	return f(ctx, req)
}

func TestPluginEvaluatorCallsWorkerAndPropagatesErrors(t *testing.T) {
	req := EvaluationRequest{Now: instant("2029-12-15T00:00:00Z"), Candidates: []Wire{seasonalCandidate()}}
	calls := 0
	transport := callerFunc(func(ctx context.Context, op string, request, result any) error {
		calls++
		if op != EvaluationOperation || !reflect.DeepEqual(request, req) {
			t.Fatal("incorrect operation or payload")
		}
		got, err := (Engine{}).Evaluate(ctx, request.(EvaluationRequest))
		if err != nil {
			return err
		}
		*result.(*EvaluationResponse) = got
		return nil
	})
	got, err := NewPluginEvaluator(transport).Evaluate(t.Context(), req)
	if err != nil || calls != 1 || len(got.Packs) != 1 {
		t.Fatal(got, err, calls)
	}
	failure := errors.New("worker exited")
	got, err = NewPluginEvaluator(callerFunc(func(context.Context, string, any, any) error { return failure })).Evaluate(t.Context(), req)
	if !errors.Is(err, failure) || got.Packs != nil {
		t.Fatalf("worker error was hidden by fallback: %+v %v", got, err)
	}
	if _, err := NewPluginEvaluator(nil).Evaluate(t.Context(), req); err == nil {
		t.Fatal("missing transport accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewPluginEvaluator(transport).Evaluate(ctx, req); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("canceled request called worker: %v calls=%d", err, calls)
	}
}

func TestRunPluginServesSeasonalProtocolAndRecoversFromOperationErrors(t *testing.T) {
	req := EvaluationRequest{Now: instant("2029-12-15T00:00:00Z"), Candidates: []Wire{seasonalCandidate()}}
	var input, output bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, message := range []any{
		map[string]any{"version": 1, "operation": "unknown", "payload": req},
		map[string]any{"version": 1, "operation": EvaluationOperation, "payload": map[string]any{"now": "invalid-date"}},
		map[string]any{"version": 1, "operation": EvaluationOperation, "payload": req},
	} {
		if err := encoder.Encode(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunPlugin(&input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 3; i++ {
		var response struct {
			Version int             `json:"version"`
			Payload json.RawMessage `json:"payload"`
			Error   string          `json:"error"`
		}
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Version != 1 {
			t.Fatal(response)
		}
		if i < 2 {
			if response.Error == "" {
				t.Fatal("invalid request was accepted")
			}
			continue
		}
		if response.Error != "" {
			t.Fatal(response.Error)
		}
		var got EvaluationResponse
		if err := json.Unmarshal(response.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Packs) != 1 || got.Packs[0].Window.RepeatYearly || got.Packs[0].Window.StartsAt != instant("2029-11-30T23:00:00Z") {
			t.Fatal(got)
		}
	}
}

func TestHostValidatesWorkerOutputAgainstAuthorizedSnapshot(t *testing.T) {
	candidate := seasonalCandidate()
	packs := []Pack{{ID: candidate.ID, EffectID: candidate.EffectID, Window: candidate.Window, Intensity: candidate.Intensity, Surfaces: candidate.Surfaces, Assets: candidate.Assets}}
	now := instant("2029-12-15T00:00:00Z")
	for _, tc := range []struct {
		name   string
		mutate func(*EvaluationResponse)
	}{
		{"unknown pack", func(r *EvaluationResponse) { r.Packs[0].ID = "other-tenant" }},
		{"duplicate pack", func(r *EvaluationResponse) { r.Packs = append(r.Packs, r.Packs[0]) }},
		{"invented asset", func(r *EvaluationResponse) { r.Packs[0].Assets.BannerURL = "https://cdn.example/secret.png" }},
		{"invented sprite", func(r *EvaluationResponse) {
			r.Packs[0].Assets.Sprites = append(r.Packs[0].Assets.Sprites, "https://cdn.example/secret.png")
		}},
		{"changed effect", func(r *EvaluationResponse) { r.Packs[0].EffectID = "other" }},
		{"changed intensity", func(r *EvaluationResponse) { r.Packs[0].Intensity = 0.75 }},
		{"changed surface", func(r *EvaluationResponse) { r.Packs[0].Surfaces = []string{SurfaceAll} }},
		{"invented dates", func(r *EvaluationResponse) { r.Packs[0].Window.StartsAt = instant("2029-10-31T23:00:00Z") }},
		{"extended expiry", func(r *EvaluationResponse) { r.Packs[0].Window.EndsAt = instant("2030-12-31T23:00:00Z") }},
		{"expired", func(r *EvaluationResponse) {
			r.Packs[0].Window = Window{StartsAt: instant("2028-11-30T23:00:00Z"), EndsAt: instant("2028-12-31T23:00:00Z")}
		}},
		{"recurrence leaked", func(r *EvaluationResponse) { r.Packs[0].Window.RepeatYearly = true }},
		{"timezone leaked", func(r *EvaluationResponse) { r.Packs[0].Window.Timezone = "Europe/Amsterdam" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewServiceWithEvaluator(nil, nil, nil, evaluatorFunc(func(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
				got, err := (Engine{}).Evaluate(ctx, req)
				tc.mutate(&got)
				return got, err
			}))
			got, err := svc.evaluateActive(t.Context(), packs, now)
			if !errors.Is(err, ErrInvalidEvaluation) || got != nil {
				t.Fatalf("unsafe response accepted: %+v %v", got, err)
			}
		})
	}
	svc := NewServiceWithEvaluator(nil, nil, nil, Engine{})
	got, err := svc.evaluateActive(t.Context(), packs, now)
	if err != nil || len(got) != 1 || got[0].ID != candidate.ID {
		t.Fatal(got, err)
	}
}

func TestHostValidatesOneOffWindowAndCancellation(t *testing.T) {
	original := Window{StartsAt: instant("2029-12-01T00:00:00Z"), EndsAt: instant("2030-01-01T00:00:00Z")}
	now := instant("2029-12-15T00:00:00Z")
	if !validOccurrence(original, original, now) {
		t.Fatal("valid one-off occurrence rejected")
	}
	changed := original
	changed.EndsAt = instant("2030-01-02T00:00:00Z")
	if validOccurrence(original, changed, now) {
		t.Fatal("one-off expiry extension accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	svc := NewServiceWithEvaluator(nil, nil, nil, evaluatorFunc(func(context.Context, EvaluationRequest) (EvaluationResponse, error) {
		cancel()
		return EvaluationResponse{Packs: []Wire{}}, nil
	}))
	if _, err := svc.evaluateActive(ctx, nil, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	failure := errors.New("plugin unavailable")
	svc = NewServiceWithEvaluator(nil, nil, nil, evaluatorFunc(func(context.Context, EvaluationRequest) (EvaluationResponse, error) {
		return EvaluationResponse{}, failure
	}))
	if got, err := svc.evaluateActive(t.Context(), nil, now); !errors.Is(err, failure) || got != nil {
		t.Fatal(got, err)
	}
}

func TestOptionalPresentationOmitsWorkerFailuresWithoutFallback(t *testing.T) {
	candidate := seasonalCandidate()
	packs := []Pack{{ID: candidate.ID, EffectID: candidate.EffectID, Window: candidate.Window, Intensity: candidate.Intensity, Surfaces: candidate.Surfaces, Assets: candidate.Assets}}
	now := instant("2029-12-15T00:00:00Z")
	for _, tc := range []struct {
		name      string
		evaluator Evaluator
	}{
		{"missing evaluator", nil},
		{"worker unavailable", NewPluginEvaluator(callerFunc(func(context.Context, string, any, any) error { return errors.New("worker unavailable") }))},
		{"worker deadline", NewPluginEvaluator(callerFunc(func(context.Context, string, any, any) error { return context.DeadlineExceeded }))},
		{"unsafe output", evaluatorFunc(func(context.Context, EvaluationRequest) (EvaluationResponse, error) {
			return EvaluationResponse{Packs: []Wire{{ID: "foreign"}}}, nil
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewServiceWithEvaluator(nil, nil, nil, tc.evaluator)
			got, err := svc.evaluatePresentation(t.Context(), packs, now)
			if err != nil || got == nil || len(got) != 0 {
				t.Fatalf("optional failure leaked or fallback ran: %+v %v", got, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	svc := NewServiceWithEvaluator(nil, nil, nil, NewPluginEvaluator(nil))
	if _, err := svc.evaluatePresentation(ctx, packs, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation hidden: %v", err)
	}
}

func TestHostRejectsAlternateDSTAmbiguousInstant(t *testing.T) {
	schedule := Window{StartsAt: instant("2025-11-01T05:30:00Z"), EndsAt: instant("2025-11-02T06:30:00Z"), RepeatYearly: true, Timezone: "America/New_York"}
	now := instant("2026-11-01T12:00:00Z")
	occurrence, active := schedule.At(now)
	if !active || !validOccurrence(schedule, occurrence, now) {
		t.Fatal("expected valid yearly occurrence", occurrence)
	}
	alternate := occurrence
	alternate.StartsAt = alternate.StartsAt.Add(time.Hour)
	if validOccurrence(schedule, alternate, now) {
		t.Fatal("ambiguous timezone hour altered stored occurrence")
	}
}

func TestHostPreservesAuthorizedCandidatePriority(t *testing.T) {
	candidate := seasonalCandidate()
	first := Pack{ID: "first", EffectID: candidate.EffectID, Window: candidate.Window, Intensity: candidate.Intensity, Surfaces: candidate.Surfaces, Assets: candidate.Assets}
	second := first
	second.ID = "second"
	packs := []Pack{first, second}
	now := instant("2029-12-15T00:00:00Z")
	for _, tc := range []struct {
		name     string
		selected []int
		invalid  bool
	}{
		{"ordered complete result", []int{0, 1}, false},
		{"reversed valid packs", []int{1, 0}, true},
		{"legal first subset", []int{0}, false},
		{"legal later subset", []int{1}, false},
		{"legal empty subset", []int{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewServiceWithEvaluator(nil, nil, nil, evaluatorFunc(func(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
				active, err := (Engine{}).Evaluate(ctx, req)
				if err != nil {
					return EvaluationResponse{}, err
				}
				selected := EvaluationResponse{Packs: make([]Wire, 0, len(tc.selected))}
				for _, i := range tc.selected {
					selected.Packs = append(selected.Packs, active.Packs[i])
				}
				return selected, nil
			}))
			got, err := svc.evaluateActive(t.Context(), packs, now)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidEvaluation) || got != nil {
					t.Fatalf("reordered response accepted: %+v %v", got, err)
				}
				return
			}
			if err != nil || len(got) != len(tc.selected) {
				t.Fatal(got, err)
			}
			for i, index := range tc.selected {
				if got[i].ID != packs[index].ID {
					t.Fatalf("subset priority changed: %+v", got)
				}
			}
		})
	}
}

func TestHostCanonicalizesEquivalentWorkerOffsetsToUTC(t *testing.T) {
	candidate := seasonalCandidate()
	yearly := Pack{ID: candidate.ID, EffectID: candidate.EffectID, Window: candidate.Window, Intensity: candidate.Intensity, Surfaces: candidate.Surfaces, Assets: candidate.Assets}
	oneOff := yearly
	oneOff.ID = "one-off"
	oneOff.Window = Window{StartsAt: instant("2029-12-01T00:00:00Z"), EndsAt: instant("2030-01-01T00:00:00Z")}
	now := instant("2029-12-15T00:00:00Z")
	for _, pack := range []Pack{oneOff, yearly} {
		t.Run(pack.ID, func(t *testing.T) {
			var expected Window
			svc := NewServiceWithEvaluator(nil, nil, nil, evaluatorFunc(func(ctx context.Context, req EvaluationRequest) (EvaluationResponse, error) {
				result, err := (Engine{}).Evaluate(ctx, req)
				if err != nil {
					return result, err
				}
				expected = result.Packs[0].Window
				offset := time.FixedZone("worker-offset", 3600)
				result.Packs[0].Window.StartsAt = result.Packs[0].Window.StartsAt.In(offset)
				result.Packs[0].Window.EndsAt = result.Packs[0].Window.EndsAt.In(offset)
				return result, nil
			}))
			got, err := svc.evaluateActive(t.Context(), []Pack{pack}, now)
			if err != nil || len(got) != 1 {
				t.Fatal(got, err)
			}
			if got[0].Window != expected || got[0].Window.StartsAt.Location() != time.UTC || got[0].Window.EndsAt.Location() != time.UTC {
				t.Fatalf("worker offsets leaked into canonical projection: %+v", got[0].Window)
			}
		})
	}
}
