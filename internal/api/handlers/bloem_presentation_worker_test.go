package handlers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloempresentation"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// Real executable transport is reused by home wire and tenancy regressions.
// The optional directory allows the integration gate to supply prebuilt workers.
func promotionWorkerEvaluator(t *testing.T) promotions.Evaluator {
	t.Helper()
	directory := os.Getenv("BLOEM_PRESENTATION_TEST_PLUGIN_DIR")
	if directory == "" {
		directory = t.TempDir()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(directory, "promotions"), "./cmd/bloem-presentation-promotions")
		command.Dir = filepath.Join("..", "..", "..")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build promotion worker: %v: %s", err, output)
		}
	}
	client, err := bloempresentation.New(filepath.Join(directory, "promotions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return promotions.NewPluginEvaluator(client)
}
