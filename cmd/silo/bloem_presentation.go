package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/Silo-Server/silo-server/internal/bloempresentation"
)

type managedPresentationRunner struct {
	*bloempresentation.Client
	stopped chan struct{}
}

const bloemPresentationPluginDirectory = "/usr/local/lib/bloem/plugins"

// unavailablePresentationRunner preserves process-only runtime behavior when
// construction fails. The feature services omit optional presentation on errors.
type unavailablePresentationRunner struct{ err error }

func (r unavailablePresentationRunner) Call(ctx context.Context, _ string, _ any, _ any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.err
}

func bloemPresentationPluginPath(directory, feature string) (string, error) {
	if directory == "" {
		directory = bloemPresentationPluginDirectory
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("presentation plugin directory must be a clean absolute path")
	}
	switch feature {
	case "promotions", "ambience":
		return filepath.Join(directory, feature), nil
	default:
		return "", errors.New("unknown bundled presentation plugin")
	}
}

// bloemPresentationRunners builds both runners, starting their workers in
// parallel (see bloemPresentationRunner).
func bloemPresentationRunners(ctx context.Context) (bloempresentation.Runner, bloempresentation.Runner) {
	directory := os.Getenv("BLOEM_PRESENTATION_PLUGIN_DIR")
	var promotionRunner, ambienceRunner bloempresentation.Runner
	var started sync.WaitGroup
	started.Go(func() { promotionRunner = bloemPresentationRunner(ctx, directory, "promotions") })
	started.Go(func() { ambienceRunner = bloemPresentationRunner(ctx, directory, "ambience") })
	started.Wait()
	return promotionRunner, ambienceRunner
}

func bloemPresentationRunner(ctx context.Context, directory, feature string) bloempresentation.Runner {
	path, err := bloemPresentationPluginPath(directory, feature)
	if err != nil {
		return unavailablePresentationRunner{err: err}
	}
	client, err := bloempresentation.New(path)
	if err != nil {
		slog.WarnContext(ctx, "presentation plugin unavailable", "feature", feature, "error", err)
		return unavailablePresentationRunner{err: fmt.Errorf("presentation plugin %s unavailable: %w", feature, err)}
	}
	runner := &managedPresentationRunner{Client: client, stopped: make(chan struct{})}
	go func() {
		defer close(runner.stopped)
		<-ctx.Done()
		if err := client.Close(); err != nil {
			slog.WarnContext(ctx, "closing presentation plugin", "feature", feature, "error", err)
		}
	}()
	// Start the worker now rather than inside the first request's call
	// budget, which a cold process start on a loaded host can exceed. A worker
	// that does not start is left to the first call, which starts it again.
	if err := client.Warm(ctx); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "presentation plugin did not start", "feature", feature, "error", err)
	}
	return runner
}
