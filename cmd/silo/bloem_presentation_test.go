package main

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

func TestBloemPresentationDefaultPaths(t *testing.T) {
	for _, feature := range []string{"promotions", "ambience"} {
		got, err := bloemPresentationPluginPath("", feature)
		if err != nil || got != "/usr/local/lib/bloem/plugins/"+feature {
			t.Fatalf("default %s path = %q, %v", feature, got, err)
		}
	}
	for _, directory := range []string{"relative", "/tmp/../plugins", "/tmp/plugins/"} {
		if _, err := bloemPresentationPluginPath(directory, "promotions"); err == nil {
			t.Fatalf("accepted %q", directory)
		}
	}
}

func TestBloemPresentationRuntimeRunsBundledWorkersAndClosesOnShutdown(t *testing.T) {
	directory := buildBundledPresentationWorkers(t)
	t.Setenv("BLOEM_PRESENTATION_PLUGIN_DIR", directory)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	promotionRunner, ambienceRunner := bloemPresentationRunners(ctx)
	now := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	candidates, err := promotions.NewPluginEvaluator(promotionRunner).Candidates(t.Context(), promotions.CandidateInput{
		Promotions: []promotions.Promotion{{ID: "active", Surfaces: []string{"home"}, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}},
		Query:      promotions.Query{Surface: "home"}, Now: now,
	})
	if err != nil || len(candidates.IDs) != 1 || candidates.IDs[0] != "active" {
		t.Fatalf("runtime promotion worker: %+v, %v", candidates, err)
	}
	packs, err := ambience.NewPluginEvaluator(ambienceRunner).Evaluate(t.Context(), ambience.EvaluationRequest{
		Now:        now,
		Candidates: []ambience.Wire{{ID: "active", Window: ambience.Window{StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}}},
	})
	if err != nil || len(packs.Packs) != 1 || packs.Packs[0].ID != "active" {
		t.Fatalf("runtime ambience worker: %+v, %v", packs, err)
	}
	cancel()
	for _, runner := range []*managedPresentationRunner{promotionRunner.(*managedPresentationRunner), ambienceRunner.(*managedPresentationRunner)} {
		select {
		case <-runner.stopped:
		case <-time.After(2 * time.Second):
			t.Fatal("shutdown did not close the running worker")
		}
		if err := runner.Call(t.Context(), "after.shutdown", nil, new(any)); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("closed worker restarted: %v", err)
		}
	}
}

func TestBloemPresentationMissingOrInvalidWorkerHasNoPureFallback(t *testing.T) {
	for _, directory := range []string{t.TempDir(), "relative-directory"} {
		ctx, cancel := context.WithCancel(t.Context())
		runner := bloemPresentationRunner(ctx, directory, "promotions")
		_, err := promotions.NewPluginEvaluator(runner).Candidates(t.Context(), promotions.CandidateInput{Query: promotions.Query{Surface: "home"}, Now: time.Now()})
		if err == nil {
			t.Fatal("unavailable worker fell back to pure evaluation")
		}
		canceled, stop := context.WithCancel(t.Context())
		stop()
		if err := runner.Call(canceled, promotions.OperationCandidates, nil, new(any)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
		cancel()
	}
}

func buildBundledPresentationWorkers(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	root := filepath.Join("..", "..")
	for _, feature := range []string{"promotions", "ambience"} {
		buildCtx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		cmd := exec.CommandContext(buildCtx, "go", "build", "-o", filepath.Join(directory, feature), "./cmd/bloem-presentation-"+feature)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("build %s worker: %v: %s", feature, err, output)
		}
	}

	return directory
}
